package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/api"
	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

func TestFranchiseEndpoints(t *testing.T) {
	repo := memory.NewMemoryRepository()
	jwtSecret := []byte("test-jwt-secret-key-32bytes-long")

	staffID := uuid.New()
	restID := uuid.New()

	rest := &restaurant.Restaurant{
		ID:                 restID,
		Name:               "Spice Route CP",
		SubscriptionPlan:   "PRO",
		SubscriptionStatus: "ACTIVE",
		SubscriptionEndAt:  time.Now().UTC().Add(30 * 24 * time.Hour),
	}
	_ = repo.CreateRestaurant(nil, rest)

	staffSvc := service.NewStaffService(repo, jwtSecret)
	orderSvc := service.NewOrderService(repo)
	apiHandler := handlers.NewAPIHandler(nil, orderSvc, nil, nil, nil, nil, nil, nil, repo, "whsec")
	apiHandler.SetStaffService(staffSvc)
	apiHandler.SetJWTSecret(jwtSecret)

	router := api.NewRouter(apiHandler, repo, jwtSecret)

	tokenStr, err := crypto.GenerateStaffJWT(jwtSecret, staffID, restID, "SUPER_ADMIN", true, 24*time.Hour)
	if err != nil {
		t.Fatalf("failed to generate staff token: %v", err)
	}

	// 1. Test GET /api/v1/franchise/outlets
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/franchise/outlets", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 for GET /api/v1/franchise/outlets, got %d. Body: %s", w.Code, w.Body.String())
	}

	var outlets []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &outlets); err != nil {
		t.Fatalf("failed to parse outlets response: %v", err)
	}
	if len(outlets) == 0 {
		t.Fatalf("expected at least 1 franchise outlet in response")
	}

	// 2. Test GET /api/v1/franchise/summary
	reqSum, _ := http.NewRequest(http.MethodGet, "/api/v1/franchise/summary", nil)
	reqSum.Header.Set("Authorization", "Bearer "+tokenStr)
	wSum := httptest.NewRecorder()
	router.ServeHTTP(wSum, reqSum)

	if wSum.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 for GET /api/v1/franchise/summary, got %d. Body: %s", wSum.Code, wSum.Body.String())
	}

	var summary map[string]interface{}
	if err := json.Unmarshal(wSum.Body.Bytes(), &summary); err != nil {
		t.Fatalf("failed to parse franchise summary response: %v", err)
	}
	if _, ok := summary["total_outlets"]; !ok {
		t.Fatalf("expected total_outlets key in summary response")
	}
}
