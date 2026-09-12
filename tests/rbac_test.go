package tests

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/adapter/forecast"
	"github.com/devrishijain/table-manager/internal/adapter/storage"
	"github.com/devrishijain/table-manager/internal/api"
	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

func setupTestRouter(t *testing.T) (http.Handler, *memory.MemoryRepository, []byte) {
	jwtSecret := []byte("test-jwt-secret-key-at-least-32-bytes")
	repo := memory.NewMemoryRepository()
	objStore := storage.NewMemoryObjectStore()
	forecastProv := forecast.NewWeightedMovingAverageForecast()

	sessionSvc := service.NewSessionService(repo)
	orderSvc := service.NewOrderService(repo)
	exitSvc := service.NewExitService(repo)
	ledgerSvc := service.NewLedgerService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "test-webhook-secret")
	analyticsSvc := service.NewAnalyticsService(repo, forecastProv)
	onboardSvc := service.NewOnboardingService(repo)

	handler := handlers.NewAPIHandler(
		sessionSvc,
		orderSvc,
		paymentSvc,
		exitSvc,
		ledgerSvc,
		analyticsSvc,
		onboardSvc,
		objStore,
		repo,
		"test-webhook-secret",
	)

	router := api.NewRouter(handler, repo, jwtSecret)
	return router, repo, jwtSecret
}

func TestGranularRoleBasedAccessControl(t *testing.T) {
	router, repo, jwtSecret := setupTestRouter(t)
	restID := uuid.New()

	_ = repo.CreateRestaurant(context.Background(), &restaurant.Restaurant{
		ID:     restID,
		Name:   "Test Bistro",
		Status: restaurant.StatusActive,
	})

	t.Run("Guard_Cannot_Confirm_Payment", func(t *testing.T) {
		guardID := uuid.New()
		guardToken, err := crypto.GenerateGuardJWT(jwtSecret, guardID, restID, 1*time.Hour)
		if err != nil {
			t.Fatalf("failed to generate guard token: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/payments/confirm", bytes.NewBuffer([]byte(`{}`)))
		req.Header.Set("Authorization", "Bearer "+guardToken)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Fatalf("expected guard payment confirm to be rejected with 401/403, got %d", w.Code)
		}
	})

	t.Run("Guard_Cannot_Force_Close_Session", func(t *testing.T) {
		guardID := uuid.New()
		guardToken, err := crypto.GenerateGuardJWT(jwtSecret, guardID, restID, 1*time.Hour)
		if err != nil {
			t.Fatalf("failed to generate guard token: %v", err)
		}

		sessionID := uuid.New()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/sessions/"+sessionID.String()+"/force-close", bytes.NewBuffer([]byte(`{"reason":"fraud"}`)))
		req.Header.Set("Authorization", "Bearer "+guardToken)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Fatalf("expected guard force close to be rejected with 401/403, got %d", w.Code)
		}
	})

	t.Run("Waiter_Cannot_Force_Close_Session", func(t *testing.T) {
		waiterID := uuid.New()
		waiterToken, err := crypto.GenerateStaffJWT(jwtSecret, waiterID, restID, string(restaurant.RoleWaiter), false, 1*time.Hour)
		if err != nil {
			t.Fatalf("failed to generate waiter token: %v", err)
		}

		sessionID := uuid.New()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/sessions/"+sessionID.String()+"/force-close", bytes.NewBuffer([]byte(`{"reason":"test"}`)))
		req.Header.Set("Authorization", "Bearer "+waiterToken)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("expected waiter force close to be 403 Forbidden, got %d", w.Code)
		}
	})

	t.Run("Waiter_Cannot_Access_Platform_Admin", func(t *testing.T) {
		waiterID := uuid.New()
		waiterToken, err := crypto.GenerateStaffJWT(jwtSecret, waiterID, restID, string(restaurant.RoleWaiter), false, 1*time.Hour)
		if err != nil {
			t.Fatalf("failed to generate waiter token: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/restaurants", nil)
		req.Header.Set("Authorization", "Bearer "+waiterToken)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("expected waiter token to be forbidden from /admin/*, got %d", w.Code)
		}
	})

	t.Run("Restaurant_Owner_Cannot_Access_Platform_Admin", func(t *testing.T) {
		ownerID := uuid.New()
		ownerToken, err := crypto.GenerateStaffJWT(jwtSecret, ownerID, restID, string(restaurant.RoleRestaurantOwner), false, 1*time.Hour)
		if err != nil {
			t.Fatalf("failed to generate owner token: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/restaurants", nil)
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("expected tenant owner to be strictly forbidden from platform admin, got %d", w.Code)
		}
	})

	t.Run("Platform_Super_Admin_Can_Access_Platform_Admin", func(t *testing.T) {
		superAdminID := uuid.New()
		adminToken, err := crypto.GenerateStaffJWT(jwtSecret, superAdminID, uuid.Nil, string(restaurant.RoleSuperAdmin), true, 1*time.Hour)
		if err != nil {
			t.Fatalf("failed to generate super admin token: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/restaurants", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected super admin to succeed on /admin/restaurants, got %d", w.Code)
		}
	})
}
