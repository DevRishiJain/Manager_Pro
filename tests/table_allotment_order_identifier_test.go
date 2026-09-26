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
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/google/uuid"
)

func TestTableAllotmentAndOrderUniqueDinerIdentification(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	jwtSecret := []byte("test-super-secret-jwt-key-32-bytes!")
	restID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

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

	// 1. Authenticate Waiter & Kitchen
	waiterBody := map[string]string{
		"identifier": "EMP-WTR-001",
		"password":   "password123",
	}
	waiterJSON, _ := json.Marshal(waiterBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/login", bytes.NewBuffer(waiterJSON))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("waiter login failed with status %d: %s", rec.Code, rec.Body.String())
	}
	var waiterResp struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &waiterResp)
	waiterToken := waiterResp.Token

	// Kitchen login
	kitchenBody := map[string]string{
		"identifier": "EMP-KIT-001",
		"password":   "password123",
	}
	kitchenJSON, _ := json.Marshal(kitchenBody)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/staff/login", bytes.NewBuffer(kitchenJSON))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var kitchenResp struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &kitchenResp)
	kitchenToken := kitchenResp.Token

	// 2. Customer sits at Table 4 (QR Token "TBL-004") and starts session
	scanReqBody := map[string]interface{}{
		"table_token":         "TBL-004",
		"display_name":        "Dev Rishi Jain",
		"customer_phone":      "9876543210",
		"guest_count":         4,
		"device_fingerprint":  "fp-test-safari-mac",
	}
	scanJSON, _ := json.Marshal(scanReqBody)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/session/start", bytes.NewBuffer(scanJSON))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("session creation failed: status %d: %s", rec.Code, rec.Body.String())
	}

	var sess session.DiningSession
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)
	if sess.CustomerName != "Dev Rishi Jain" {
		t.Fatalf("expected CustomerName 'Dev Rishi Jain', got '%s'", sess.CustomerName)
	}
	if sess.CustomerPhone != "9876543210" {
		t.Fatalf("expected CustomerPhone '9876543210', got '%s'", sess.CustomerPhone)
	}
	if sess.GuestCount != 4 {
		t.Fatalf("expected GuestCount 4, got %d", sess.GuestCount)
	}

	// Verify table assigned is indeed Table 4
	assignedTable, err := repo.GetTableByID(ctx, sess.TableID)
	if err != nil || assignedTable == nil {
		t.Fatalf("failed to retrieve table by ID: %v", err)
	}
	if assignedTable.TableNumber != "Table 4" {
		t.Fatalf("expected TableNumber 'Table 4', got '%s'", assignedTable.TableNumber)
	}

	// 3. Customer places order
	menuItems, _ := repo.ListMenuItems(ctx, restID)
	if len(menuItems) == 0 {
		t.Fatalf("no menu items available")
	}

	cartPayload := map[string]interface{}{
		"items": []map[string]interface{}{
			{
				"menu_item_id": menuItems[0].ID,
				"quantity":     2,
				"special_instructions": "Make it extra crisp",
			},
		},
	}
	cartJSON, _ := json.Marshal(cartPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/session/"+sess.ID.String()+"/orders", bytes.NewBuffer(cartJSON))
	req.Header.Set("X-Session-Token", sess.SessionToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("place order failed with status %d: %s", rec.Code, rec.Body.String())
	}

	var placeOrderResp struct {
		Order order.Order `json:"order"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &placeOrderResp)
	createdOrder := placeOrderResp.Order

	// Invariants check on placed order
	if createdOrder.TableNumber != "Table 4" {
		t.Fatalf("expected placed order to have TableNumber 'Table 4', got '%s'", createdOrder.TableNumber)
	}
	if createdOrder.CustomerName != "Dev Rishi Jain" {
		t.Fatalf("expected placed order CustomerName 'Dev Rishi Jain', got '%s'", createdOrder.CustomerName)
	}
	if createdOrder.CustomerPhone != "9876543210" {
		t.Fatalf("expected placed order CustomerPhone '9876543210', got '%s'", createdOrder.CustomerPhone)
	}
	if createdOrder.GuestCount != 4 {
		t.Fatalf("expected placed order GuestCount 4, got %d", createdOrder.GuestCount)
	}

	// 4. Waiter queries Pending Orders: must see Table 4 and Dev Rishi Jain
	req = httptest.NewRequest(http.MethodGet, "/api/v1/staff/orders/pending", nil)
	req.Header.Set("Authorization", "Bearer "+waiterToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get pending orders failed: status %d: %s", rec.Code, rec.Body.String())
	}

	var pendingList []handlers.PendingOrderResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &pendingList)
	if len(pendingList) != 1 {
		t.Fatalf("expected 1 pending order, got %d", len(pendingList))
	}
	entry := pendingList[0]
	if entry.TableNumber != "Table 4" {
		t.Fatalf("expected entry TableNumber 'Table 4', got '%s'", entry.TableNumber)
	}
	if entry.CustomerName != "Dev Rishi Jain" {
		t.Fatalf("expected entry CustomerName 'Dev Rishi Jain', got '%s'", entry.CustomerName)
	}
	if entry.GuestCount != 4 {
		t.Fatalf("expected entry GuestCount 4, got %d", entry.GuestCount)
	}
	if entry.Order.TableNumber != "Table 4" {
		t.Fatalf("expected entry.Order.TableNumber 'Table 4', got '%s'", entry.Order.TableNumber)
	}

	// 5. Waiter accepts the order
	acceptURL := "/api/v1/staff/orders/" + createdOrder.ID.String() + "/accept"
	req = httptest.NewRequest(http.MethodPost, acceptURL, nil)
	req.Header.Set("Authorization", "Bearer "+waiterToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("waiter accept failed: %d: %s", rec.Code, rec.Body.String())
	}

	// 6. Kitchen queries KDS queue: must see Table 4, Dev Rishi Jain, 4 Guests
	req = httptest.NewRequest(http.MethodGet, "/api/v1/kitchen/orders/queue", nil)
	req.Header.Set("Authorization", "Bearer "+kitchenToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get kitchen queue failed: %d: %s", rec.Code, rec.Body.String())
	}

	var kitchenQueue []order.Order
	_ = json.Unmarshal(rec.Body.Bytes(), &kitchenQueue)
	if len(kitchenQueue) != 1 {
		t.Fatalf("expected 1 kitchen queue order, got %d", len(kitchenQueue))
	}
	kOrder := kitchenQueue[0]
	if kOrder.TableNumber != "Table 4" {
		t.Fatalf("expected kOrder.TableNumber 'Table 4', got '%s'", kOrder.TableNumber)
	}
	if kOrder.CustomerName != "Dev Rishi Jain" {
		t.Fatalf("expected kOrder.CustomerName 'Dev Rishi Jain', got '%s'", kOrder.CustomerName)
	}
	if kOrder.CustomerPhone != "9876543210" {
		t.Fatalf("expected kOrder.CustomerPhone '9876543210', got '%s'", kOrder.CustomerPhone)
	}
	if kOrder.GuestCount != 4 {
		t.Fatalf("expected kOrder.GuestCount 4, got %d", kOrder.GuestCount)
	}
}
