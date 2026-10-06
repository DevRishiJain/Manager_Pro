package tests

import (
	"bytes"
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
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func TestSubscriptionMiddlewareAndRenewalFlow(t *testing.T) {
	repo := memory.NewMemoryRepository()
	jwtSecret := []byte("test-jwt-secret-key-32bytes-long")

	passHash, _ := bcrypt.GenerateFromPassword([]byte("Password123!"), bcrypt.DefaultCost)
	staffID := uuid.New()
	restID := uuid.New()

	// 1. Create restaurant with an EXPIRED subscription
	rest := &restaurant.Restaurant{
		ID:                 restID,
		Name:               "Expired Bistro",
		SubscriptionPlan:   "PRO",
		SubscriptionStatus: "EXPIRED",
		SubscriptionEndAt:  time.Now().UTC().Add(-2 * 24 * time.Hour),
	}
	if err := repo.CreateRestaurant(nil, rest); err != nil {
		t.Fatalf("failed to seed restaurant: %v", err)
	}

	staff := &restaurant.StaffUser{
		ID:           staffID,
		RestaurantID: restID,
		Name:         "Alice Admin",
		Email:        "alice@bistro.com",
		EmployeeID:   "EMP-ADM-001",
		Role:         restaurant.RoleRestaurantAdmin,
		PasswordHash: string(passHash),
		IsActive:     true,
	}
	if err := repo.CreateStaff(nil, staff); err != nil {
		t.Fatalf("failed to seed staff user: %v", err)
	}

	staffSvc := service.NewStaffService(repo, jwtSecret)
	orderSvc := service.NewOrderService(repo)
	apiHandler := handlers.NewAPIHandler(nil, orderSvc, nil, nil, nil, nil, nil, nil, repo, "whsec")
	apiHandler.SetStaffService(staffSvc)
	apiHandler.SetJWTSecret(jwtSecret)

	r := api.NewRouter(apiHandler, repo, jwtSecret)

	// Step 1: Login MUST succeed even when subscription is EXPIRED
	loginReqBody, _ := json.Marshal(map[string]string{
		"identifier": "alice@bistro.com",
		"password":   "Password123!",
	})
	reqLogin := httptest.NewRequest("POST", "/api/v1/auth/staff/login", bytes.NewBuffer(loginReqBody))
	reqLogin.Header.Set("Content-Type", "application/json")
	wLogin := httptest.NewRecorder()
	r.ServeHTTP(wLogin, reqLogin)

	if wLogin.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on login for expired subscription, got %d: %s", wLogin.Code, wLogin.Body.String())
	}

	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(wLogin.Body.Bytes(), &loginResp); err != nil || loginResp.Token == "" {
		t.Fatalf("failed to parse login token response: %v", err)
	}

	// Step 2: Operational endpoint (e.g. List Pending Orders) MUST return 402 Payment Required
	reqOperational := httptest.NewRequest("GET", "/api/v1/staff/orders/pending", nil)
	reqOperational.Header.Set("Authorization", "Bearer "+loginResp.Token)
	wOperational := httptest.NewRecorder()
	r.ServeHTTP(wOperational, reqOperational)

	if wOperational.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402 Payment Required for operational endpoint, got %d: %s", wOperational.Code, wOperational.Body.String())
	}

	// Step 3: Fetch Subscription endpoint MUST work (unblocked)
	reqSub := httptest.NewRequest("GET", "/api/v1/restaurant/subscription", nil)
	reqSub.Header.Set("Authorization", "Bearer "+loginResp.Token)
	wSub := httptest.NewRecorder()
	r.ServeHTTP(wSub, reqSub)

	if wSub.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on subscription status check, got %d: %s", wSub.Code, wSub.Body.String())
	}

	var subResp struct {
		IsActive      bool `json:"is_active"`
		DaysRemaining int  `json:"days_remaining"`
	}
	_ = json.Unmarshal(wSub.Body.Bytes(), &subResp)
	if subResp.IsActive {
		t.Fatalf("expected IsActive == false for expired subscription")
	}

	// Step 4: Call Renewal Endpoint to extend by 30 days
	renewReqBody, _ := json.Marshal(map[string]int{
		"days": 30,
	})
	reqRenew := httptest.NewRequest("POST", "/api/v1/restaurant/subscription/renew", bytes.NewBuffer(renewReqBody))
	reqRenew.Header.Set("Authorization", "Bearer "+loginResp.Token)
	reqRenew.Header.Set("Content-Type", "application/json")
	wRenew := httptest.NewRecorder()
	r.ServeHTTP(wRenew, reqRenew)

	if wRenew.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on subscription renewal, got %d: %s", wRenew.Code, wRenew.Body.String())
	}

	// Step 5: Operational endpoint MUST now succeed (200 OK)!
	wOperational2 := httptest.NewRecorder()
	r.ServeHTTP(wOperational2, reqOperational)

	if wOperational2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on operational endpoint after renewal, got %d: %s", wOperational2.Code, wOperational2.Body.String())
	}
}
