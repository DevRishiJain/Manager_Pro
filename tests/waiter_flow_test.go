package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/api"
	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

func TestWaiterScreenAndStaffAPIs(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	jwtSecret := []byte("test-super-secret-jwt-key-32-bytes!")

	// Seed restaurant
	restID := uuid.New()
	rest := &restaurant.Restaurant{
		ID:                restID,
		Name:              "Spice Route Bistro",
		CommissionRateBps: 200,
		Status:            restaurant.StatusActive,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	_ = repo.CreateRestaurant(ctx, rest)

	// Seed table
	tableID := uuid.New()
	tbl := &restaurant.Table{
		ID:           tableID,
		RestaurantID: restID,
		TableNumber:  "Table 7",
		TableToken:   "tok-table-7",
		IsActive:     true,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	_ = repo.CreateTable(ctx, tbl)

	// Seed menu item
	catID := uuid.New()
	_ = repo.CreateCategory(ctx, &restaurant.MenuCategory{
		ID:           catID,
		RestaurantID: restID,
		Name:         "Starters",
	})
	menuItemID := uuid.New()
	_ = repo.CreateMenuItem(ctx, &restaurant.MenuItem{
		ID:           menuItemID,
		RestaurantID: restID,
		CategoryID:   catID,
		Name:         "Paneer Tikka",
		Price:        money.New(25000),
		IsAvailable:  true,
		CGSTRateBps:  250,
		SGSTRateBps:  250,
	})

	// Initialize services
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

	// 1. Create Staff via Admin
	adminID := uuid.New()
	adminToken, err := crypto.GenerateStaffJWT(jwtSecret, adminID, restID, "RESTAURANT_ADMIN", false, 24*time.Hour)
	if err != nil {
		t.Fatalf("failed to generate admin token: %v", err)
	}

	createStaffBody, _ := json.Marshal(map[string]interface{}{
		"name":     "Ravi Kumar",
		"phone":    "+919876543210",
		"email":    "ravi@spiceroute.com",
		"password": "Password123!",
		"role":     "WAITER",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurant/staff", bytes.NewReader(createStaffBody))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on staff creation, got %d: %s", rec.Code, rec.Body.String())
	}

	var createdStaff restaurant.StaffUser
	_ = json.Unmarshal(rec.Body.Bytes(), &createdStaff)
	if createdStaff.EmployeeID != "EMP-WTR-001" {
		t.Fatalf("expected employee ID EMP-WTR-001, got '%s'", createdStaff.EmployeeID)
	}

	// 2. Waiter Login with Email
	loginBody, _ := json.Marshal(map[string]interface{}{
		"identifier": "ravi@spiceroute.com",
		"password":   "Password123!",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/staff/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on waiter login, got %d: %s", rec.Code, rec.Body.String())
	}

	var loginResp struct {
		Token string               `json:"token"`
		Staff restaurant.StaffUser `json:"staff"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &loginResp)
	if loginResp.Staff.EmployeeID != "EMP-WTR-001" {
		t.Fatalf("expected logged-in employee ID EMP-WTR-001, got '%s'", loginResp.Staff.EmployeeID)
	}
	waiterToken := loginResp.Token

	// 3. Customer Session & Order Creation
	sess, _, err := sessionSvc.StartSession(ctx, tbl.TableToken, "device-1", "Customer", "+919876543210", "", 2, "fp-1")
	if err != nil {
		t.Fatalf("failed to start session: %v", err)
	}

	ord, _, err := orderSvc.PlaceOrder(ctx, sess.ID, []order.CartItem{
		{
			MenuItemID: menuItemID,
			Quantity:   2,
		},
	})
	if err != nil {
		t.Fatalf("failed to place order: %v", err)
	}

	// 4. Kitchen queue MUST NOT see this order yet
	kitchenToken, _ := crypto.GenerateStaffJWT(jwtSecret, uuid.New(), restID, "KITCHEN", false, 24*time.Hour)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/kitchen/orders/queue", nil)
	req.Header.Set("Authorization", "Bearer "+kitchenToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for kitchen queue, got %d: %s", rec.Code, rec.Body.String())
	}
	var kitchenQueue []order.Order
	_ = json.Unmarshal(rec.Body.Bytes(), &kitchenQueue)
	if len(kitchenQueue) != 0 {
		t.Fatalf("expected kitchen queue to be empty before order acceptance, got %d orders", len(kitchenQueue))
	}

	// 5. Waiter pending orders API MUST show this order with table number
	req = httptest.NewRequest(http.MethodGet, "/api/v1/staff/orders/pending", nil)
	req.Header.Set("Authorization", "Bearer "+waiterToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for waiter pending orders, got %d: %s", rec.Code, rec.Body.String())
	}
	var pendingOrders []handlers.PendingOrderResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &pendingOrders)
	if len(pendingOrders) != 1 {
		t.Fatalf("expected 1 pending order for waiter, got %d", len(pendingOrders))
	}
	if pendingOrders[0].TableNumber != "Table 7" {
		t.Fatalf("expected Table 7, got '%s'", pendingOrders[0].TableNumber)
	}

	// 6. Waiter accepts the order
	req = httptest.NewRequest(http.MethodPost, "/api/v1/staff/orders/"+ord.ID.String()+"/accept", nil)
	req.Header.Set("Authorization", "Bearer "+waiterToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on order accept, got %d: %s", rec.Code, rec.Body.String())
	}
	var acceptedOrder order.Order
	_ = json.Unmarshal(rec.Body.Bytes(), &acceptedOrder)
	if acceptedOrder.Status != order.StateAccepted {
		t.Fatalf("expected status ACCEPTED, got %s", acceptedOrder.Status)
	}
	if acceptedOrder.AcceptedByStaffID == nil || *acceptedOrder.AcceptedByStaffID != createdStaff.ID {
		t.Fatalf("expected order AcceptedByStaffID to be waiter ID %s, got %v", createdStaff.ID, acceptedOrder.AcceptedByStaffID)
	}

	// 7. Kitchen queue MUST now see this accepted order
	req = httptest.NewRequest(http.MethodGet, "/api/v1/kitchen/orders/queue", nil)
	req.Header.Set("Authorization", "Bearer "+kitchenToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	_ = json.Unmarshal(rec.Body.Bytes(), &kitchenQueue)
	if len(kitchenQueue) != 1 {
		t.Fatalf("expected kitchen queue to have 1 order after acceptance, got %d", len(kitchenQueue))
	}
	if kitchenQueue[0].ID != ord.ID {
		t.Fatalf("expected kitchen queue order ID %s, got %s", ord.ID, kitchenQueue[0].ID)
	}

	// 8. Pending orders queue for waiter is now cleared
	req = httptest.NewRequest(http.MethodGet, "/api/v1/staff/orders/pending", nil)
	req.Header.Set("Authorization", "Bearer "+waiterToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	_ = json.Unmarshal(rec.Body.Bytes(), &pendingOrders)
	if len(pendingOrders) != 0 {
		t.Fatalf("expected 0 pending orders after acceptance, got %d", len(pendingOrders))
	}

	// 9. Verify StaffAction audit log was recorded with waiter's staff ID
	staffActions, err := repo.ListStaffActions(ctx, restID, 10, 0)
	if err != nil {
		t.Fatalf("failed to list staff actions: %v", err)
	}
	foundAcceptAction := false
	for _, action := range staffActions {
		if action.ActionType == "ORDER_ACCEPTED" && action.StaffID == createdStaff.ID {
			foundAcceptAction = true
			break
		}
	}
	if !foundAcceptAction {
		t.Fatalf("expected to find ORDER_ACCEPTED staff action for waiter %s", createdStaff.ID)
	}
}
