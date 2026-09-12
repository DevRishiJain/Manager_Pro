package risk

import (
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
)

type Tier string

const (
	TierNormal         Tier = "NORMAL"
	TierStepUpRequired Tier = "STEP_UP_REQUIRED"
	TierManualReview   Tier = "MANUAL_REVIEW"
	TierBlocked        Tier = "BLOCKED"
)

type EvaluationContext struct {
	CurrentRunningTotal money.Money
	NewOrderTotal       money.Money
	FirstOrderTotal     money.Money
	OrderCountLast5Min  int
	FailedPaymentCount  int
	ParticipantCount    int
	Settings            restaurant.RestaurantSettings
}

// Evaluate evaluates dining session risk based on configured thresholds.
func Evaluate(ctx EvaluationContext) Tier {
	candidateTotal := ctx.CurrentRunningTotal.AmountMinorUnits + ctx.NewOrderTotal.AmountMinorUnits

	// Extreme abuse pattern: e.g. > 10 failed payments or > 10 orders in 5 minutes
	if ctx.FailedPaymentCount >= 10 || ctx.OrderCountLast5Min > 10 {
		return TierBlocked
	}

	// Step up verification if order jump exceeds ratio
	// (e.g. verified with 300, now adding 8000 -> jump factor exceeded)
	if ctx.FirstOrderTotal.AmountMinorUnits > 0 {
		jumpFactor := int64(ctx.Settings.RapidOrderJumpFactor)
		if jumpFactor <= 0 {
			jumpFactor = 3
		}
		if ctx.NewOrderTotal.AmountMinorUnits >= ctx.FirstOrderTotal.AmountMinorUnits*jumpFactor &&
			ctx.NewOrderTotal.AmountMinorUnits > 300000 { // at least 3000 INR
			return TierStepUpRequired
		}
	}

	// High value threshold trigger
	if candidateTotal >= ctx.Settings.HighValueThresholdMinor {
		return TierStepUpRequired
	}

	// Moderate triggers: 3 failed payments or 5 rapid orders
	if ctx.FailedPaymentCount >= 3 || ctx.OrderCountLast5Min >= 5 || ctx.ParticipantCount > 15 {
		return TierManualReview
	}

	return TierNormal
}
