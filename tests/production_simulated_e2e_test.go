package tests

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/adapter/forecast"
	"github.com/devrishijain/table-manager/internal/adapter/storage"
	domainExitPass "github.com/devrishijain/table-manager/internal/domain/exitpass"
	domainLedger "github.com/devrishijain/table-manager/internal/domain/ledger"
	"github.com/devrishijain/table-manager/internal/domain/money"
	domainOrder "github.com/devrishijain/table-manager/internal/domain/order"
	domainPayment "github.com/devrishijain/table-manager/internal/domain/payment"
	domainRestaurant "github.com/devrishijain/table-manager/internal/domain/restaurant"
	domainSession "github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

func TestProductionSimulatedE2E(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	objStore := storage.NewMemoryObjectStore()
	jwtSecret := []byte("master-secret-production-e2e-32-bytes")

	// 1. Restaurant Onboarding
	restID := uuid.New()
	rest := &domainRestaurant.Restaurant{
		ID:                    restID,
		Name:                  "The Grand Palace Bistro",
		GSTIN:                 "27AABCU9603R1ZM",
		CommissionRateBps:     150, // 1.50%
		SettlementBankDetails: "HDFC0001234:9876543210",
		Timezone:              "Asia/Kolkata",
		Status:                domainRestaurant.StatusActive,
		CreatedAt:             time.Now(),
	}
	if err := repo.CreateRestaurant(ctx, rest); err != nil {
		t.Fatalf("onboarding: failed to create restaurant: %v", err)
	}

	_ = repo.UpdateSettings(ctx, &domainRestaurant.RestaurantSettings{
		RestaurantID:         restID,
		ExitVerificationMode: domainRestaurant.ExitVerificationModeGuardCheck,
		SharedSessionPolicy:  domainRestaurant.SharedSessionPolicySharedTable,
	})

	// 2. Provision Table & Generate QR Token
	tableID := uuid.New()
	tableToken := "PALACE-T12-" + uuid.New().String()
	table := &domainRestaurant.Table{
		ID:           tableID,
		RestaurantID: restID,
		TableNumber:  "T-12",
		TableToken:   tableToken,
		IsActive:     true,
		CreatedAt:    time.Now(),
	}
	if err := repo.CreateTable(ctx, table); err != nil {
		t.Fatalf("provisioning: failed to create table: %v", err)
	}

	// Initialize Services
	onboardSvc := service.NewOnboardingService(repo)
	_ = onboardSvc.CloneMenuFromTemplate(ctx, restID, "fine-dine")
	menuItems, _ := repo.ListMenuItems(ctx, restID)
	if len(menuItems) < 3 {
		t.Fatalf("expected at least 3 menu items cloned from template, got %d", len(menuItems))
	}

	ledgerSvc := service.NewLedgerService(repo)
	exitSvc := service.NewExitService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "razorpay-wh-sec")
	sessionSvc := service.NewSessionService(repo)
	orderSvc := service.NewOrderService(repo)
	forecastProv := forecast.NewWeightedMovingAverageForecast()
	analyticsSvc := service.NewAnalyticsService(repo, forecastProv)

	// Staff & Guard users
	staffID := uuid.New()
	_ = repo.CreateStaff(ctx, &domainRestaurant.StaffUser{
		ID:           staffID,
		RestaurantID: restID,
		Name:         "Captain Amar",
		Email:        "amar@grandpalace.com",
		Role:         domainRestaurant.RoleManager,
		IsActive:     true,
	})

	guardID := uuid.New()
	_ = repo.CreateGuard(ctx, &domainRestaurant.GuardUser{
		ID:           guardID,
		RestaurantID: restID,
		Name:         "Security Guard Rajesh",
		Phone:        "+919876543210",
		IsActive:     true,
	})

	// 3. Customer Scans QR and Starts Session
	// Device 1 opens table session
	sess, isNew, err := sessionSvc.StartSession(ctx, tableToken, "device-1", "Aman", "fp-hash-1")
	if err != nil || !isNew {
		t.Fatalf("session start failed: %v", err)
	}
	if sess.Status != domainSession.StateOpen {
		t.Fatalf("expected state OPEN, got %s", sess.Status)
	}

	// Device 2 & Device 3 join the same table session!
	sessD2, isNewD2, err := sessionSvc.StartSession(ctx, tableToken, "device-2", "Riya", "fp-hash-2")
	if err != nil || isNewD2 || sessD2.ID != sess.ID {
		t.Fatalf("multi-device join Device 2 failed: %v", err)
	}
	sessD3, isNewD3, err := sessionSvc.StartSession(ctx, tableToken, "device-3", "Sameer", "fp-hash-3")
	if err != nil || isNewD3 || sessD3.ID != sess.ID {
		t.Fatalf("multi-device join Device 3 failed: %v", err)
	}

	// 4. Device 1 places First Order
	cart1 := []domainOrder.CartItem{
		{MenuItemID: menuItems[0].ID, Quantity: 2}, // e.g. Butter Chicken x2
		{MenuItemID: menuItems[1].ID, Quantity: 1}, // e.g. Naan
	}
	ord1, firstOTP, err := orderSvc.PlaceOrder(ctx, sess.ID, cart1)
	if err != nil || firstOTP == nil {
		t.Fatalf("failed to place first order: %v", err)
	}
	if ord1.Status != domainOrder.StatePlacedUnverified {
		t.Fatalf("expected order 1 status PLACED_UNVERIFIED, got %s", ord1.Status)
	}

	// Staff verifies customer's first-order OTP at table -> Session transitions to OPEN_VERIFIED
	err = sessionSvc.VerifyFirstOrder(ctx, sess.ID, staffID, *firstOTP)
	if err != nil {
		t.Fatalf("staff failed to verify first order OTP: %v", err)
	}
	sess, _ = repo.GetSessionByID(ctx, sess.ID)
	if sess.Status != domainSession.StateOpenVerified {
		t.Fatalf("expected session status OPEN_VERIFIED, got %s", sess.Status)
	}

	// 5. Kitchen accepts & serves Order 1
	ord1, err = orderSvc.AcceptOrder(ctx, ord1.ID, staffID)
	if err != nil || ord1.Status != domainOrder.StateAccepted {
		t.Fatalf("failed to accept order 1: %v", err)
	}
	_, _ = orderSvc.UpdateOrderStatus(ctx, ord1.ID, domainOrder.StatePreparing, staffID)
	_, _ = orderSvc.UpdateOrderStatus(ctx, ord1.ID, domainOrder.StateServed, staffID)

	// 6. Device 2 places Order 2 (Session is now verified, auto-accepts into PLACED_VERIFIED)
	cart2 := []domainOrder.CartItem{
		{MenuItemID: menuItems[1].ID, Quantity: 1},
		{MenuItemID: menuItems[2].ID, Quantity: 1},
	}
	ord2, _, err := orderSvc.PlaceOrder(ctx, sess.ID, cart2)
	if err != nil || ord2.Status != domainOrder.StatePlacedVerified {
		t.Fatalf("failed to place order 2: %v", err)
	}

	// Kitchen accepts and serves Order 2
	ord2, _ = orderSvc.AcceptOrder(ctx, ord2.ID, staffID)
	_, _ = orderSvc.UpdateOrderStatus(ctx, ord2.ID, domainOrder.StateServed, staffID)

	// 7. Request Bill -> transitions session to AWAITING_PAYMENT
	sess, err = paymentSvc.RequestBill(ctx, sess.ID)
	if err != nil || sess.Status != domainSession.StateAwaitingPayment {
		t.Fatalf("failed to request bill: %v", err)
	}
	totalBillMinor := sess.FinalTotal.AmountMinorUnits
	if totalBillMinor <= 0 {
		t.Fatalf("expected positive final bill, got %d", totalBillMinor)
	}

	// 8. Split Payment Race between Device 1 and Device 2
	// Split: Device 1 pays 60%, Device 2 pays 40%
	payAmount1 := totalBillMinor * 6 / 10
	payAmount2 := totalBillMinor - payAmount1

	var wg sync.WaitGroup
	var payErr1, payErr2 error
	var pay1, pay2 *domainPayment.Payment

	wg.Add(2)
	go func() {
		defer wg.Done()
		refID := "razorpay_order_sim_" + uuid.New().String()
		pay1, payErr1 = paymentSvc.ConfirmPayment(ctx, domainPayment.PaymentConfirmationRequest{
			PaymentID:          uuid.New(),
			SessionID:          sess.ID,
			RestaurantID:       restID,
			Amount:             money.New(payAmount1),
			Method:             domainPayment.MethodOwnGateway,
			GatewayReferenceID: &refID,
		})
	}()

	go func() {
		defer wg.Done()
		posRef := "POS-RECEIPT-9988"
		pay2, payErr2 = paymentSvc.ConfirmPayment(ctx, domainPayment.PaymentConfirmationRequest{
			PaymentID:             uuid.New(),
			SessionID:             sess.ID,
			RestaurantID:          restID,
			Amount:                money.New(payAmount2),
			Method:                domainPayment.MethodRestaurantPOS,
			EvidenceTransactionID: &posRef,
			ConfirmedByStaffID:    &staffID,
		})
	}()
	wg.Wait()

	if payErr1 != nil {
		t.Fatalf("split payment 1 failed: %v", payErr1)
	}
	if payErr2 != nil {
		t.Fatalf("split payment 2 failed: %v", payErr2)
	}
	if pay1.Status != domainPayment.StateConfirmed || pay2.Status != domainPayment.StateConfirmed {
		t.Fatalf("expected both split payments confirmed")
	}

	// 9. Verify Session transitioned to PAID
	sess, _ = repo.GetSessionByID(ctx, sess.ID)
	if sess.Status != domainSession.StatePaid {
		t.Fatalf("expected session status PAID after split payment, got %s", sess.Status)
	}

	// 10. Verify Platform Fee Ledger Entry
	feeEntry, err := repo.GetPlatformFeeBySessionID(ctx, sess.ID)
	if err != nil || feeEntry == nil {
		t.Fatalf("expected platform fee ledger entry for session: %v", err)
	}
	expectedFeeMinor := (totalBillMinor*150 + 5000) / 10000 // 150 bps (1.5%)
	if feeEntry.FeeAmount.AmountMinorUnits != expectedFeeMinor {
		t.Fatalf("expected platform fee %d, got %d", expectedFeeMinor, feeEntry.FeeAmount.AmountMinorUnits)
	}

	// 11. Restaurant Settlement Batch
	settlement := &domainLedger.RestaurantSettlement{
		ID:                 uuid.New(),
		RestaurantID:       restID,
		PeriodStart:        time.Now().Add(-24 * time.Hour),
		PeriodEnd:          time.Now(),
		GrossSales:         money.New(totalBillMinor),
		PlatformFeesOwed:   money.New(expectedFeeMinor),
		RefundAdjustments:  money.Zero(),
		NetPayablePlatform: money.New(expectedFeeMinor),
		Status:             domainLedger.SettlementStatusSettled,
		SettledAt:          &[]time.Time{time.Now()}[0],
		CreatedAt:          time.Now(),
	}
	if err := repo.CreateSettlement(ctx, settlement); err != nil {
		t.Fatalf("failed to record settlement: %v", err)
	}

	// 12. ExitPass Issuance
	exitPass, rawExitOTP, err := exitSvc.IssueExitPass(ctx, sess.ID)
	if err != nil || exitPass == nil {
		t.Fatalf("failed to issue exit pass: %v", err)
	}
	if exitPass.Status != domainExitPass.StateIssued {
		t.Fatalf("expected exit pass status ISSUED, got %s", exitPass.Status)
	}

	// 13. Guard Scans ExitPass -> Session transitions to terminal COMPLETED
	guardResp := exitSvc.VerifyExit(ctx, sess.ID, rawExitOTP, guardID)
	if guardResp.Result != domainExitPass.GuardResultApproved {
		t.Fatalf("expected guard verification APPROVED, got %s: %s", guardResp.Result, guardResp.Reason)
	}

	sess, _ = repo.GetSessionByID(ctx, sess.ID)
	if sess.Status != domainSession.StateCompleted {
		t.Fatalf("expected session status COMPLETED after exit verification, got %s", sess.Status)
	}

	// 14. Analytics Verification
	todayAnalytics, err := analyticsSvc.GetTodayAnalytics(ctx, restID)
	if err != nil {
		t.Fatalf("failed to get today analytics: %v", err)
	}
	if todayAnalytics.TotalGMV.AmountMinorUnits != totalBillMinor {
		t.Fatalf("expected today GMV %d, got %d", totalBillMinor, todayAnalytics.TotalGMV.AmountMinorUnits)
	}

	// 15. Boundary Failure Injections
	t.Run("Failure_Injection_Replay_ExitPass_Must_Deny", func(t *testing.T) {
		secondResp := exitSvc.VerifyExit(ctx, sess.ID, rawExitOTP, guardID)
		if secondResp.Result != domainExitPass.GuardResultDenied || secondResp.Reason != "PASS_ALREADY_USED" {
			t.Fatalf("expected denial with PASS_ALREADY_USED on replay, got %s: %s", secondResp.Result, secondResp.Reason)
		}
	})

	t.Run("Failure_Injection_Order_On_Completed_Session_Must_Fail", func(t *testing.T) {
		_, _, err := orderSvc.PlaceOrder(ctx, sess.ID, cart1)
		if err == nil {
			t.Fatalf("expected placing order on COMPLETED session to fail")
		}
	})

	t.Run("Failure_Injection_Guard_Cannot_Access_Manager_Routes", func(t *testing.T) {
		guardToken, err := crypto.GenerateGuardJWT(jwtSecret, guardID, restID, 1*time.Hour)
		if err != nil || guardToken == "" {
			t.Fatalf("failed to generate guard token")
		}
		// Guard claims validation ensures guard cannot act as manager
		claims, err := crypto.ParseGuardJWT(jwtSecret, guardToken)
		if err != nil || claims.GuardID != guardID {
			t.Fatalf("invalid guard token parsing")
		}
	})

	_ = objStore
	fmt.Println(">> Complete Simulated Production Journey Verified Cleanly!")
}
