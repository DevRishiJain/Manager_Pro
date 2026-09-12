package ledger

import (
	"time"

	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/google/uuid"
)

type SettlementStatus string

const (
	SettlementStatusPending  SettlementStatus = "PENDING"
	SettlementStatusInvoiced SettlementStatus = "INVOICED"
	SettlementStatusSettled  SettlementStatus = "SETTLED"
)

// PlatformFeeLedgerEntry records platform commission earned per completed dining session.
// Rate applied is stored explicitly for historical immutability.
type PlatformFeeLedgerEntry struct {
	ID               uuid.UUID        `json:"id"`
	RestaurantID     uuid.UUID        `json:"restaurant_id"`
	SessionID        uuid.UUID        `json:"session_id"`
	GMVAmount        money.Money      `json:"gmv_amount"`
	FeeAmount        money.Money      `json:"fee_amount"`
	FeeRateApplied   int64            `json:"fee_rate_applied"` // Basis points, e.g. 100 = 1.00%
	BillingPeriod    string           `json:"billing_period"`   // e.g. "2026-09"
	SettlementStatus SettlementStatus `json:"settlement_status"`
	CreatedAt        time.Time        `json:"created_at"`
}

// RefundAdjustment modifies platform fee when a refund occurs after settlement/completion.
type RefundAdjustment struct {
	ID                  uuid.UUID   `json:"id"`
	PlatformFeeLedgerID uuid.UUID   `json:"platform_fee_ledger_id"`
	RestaurantID        uuid.UUID   `json:"restaurant_id"`
	SessionID           uuid.UUID   `json:"session_id"`
	RefundID            uuid.UUID   `json:"refund_id"`
	OriginalFee         money.Money `json:"original_fee"`
	AdjustedFeeReduction money.Money `json:"adjusted_fee_reduction"`
	Reason              string      `json:"reason"`
	CreatedAt           time.Time   `json:"created_at"`
}

// RestaurantSettlement tracks the periodic net cash balance between restaurant and platform.
type RestaurantSettlement struct {
	ID                       uuid.UUID        `json:"id"`
	RestaurantID             uuid.UUID        `json:"restaurant_id"`
	PeriodStart              time.Time        `json:"period_start"`
	PeriodEnd                time.Time        `json:"period_end"`
	GrossSales               money.Money      `json:"gross_sales"`
	PlatformFeesOwed         money.Money      `json:"platform_fees_owed"`
	RefundAdjustments        money.Money      `json:"refund_adjustments"`
	NetPayablePlatform       money.Money      `json:"net_payable_platform"`
	Status                   SettlementStatus `json:"status"`
	InvoicedAt               *time.Time       `json:"invoiced_at,omitempty"`
	SettledAt                *time.Time       `json:"settled_at,omitempty"`
	CreatedAt                time.Time        `json:"created_at"`
}
