package tests

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

func TestPhase5TenantAndPlatformOperationsEndpoints(t *testing.T) {
	router, repo, jwtSecret := setupTestRouter(t)
	restID := uuid.New()
	managerID := uuid.New()
	superAdminID := uuid.New()

	_ = repo.CreateRestaurant(context.Background(), &restaurant.Restaurant{
		ID:                restID,
		Name:              "Spice Symphony",
		CommissionRateBps: 100,
		Status:            restaurant.StatusActive,
		Timezone:          "Asia/Kolkata",
	})

	_ = repo.CreateStaff(context.Background(), &restaurant.StaffUser{
		ID:           managerID,
		RestaurantID: restID,
		Name:         "Manager Rajiv",
		Email:        "rajiv@spicesymphony.com",
		Role:         restaurant.RoleRestaurantAdmin,
		IsActive:     true,
	})

	managerToken, _ := crypto.GenerateStaffJWT(jwtSecret, managerID, restID, string(restaurant.RoleRestaurantAdmin), false, 1*time.Hour)
	superAdminToken, _ := crypto.GenerateStaffJWT(jwtSecret, superAdminID, uuid.Nil, string(restaurant.RoleSuperAdmin), true, 1*time.Hour)

	// 1. Month-to-date analytics
	reqMTD := httptest.NewRequest(http.MethodGet, "/api/v1/restaurant/analytics/month-to-date", nil)
	reqMTD.Header.Set("Authorization", "Bearer "+managerToken)
	wMTD := httptest.NewRecorder()
	router.ServeHTTP(wMTD, reqMTD)
	if wMTD.Code != http.StatusOK {
		t.Errorf("expected 200 for month-to-date analytics, got %d", wMTD.Code)
	}

	// 2. Period comparison
	reqComp := httptest.NewRequest(http.MethodGet, "/api/v1/restaurant/analytics/compare", nil)
	reqComp.Header.Set("Authorization", "Bearer "+managerToken)
	wComp := httptest.NewRecorder()
	router.ServeHTTP(wComp, reqComp)
	if wComp.Code != http.StatusOK {
		t.Errorf("expected 200 for period comparison, got %d", wComp.Code)
	}

	// 3. Peak hours analysis
	reqPeak := httptest.NewRequest(http.MethodGet, "/api/v1/restaurant/analytics/peak-hours", nil)
	reqPeak.Header.Set("Authorization", "Bearer "+managerToken)
	wPeak := httptest.NewRecorder()
	router.ServeHTTP(wPeak, reqPeak)
	if wPeak.Code != http.StatusOK {
		t.Errorf("expected 200 for peak hours analysis, got %d", wPeak.Code)
	}

	// 4. Settlements list
	reqSet := httptest.NewRequest(http.MethodGet, "/api/v1/restaurant/settlements", nil)
	reqSet.Header.Set("Authorization", "Bearer "+managerToken)
	wSet := httptest.NewRecorder()
	router.ServeHTTP(wSet, reqSet)
	if wSet.Code != http.StatusOK {
		t.Errorf("expected 200 for settlements, got %d", wSet.Code)
	}

	// 5. Menu Category Creation & Listing
	reqCatCreate := httptest.NewRequest(http.MethodPost, "/api/v1/restaurant/menu/categories", bytes.NewBuffer([]byte(`{"name":"Desserts","display_order":3}`)))
	reqCatCreate.Header.Set("Authorization", "Bearer "+managerToken)
	reqCatCreate.Header.Set("Content-Type", "application/json")
	wCatCreate := httptest.NewRecorder()
	router.ServeHTTP(wCatCreate, reqCatCreate)
	if wCatCreate.Code != http.StatusCreated {
		t.Errorf("expected 201 for category create, got %d", wCatCreate.Code)
	}

	reqCatList := httptest.NewRequest(http.MethodGet, "/api/v1/restaurant/menu/categories", nil)
	reqCatList.Header.Set("Authorization", "Bearer "+managerToken)
	wCatList := httptest.NewRecorder()
	router.ServeHTTP(wCatList, reqCatList)
	if wCatList.Code != http.StatusOK {
		t.Errorf("expected 200 for category list, got %d", wCatList.Code)
	}

	// 6. Settings GET and PUT
	reqSettingsGet := httptest.NewRequest(http.MethodGet, "/api/v1/restaurant/settings", nil)
	reqSettingsGet.Header.Set("Authorization", "Bearer "+managerToken)
	wSettingsGet := httptest.NewRecorder()
	router.ServeHTTP(wSettingsGet, reqSettingsGet)
	if wSettingsGet.Code != http.StatusOK {
		t.Errorf("expected 200 for settings GET, got %d", wSettingsGet.Code)
	}

	reqSettingsPut := httptest.NewRequest(http.MethodPut, "/api/v1/restaurant/settings", bytes.NewBuffer([]byte(`{
		"exit_verification_mode":"EXIT_GUARD_ENABLED",
		"shared_session_policy":"SHARED_TABLE_SESSION",
		"high_value_threshold_minor":600000
	}`)))
	reqSettingsPut.Header.Set("Authorization", "Bearer "+managerToken)
	reqSettingsPut.Header.Set("Content-Type", "application/json")
	wSettingsPut := httptest.NewRecorder()
	router.ServeHTTP(wSettingsPut, reqSettingsPut)
	if wSettingsPut.Code != http.StatusOK {
		t.Errorf("expected 200 for settings PUT, got %d", wSettingsPut.Code)
	}

	// 7. Payment Proof Upload
	validJPEG := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00}
	reqUpload := httptest.NewRequest(http.MethodPost, "/api/v1/restaurant/upload-proof?payment_id="+uuid.New().String(), bytes.NewBuffer(validJPEG))
	reqUpload.Header.Set("Authorization", "Bearer "+managerToken)
	reqUpload.Header.Set("Content-Type", "image/jpeg")
	wUpload := httptest.NewRecorder()
	router.ServeHTTP(wUpload, reqUpload)
	if wUpload.Code != http.StatusCreated {
		t.Errorf("expected 201 for valid payment proof upload, got %d: %s", wUpload.Code, wUpload.Body.String())
	}

	// 8. Platform Admin: Get Restaurant Details
	reqAdminDetail := httptest.NewRequest(http.MethodGet, "/api/v1/admin/restaurants/"+restID.String(), nil)
	reqAdminDetail.Header.Set("Authorization", "Bearer "+superAdminToken)
	wAdminDetail := httptest.NewRecorder()
	router.ServeHTTP(wAdminDetail, reqAdminDetail)
	if wAdminDetail.Code != http.StatusOK {
		t.Errorf("expected 200 for admin restaurant details, got %d", wAdminDetail.Code)
	}

	// 9. Platform Admin: Global Analytics
	reqAdminAnalytics := httptest.NewRequest(http.MethodGet, "/api/v1/admin/analytics/platform", nil)
	reqAdminAnalytics.Header.Set("Authorization", "Bearer "+superAdminToken)
	wAdminAnalytics := httptest.NewRecorder()
	router.ServeHTTP(wAdminAnalytics, reqAdminAnalytics)
	if wAdminAnalytics.Code != http.StatusOK {
		t.Errorf("expected 200 for platform global analytics, got %d", wAdminAnalytics.Code)
	}

	// 10. Platform Admin: Fraud Review Queue
	reqFraud := httptest.NewRequest(http.MethodGet, "/api/v1/admin/fraud-review", nil)
	reqFraud.Header.Set("Authorization", "Bearer "+superAdminToken)
	wFraud := httptest.NewRecorder()
	router.ServeHTTP(wFraud, reqFraud)
	if wFraud.Code != http.StatusOK {
		t.Errorf("expected 200 for fraud review queue, got %d", wFraud.Code)
	}

	// 11. Platform Admin: Suspend and Reactivate
	reqSuspend := httptest.NewRequest(http.MethodPost, "/api/v1/admin/restaurants/"+restID.String()+"/suspend", bytes.NewBuffer([]byte(`{"reason":"Compliance audit review"}`)))
	reqSuspend.Header.Set("Authorization", "Bearer "+superAdminToken)
	reqSuspend.Header.Set("Content-Type", "application/json")
	wSuspend := httptest.NewRecorder()
	router.ServeHTTP(wSuspend, reqSuspend)
	if wSuspend.Code != http.StatusOK {
		t.Errorf("expected 200 for restaurant suspend, got %d", wSuspend.Code)
	}

	reqReactivate := httptest.NewRequest(http.MethodPost, "/api/v1/admin/restaurants/"+restID.String()+"/reactivate", nil)
	reqReactivate.Header.Set("Authorization", "Bearer "+superAdminToken)
	wReactivate := httptest.NewRecorder()
	router.ServeHTTP(wReactivate, reqReactivate)
	if wReactivate.Code != http.StatusOK {
		t.Errorf("expected 200 for restaurant reactivate, got %d", wReactivate.Code)
	}
}
