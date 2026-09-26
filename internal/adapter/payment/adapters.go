package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/google/uuid"
)

var (
	ErrMissingStaffActor   = errors.New("payment method requires authenticated staff confirmation")
	ErrMissingEvidence     = errors.New("external platform payment requires transaction ID and evidentiary proof photo")
	ErrInvalidSignature    = errors.New("gateway signature verification failed")
	ErrUnsupportedPlatform = errors.New("unsupported external platform name")
)

// ---------------- 1. OwnGatewayAdapter (Razorpay) ----------------

type OwnGatewayAdapter struct {
	webhookSecret string
}

func NewOwnGatewayAdapter(webhookSecret string) *OwnGatewayAdapter {
	return &OwnGatewayAdapter{webhookSecret: webhookSecret}
}

func (a *OwnGatewayAdapter) Method() payment.Method {
	return payment.MethodOwnGateway
}

func (a *OwnGatewayAdapter) RequiresStaffConfirmation() bool {
	return false
}

func (a *OwnGatewayAdapter) RequiresEvidence() bool {
	return false
}

func (a *OwnGatewayAdapter) Confirm(ctx context.Context, req payment.PaymentConfirmationRequest) (*payment.Payment, error) {
	// Verify signature if secret and signature are provided
	if a.webhookSecret != "" && req.GatewaySignature != nil {
		mac := hmac.New(sha256.New, []byte(a.webhookSecret))
		mac.Write(req.RawPayload)
		expectedSig := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(*req.GatewaySignature), []byte(expectedSig)) {
			return nil, ErrInvalidSignature
		}
	}

	now := time.Now()
	p := &payment.Payment{
		ID:                 req.PaymentID,
		SessionID:          req.SessionID,
		RestaurantID:       req.RestaurantID,
		Method:             payment.MethodOwnGateway,
		Amount:             req.Amount,
		Status:             payment.StateConfirmed,
		GatewayReferenceID: req.GatewayReferenceID,
		ConfirmedAt:        &now,
		Version:            1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	return p, nil
}

// ---------------- 2. CashAdapter ----------------

type CashAdapter struct{}

func NewCashAdapter() *CashAdapter {
	return &CashAdapter{}
}

func (a *CashAdapter) Method() payment.Method {
	return payment.MethodCash
}

func (a *CashAdapter) RequiresStaffConfirmation() bool {
	return true
}

func (a *CashAdapter) RequiresEvidence() bool {
	return false
}

func (a *CashAdapter) Confirm(ctx context.Context, req payment.PaymentConfirmationRequest) (*payment.Payment, error) {
	if req.ConfirmedByStaffID == nil || *req.ConfirmedByStaffID == uuid.Nil {
		return nil, ErrMissingStaffActor
	}

	payID := req.PaymentID
	if payID == uuid.Nil {
		payID = uuid.New()
	}

	now := time.Now()
	p := &payment.Payment{
		ID:                 payID,
		SessionID:          req.SessionID,
		RestaurantID:       req.RestaurantID,
		Method:             payment.MethodCash,
		Amount:             req.Amount,
		Status:             payment.StateConfirmed,
		ConfirmedByStaffID: req.ConfirmedByStaffID,
		ConfirmedAt:        &now,
		Version:            1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	return p, nil
}

// ---------------- 3. RestaurantPOSAdapter ----------------

type RestaurantPOSAdapter struct{}

func NewRestaurantPOSAdapter() *RestaurantPOSAdapter {
	return &RestaurantPOSAdapter{}
}

func (a *RestaurantPOSAdapter) Method() payment.Method {
	return payment.MethodRestaurantPOS
}

func (a *RestaurantPOSAdapter) RequiresStaffConfirmation() bool {
	return true
}

func (a *RestaurantPOSAdapter) RequiresEvidence() bool {
	return false // Evidence optional for internal POS
}

func (a *RestaurantPOSAdapter) Confirm(ctx context.Context, req payment.PaymentConfirmationRequest) (*payment.Payment, error) {
	if req.ConfirmedByStaffID == nil || *req.ConfirmedByStaffID == uuid.Nil {
		return nil, ErrMissingStaffActor
	}

	payID := req.PaymentID
	if payID == uuid.Nil {
		payID = uuid.New()
	}

	now := time.Now()
	p := &payment.Payment{
		ID:                    payID,
		SessionID:             req.SessionID,
		RestaurantID:          req.RestaurantID,
		Method:                payment.MethodRestaurantPOS,
		Amount:                req.Amount,
		Status:                payment.StateConfirmed,
		EvidenceTransactionID: req.EvidenceTransactionID,
		EvidenceBucket:        req.EvidenceBucket,
		EvidenceObjectKey:     req.EvidenceObjectKey,
		EvidenceContentType:   req.EvidenceContentType,
		EvidenceSizeBytes:     req.EvidenceSizeBytes,
		EvidenceSHA256:        req.EvidenceSHA256,
		EvidenceUploadedAt:    req.EvidenceUploadedAt,
		EvidencePhotoURL:      req.EvidencePhotoURL,
		ConfirmedByStaffID:    req.ConfirmedByStaffID,
		ConfirmedAt:           &now,
		Version:               1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	return p, nil
}

// ---------------- 4. ExternalPlatformAdapter (EazyDiner, Dineout, District) ----------------

// ExternalPlatformAdapter handles 3rd-party aggregators before official API partnership.
//
// DESIGN CRITICAL NOTE: Staff confirmation is the actual trust boundary.
// Evidence photo & transaction ID are audit trails, NOT automated validation controls.
type ExternalPlatformAdapter struct{}

func NewExternalPlatformAdapter() *ExternalPlatformAdapter {
	return &ExternalPlatformAdapter{}
}

func (a *ExternalPlatformAdapter) Method() payment.Method {
	return payment.MethodExternalPlatform
}

func (a *ExternalPlatformAdapter) RequiresStaffConfirmation() bool {
	return true
}

func (a *ExternalPlatformAdapter) RequiresEvidence() bool {
	return true
}

func (a *ExternalPlatformAdapter) Confirm(ctx context.Context, req payment.PaymentConfirmationRequest) (*payment.Payment, error) {
	if req.ConfirmedByStaffID == nil || *req.ConfirmedByStaffID == uuid.Nil {
		return nil, ErrMissingStaffActor
	}
	if req.ExternalPlatformName == nil || *req.ExternalPlatformName == "" {
		return nil, ErrUnsupportedPlatform
	}
	hasPhoto := (req.EvidencePhotoURL != nil && *req.EvidencePhotoURL != "") || (req.EvidenceObjectKey != nil && *req.EvidenceObjectKey != "")
	if req.EvidenceTransactionID == nil || *req.EvidenceTransactionID == "" || !hasPhoto {
		return nil, ErrMissingEvidence
	}

	now := time.Now()
	p := &payment.Payment{
		ID:                    req.PaymentID,
		SessionID:             req.SessionID,
		RestaurantID:          req.RestaurantID,
		Method:                payment.MethodExternalPlatform,
		ExternalPlatformName:  req.ExternalPlatformName,
		Amount:                req.Amount,
		Status:                payment.StateConfirmed,
		EvidenceTransactionID: req.EvidenceTransactionID,
		EvidenceBucket:        req.EvidenceBucket,
		EvidenceObjectKey:     req.EvidenceObjectKey,
		EvidenceContentType:   req.EvidenceContentType,
		EvidenceSizeBytes:     req.EvidenceSizeBytes,
		EvidenceSHA256:        req.EvidenceSHA256,
		EvidenceUploadedAt:    req.EvidenceUploadedAt,
		EvidencePhotoURL:      req.EvidencePhotoURL,
		ConfirmedByStaffID:    req.ConfirmedByStaffID,
		ConfirmedAt:           &now,
		Version:               1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	return p, nil
}

// ---------------- 5. POSDirectAPIAdapter (Phase 4 Stub) ----------------

// POSDirectAPIAdapter represents future direct POS sync (e.g. Petpooja, Posist).
// Follows identical PaymentConfirmationAdapter contract without touching core logic.
type POSDirectAPIAdapter struct{}

func NewPOSDirectAPIAdapter() *POSDirectAPIAdapter {
	return &POSDirectAPIAdapter{}
}

func (a *POSDirectAPIAdapter) Method() payment.Method {
	return "POS_DIRECT_API"
}

func (a *POSDirectAPIAdapter) RequiresStaffConfirmation() bool {
	return false // Automated direct POS hook
}

func (a *POSDirectAPIAdapter) RequiresEvidence() bool {
	return false
}

func (a *POSDirectAPIAdapter) Confirm(ctx context.Context, req payment.PaymentConfirmationRequest) (*payment.Payment, error) {
	now := time.Now()
	p := &payment.Payment{
		ID:                 req.PaymentID,
		SessionID:          req.SessionID,
		RestaurantID:       req.RestaurantID,
		Method:             "POS_DIRECT_API",
		Amount:             req.Amount,
		Status:             payment.StateConfirmed,
		GatewayReferenceID: req.GatewayReferenceID,
		ConfirmedAt:        &now,
		Version:            1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	return p, nil
}
