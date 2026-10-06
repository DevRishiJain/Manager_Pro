package tests

import (
	"context"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/adapter/forecast"
	"github.com/devrishijain/table-manager/internal/adapter/storage"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/google/uuid"
)

func TestPhase1_OrderStatusHistoryAndAtomicTransitions(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	orderSvc := service.NewOrderService(repo)

	restID := uuid.New()
	tableID := uuid.New()
	_ = repo.CreateTable(ctx, &restaurant.Table{
		ID:           tableID,
		RestaurantID: restID,
		TableNumber:  "Table 1",
		IsActive:     true,
	})

	sess := &session.DiningSession{
		ID:             uuid.New(),
		RestaurantID:   restID,
		TableID:        tableID,
		SessionToken:   "sess-token-123",
		CustomerName:   "Rahul Verma",
		CustomerPhone:  "9876543210",
		GuestCount:     2,
		Status:         session.StateOpenVerified,
		OpenedAt:       time.Now(),
		LastActivityAt: time.Now(),
		RunningTotal:   money.Zero(),
		FinalTotal:     money.Zero(),
		Version:        1,
	}
	_ = repo.CreateSession(ctx, sess)

	// Menu Item
	catID := uuid.New()
	_ = repo.CreateCategory(ctx, &restaurant.MenuCategory{ID: catID, RestaurantID: restID, Name: "Mains"})
	dishID := uuid.New()
	_ = repo.CreateMenuItem(ctx, &restaurant.MenuItem{
		ID:           dishID,
		RestaurantID: restID,
		CategoryID:   catID,
		Name:         "Butter Chicken",
		Price:        money.New(45000),
		IsAvailable:  true,
	})

	// 1. Place order -> creates initial history entry
	ord, _, err := orderSvc.PlaceOrder(ctx, sess.ID, []order.CartItem{
		{MenuItemID: dishID, Quantity: 1},
	})
	if err != nil {
		t.Fatalf("failed to create order: %v", err)
	}

	hist1, err := repo.GetOrderStatusHistory(ctx, ord.ID)
	if err != nil || len(hist1) != 1 {
		t.Fatalf("expected 1 history entry, got %d, err: %v", len(hist1), err)
	}
	if hist1[0].ToStatus != ord.Status {
		t.Fatalf("expected to_status %s, got %s", ord.Status, hist1[0].ToStatus)
	}

	// 2. Waiter accepts order -> second history entry
	staffID := uuid.New()
	ord, err = orderSvc.AcceptOrder(ctx, ord.ID, staffID)
	if err != nil {
		t.Fatalf("failed to accept order: %v", err)
	}

	hist2, err := repo.GetOrderStatusHistory(ctx, ord.ID)
	if err != nil || len(hist2) != 2 {
		t.Fatalf("expected 2 history entries, got %d, err: %v", len(hist2), err)
	}
	if hist2[1].ToStatus != order.StateAccepted {
		t.Fatalf("expected to_status ACCEPTED, got %s", hist2[1].ToStatus)
	}

	// 3. Kitchen status updates -> third and fourth history entries
	chefID := uuid.New()
	ord, err = orderSvc.UpdateOrderStatus(ctx, ord.ID, order.StatePreparing, chefID)
	if err != nil {
		t.Fatalf("failed to update to preparing: %v", err)
	}
	ord, err = orderSvc.UpdateOrderStatus(ctx, ord.ID, order.StateReady, chefID)
	if err != nil {
		t.Fatalf("failed to update to ready: %v", err)
	}

	hist3, err := repo.GetOrderStatusHistory(ctx, ord.ID)
	if err != nil || len(hist3) != 4 {
		t.Fatalf("expected 4 history entries, got %d, err: %v", len(hist3), err)
	}
	if hist3[2].ToStatus != order.StatePreparing || hist3[3].ToStatus != order.StateReady {
		t.Fatalf("history sequence mismatch: %+v", hist3)
	}
}

func TestPhase1_CashFirstPaymentAndManagerVoidFlow(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	objStore := storage.NewMemoryObjectStore()
	forecastProvider := forecast.NewWeightedMovingAverageForecast()

	exitSvc := service.NewExitService(repo)
	ledgerSvc := service.NewLedgerService(repo)
	_ = service.NewAnalyticsService(repo, forecastProvider)
	_ = objStore

	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "test-razorpay-secret")

	restID := uuid.New()
	tableID := uuid.New()
	sessID := uuid.New()

	sess := &session.DiningSession{
		ID:             sessID,
		RestaurantID:   restID,
		TableID:        tableID,
		SessionToken:   "tok-sess-payment",
		CustomerName:   "Priya Sharma",
		CustomerPhone:  "9811223344",
		GuestCount:     2,
		Status:         session.StateOpenVerified,
		OpenedAt:       time.Now(),
		LastActivityAt: time.Now(),
		RunningTotal:   money.New(50000), // Rs. 500.00
		FinalTotal:     money.New(50000),
		Version:        1,
	}
	_ = repo.CreateSession(ctx, sess)

	// 1. Move to bill requested
	sess, err := paymentSvc.RequestBill(ctx, sessID)
	if err != nil {
		t.Fatalf("failed to request bill: %v", err)
	}
	if sess.Status != session.StateAwaitingPayment {
		t.Fatalf("expected AWAITING_PAYMENT, got %s", sess.Status)
	}

	// 2. Online payments should be DISABLED by default
	_, err = paymentSvc.InitiatePayment(ctx, sessID, payment.MethodOwnGateway, money.New(50000), nil)
	if err != service.ErrOnlinePaymentsDisabled {
		t.Fatalf("expected ErrOnlinePaymentsDisabled, got: %v", err)
	}

	// 3. Cash & UPI_QR payments succeed
	upiPay, err := paymentSvc.InitiatePayment(ctx, sessID, payment.MethodUPIQR, money.New(50000), nil)
	if err != nil {
		t.Fatalf("failed to initiate UPI payment: %v", err)
	}
	if upiPay.Status != payment.StatePendingConfirmation {
		t.Fatalf("expected PENDING_CONFIRMATION, got %s", upiPay.Status)
	}

	// 4. Staff confirms payment
	waiterID := uuid.New()
	_ = repo.CreateStaff(ctx, &restaurant.StaffUser{
		ID:           waiterID,
		RestaurantID: restID,
		Name:         "Rohan Waiter",
		Email:        "rohan@test.com",
		Role:         restaurant.RoleWaiter,
		IsActive:     true,
	})

	confirmedPay, err := paymentSvc.ConfirmPayment(ctx, payment.PaymentConfirmationRequest{
		PaymentID:          upiPay.ID,
		SessionID:          sessID,
		RestaurantID:       restID,
		Method:             payment.MethodUPIQR,
		Amount:             money.New(50000),
		ConfirmedByStaffID: &waiterID,
	})
	if err != nil {
		t.Fatalf("failed to confirm payment: %v", err)
	}
	if confirmedPay.Status != payment.StateConfirmed {
		t.Fatalf("expected CONFIRMED, got %s", confirmedPay.Status)
	}

	// Session is now paid
	sessAfter, _ := repo.GetSessionByID(ctx, sessID)
	if sessAfter.Status != session.StatePaid {
		t.Fatalf("expected session status PAID, got %s", sessAfter.Status)
	}

	// 5. Subsequent attempt to pay fully settled session must fail
	_, err = paymentSvc.InitiatePayment(ctx, sessID, payment.MethodCash, money.New(50000), nil)
	if err == nil {
		t.Fatal("expected error on paying fully settled session, got nil")
	}

	// 6. Non-manager (waiter) attempts to void payment -> must fail
	_, err = paymentSvc.VoidPayment(ctx, confirmedPay.ID, waiterID, "Accidental duplicate")
	if err == nil {
		t.Fatal("expected unauthorized error when waiter voids payment, got nil")
	}

	// 7. Manager voids payment -> succeeds, reverts session state
	managerID := uuid.New()
	_ = repo.CreateStaff(ctx, &restaurant.StaffUser{
		ID:           managerID,
		RestaurantID: restID,
		Name:         "Vikram Manager",
		Email:        "vikram@test.com",
		Role:         restaurant.RoleManager,
		IsActive:     true,
	})

	voidedPay, err := paymentSvc.VoidPayment(ctx, confirmedPay.ID, managerID, "Guest paid wrong UPI QR")
	if err != nil {
		t.Fatalf("manager void failed: %v", err)
	}
	if voidedPay.Status != payment.StateVoided {
		t.Fatalf("expected VOIDED, got %s", voidedPay.Status)
	}

	// Check session reverted to AWAITING_PAYMENT
	sessReverted, _ := repo.GetSessionByID(ctx, sessID)
	if sessReverted.Status != session.StateAwaitingPayment {
		t.Fatalf("expected session status reverted to AWAITING_PAYMENT, got %s", sessReverted.Status)
	}
}
