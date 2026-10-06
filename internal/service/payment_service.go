package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	adapterpay "github.com/devrishijain/table-manager/internal/adapter/payment"
	"github.com/devrishijain/table-manager/internal/domain/audit"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/devrishijain/table-manager/internal/ws"
	"github.com/google/uuid"
)

var (
	ErrPaymentNotFound        = errors.New("payment not found")
	ErrRefundExceedsPaid      = errors.New("refund amount exceeds original confirmed payment")
	ErrAdapterNotFound        = errors.New("no payment adapter registered for method")
	ErrSessionNotPayable      = errors.New("session is not in a payable state")
	ErrUnauthorizedRefund     = errors.New("unauthorized: manager or owner role required to issue refunds")
	ErrOnlinePaymentsDisabled = errors.New("online payments are currently disabled; please select Cash, UPI QR, or Card at counter")
	ErrSessionAlreadyPaid     = errors.New("session balance is already fully settled")
)

type PaymentService struct {
	repo          storage.Repository
	adapters      map[payment.Method]payment.PaymentConfirmationAdapter
	ledgerService *LedgerService
	exitService   *ExitService
	onlineEnabled bool
	dispatcher    *ws.OutboxDispatcher
}

func (s *PaymentService) SetOutboxDispatcher(d *ws.OutboxDispatcher) {
	s.dispatcher = d
}

func NewPaymentService(
	repo storage.Repository,
	ledgerService *LedgerService,
	exitService *ExitService,
	razorpaySecret string,
) *PaymentService {
	adapters := make(map[payment.Method]payment.PaymentConfirmationAdapter)
	adapters[payment.MethodOwnGateway] = adapterpay.NewOwnGatewayAdapter(razorpaySecret)
	adapters[payment.MethodCash] = adapterpay.NewCashAdapter()
	adapters[payment.MethodRestaurantPOS] = adapterpay.NewRestaurantPOSAdapter()
	adapters[payment.MethodExternalPlatform] = adapterpay.NewExternalPlatformAdapter()
	adapters[payment.MethodUPIQR] = adapterpay.NewUPIQRAdapter()
	adapters[payment.MethodPosCard] = adapterpay.NewPOSCardAdapter()
	adapters["POS_DIRECT_API"] = adapterpay.NewPOSDirectAPIAdapter()

	return &PaymentService{
		repo:          repo,
		adapters:      adapters,
		ledgerService: ledgerService,
		exitService:   exitService,
		onlineEnabled: false, // Cash-first by default
	}
}

func (s *PaymentService) SetOnlineEnabled(enabled bool) {
	s.onlineEnabled = enabled
}

// RequestBill moves session from OPEN_VERIFIED to AWAITING_PAYMENT, locking running_total into final_total.
func (s *PaymentService) RequestBill(ctx context.Context, sessionID uuid.UUID) (*session.DiningSession, error) {
	sess, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, ErrSessionNotFound
	}
	if sess.Status.IsTerminal() {
		return nil, session.ErrSessionClosed
	}

	if sess.Status == session.StateOpenVerified || sess.Status == session.StateOpen {
		if err := session.ValidateTransition(sess.Status, session.StateAwaitingPayment); err != nil {
			return nil, err
		}
		sess.Status = session.StateAwaitingPayment
		sess.FinalTotal = sess.RunningTotal
		sess.LastActivityAt = time.Now()
		if err := s.repo.UpdateSession(ctx, sess); err != nil {
			return nil, err
		}

		if s.dispatcher != nil {
			rooms := []string{
				fmt.Sprintf("session:%s", sess.ID.String()),
				fmt.Sprintf("restaurant:%s:floor", sess.RestaurantID.String()),
			}
			_, _ = ws.PublishEventToRooms(ctx, s.repo, s.dispatcher, sess.RestaurantID, "BILL_REQUESTED", rooms, sess.ID.String(), sess)
		}
	}

	return sess, nil
}

// InitiatePayment creates a PENDING_CONFIRMATION Payment record with strict server-side balance validation.
func (s *PaymentService) InitiatePayment(ctx context.Context, sessionID uuid.UUID, method payment.Method, amount money.Money, externalPlatformName *string) (*payment.Payment, error) {
	if method == payment.MethodOwnGateway && !s.onlineEnabled {
		return nil, ErrOnlinePaymentsDisabled
	}

	sess, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, ErrSessionNotFound
	}
	if sess.Status != session.StateAwaitingPayment && sess.Status != session.StateOpenVerified {
		return nil, ErrSessionNotPayable
	}

	// Calculate remaining unpaid balance on server
	allPayments, _ := s.repo.GetPaymentsBySessionID(ctx, sessionID)
	var totalPaidMinor int64
	for _, pay := range allPayments {
		if pay.Status == payment.StateConfirmed {
			totalPaidMinor += pay.Amount.AmountMinorUnits
		}
	}

	targetBill := sess.FinalTotal
	if targetBill.IsZero() {
		targetBill = sess.RunningTotal
	}
	remainingMinor := targetBill.AmountMinorUnits - totalPaidMinor
	if remainingMinor <= 0 && targetBill.AmountMinorUnits > 0 {
		return nil, ErrSessionAlreadyPaid
	}

	if amount.IsZero() || amount.AmountMinorUnits <= 0 {
		amount = money.New(remainingMinor)
	}

	now := time.Now()
	p := &payment.Payment{
		ID:                   uuid.New(),
		SessionID:            sessionID,
		RestaurantID:         sess.RestaurantID,
		Method:               method,
		ExternalPlatformName: externalPlatformName,
		Amount:               amount,
		Status:               payment.StatePendingConfirmation,
		Version:              1,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	if err := s.repo.CreatePayment(ctx, p); err != nil {
		return nil, err
	}

	rooms := []string{
		fmt.Sprintf("session:%s", p.SessionID.String()),
		fmt.Sprintf("restaurant:%s:floor", p.RestaurantID.String()),
	}
	_, _ = ws.PublishEventToRooms(ctx, s.repo, s.dispatcher, p.RestaurantID, "PAYMENT_INITIATED", rooms, p.ID.String(), p)

	return p, nil
}

// ConfirmPayment confirms a payment via its designated adapter and updates the session balance.
func (s *PaymentService) ConfirmPayment(ctx context.Context, req payment.PaymentConfirmationRequest) (*payment.Payment, error) {
	adapter, ok := s.adapters[req.Method]
	if !ok {
		return nil, ErrAdapterNotFound
	}

	sess, err := s.repo.GetSessionByID(ctx, req.SessionID)
	if err != nil {
		return nil, ErrSessionNotFound
	}

	if req.PaymentID == uuid.Nil {
		pendingPayments, _ := s.repo.GetPaymentsBySessionID(ctx, req.SessionID)
		for _, pp := range pendingPayments {
			if pp.Status == payment.StatePendingConfirmation {
				req.PaymentID = pp.ID
				break
			}
		}
		if req.PaymentID == uuid.Nil {
			req.PaymentID = uuid.New()
		}
	}

	// Adapter execution
	confirmedPayment, err := adapter.Confirm(ctx, req)
	if err != nil {
		return nil, err
	}

	// Update or save confirmed payment
	existing, err := s.repo.GetPaymentByID(ctx, req.PaymentID)
	if err == nil && existing != nil {
		confirmedPayment.ID = existing.ID
		confirmedPayment.Version = existing.Version
		if err := s.repo.UpdatePayment(ctx, confirmedPayment); err != nil {
			return nil, err
		}
	} else {
		if confirmedPayment.ID == uuid.Nil {
			confirmedPayment.ID = req.PaymentID
		}
		if err := s.repo.CreatePayment(ctx, confirmedPayment); err != nil {
			return nil, err
		}
	}

	now := time.Now()
	payBytes, _ := json.Marshal(confirmedPayment)
	auditLogID := uuid.New()

	actorType := audit.ActorTypeCustomer
	actorID := sess.SessionToken
	if req.ConfirmedByStaffID != nil {
		actorType = audit.ActorTypeStaff
		actorID = req.ConfirmedByStaffID.String()
	}

	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           auditLogID,
		ActorType:    actorType,
		ActorID:      actorID,
		RestaurantID: req.RestaurantID,
		SessionID:    &req.SessionID,
		Action:       fmt.Sprintf("PAYMENT_CONFIRMED_%s", req.Method),
		AfterState:   payBytes,
		CreatedAt:    now,
	})

	// If confirmed by staff, record in StaffAction
	if req.ConfirmedByStaffID != nil {
		_ = s.repo.AppendStaffAction(ctx, &audit.StaffAction{
			ID:           uuid.New(),
			AuditLogID:   auditLogID,
			StaffID:      *req.ConfirmedByStaffID,
			RestaurantID: req.RestaurantID,
			SessionID:    &req.SessionID,
			ActionType:   "PAYMENT_CONFIRM",
			Reason:       string(req.Method),
			CreatedAt:    now,
		})
	}

	// Reconcile total payments against session bill
	allPayments, _ := s.repo.GetPaymentsBySessionID(ctx, req.SessionID)
	var totalPaidMinor int64
	for _, p := range allPayments {
		if p.Status == payment.StateConfirmed {
			totalPaidMinor += p.Amount.AmountMinorUnits
		}
	}

	targetBill := sess.FinalTotal
	if targetBill.IsZero() {
		targetBill = sess.RunningTotal
		sess.FinalTotal = targetBill
	}

	// Check if fully settled: sum(CONFIRMED) >= final_total
	if totalPaidMinor >= targetBill.AmountMinorUnits && targetBill.AmountMinorUnits > 0 {
		// Overpayment handling: create explicit adjustment record if total payments exceed bill and not recorded yet
		if totalPaidMinor > targetBill.AmountMinorUnits {
			adjs, _ := s.repo.GetAdjustmentsBySessionID(ctx, sess.ID)
			hasOverpay := false
			for _, a := range adjs {
				if a.Type == payment.AdjustmentTypeOverpaymentCredit {
					hasOverpay = true
					break
				}
			}
			if !hasOverpay {
				overpayMinor := totalPaidMinor - targetBill.AmountMinorUnits
				_ = s.repo.CreateAdjustment(ctx, &payment.Adjustment{
					ID:           uuid.New(),
					SessionID:    sess.ID,
					RestaurantID: sess.RestaurantID,
					Type:         payment.AdjustmentTypeOverpaymentCredit,
					Amount:       money.New(overpayMinor),
					Notes:        "Automated overpayment credit balance",
					CreatedAt:    now,
				})
			}
		}
		// Move session from AWAITING_PAYMENT to PAID
		if sess.Status != session.StatePaid && sess.Status != session.StateCompleted {
			sess.Status = session.StatePaid
			sess.LastActivityAt = now
			closeReason := session.CloseReasonPaid
			sess.CloseReason = &closeReason

			// Check if update succeeded (optimistic lock winner).
			// If two concurrent payments finish together, only ONE goroutine wins the session transition!
			if err := s.repo.UpdateSession(ctx, sess); err == nil {
				// Calculate Platform Fee Ledger Entry (idempotent)
				if s.ledgerService != nil {
					_, _ = s.ledgerService.ComputeSessionPlatformFee(ctx, sess)
				}

				// Issue Exit Pass OTP so gatepass is active for customer display & waiter verification
				if s.exitService != nil {
					_, _, _ = s.exitService.IssueExitPass(ctx, sess.ID)
				}

				// Outbox event
				_ = s.repo.StoreOutboxEvent(ctx, &storage.OutboxEvent{
					ID:           uuid.New(),
					RestaurantID: sess.RestaurantID,
					EventType:    "SESSION_PAID",
					AggregateID:  sess.ID.String(),
					Payload:      payBytes,
					Status:       storage.OutboxStatusPending,
					CreatedAt:    now,
				})
			}
		}
	}

	rooms := []string{
		fmt.Sprintf("session:%s", confirmedPayment.SessionID.String()),
		fmt.Sprintf("restaurant:%s:floor", confirmedPayment.RestaurantID.String()),
		fmt.Sprintf("restaurant:%s:dashboard", confirmedPayment.RestaurantID.String()),
	}
	_, _ = ws.PublishEventToRooms(ctx, s.repo, s.dispatcher, confirmedPayment.RestaurantID, "PAYMENT_CONFIRMED", rooms, confirmedPayment.ID.String(), confirmedPayment)

	return confirmedPayment, nil
}

// IssueRefund handles partial or full refunds.
func (s *PaymentService) IssueRefund(ctx context.Context, paymentID, staffID uuid.UUID, amount money.Money, reason string) (*payment.Refund, error) {
	staff, err := s.repo.GetStaffByID(ctx, staffID)
	if err != nil || !staff.Role.CanIssueRefund() {
		return nil, ErrUnauthorizedRefund
	}

	pay, err := s.repo.GetPaymentByID(ctx, paymentID)
	if err != nil {
		return nil, ErrPaymentNotFound
	}
	if amount.GreaterThan(pay.Amount) {
		return nil, ErrRefundExceedsPaid
	}

	now := time.Now()
	refund := &payment.Refund{
		ID:           uuid.New(),
		PaymentID:    paymentID,
		SessionID:    pay.SessionID,
		RestaurantID: pay.RestaurantID,
		Amount:       amount,
		Reason:       reason,
		InitiatedBy:  staffID,
		CreatedAt:    now,
	}

	if err := s.repo.CreateRefund(ctx, refund); err != nil {
		return nil, err
	}

	// If session already COMPLETED, record a RefundAdjustment on the Platform Fee Ledger
	sess, err := s.repo.GetSessionByID(ctx, pay.SessionID)
	if err == nil && sess.Status == session.StateCompleted && s.ledgerService != nil {
		_ = s.ledgerService.RecordRefundAdjustment(ctx, sess, refund)
	}

	return refund, nil
}

func (s *PaymentService) GetPaymentsBySession(ctx context.Context, sessionID uuid.UUID) ([]payment.Payment, error) {
	return s.repo.GetPaymentsBySessionID(ctx, sessionID)
}

// VoidPayment handles manager-authorized payment voiding with state reversal and immutable audit logging.
func (s *PaymentService) VoidPayment(ctx context.Context, paymentID, staffID uuid.UUID, reason string) (*payment.Payment, error) {
	p, err := s.repo.GetPaymentByID(ctx, paymentID)
	if err != nil {
		return nil, ErrPaymentNotFound
	}
	if p.Status != payment.StateConfirmed {
		return nil, fmt.Errorf("cannot void payment in status %s: only confirmed payments can be voided", p.Status)
	}

	// Verify manager permissions
	staff, err := s.repo.GetStaffByID(ctx, staffID)
	if err != nil {
		return nil, errors.New("unauthorized: staff member not found")
	}
	if staff.RestaurantID != p.RestaurantID {
		return nil, errors.New("unauthorized: cross-tenant staff action")
	}
	if staff.Role != "MANAGER" && staff.Role != "RESTAURANT_ADMIN" && staff.Role != "RESTAURANT_OWNER" {
		return nil, errors.New("unauthorized: manager or admin role required to void payment")
	}

	if err := payment.ValidateTransition(p.Status, payment.StateVoided); err != nil {
		return nil, err
	}

	now := time.Now()
	p.Status = payment.StateVoided
	p.UpdatedAt = now
	if err := s.repo.UpdatePayment(ctx, p); err != nil {
		return nil, err
	}

	// Reconcile session state if it was terminal or paid
	sess, err := s.repo.GetSessionByID(ctx, p.SessionID)
	if err == nil {
		allPayments, _ := s.repo.GetPaymentsBySessionID(ctx, p.SessionID)
		var remainingPaidMinor int64
		for _, pay := range allPayments {
			if pay.Status == payment.StateConfirmed {
				remainingPaidMinor += pay.Amount.AmountMinorUnits
			}
		}
		if remainingPaidMinor < sess.FinalTotal.AmountMinorUnits {
			if sess.Status == session.StatePaid || sess.Status == session.StateCompleted {
				sess.Status = session.StateAwaitingPayment
				sess.UpdatedAt = now
				_ = s.repo.UpdateSession(ctx, sess)
			}
		}
	}

	// Audit logging & Outbox event
	payBytes, _ := json.Marshal(p)
	auditID := uuid.New()
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           auditID,
		ActorType:    audit.ActorTypeStaff,
		ActorID:      staffID.String(),
		RestaurantID: p.RestaurantID,
		SessionID:    &p.SessionID,
		Action:       "PAYMENT_VOIDED",
		AfterState:   payBytes,
		CreatedAt:    now,
	})
	_ = s.repo.AppendStaffAction(ctx, &audit.StaffAction{
		ID:           uuid.New(),
		AuditLogID:   auditID,
		StaffID:      staffID,
		RestaurantID: p.RestaurantID,
		SessionID:    &p.SessionID,
		ActionType:   "PAYMENT_VOID",
		Reason:       reason,
		Metadata:     payBytes,
		CreatedAt:    now,
	})
	rooms := []string{
		fmt.Sprintf("session:%s", p.SessionID.String()),
		fmt.Sprintf("restaurant:%s:floor", p.RestaurantID.String()),
		fmt.Sprintf("restaurant:%s:dashboard", p.RestaurantID.String()),
	}
	_, _ = ws.PublishEventToRooms(ctx, s.repo, s.dispatcher, p.RestaurantID, "PAYMENT_VOIDED", rooms, p.ID.String(), p)

	return p, nil
}
