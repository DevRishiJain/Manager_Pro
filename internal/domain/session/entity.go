package session

import (
	"errors"
	"fmt"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/google/uuid"
)

var (
	ErrInvalidStateTransition = errors.New("invalid session state transition")
	ErrSessionClosed          = errors.New("session is already in a terminal state")
	ErrOptimisticLockConflict = errors.New("optimistic lock conflict: session version mismatch")
)

type State string

const (
	StateOpen            State = "OPEN"
	StateOpenVerified    State = "OPEN_VERIFIED"
	StateAwaitingPayment State = "AWAITING_PAYMENT"
	StatePaid            State = "PAID"
	StateCompleted       State = "COMPLETED"
	StateWalkout         State = "WALKOUT"
	StateExpired         State = "EXPIRED"
	StateForceClosed     State = "FORCE_CLOSED"
)

func (s State) IsTerminal() bool {
	switch s {
	case StateCompleted, StateWalkout, StateExpired, StateForceClosed:
		return true
	default:
		return false
	}
}

type CloseReason string

const (
	CloseReasonPaid        CloseReason = "PAID"
	CloseReasonWalkout     CloseReason = "WALKOUT"
	CloseReasonForceClosed CloseReason = "FORCE_CLOSED"
	CloseReasonExpired     CloseReason = "EXPIRED"
)

type ActorType string

const (
	ActorCustomer ActorType = "CUSTOMER_SESSION"
	ActorStaff    ActorType = "STAFF_USER"
	ActorGuard    ActorType = "GUARD_USER"
	ActorSystem   ActorType = "SYSTEM"
)

type Actor struct {
	Type ActorType `json:"type"`
	ID   string    `json:"id"`
}

// AllowedTransitions defines valid DiningSession state machine moves.
var AllowedTransitions = map[State][]State{
	StateOpen: {
		StateOpenVerified,
		StateExpired,
		StateForceClosed,
	},
	StateOpenVerified: {
		StateAwaitingPayment,
		StateExpired,
		StateForceClosed,
	},
	StateAwaitingPayment: {
		StatePaid,
		StateOpenVerified, // customer adds another order before paying
		StateWalkout,
		StateForceClosed,
	},
	StatePaid: {
		StateCompleted, // exit verified by guard or immediate if no guard module
	},
	StateCompleted:   {},
	StateWalkout:     {},
	StateExpired:     {},
	StateForceClosed: {},
}

func ValidateTransition(current, target State) error {
	allowed, ok := AllowedTransitions[current]
	if !ok {
		return fmt.Errorf("%w: unknown state %s", ErrInvalidStateTransition, current)
	}
	for _, next := range allowed {
		if next == target {
			return nil
		}
	}
	return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidStateTransition, current, target)
}

type DiningSession struct {
	CustomerName          string       `json:"customer_name,omitempty"`
	CustomerPhone         string       `json:"customer_phone,omitempty"`
	GuestCount            int          `json:"guest_count,omitempty"`
	VehicleNumber         string       `json:"vehicle_number,omitempty"`
	ID                    uuid.UUID    `json:"id"`
	RestaurantID          uuid.UUID    `json:"restaurant_id"`
	TableID               uuid.UUID    `json:"table_id"`
	Status                State        `json:"status"`
	OpenedAt              time.Time    `json:"opened_at"`
	ClosedAt              *time.Time   `json:"closed_at,omitempty"`
	VerifiedAt            *time.Time   `json:"verified_at,omitempty"`
	VerifiedByStaffID     *uuid.UUID   `json:"verified_by_staff_id,omitempty"`
	RunningTotal          money.Money  `json:"running_total"`
	FinalTotal            money.Money  `json:"final_total"`
	PlatformFeeAmount     money.Money  `json:"platform_fee_amount"`
	SessionToken          string       `json:"session_token"`
	DeviceFingerprint     string       `json:"device_fingerprint"`
	LastActivityAt        time.Time    `json:"last_activity_at"`
	ExpiryDeadline        time.Time    `json:"expiry_deadline"`
	AssistanceReason      string       `json:"assistance_reason,omitempty"`
	AssistanceRequestedAt *time.Time   `json:"assistance_requested_at,omitempty"`
	AssignedWaiterID      *uuid.UUID   `json:"assigned_waiter_id,omitempty"`
	AssignedWaiterName    string       `json:"assigned_waiter_name,omitempty"`
	CloseReason           *CloseReason `json:"close_reason,omitempty"`
	ClosedByActorType     *ActorType   `json:"closed_by_actor_type,omitempty"`
	ClosedByActorID       *string      `json:"closed_by_actor_id,omitempty"`
	Version               int          `json:"version"`
	CreatedAt             time.Time    `json:"created_at"`
	UpdatedAt             time.Time    `json:"updated_at"`
}

type SessionParticipant struct {
	CustomerPhone string    `json:"customer_phone,omitempty"`
	GuestCount    int       `json:"guest_count,omitempty"`
	VehicleNumber string    `json:"vehicle_number,omitempty"`
	ID            uuid.UUID `json:"id"`
	SessionID     uuid.UUID `json:"session_id"`
	DeviceToken   string    `json:"device_token"`
	DisplayName   string    `json:"display_name,omitempty"`
	JoinedAt      time.Time `json:"joined_at"`
}
