package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/devrishijain/table-manager/internal/api"
	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordResetFlow(t *testing.T) {
	repo := memory.NewMemoryRepository()
	jwtSecret := []byte("test-jwt-secret-key-32bytes-long")

	// Create a test staff member
	passHash, _ := bcrypt.GenerateFromPassword([]byte("OldPassword123!"), bcrypt.DefaultCost)
	staffID := uuid.New()
	restID := uuid.New()

	staff := &restaurant.StaffUser{
		ID:           staffID,
		RestaurantID: restID,
		Name:         "John Doe",
		Email:        "john@example.com",
		EmployeeID:   "EMP-WTR-001",
		Role:         restaurant.RoleWaiter,
		PasswordHash: string(passHash),
		IsActive:     true,
	}
	if err := repo.CreateStaff(nil, staff); err != nil {
		t.Fatalf("failed to seed staff user: %v", err)
	}

	staffSvc := service.NewStaffService(repo, jwtSecret)
	apiHandler := handlers.NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, repo, "whsec")
	apiHandler.SetStaffService(staffSvc)
	apiHandler.SetJWTSecret(jwtSecret)

	r := api.NewRouter(apiHandler, repo, jwtSecret)

	// Step 1: Request Password Reset
	forgotReqBody, _ := json.Marshal(map[string]string{
		"identifier": "john@example.com",
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/forgot-password", bytes.NewBuffer(forgotReqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on forgot password, got %d: %s", w.Code, w.Body.String())
	}

	var forgotResp struct {
		Message    string `json:"message"`
		ResetToken string `json:"reset_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &forgotResp); err != nil {
		t.Fatalf("failed to unmarshal forgot password response: %v", err)
	}

	if forgotResp.ResetToken == "" {
		t.Fatalf("expected non-empty reset_token in dev response")
	}

	// Step 2: Reset Password using the token
	resetReqBody, _ := json.Marshal(map[string]string{
		"token":        forgotResp.ResetToken,
		"new_password": "NewSecretPassword123!",
	})
	req2 := httptest.NewRequest("POST", "/api/v1/auth/reset-password", bytes.NewBuffer(resetReqBody))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on reset password, got %d: %s", w2.Code, w2.Body.String())
	}

	// Step 3: Verify user can login with new password
	loginReqBody, _ := json.Marshal(map[string]string{
		"identifier": "john@example.com",
		"password":   "NewSecretPassword123!",
	})
	req3 := httptest.NewRequest("POST", "/api/v1/auth/staff/login", bytes.NewBuffer(loginReqBody))
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on login with new password, got %d: %s", w3.Code, w3.Body.String())
	}

	// Step 4: Reusing the same reset token should fail (single-use enforcement)
	w4 := httptest.NewRecorder()
	r.ServeHTTP(w4, req2)
	if w4.Code == http.StatusOK {
		t.Fatalf("expected reset token reuse to fail, but got 200 OK")
	}
}
