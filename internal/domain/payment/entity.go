package payment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/google/uuid"
)

var (
	ErrInvalidPaymentTransition = errors.New("invalid payment state transition")
	ErrStaffAuthRequired        = errors.New("staff confirmation required for this payment method")
	ErrEvidenceRequired         = errors.New("evidence transaction ID and proof photo required for this payment method")
	ErrGatewaySignatureFailed   = errors.New("gateway signature verification failed")
	ErrPaymentAlreadyProcessed  = errors.New("payment has already been processed")
)

type Method string

const (
	MethodOwnGateway       Method = "OWN_GATEWAY"
	MethodCash             Method = "CASH"
	MethodRestaurantPOS    Method = "RESTAURANT_POS"
	MethodExternalPlatform Method = "EXTERNAL_PLATFORM"
)

type State string

const (
	StateInitiated           State = "INITIATED"
	StatePendingConfirmation State = "PENDING_CONFIRMATION"
	StateConfirmed           State = "CONFIRMED"
	StateFailed              State = "FAILED"
)

var AllowedPaymentTransitions = map[State][]State{
	StateInitiated: {
		StatePendingConfirmation,
		StateConfirmed, // Direct webhook or instant confirmation
		StateFailed,
	},
	StatePendingConfirmation: {
		StateConfirmed,
		StateFailed,
	},
	StateConfirmed: {},
	StateFailed:    {},
}

func ValidateTransition(current, target State) error {
	allowed, ok := AllowedPaymentTransitions[current]
	if !ok {
		return fmt.Errorf("%w: unknown state %s", ErrInvalidPaymentTransition, current)
	}
	for _, next := range allowed {
		if next == target {
			return nil
		}
	}
	return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidPaymentTransition, current, target)
}

type Payment struct {
	ID                    uuid.UUID   `json:"id"`
	SessionID             uuid.UUID   `json:"session_id"`
	RestaurantID          uuid.UUID   `json:"restaurant_id"`
	Method                Method      `json:"method"`
	ExternalPlatformName  *string     `json:"external_platform_name,omitempty"` // e.g. EAZYDINER, DINEOUT, DISTRICT
	Amount                money.Money `json:"amount"`
	Status                State       `json:"status"`
	GatewayReferenceID    *string     `json:"gateway_reference_id,omitempty"` // Razorpay order_id / payment_id
	EvidenceTransactionID *string     `json:"evidence_transaction_id,omitempty"`
	EvidenceBucket        *string     `json:"evidence_bucket,omitempty"`
	EvidenceObjectKey     *string     `json:"evidence_object_key,omitempty"`
	EvidenceContentType   *string     `json:"evidence_content_type,omitempty"`
	EvidenceSizeBytes     *int64      `json:"evidence_size_bytes,omitempty"`
	EvidenceSHA256        *string     `json:"evidence_sha256,omitempty"`
	EvidenceUploadedAt    *time.Time  `json:"evidence_uploaded_at,omitempty"`
	EvidencePhotoURL      *string     `json:"evidence_photo_url,omitempty"`
	ConfirmedByStaffID    *uuid.UUID  `json:"confirmed_by_staff_id,omitempty"`
	ConfirmedAt           *time.Time  `json:"confirmed_at,omitempty"`
	Version               int         `json:"version"`
	CreatedAt             time.Time   `json:"created_at"`
	UpdatedAt             time.Time   `json:"updated_at"`
}

type Refund struct {
	ID           uuid.UUID   `json:"id"`
	PaymentID    uuid.UUID   `json:"payment_id"`
	SessionID    uuid.UUID   `json:"session_id"`
	RestaurantID uuid.UUID   `json:"restaurant_id"`
	Amount       money.Money `json:"amount"`
	Reason       string      `json:"reason"`
	InitiatedBy  uuid.UUID   `json:"initiated_by"` // staff_id
	CreatedAt    time.Time   `json:"created_at"`
}

type AdjustmentType string

const (
	AdjustmentTypeOverpaymentRefund AdjustmentType = "OVERPAYMENT_REFUND"
	AdjustmentTypeOverpaymentCredit AdjustmentType = "OVERPAYMENT_CREDIT"
	AdjustmentTypeManualCorrection  AdjustmentType = "MANUAL_CORRECTION"
)

type Adjustment struct {
	ID           uuid.UUID      `json:"id"`
	SessionID    uuid.UUID      `json:"session_id"`
	RestaurantID uuid.UUID      `json:"restaurant_id"`
	Type         AdjustmentType `json:"type"`
	Amount       money.Money    `json:"amount"`
	Notes        string         `json:"notes"`
	CreatedBy    uuid.UUID      `json:"created_by"`
	CreatedAt    time.Time      `json:"created_at"`
}

type PaymentConfirmationRequest struct {
	PaymentID             uuid.UUID
	SessionID             uuid.UUID
	RestaurantID          uuid.UUID
	Amount                money.Money
	Method                Method
	ExternalPlatformName  *string
	GatewayReferenceID    *string
	GatewaySignature      *string
	EvidenceTransactionID *string
	EvidenceBucket        *string
	EvidenceObjectKey     *string
	EvidenceContentType   *string
	EvidenceSizeBytes     *int64
	EvidenceSHA256        *string
	EvidenceUploadedAt    *time.Time
	EvidencePhotoURL      *string
	ConfirmedByStaffID    *uuid.UUID // Required for Cash, POS, External Platform
	RawPayload            []byte
}

// PaymentConfirmationAdapter is the pluggable interface for payment settlement rails.
type PaymentConfirmationAdapter interface {
	Method() Method
	RequiresStaffConfirmation() bool
	RequiresEvidence() bool
	Confirm(ctx context.Context, req PaymentConfirmationRequest) (*Payment, error)
}
