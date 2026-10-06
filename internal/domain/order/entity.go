package order

import (
	"errors"
	"fmt"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/google/uuid"
)

var (
	ErrInvalidOrderTransition = errors.New("invalid order state transition")
	ErrOrderTerminal          = errors.New("order is already in a terminal state")
	ErrItemOutOfStock         = errors.New("menu item is out of stock")
	ErrPriceMismatch          = errors.New("client price does not match current menu price")
)

type State string

const (
	StatePlacedUnverified State = "PLACED_UNVERIFIED"
	StatePlacedVerified   State = "PLACED_VERIFIED"
	StateAccepted         State = "ACCEPTED"
	StateRejected         State = "REJECTED"
	StatePreparing        State = "PREPARING"
	StateReady            State = "READY"
	StateServed           State = "SERVED"
	StateCancelled        State = "CANCELLED"
)

func (s State) IsTerminal() bool {
	return s == StateServed || s == StateRejected || s == StateCancelled
}

type CancellationStage string

const (
	CancellationPreAcceptance        CancellationStage = "PRE_ACCEPTANCE"
	CancellationPostAcceptancePrePrep CancellationStage = "POST_ACCEPTANCE_PRE_PREP"
	CancellationPostPrepStart        CancellationStage = "POST_PREP_START"
)

var AllowedOrderTransitions = map[State][]State{
	StatePlacedUnverified: {
		StatePlacedVerified,
		StateAccepted, // first order direct accept by staff OTP
		StateCancelled,
	},
	StatePlacedVerified: {
		StateAccepted,
		StatePreparing,
		StateReady,
		StateRejected,
		StateCancelled,
	},
	StateAccepted: {
		StatePreparing,
		StateCancelled,
	},
	StatePreparing: {
		StateReady,
		StateCancelled,
	},
	StateReady: {
		StateServed,
		StateCancelled,
	},
	StateServed:    {},
	StateRejected:  {},
	StateCancelled: {},
}

func ValidateTransition(current, target State) error {
	allowed, ok := AllowedOrderTransitions[current]
	if !ok {
		return fmt.Errorf("%w: unknown state %s", ErrInvalidOrderTransition, current)
	}
	for _, next := range allowed {
		if next == target {
			return nil
		}
	}
	return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidOrderTransition, current, target)
}

type Order struct {
	ID                       uuid.UUID          `json:"id"`
	SessionID                uuid.UUID          `json:"session_id"`
	RestaurantID             uuid.UUID          `json:"restaurant_id"`
	SequenceNumber           int                `json:"sequence_number"`
	TableNumber              string             `json:"table_number,omitempty"`
	CustomerName             string             `json:"customer_name,omitempty"`
	CustomerPhone            string             `json:"customer_phone,omitempty"`
	GuestCount               int                `json:"guest_count,omitempty"`
	VehicleNumber            string             `json:"vehicle_number,omitempty"`
	Status                   State              `json:"status"`
	PlacedAt                 time.Time          `json:"placed_at"`
	AcceptedAt               *time.Time         `json:"accepted_at,omitempty"`
	AcceptedByStaffID        *uuid.UUID         `json:"accepted_by_staff_id,omitempty"`
	Subtotal                 money.Money        `json:"subtotal"`
	TaxTotal                 money.Money        `json:"tax_total"`
	Total                    money.Money        `json:"total"`
	CancelledAt              *time.Time         `json:"cancelled_at,omitempty"`
	CancellationStage        *CancellationStage `json:"cancellation_stage,omitempty"`
	CancellationFeeApplicable bool              `json:"cancellation_fee_applicable,omitempty"`
	Items                    []OrderItem        `json:"items,omitempty"`
	Version                  int                `json:"version,omitempty"`
	CreatedAt                time.Time          `json:"created_at,omitempty"`
	UpdatedAt                time.Time          `json:"updated_at,omitempty"`
}

// OrderItem represents a complete price and tax snapshot at order submission time.
// Once created, menu price changes never affect historical OrderItems.
type OrderItem struct {
	ID                  uuid.UUID   `json:"id"`
	OrderID             uuid.UUID   `json:"order_id,omitempty"`
	MenuItemID          uuid.UUID   `json:"menu_item_id,omitempty"`
	VariantID           *uuid.UUID  `json:"variant_id,omitempty"`
	ItemNameSnapshot    string      `json:"item_name_snapshot"`
	Quantity            int         `json:"quantity"`
	UnitPriceSnapshot   money.Money `json:"unit_price_snapshot"`
	LineTotal           money.Money `json:"line_total"`
	HSNSACCodeSnapshot  string      `json:"hsn_sac_code_snapshot,omitempty"`  // e.g. "996331"
	CGSTRateBpsSnapshot int64       `json:"cgst_rate_bps_snapshot,omitempty"` // Basis points, e.g. 250 for 2.5%
	SGSTRateBpsSnapshot int64       `json:"sgst_rate_bps_snapshot,omitempty"` // Basis points, e.g. 250 for 2.5%
	CGSTAmount          money.Money `json:"cgst_amount,omitempty"`
	SGSTAmount          money.Money `json:"sgst_amount,omitempty"`
	SpecialInstructions string      `json:"special_instructions,omitempty"`
	CreatedAt           time.Time   `json:"created_at,omitempty"`
}

// CartItem is an ephemeral client submission payload for ordering.
// Never stored in the database.
type CartItem struct {
	MenuItemID          uuid.UUID `json:"menu_item_id"`
	VariantID           *uuid.UUID `json:"variant_id,omitempty"`
	Quantity            int       `json:"quantity"`
	SpecialInstructions string    `json:"special_instructions,omitempty"`
}

type StatusHistory struct {
	ID               uuid.UUID  `json:"id"`
	OrderID          uuid.UUID  `json:"order_id"`
	RestaurantID     uuid.UUID  `json:"restaurant_id"`
	FromStatus       State      `json:"from_status"`
	ToStatus         State      `json:"to_status"`
	ChangedByStaffID *uuid.UUID `json:"changed_by_staff_id,omitempty"`
	Reason           string     `json:"reason,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}
