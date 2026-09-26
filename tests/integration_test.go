package tests

import (
	"context"
	"testing"

	adapterpay "github.com/devrishijain/table-manager/internal/adapter/payment"
	"github.com/devrishijain/table-manager/internal/adapter/storage"
	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/google/uuid"
)

func TestFullDiningSessionLifecycleEndToEnd(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	_ = storage.NewMemoryObjectStore()

	// 1. Initialize services
	sessionSvc := service.NewSessionService(repo)
	orderSvc := service.NewOrderService(repo)
	exitSvc := service.NewExitService(repo)
	ledgerSvc := service.NewLedgerService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "secret-123")
	onboardingSvc := service.NewOnboardingService(repo)

	// 2. Onboard restaurant
	restID := uuid.New()
	rest := &restaurant.Restaurant{
		ID:                restID,
		Name:              "The Grand Dining OS",
		GSTIN:             "29ABCDE1234F1Z5",
		CommissionRateBps: 100, // 1.00%
		Status:            restaurant.StatusActive,
		Timezone:          "Asia/Kolkata",
	}
	_, err := onboardingSvc.StartOnboarding(ctx, rest, nil)
	if err != nil {
		t.Fatalf("failed to onboard restaurant: %v", err)
	}

	// Configure restaurant to require exit guard verification
	settings, _ := repo.GetSettings(ctx, restID)
	settings.ExitVerificationMode = restaurant.ExitVerificationModeGuardCheck
	_ = repo.UpdateSettings(ctx, settings)

	// Provision tables & create menu
	tables, err := onboardingSvc.BatchProvisionTables(ctx, restID, 1, 1)
	if err != nil || len(tables) == 0 {
		t.Fatalf("failed to provision table: %v", err)
	}
	tableToken := tables[0].TableToken

	_ = onboardingSvc.CloneMenuFromTemplate(ctx, restID, "fine-dine")
	menuItems, _ := repo.ListMenuItems(ctx, restID)
	if len(menuItems) == 0 {
		t.Fatal("expected menu items to be populated")
	}

	// Create staff & guard users
	staffID := uuid.New()
	_ = repo.CreateStaff(ctx, &restaurant.StaffUser{
		ID:           staffID,
		RestaurantID: restID,
		Name:         "Captain Vikram",
		Email:        "vikram@restaurant.com",
		Role:         restaurant.RoleManager,
		IsActive:     true,
	})

	guardID := uuid.New()
	_ = repo.CreateGuard(ctx, &restaurant.GuardUser{
		ID:           guardID,
		RestaurantID: restID,
		Name:         "Security Guard Ramesh",
		Phone:        "+919876543210",
		IsActive:     true,
	})

	// 3. Customer scans Table QR -> Session opens
	sess, isNew, err := sessionSvc.StartSession(ctx, tableToken, "customer-device-1", "Aman", "+919876543210", 2, "fp-cookie-1")
	if err != nil || !isNew {
		t.Fatalf("failed to start session: %v", err)
	}
	if sess.Status != session.StateOpen {
		t.Fatalf("expected session status OPEN, got %s", sess.Status)
	}

	// 4. Customer places first order
	cart1 := []order.CartItem{
		{MenuItemID: menuItems[0].ID, Quantity: 2},
	}
	ord1, firstOTP, err := orderSvc.PlaceOrder(ctx, sess.ID, cart1)
	if err != nil || firstOTP == nil {
		t.Fatalf("failed to place first order or missing first OTP: %v", err)
	}
	if ord1.Status != order.StatePlacedUnverified {
		t.Fatalf("expected order 1 status PLACED_UNVERIFIED, got %s", ord1.Status)
	}

	// 5. Staff verifies customer's first-order OTP -> Session moves to OPEN_VERIFIED
	err = sessionSvc.VerifyFirstOrder(ctx, sess.ID, staffID, *firstOTP)
	if err != nil {
		t.Fatalf("failed to verify first order OTP: %v", err)
	}

	sess, _ = repo.GetSessionByID(ctx, sess.ID)
	if sess.Status != session.StateOpenVerified {
		t.Fatalf("expected session status OPEN_VERIFIED, got %s", sess.Status)
	}

	// Staff accepts order into kitchen
	ord1, err = orderSvc.AcceptOrder(ctx, ord1.ID, staffID)
	if err != nil || ord1.Status != order.StateAccepted {
		t.Fatalf("failed to accept order 1: %v", err)
	}

	// Kitchen marks PREPARING -> READY -> SERVED
	_, _ = orderSvc.UpdateOrderStatus(ctx, ord1.ID, order.StatePreparing, staffID)
	_, _ = orderSvc.UpdateOrderStatus(ctx, ord1.ID, order.StateReady, staffID)
	_, _ = orderSvc.UpdateOrderStatus(ctx, ord1.ID, order.StateServed, staffID)

	// 6. Customer places 2nd order -> auto-flows as PLACED_VERIFIED
	cart2 := []order.CartItem{
		{MenuItemID: menuItems[1].ID, Quantity: 1},
	}
	ord2, _, err := orderSvc.PlaceOrder(ctx, sess.ID, cart2)
	if err != nil || ord2.Status != order.StatePlacedVerified {
		t.Fatalf("expected order 2 status PLACED_VERIFIED: %v", err)
	}
	_, _ = orderSvc.AcceptOrder(ctx, ord2.ID, staffID)
	_, _ = orderSvc.UpdateOrderStatus(ctx, ord2.ID, order.StateServed, staffID)

	// Refresh session totals
	sess, _ = repo.GetSessionByID(ctx, sess.ID)
	finalExpectedTotal := ord1.Total.AmountMinorUnits + ord2.Total.AmountMinorUnits
	if sess.RunningTotal.AmountMinorUnits != finalExpectedTotal {
		t.Fatalf("expected running total %d, got %d", finalExpectedTotal, sess.RunningTotal.AmountMinorUnits)
	}

	// 7. Customer requests bill -> Session moves to AWAITING_PAYMENT
	sess, err = paymentSvc.RequestBill(ctx, sess.ID)
	if err != nil || sess.Status != session.StateAwaitingPayment {
		t.Fatalf("failed to request bill: %v", err)
	}

	// 8. Split Payments:
	// Partial payment 1: ₹300 via Cash
	partialCashMinor := int64(30000)
	cashReq := payment.PaymentConfirmationRequest{
		PaymentID:          uuid.New(),
		SessionID:          sess.ID,
		RestaurantID:       restID,
		Amount:             money.New(partialCashMinor),
		Method:             payment.MethodCash,
		ConfirmedByStaffID: &staffID,
	}
	_, err = paymentSvc.ConfirmPayment(ctx, cashReq)
	if err != nil {
		t.Fatalf("failed to confirm partial cash payment: %v", err)
	}

	// Session must still be AWAITING_PAYMENT because bill is not yet fully covered!
	sess, _ = repo.GetSessionByID(ctx, sess.ID)
	if sess.Status != session.StateAwaitingPayment {
		t.Fatalf("expected session to remain in AWAITING_PAYMENT on partial payment, got %s", sess.Status)
	}

	// Partial payment 2: Remaining balance via Own Gateway (Razorpay)
	remainingMinor := finalExpectedTotal - partialCashMinor
	gwReq := payment.PaymentConfirmationRequest{
		PaymentID:    uuid.New(),
		SessionID:    sess.ID,
		RestaurantID: restID,
		Amount:       money.New(remainingMinor),
		Method:       payment.MethodOwnGateway,
	}
	_, err = paymentSvc.ConfirmPayment(ctx, gwReq)
	if err != nil {
		t.Fatalf("failed to confirm gateway payment: %v", err)
	}

	// 9. Session must now be in PAID state!
	sess, _ = repo.GetSessionByID(ctx, sess.ID)
	if sess.Status != session.StatePaid {
		t.Fatalf("expected session status PAID, got %s", sess.Status)
	}

	// Verify Platform Fee Ledger Entry exists and is mathematically exact
	feeEntry, err := repo.GetPlatformFeeBySessionID(ctx, sess.ID)
	if err != nil || feeEntry == nil {
		t.Fatal("expected PlatformFeeLedgerEntry to be generated")
	}
	expectedFeeMinor := (finalExpectedTotal*100 + 5000) / 10000
	if feeEntry.FeeAmount.AmountMinorUnits != expectedFeeMinor {
		t.Errorf("expected platform fee %d, got %d", expectedFeeMinor, feeEntry.FeeAmount.AmountMinorUnits)
	}

	// 10. ExitPass issuance & Guard verification
	exitPass, err := repo.GetExitPassBySessionID(ctx, sess.ID)
	if err != nil || exitPass == nil {
		t.Fatal("expected ExitPass to be issued upon session PAID")
	}

	// Re-derive customer's exit OTP from exit service issuance
	_, rawExitOTP, err := exitSvc.IssueExitPass(ctx, sess.ID)
	if err != nil {
		t.Fatalf("failed to issue exit pass: %v", err)
	}

	// Guard checks exit pass
	guardResp := exitSvc.VerifyExit(ctx, sess.ID, rawExitOTP, guardID)
	if guardResp.Result != exitpass.GuardResultApproved {
		t.Fatalf("expected guard verification APPROVED, got %s: %s", guardResp.Result, guardResp.Reason)
	}

	// Underlying session must now be in terminal COMPLETED state!
	sess, _ = repo.GetSessionByID(ctx, sess.ID)
	if sess.Status != session.StateCompleted {
		t.Fatalf("expected session status COMPLETED, got %s", sess.Status)
	}

	// 11. ExitPass reuse attempt: must be DENIED with PASS_ALREADY_USED
	secondGuardResp := exitSvc.VerifyExit(ctx, sess.ID, rawExitOTP, guardID)
	if secondGuardResp.Result != exitpass.GuardResultDenied || secondGuardResp.Reason != "PASS_ALREADY_USED" {
		t.Fatalf("expected guard to deny reuse with PASS_ALREADY_USED, got %s: %s", secondGuardResp.Result, secondGuardResp.Reason)
	}

	// 12. Verify Audit Log entries were written throughout the lifecycle
	auditLogs, err := repo.ListAuditLogs(ctx, restID, 100, 0)
	if err != nil || len(auditLogs) < 5 {
		t.Fatalf("expected rich audit log history, found %d logs", len(auditLogs))
	}
}

func TestExternalPlatformMandatoryEvidenceEnforcement(t *testing.T) {
	ctx := context.Background()
	adapter := adapterpay.NewExternalPlatformAdapter()
	staffID := uuid.New()
	platformName := "EAZYDINER"

	// 1. Missing evidence photo & txn ID -> MUST FAIL
	reqWithoutEvidence := payment.PaymentConfirmationRequest{
		PaymentID:            uuid.New(),
		SessionID:            uuid.New(),
		RestaurantID:         uuid.New(),
		Amount:               money.New(10000),
		Method:               payment.MethodExternalPlatform,
		ExternalPlatformName: &platformName,
		ConfirmedByStaffID:   &staffID,
	}
	_, err := adapter.Confirm(ctx, reqWithoutEvidence)
	if err == nil {
		t.Error("expected external platform without evidence to be rejected, but it succeeded")
	}

	// 2. With mandatory evidence -> Succeeds
	txnID := "EZD-998877"
	photoURL := "https://storage.table-manager.internal/payment-proofs/rest1/pay1/proof.jpg"
	reqWithEvidence := reqWithoutEvidence
	reqWithEvidence.EvidenceTransactionID = &txnID
	reqWithEvidence.EvidencePhotoURL = &photoURL

	p, err := adapter.Confirm(ctx, reqWithEvidence)
	if err != nil {
		t.Fatalf("expected external platform with evidence to succeed: %v", err)
	}
	if p.Status != payment.StateConfirmed {
		t.Errorf("expected confirmed status, got %s", p.Status)
	}
}
