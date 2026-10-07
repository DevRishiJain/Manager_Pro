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
	"golang.org/x/crypto/bcrypt"
)

func TestFranchiseSuiteEndpoints(t *testing.T) {
	repo := memory.NewMemoryRepository()
	jwtSecret := []byte("test-jwt-secret-key-32bytes-long")

	passHash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)

	franchiseRestID := uuid.New()
	franchiseStaffID := uuid.New()
	ownerRestID := uuid.New()
	ownerStaffID := uuid.New()

	franchiseRest := &restaurant.Restaurant{
		ID:                 franchiseRestID,
		Name:               "Spice Route CP",
		Slug:               "spiceroute-cp",
		SubscriptionPlan:   "PRO",
		SubscriptionStatus: "ACTIVE",
		SubscriptionEndAt:  time.Now().UTC().Add(30 * 24 * time.Hour),
	}
	_ = repo.CreateRestaurant(nil, franchiseRest)
	_ = repo.CreateStaff(nil, &restaurant.StaffUser{
		ID:           franchiseStaffID,
		RestaurantID: franchiseRestID,
		EmployeeID:   "EMP-FRN-001",
		Name:         "Franchise Owner",
		Email:        "franchise@spiceroute.com",
		PasswordHash: string(passHash),
		Role:         restaurant.RoleFranchiseOwner,
		IsActive:     true,
	})

	ownerRest := &restaurant.Restaurant{
		ID:                 ownerRestID,
		Name:               "Spice Route Noida",
		Slug:               "spiceroute-noida",
		SubscriptionPlan:   "PRO",
		SubscriptionStatus: "ACTIVE",
		SubscriptionEndAt:  time.Now().UTC().Add(30 * 24 * time.Hour),
	}
	_ = repo.CreateRestaurant(nil, ownerRest)
	_ = repo.CreateStaff(nil, &restaurant.StaffUser{
		ID:           ownerStaffID,
		RestaurantID: ownerRestID,
		EmployeeID:   "EMP-OWN-001",
		Name:         "Outlet Owner",
		Email:        "owner@spiceroute.com",
		PasswordHash: string(passHash),
		Role:         restaurant.RoleRestaurantOwner,
		IsActive:     true,
	})

	staffSvc := service.NewStaffService(repo, jwtSecret)
	orderSvc := service.NewOrderService(repo)
	apiHandler := handlers.NewAPIHandler(nil, orderSvc, nil, nil, nil, nil, nil, nil, repo, "whsec")
	apiHandler.SetStaffService(staffSvc)
	apiHandler.SetJWTSecret(jwtSecret)

	router := api.NewRouter(apiHandler, repo, jwtSecret)

	franchiseToken, err := crypto.GenerateStaffJWT(jwtSecret, franchiseStaffID, franchiseRestID, "FRANCHISE_OWNER", false, 24*time.Hour)
	if err != nil {
		t.Fatalf("Failed to generate franchise JWT: %v", err)
	}

	ownerToken, err := crypto.GenerateStaffJWT(jwtSecret, ownerStaffID, ownerRestID, "RESTAURANT_OWNER", false, 24*time.Hour)
	if err != nil {
		t.Fatalf("Failed to generate owner JWT: %v", err)
	}

	// 1. Test Generate Franchise Invite Code (self-heals franchise for the owner)
	req1 := httptest.NewRequest("POST", "/api/v1/franchise/invite-code", nil)
	req1.Header.Set("Authorization", "Bearer "+franchiseToken)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for invite-code, got %d: %s", w1.Code, w1.Body.String())
	}

	var res1 map[string]interface{}
	if err := json.NewDecoder(w1.Body).Decode(&res1); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	code, ok := res1["code"].(string)
	if !ok || len(code) == 0 {
		t.Fatalf("Expected non-empty invite code in response")
	}
	if _, ok := res1["franchise_id"]; !ok {
		t.Fatalf("Expected franchise_id in invite-code response")
	}

	// 2. Public invite lookup returns valid details
	reqLookup := httptest.NewRequest("GET", "/api/v1/public/franchise/invite/"+code, nil)
	wLookup := httptest.NewRecorder()
	router.ServeHTTP(wLookup, reqLookup)
	if wLookup.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for public invite lookup, got %d", wLookup.Code)
	}
	var lookup map[string]interface{}
	_ = json.NewDecoder(wLookup.Body).Decode(&lookup)
	if valid, _ := lookup["valid"].(bool); !valid {
		t.Fatalf("Expected invite lookup to be valid: %v", lookup)
	}

	// 3. Test Link Restaurant using generated OTP code
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
		t.Fatalf("Expected status 200 for link-franchise, got %d: %s", w2.Code, w2.Body.String())
	}

	// 4. Test Direct Outlet Creation persists the restaurant
	createPayload, _ := json.Marshal(map[string]string{
		"name":     "Spice Route BKC",
		"slug":     "spiceroute-bkc",
		"email":    "bkc@spiceroute.com",
		"password": "password123",
	})
	req3 := httptest.NewRequest("POST", "/api/v1/franchise/outlets/create", bytes.NewBuffer(createPayload))
	req3.Header.Set("Authorization", "Bearer "+franchiseToken)
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for outlets/create, got %d: %s", w3.Code, w3.Body.String())
	}

	// 5. Outlets list now contains all three linked restaurants
	req4 := httptest.NewRequest("GET", "/api/v1/franchise/outlets", nil)
	req4.Header.Set("Authorization", "Bearer "+franchiseToken)
	w4 := httptest.NewRecorder()
	router.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for outlets list, got %d: %s", w4.Code, w4.Body.String())
	}
	var outlets []map[string]interface{}
	_ = json.NewDecoder(w4.Body).Decode(&outlets)
	if len(outlets) != 3 {
		t.Fatalf("Expected exactly 3 outlets, got %d: %s", len(outlets), w4.Body.String())
	}
}
