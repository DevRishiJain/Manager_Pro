package service

import (
	"context"
	"fmt"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/ledger"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/google/uuid"
)

type LedgerService struct {
	repo storage.Repository
}

func NewLedgerService(repo storage.Repository) *LedgerService {
	return &LedgerService{repo: repo}
}

// ComputeSessionPlatformFee computes and stores the immutable platform commission for a session.
//
// BUSINESS RULES ENFORCED (§6):
// 1. Fee is computed ONCE per session at AWAITING_PAYMENT -> PAID transition.
// 2. GMV Base calculation:
//   - Non-cancelled SERVED orders are included.
//   - Cancelled PRE_ACCEPTANCE and POST_ACCEPTANCE_PRE_PREP orders are EXCLUDED.
//   - Cancelled POST_PREP_START orders ARE INCLUDED in GMV: The platform completed
//     its verification, routing, and kitchen order delivery.
//
// 3. Rounding Policy: Round-Half-Up to the nearest integer minor unit (paisa).
// 4. Zero fee on WALKOUT, EXPIRED, FORCE_CLOSED with zero payment.
func (s *LedgerService) ComputeSessionPlatformFee(ctx context.Context, sess *session.DiningSession) (*ledger.PlatformFeeLedgerEntry, error) {
	// Check if already computed
	existing, err := s.repo.GetPlatformFeeBySessionID(ctx, sess.ID)
	if err == nil && existing != nil {
		return existing, nil
	}

	rest, err := s.repo.GetRestaurantByID(ctx, sess.RestaurantID)
	if err != nil {
		return nil, err
	}

	orders, _ := s.repo.GetOrdersBySessionID(ctx, sess.ID)
	var gmvMinor int64

	for _, o := range orders {
		if o.Status == order.StateCancelled {
			// Policy rule: POST_PREP_START cancellation counts towards platform GMV
			if o.CancellationStage != nil && *o.CancellationStage == order.CancellationPostPrepStart {
				gmvMinor += o.Total.AmountMinorUnits
			}
		} else {
			// All served/active orders count
			gmvMinor += o.Total.AmountMinorUnits
		}
	}

	if gmvMinor == 0 && sess.FinalTotal.AmountMinorUnits > 0 {
		gmvMinor = sess.FinalTotal.AmountMinorUnits
	}

	gmvMoney := money.New(gmvMinor)
	feeRateBps := rest.CommissionRateBps
	if feeRateBps <= 0 {
		feeRateBps = 100 // default 1.00%
	}

	// Deterministic Round-Half-Up to nearest paisa: (gmv * bps + 5000) / 10000
	feeMoney, err := gmvMoney.MultiplyFractionRoundHalfUp(feeRateBps, 10000)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	billingPeriod := now.Format("2006-01")

	entry := &ledger.PlatformFeeLedgerEntry{
		ID:               uuid.New(),
		RestaurantID:     sess.RestaurantID,
		SessionID:        sess.ID,
		GMVAmount:        gmvMoney,
		FeeAmount:        feeMoney,
		FeeRateApplied:   feeRateBps,
		BillingPeriod:    billingPeriod,
		SettlementStatus: ledger.SettlementStatusPending,
		CreatedAt:        now,
	}

	if err := s.repo.CreatePlatformFeeEntry(ctx, entry); err != nil {
		return nil, err
	}

	// Update fee on session
	sess.PlatformFeeAmount = feeMoney
	_ = s.repo.UpdateSession(ctx, sess)

	return entry, nil
}

// RecordRefundAdjustment adjusts platform fee when a refund occurs after settlement.
func (s *LedgerService) RecordRefundAdjustment(ctx context.Context, sess *session.DiningSession, ref *payment.Refund) error {
	feeEntry, err := s.repo.GetPlatformFeeBySessionID(ctx, sess.ID)
	if err != nil || feeEntry == nil {
		return nil
	}

	// Calculate reduction in fee proportional to refund
	feeReduction, _ := ref.Amount.MultiplyFractionRoundHalfUp(feeEntry.FeeRateApplied, 10000)

	adj := &ledger.RefundAdjustment{
		ID:                   uuid.New(),
		PlatformFeeLedgerID:  feeEntry.ID,
		RestaurantID:         sess.RestaurantID,
		SessionID:            sess.ID,
		RefundID:             ref.ID,
		OriginalFee:          feeEntry.FeeAmount,
		AdjustedFeeReduction: feeReduction,
		Reason:               fmt.Sprintf("Refund adjustment: %s", ref.Reason),
		CreatedAt:            time.Now(),
	}

	return s.repo.CreateRefundAdjustment(ctx, adj)
}

// GenerateSettlementBatch aggregates weekly/monthly transactions into a settlement invoice.
func (s *LedgerService) GenerateSettlementBatch(ctx context.Context, restaurantID uuid.UUID, periodStart, periodEnd time.Time) (*ledger.RestaurantSettlement, error) {
	periodStr := periodStart.Format("2006-01")
	fees, err := s.repo.ListPlatformFees(ctx, restaurantID, periodStr)
	if err != nil {
		return nil, err
	}

	var grossSalesMinor int64
	var feesOwedMinor int64
	for _, f := range fees {
		grossSalesMinor += f.GMVAmount.AmountMinorUnits
		feesOwedMinor += f.FeeAmount.AmountMinorUnits
	}

	netPayableMinor := feesOwedMinor

	settlement := &ledger.RestaurantSettlement{
		ID:                  uuid.New(),
		RestaurantID:        restaurantID,
		PeriodStart:         periodStart,
		PeriodEnd:           periodEnd,
		GrossSales:          money.New(grossSalesMinor),
		PlatformFeesOwed:    money.New(feesOwedMinor),
		RefundAdjustments:   money.Zero(),
		NetPayablePlatform:  money.New(netPayableMinor),
		Status:              ledger.SettlementStatusPending,
		CreatedAt:           time.Now(),
	}

	if err := s.repo.CreateSettlement(ctx, settlement); err != nil {
		return nil, err
	}
	return settlement, nil
}

func (s *LedgerService) GetRunningPayable(ctx context.Context, restaurantID uuid.UUID) (money.Money, error) {
	fees, err := s.repo.ListPlatformFees(ctx, restaurantID, "")
	if err != nil {
		return money.Zero(), err
	}
	var totalFeeMinor int64
	for _, f := range fees {
		if f.SettlementStatus == ledger.SettlementStatusPending {
			totalFeeMinor += f.FeeAmount.AmountMinorUnits
		}
	}
	return money.New(totalFeeMinor), nil
}

func (s *LedgerService) ListSettlements(ctx context.Context, restaurantID uuid.UUID) ([]ledger.RestaurantSettlement, error) {
	return s.repo.ListSettlements(ctx, restaurantID)
}
