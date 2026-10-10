package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/api/middleware"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

func TestStaffQuickBilling_Success(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()

	restID := uuid.New()
	staffID := uuid.New()

	_ = repo.CreateRestaurant(ctx, &restaurant.Restaurant{
		ID:     restID,
		Name:   "Quick POS Diner",
		Status: restaurant.StatusActive,
	})

	table := &restaurant.Table{
		ID:           uuid.New(),
		RestaurantID: restID,
		TableNumber:  "T1",
		TableToken:   "tok_test_table_1",
		Capacity:     4,
		IsActive:     true,
	}
	_ = repo.CreateTable(ctx, table)

	item := &restaurant.MenuItem{
		ID:           uuid.New(),
		RestaurantID: restID,
		Name:         "Butter Naan",
		Price:        money.New(5000), // ₹50.00
		IsAvailable:  true,
		CGSTRateBps:  250,
		SGSTRateBps:  250,
	}
	_ = repo.CreateMenuItem(ctx, item)

	sessionSvc := service.NewSessionService(repo)
	orderSvc := service.NewOrderService(repo)
	exitSvc := service.NewExitService(repo).WithSecret("secret-32-bytes-test-key-12345")
	ledgerSvc := service.NewLedgerService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "secret")
	analyticsSvc := service.NewAnalyticsService(repo, nil)
	onboardingSvc := service.NewOnboardingService(repo)

	h := handlers.NewAPIHandler(
		sessionSvc,
		orderSvc,
		paymentSvc,
		exitSvc,
		ledgerSvc,
		analyticsSvc,
		onboardingSvc,
		nil,
		repo,
		"whsec",
	)

	reqPayload := handlers.QuickBillingRequest{
		OrderType:    "TAKEAWAY",
		TableNumber:  "T1",
		CustomerName: "John Doe",
		Items: []handlers.QuickBillingItemRequest{
			{
				MenuItemID: item.ID,
				Quantity:   2,
			},
		},
		Payment: handlers.QuickBillingPaymentRequest{
			Method:        "CASH",
			AmountMinor:   10500,
			TenderedMinor: 11000,
			ChangeMinor:   500,
		},
	}
	body, _ := json.Marshal(reqPayload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/billing/quick", bytes.NewReader(body))
	claims := &crypto.StaffClaims{
		StaffID:      staffID,
		RestaurantID: restID,
		Role:         "WAITER",
		Name:         "Rishi Jain",
	}
	ctx = context.WithValue(req.Context(), middleware.StaffContextKey, claims)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.StaffQuickBilling(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp handlers.QuickBillingResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success true, got false")
	}
	if resp.OrderType != "TAKEAWAY" {
		t.Errorf("expected TAKEAWAY, got %s", resp.OrderType)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	if resp.Items[0].TotalMinor != 10000 {
		t.Errorf("expected item total 10000, got %d", resp.Items[0].TotalMinor)
	}
	if resp.CashierName != "Rishi Jain" {
		t.Errorf("expected cashier Rishi Jain, got %s", resp.CashierName)
	}
}

func TestStaffQuickBilling_EmptyItems(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	h := handlers.NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, repo, "whsec")

	reqPayload := handlers.QuickBillingRequest{
		Items: []handlers.QuickBillingItemRequest{},
	}
	body, _ := json.Marshal(reqPayload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/billing/quick", bytes.NewReader(body))
	claims := &crypto.StaffClaims{
		StaffID:      uuid.New(),
		RestaurantID: uuid.New(),
		Role:         "WAITER",
	}
	ctx = context.WithValue(req.Context(), middleware.StaffContextKey, claims)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.StaffQuickBilling(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for empty items, got %d", rec.Code)
	}
}

func TestStaffQuickBilling_Unauthorized(t *testing.T) {
	repo := memory.NewMemoryRepository()
	h := handlers.NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, repo, "whsec")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/billing/quick", bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()
	h.StaffQuickBilling(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 for unauthorized, got %d", rec.Code)
	}
}
