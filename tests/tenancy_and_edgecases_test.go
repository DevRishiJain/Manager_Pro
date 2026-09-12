package tests

import (
	"context"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/api/middleware"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/risk"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

func TestCrossTenantIsolationProbing(t *testing.T) {
	restA := uuid.New()
	restB := uuid.New()
	staffA := uuid.New()

	staffClaimsA := &crypto.StaffClaims{
		StaffID:      staffA,
		RestaurantID: restA,
		Role:         string(restaurant.RoleManager),
		IsPlatform:   false,
	}

	ctxA := context.WithValue(context.Background(), middleware.StaffContextKey, staffClaimsA)

	t.Run("Staff_A_Can_Access_Own_Restaurant", func(t *testing.T) {
		if !middleware.ValidateTenantScope(ctxA, restA) {
			t.Fatal("expected staff A to have access to Restaurant A")
		}
	})

	t.Run("Staff_A_Cannot_Access_Foreign_Restaurant_B", func(t *testing.T) {
		if middleware.ValidateTenantScope(ctxA, restB) {
			t.Fatal("security violation: staff from Restaurant A was allowed access to Restaurant B")
		}
	})
}

func TestRiskTieringEngine(t *testing.T) {
	settings := restaurant.DefaultSettings(uuid.New())
	settings.HighValueThresholdMinor = 500000 // 5000 INR
	settings.RapidOrderJumpFactor = 3

	t.Run("Tier1_Normal_Order", func(t *testing.T) {
		ctxNormal := risk.EvaluationContext{
			CurrentRunningTotal: money.New(100000), // 1000 INR
			NewOrderTotal:       money.New(50000),  // 500 INR
			Settings:            settings,
		}
		if tier := risk.Evaluate(ctxNormal); tier != risk.TierNormal {
			t.Fatalf("expected TierNormal, got %s", tier)
		}
	})

	t.Run("Tier2_High_Value_Threshold_Jump", func(t *testing.T) {
		ctxHighVal := risk.EvaluationContext{
			CurrentRunningTotal: money.New(300000),
			NewOrderTotal:       money.New(250000), // Total 5500 INR >= 5000 INR
			Settings:            settings,
		}
		if tier := risk.Evaluate(ctxHighVal); tier != risk.TierStepUpRequired {
			t.Fatalf("expected TierStepUpRequired for high value, got %s", tier)
		}
	})

	t.Run("Tier2_Rapid_Order_Jump_Versus_First_Order", func(t *testing.T) {
		ctxJump := risk.EvaluationContext{
			FirstOrderTotal: money.New(30000),  // 300 INR
			NewOrderTotal:   money.New(800000), // 8000 INR (> 3x and > 3000 INR)
			Settings:        settings,
		}
		if tier := risk.Evaluate(ctxJump); tier != risk.TierStepUpRequired {
			t.Fatalf("expected TierStepUpRequired for sudden order jump, got %s", tier)
		}
	})

	t.Run("Tier3_Moderate_Risk_Manual_Review", func(t *testing.T) {
		ctxReview := risk.EvaluationContext{
			FailedPaymentCount: 3,
			Settings:           settings,
		}
		if tier := risk.Evaluate(ctxReview); tier != risk.TierManualReview {
			t.Fatalf("expected TierManualReview, got %s", tier)
		}
	})

	t.Run("Tier4_Abusive_Failure_Flood_Blocked", func(t *testing.T) {
		ctxBlocked := risk.EvaluationContext{
			FailedPaymentCount: 11,
			Settings:           settings,
		}
		if tier := risk.Evaluate(ctxBlocked); tier != risk.TierBlocked {
			t.Fatalf("expected TierBlocked for abusive pattern, got %s", tier)
		}
	})
}

func TestPostPrepStartCancellationRetainsPlatformFeeGMV(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	ledgerSvc := service.NewLedgerService(repo)

	restID := uuid.New()
	_ = repo.CreateRestaurant(ctx, &restaurant.Restaurant{
		ID:                restID,
		Name:              "Cloud Kitchen",
		CommissionRateBps: 100, // 1.00%
		Status:            restaurant.StatusActive,
	})

	sessID := uuid.New()
	sess := &session.DiningSession{
		ID:           sessID,
		RestaurantID: restID,
		Status:       session.StateAwaitingPayment,
		FinalTotal:   money.New(100000), // 1000 INR
	}
	_ = repo.CreateSession(ctx, sess)

	t.Run("Cancellation_Post_Prep_Start_Retains_100pct_GMV", func(t *testing.T) {
		stage := order.CancellationPostPrepStart
		cancelledOrder := &order.Order{
			ID:                uuid.New(),
			SessionID:         sessID,
			RestaurantID:      restID,
			Status:            order.StateCancelled,
			CancellationStage: &stage,
			Total:             money.New(100000),
		}
		_ = repo.CreateOrder(ctx, cancelledOrder, nil)

		entry, err := ledgerSvc.ComputeSessionPlatformFee(ctx, sess)
		if err != nil {
			t.Fatalf("failed to compute platform fee: %v", err)
		}

		if entry.GMVAmount.AmountMinorUnits != 100000 {
			t.Fatalf("expected GMV 100000 retained from POST_PREP_START cancellation, got %d", entry.GMVAmount.AmountMinorUnits)
		}
		if entry.FeeAmount.AmountMinorUnits != 1000 {
			t.Fatalf("expected fee 1000 paise, got %d", entry.FeeAmount.AmountMinorUnits)
		}
	})
}

func TestOverpaymentCreditAdjustmentRecord(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	ledgerSvc := service.NewLedgerService(repo)
	exitSvc := service.NewExitService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "secret")

	restID := uuid.New()
	staffID := uuid.New()
	_ = repo.CreateRestaurant(ctx, &restaurant.Restaurant{
		ID:                restID,
		Name:              "Bistro Overpay",
		CommissionRateBps: 100,
		Status:            restaurant.StatusActive,
	})

	sessID := uuid.New()
	sess := &session.DiningSession{
		ID:           sessID,
		RestaurantID: restID,
		Status:       session.StateAwaitingPayment,
		RunningTotal: money.New(100000), // 1000 INR
		FinalTotal:   money.New(100000), // 1000 INR
		Version:      1,
		OpenedAt:     time.Now(),
	}
	_ = repo.CreateSession(ctx, sess)

	t.Run("Customer_Overpayment_Creates_Adjustment_And_Marks_Paid", func(t *testing.T) {
		payReq := payment.PaymentConfirmationRequest{
			PaymentID:          uuid.New(),
			SessionID:          sessID,
			RestaurantID:       restID,
			Amount:             money.New(120000), // 1200 INR (200 INR overpayment)
			Method:             payment.MethodCash,
			ConfirmedByStaffID: &staffID,
		}

		_, err := paymentSvc.ConfirmPayment(ctx, payReq)
		if err != nil {
			t.Fatalf("payment confirmation failed: %v", err)
		}

		updatedSess, _ := repo.GetSessionByID(ctx, sessID)
		if updatedSess.Status != session.StatePaid && updatedSess.Status != session.StateCompleted {
			t.Fatalf("expected session to reach PAID/COMPLETED, got %s", updatedSess.Status)
		}
	})
}
