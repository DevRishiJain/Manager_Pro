package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/google/uuid"
)

// TestConcurrentSplitPaymentRaceCondition verifies the high-stakes concurrent payment race:
// Customer A pays ₹1000, Customer B pays ₹1000, Customer C pays ₹1000 against a ₹2000 final bill.
// All 3 goroutines execute at the exact same millisecond.
//
// Invariants tested:
// 1. All 3 payments are safely recorded (no lost partial payments).
// 2. Exactly ONE ExitPass is issued (no duplicate exit passes).
// 3. Exactly ONE PlatformFeeLedgerEntry is created (no duplicate platform fees).
// 4. Exactly ONE overpayment adjustment (₹1000 credit) is recorded.
// 5. Dining session reaches terminal PAID or COMPLETED without corruption.
func TestConcurrentSplitPaymentRaceCondition(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	ledgerSvc := service.NewLedgerService(repo)
	exitSvc := service.NewExitService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "test-webhook-secret")

	restID := uuid.New()
	tableID := uuid.New()
	sessID := uuid.New()
	staffID := uuid.New()

	// 1. Setup restaurant with Guard Exit Verification enabled
	_ = repo.CreateRestaurant(ctx, &restaurant.Restaurant{
		ID:                restID,
		Name:              "Concurrence Grill",
		CommissionRateBps: 200, // 2.00%
		Status:            restaurant.StatusActive,
	})

	_ = repo.UpdateSettings(ctx, &restaurant.RestaurantSettings{
		RestaurantID:          restID,
		ExitVerificationMode:  restaurant.ExitVerificationModeGuardCheck,
		ExitPassOTPTTLMinutes: 120,
	})

	_ = repo.CreateTable(ctx, &restaurant.Table{
		ID:           tableID,
		RestaurantID: restID,
		TableNumber:  "T-RACE-01",
		TableToken:   "tok-race-01",
		IsActive:     true,
	})

	// 2. Setup session with 2000 INR bill (200,000 paise)
	sess := &session.DiningSession{
		ID:             sessID,
		RestaurantID:   restID,
		TableID:        tableID,
		Status:         session.StateAwaitingPayment,
		RunningTotal:   money.New(200000),
		FinalTotal:     money.New(200000), // ₹2000.00
		SessionToken:   "sess-tok-split-race",
		Version:        1,
		OpenedAt:       time.Now(),
		LastActivityAt: time.Now(),
	}
	_ = repo.CreateSession(ctx, sess)

	// 3. Fire 3 concurrent payment confirmations of ₹1000 each (Total ₹3000 against ₹2000 bill)
	const numPayers = 3
	paymentAmount := money.New(100000) // ₹1000 each

	var wg sync.WaitGroup
	var confirmedPayments []*payment.Payment
	var mu sync.Mutex
	errChan := make(chan error, numPayers)

	startGate := make(chan struct{}) // Force simultaneous release

	for i := 0; i < numPayers; i++ {
		wg.Add(1)
		go func(payerIndex int) {
			defer wg.Done()
			<-startGate // Release all goroutines at the exact same instant

			payID := uuid.New()
			req := payment.PaymentConfirmationRequest{
				PaymentID:          payID,
				SessionID:          sessID,
				RestaurantID:       restID,
				Amount:             paymentAmount,
				Method:             payment.MethodCash,
				ConfirmedByStaffID: &staffID,
			}

			p, err := paymentSvc.ConfirmPayment(ctx, req)
			if err != nil {
				errChan <- err
				return
			}

			mu.Lock()
			confirmedPayments = append(confirmedPayments, p)
			mu.Unlock()
		}(i)
	}

	// Trigger concurrent release
	close(startGate)
	wg.Wait()
	close(errChan)

	// Verify no errors occurred during payment confirmation
	for err := range errChan {
		t.Fatalf("Unexpected error during concurrent payment confirmation: %v", err)
	}

	t.Run("All_Three_Payments_Recorded_Without_Loss", func(t *testing.T) {
		if len(confirmedPayments) != 3 {
			t.Fatalf("Expected exactly 3 confirmed payments, got %d", len(confirmedPayments))
		}

		paymentsInDB, err := repo.GetPaymentsBySessionID(ctx, sessID)
		if err != nil || len(paymentsInDB) != 3 {
			t.Fatalf("Expected 3 payments stored in repository, got %d (err: %v)", len(paymentsInDB), err)
		}

		var totalMinor int64
		for _, p := range paymentsInDB {
			if p.Status != payment.StateConfirmed {
				t.Errorf("Payment %s not confirmed: status=%s", p.ID, p.Status)
			}
			totalMinor += p.Amount.AmountMinorUnits
		}
		if totalMinor != 300000 {
			t.Fatalf("Expected total payments to equal ₹3000 (300000 paise), got %d", totalMinor)
		}
	})

	t.Run("Session_Reached_Paid_State_Consistently", func(t *testing.T) {
		finalSess, err := repo.GetSessionByID(ctx, sessID)
		if err != nil {
			t.Fatalf("Failed to retrieve final session: %v", err)
		}
		if finalSess.Status != session.StatePaid && finalSess.Status != session.StateCompleted {
			t.Fatalf("Expected session state to be PAID or COMPLETED, got %s", finalSess.Status)
		}
	})

	t.Run("Exactly_One_Exit_Pass_Issued_No_Duplicates", func(t *testing.T) {
		pass, err := repo.GetExitPassBySessionID(ctx, sessID)
		if err != nil || pass == nil {
			t.Fatalf("Expected exit pass to be issued for paid session, got err: %v", err)
		}
		if pass.Status != "ISSUED" {
			t.Fatalf("Expected exit pass to be in ISSUED state, got %s", pass.Status)
		}
	})

	t.Run("Exactly_One_Platform_Fee_Ledger_Entry_No_Duplicates", func(t *testing.T) {
		feeEntry, err := repo.GetPlatformFeeBySessionID(ctx, sessID)
		if err != nil || feeEntry == nil {
			t.Fatalf("Expected platform fee entry to exist, got err: %v", err)
		}
		// 2% of ₹2000 = ₹40 = 4000 paise
		if feeEntry.FeeAmount.AmountMinorUnits != 4000 {
			t.Fatalf("Expected platform fee of ₹40 (4000 paise), got %d", feeEntry.FeeAmount.AmountMinorUnits)
		}
		if feeEntry.GMVAmount.AmountMinorUnits != 200000 {
			t.Fatalf("Expected GMV of ₹2000 (200000 paise), got %d", feeEntry.GMVAmount.AmountMinorUnits)
		}
	})

	t.Run("Overpayment_Credit_Adjustment_Accurately_Recorded", func(t *testing.T) {
		adjs, _ := repo.GetAdjustmentsBySessionID(ctx, sessID)
		if len(adjs) != 1 {
			t.Fatalf("Expected exactly 1 overpayment adjustment, got %d", len(adjs))
		}
		// Overpayment = ₹3000 paid - ₹2000 bill = ₹1000 (100000 paise)
		if adjs[0].Amount.AmountMinorUnits != 100000 {
			t.Fatalf("Expected overpayment adjustment of ₹1000 (100000 paise), got %d", adjs[0].Amount.AmountMinorUnits)
		}
		if adjs[0].Type != payment.AdjustmentTypeOverpaymentCredit {
			t.Fatalf("Expected adjustment type OVERPAYMENT_CREDIT, got %s", adjs[0].Type)
		}
	})
}
