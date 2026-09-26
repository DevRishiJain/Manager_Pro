package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/devrishijain/table-manager/internal/api"
	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/google/uuid"
)

func TestVenueTypesAndDriveInVehicleFlow(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	jwtSecret := []byte("test-super-secret-jwt-key-32-bytes!")

	sessionSvc := service.NewSessionService(repo)
	orderSvc := service.NewOrderService(repo)
	ledgerSvc := service.NewLedgerService(repo)
	exitSvc := service.NewExitService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "secret-123")
	analyticsSvc := service.NewAnalyticsService(repo, nil)
	onboardingSvc := service.NewOnboardingService(repo)
	staffSvc := service.NewStaffService(repo, jwtSecret)

	apiHandler := handlers.NewAPIHandler(
		sessionSvc,
		orderSvc,
		paymentSvc,
		exitSvc,
		ledgerSvc,
		analyticsSvc,
		onboardingSvc,
		nil,
		repo,
		"whsec_test",
	)
	apiHandler.SetStaffService(staffSvc)
	router := api.NewRouter(apiHandler, repo, jwtSecret)

	// 1. Test Onboarding a Hotel
	hotelReq := map[string]interface{}{
		"restaurant_name": "Grand Palace Hotel",
		"venue_type":      "HOTEL",
		"table_count":     5, // 5 rooms
		"admin": map[string]string{
			"email":    "manager@grandpalace.com",
			"name":     "Hotel Manager",
			"password": "Password123!",
		},
	}
	hotelJSON, _ := json.Marshal(hotelReq)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/onboard", bytes.NewBuffer(hotelJSON))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("hotel onboarding failed: %d: %s", rec.Code, rec.Body.String())
	}
	var hotelResp struct {
		Restaurant restaurant.Restaurant `json:"restaurant"`
		Tables     []restaurant.Table    `json:"tables"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &hotelResp)
	if hotelResp.Restaurant.VenueType != restaurant.VenueTypeHotel {
		t.Fatalf("expected VenueType HOTEL, got %s", hotelResp.Restaurant.VenueType)
	}
	if len(hotelResp.Tables) != 5 {
		t.Fatalf("expected 5 rooms, got %d", len(hotelResp.Tables))
	}
	if hotelResp.Tables[0].TableNumber != "Room 101" {
		t.Fatalf("expected first room to be 'Room 101', got '%s'", hotelResp.Tables[0].TableNumber)
	}

	// 2. Test Onboarding a Drive-In / Car-O-Bar
	driveInReq := map[string]interface{}{
		"restaurant_name": "Highway Car-O-Bar",
		"venue_type":      "DRIVE_IN",
		"admin": map[string]string{
			"email":    "owner@carobar.com",
			"name":     "Car-O-Bar Owner",
			"password": "Password123!",
		},
	}
	driveInJSON, _ := json.Marshal(driveInReq)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/public/onboard", bytes.NewBuffer(driveInJSON))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("drive-in onboarding failed: %d: %s", rec.Code, rec.Body.String())
	}
	var driveInResp struct {
		Restaurant restaurant.Restaurant `json:"restaurant"`
		Tables     []restaurant.Table    `json:"tables"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &driveInResp)
	if driveInResp.Restaurant.VenueType != restaurant.VenueTypeDriveIn {
		t.Fatalf("expected VenueType DRIVE_IN, got %s", driveInResp.Restaurant.VenueType)
	}
	if len(driveInResp.Tables) != 1 {
		t.Fatalf("expected 1 universal static QR for drive-in, got %d", len(driveInResp.Tables))
	}
	driveInTable := driveInResp.Tables[0]
	if driveInTable.TableNumber != "Drive-In Universal" {
		t.Fatalf("expected 'Drive-In Universal', got '%s'", driveInTable.TableNumber)
	}

	// 3. Customer scans Drive-In QR code and enters Vehicle Number Plate
	scanReq := map[string]interface{}{
		"table_token":        driveInTable.TableToken,
		"customer_name":      "Vikramaditya",
		"customer_phone":     "9876543210",
		"vehicle_number":     "DL 01 AB 1234",
		"guest_count":        3,
		"device_fingerprint": "fp-car-driver-1",
	}
	scanJSON, _ := json.Marshal(scanReq)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/session/start", bytes.NewBuffer(scanJSON))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("session start failed: %d: %s", rec.Code, rec.Body.String())
	}
	var sess session.DiningSession
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)
	if sess.VehicleNumber != "DL 01 AB 1234" {
		t.Fatalf("expected VehicleNumber 'DL 01 AB 1234', got '%s'", sess.VehicleNumber)
	}

	// 4. Seed menu item for drive-in restaurant and place order
	catID := uuid.New()
	_ = repo.CreateCategory(ctx, &restaurant.MenuCategory{
		ID:           catID,
		RestaurantID: driveInResp.Restaurant.ID,
		Name:         "Quick Bites",
	})
	menuItemID := uuid.New()
	_ = repo.CreateMenuItem(ctx, &restaurant.MenuItem{
		ID:           menuItemID,
		RestaurantID: driveInResp.Restaurant.ID,
		CategoryID:   catID,
		Name:         "Loaded Nachos & Wings",
		IsAvailable:  true,
	})

	cartPayload := map[string]interface{}{
		"items": []map[string]interface{}{
			{
				"menu_item_id": menuItemID,
				"quantity":     1,
			},
		},
	}
	cartJSON, _ := json.Marshal(cartPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/session/"+sess.ID.String()+"/orders", bytes.NewBuffer(cartJSON))
	req.Header.Set("X-Session-Token", sess.SessionToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("place order failed: %d: %s", rec.Code, rec.Body.String())
	}

	var placeResp struct {
		Order order.Order `json:"order"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &placeResp)
	ord := placeResp.Order

	// Invariant: TableNumber MUST be bound to the vehicle number plate for Drive-In orders!
	if ord.TableNumber != "Car DL 01 AB 1234" {
		t.Fatalf("expected TableNumber 'Car DL 01 AB 1234', got '%s'", ord.TableNumber)
	}
	if ord.VehicleNumber != "DL 01 AB 1234" {
		t.Fatalf("expected VehicleNumber 'DL 01 AB 1234', got '%s'", ord.VehicleNumber)
	}

	// 5. Kitchen Queue verification
	kitchenOrders, err := repo.ListKitchenQueue(ctx, driveInResp.Restaurant.ID, []order.State{order.StatePlacedUnverified, order.StatePlacedVerified})
	if err != nil || len(kitchenOrders) != 1 {
		t.Fatalf("expected 1 kitchen order, got %d (err: %v)", len(kitchenOrders), err)
	}
	if kitchenOrders[0].TableNumber != "Car DL 01 AB 1234" {
		t.Fatalf("expected kitchen order to have TableNumber 'Car DL 01 AB 1234', got '%s'", kitchenOrders[0].TableNumber)
	}
	if kitchenOrders[0].CustomerName != "Vikramaditya" {
		t.Fatalf("expected kitchen order CustomerName 'Vikramaditya', got '%s'", kitchenOrders[0].CustomerName)
	}
}
