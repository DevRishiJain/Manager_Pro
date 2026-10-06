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
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

func TestFranchiseSuiteEndpoints(t *testing.T) {
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

	franchiseToken, err := crypto.GenerateStaffJWT(jwtSecret, staffID, restID, "FRANCHISE_OWNER", false, 24*time.Hour)
	if err != nil {
		t.Fatalf("Failed to generate franchise JWT: %v", err)
	}

	ownerToken, err := crypto.GenerateStaffJWT(jwtSecret, uuid.New(), restID, "RESTAURANT_OWNER", false, 24*time.Hour)
	if err != nil {
		t.Fatalf("Failed to generate owner JWT: %v", err)
	}

	// 1. Test Generate Franchise Invite Code
	req1 := httptest.NewRequest("POST", "/api/v1/franchise/invite-code", nil)
	req1.Header.Set("Authorization", "Bearer "+franchiseToken)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for invite-code, got %d", w1.Code)
	}

	var res1 map[string]interface{}
	if err := json.NewDecoder(w1.Body).Decode(&res1); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	code, ok := res1["code"].(string)
	if !ok || len(code) == 0 {
		t.Fatalf("Expected non-empty invite code in response")
	}

	// 2. Test Link Restaurant using generated OTP code
	linkPayload, _ := json.Marshal(map[string]string{
		"code":     code,
		"password": "password123",
	})
	req2 := httptest.NewRequest("POST", "/api/v1/restaurant/link-franchise", bytes.NewBuffer(linkPayload))
	req2.Header.Set("Authorization", "Bearer "+ownerToken)
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for link-franchise, got %d", w2.Code)
	}

	// 3. Test Direct Outlet Creation
	createPayload, _ := json.Marshal(map[string]string{
		"name":  "Spice Route BKC",
		"slug":  "spiceroute-bkc",
		"email": "bkc@spiceroute.com",
	})
	req3 := httptest.NewRequest("POST", "/api/v1/franchise/outlets/create", bytes.NewBuffer(createPayload))
	req3.Header.Set("Authorization", "Bearer "+franchiseToken)
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for outlets/create, got %d", w3.Code)
	}
}
