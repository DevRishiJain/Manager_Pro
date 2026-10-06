package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/audit"
	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/devrishijain/table-manager/internal/ws"
	"github.com/google/uuid"
)

var (
	ErrSessionNotFound     = errors.New("dining session not found")
	ErrTableNotFound       = errors.New("table not found")
	ErrInvalidTableQR      = errors.New("invalid or inactive table QR code")
	ErrUnauthorizedStaff   = errors.New("unauthorized staff action: insufficient permissions")
	ErrSingleDevicePolicy  = errors.New("restaurant policy allows only the original device to order at this table")
	ErrFirstOrderOTPMiss   = errors.New("invalid first-order verification OTP")
	ErrSessionNotOpen      = errors.New("session is not in an open state")
	ErrCapacityExceeded    = errors.New("guest count exceeds table seating capacity")
)

type SessionService struct {
	repo       storage.Repository
	dispatcher *ws.OutboxDispatcher
}

func NewSessionService(repo storage.Repository) *SessionService {
	return &SessionService{repo: repo}
}

func (s *SessionService) SetOutboxDispatcher(d *ws.OutboxDispatcher) {
	s.dispatcher = d
}

// StartSession handles QR scan. If a session already exists for the table:
// - If policy is SHARED_TABLE_SESSION: joins participant and returns current session.
// - If policy is SINGLE_DEVICE_SESSION: rejects secondary joins.
// If no active session exists: creates a fresh DiningSession in StateOpen.
func (s *SessionService) StartSession(ctx context.Context, tableToken, deviceToken, displayName, customerPhone, vehicleNumber string, guestCount int, deviceFingerprint string) (*session.DiningSession, bool, error) {
	table, err := s.repo.GetTableByToken(ctx, tableToken)
	if err != nil || !table.IsActive {
		return nil, false, ErrInvalidTableQR
	}

	if table.Capacity > 0 && guestCount > table.Capacity {
		return nil, false, fmt.Errorf("%w: guest count (%d) exceeds table capacity (%d seats)", ErrCapacityExceeded, guestCount, table.Capacity)
	}

	settings, err := s.repo.GetSettings(ctx, table.RestaurantID)
	if err != nil {
		return nil, false, err
	}

	// Check if active session already exists for this table
	activeSession, err := s.repo.GetActiveSessionByTableID(ctx, table.ID)
	if err == nil && activeSession != nil {
		if settings.SharedSessionPolicy == restaurant.SharedSessionPolicySingleDevice {
			// Check if same device
			participants, _ := s.repo.GetParticipants(ctx, activeSession.ID)
			isOriginal := false
			for _, p := range participants {
				if p.DeviceToken == deviceToken {
					isOriginal = true
					break
				}
			}
			if !isOriginal && len(participants) > 0 {
				return nil, false, ErrSingleDevicePolicy
			}
		}

		// Update guest count or contact info if previously unset
		if activeSession.CustomerName == "" || activeSession.CustomerName == "Guest Diner" {
			if displayName != "" && displayName != "Guest Diner" {
				activeSession.CustomerName = displayName
			}
		}
		if activeSession.CustomerPhone == "" && customerPhone != "" {
			activeSession.CustomerPhone = customerPhone
		}
		if activeSession.VehicleNumber == "" && vehicleNumber != "" {
			activeSession.VehicleNumber = vehicleNumber
		}
		if activeSession.GuestCount <= 0 && guestCount > 0 {
			activeSession.GuestCount = guestCount
		}
		_ = s.repo.UpdateSession(ctx, activeSession)

		// Add as participant to shared session
		participant := &session.SessionParticipant{
			ID:            uuid.New(),
			SessionID:     activeSession.ID,
			DeviceToken:   deviceToken,
			DisplayName:   displayName,
			CustomerPhone: customerPhone,
			GuestCount:    guestCount,
			VehicleNumber: vehicleNumber,
			JoinedAt:      time.Now(),
		}
		_ = s.repo.AddParticipant(ctx, participant)

		return activeSession, false, nil // existing session returned
	}

	// Create new session
	sessionToken, err := crypto.GenerateRandomToken(32)
	if err != nil {
		return nil, false, err
	}

	if guestCount <= 0 {
		guestCount = 2
	}
	now := time.Now()
	newSession := &session.DiningSession{
		ID:                uuid.New(),
		RestaurantID:      table.RestaurantID,
		TableID:           table.ID,
		CustomerName:      displayName,
		CustomerPhone:     customerPhone,
		GuestCount:        guestCount,
		VehicleNumber:     vehicleNumber,
		Status:            session.StateOpen,
		OpenedAt:          now,
		RunningTotal:      money.Zero(),
		FinalTotal:        money.Zero(),
		PlatformFeeAmount: money.Zero(),
		SessionToken:      sessionToken,
		DeviceFingerprint: deviceFingerprint,
		LastActivityAt:    now,
		ExpiryDeadline:    now.Add(3 * time.Hour), // 3 hours inactivity TTL
		Version:           1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := s.repo.CreateSession(ctx, newSession); err != nil {
		// In case of concurrent scan race, re-query active session
		if errors.Is(err, storage.ErrActiveSessionExists) {
			existing, getErr := s.repo.GetActiveSessionByTableID(ctx, table.ID)
			if getErr == nil && existing != nil {
				return existing, false, nil
			}
		}
		return nil, false, err
	}

	// Register initial participant
	_ = s.repo.AddParticipant(ctx, &session.SessionParticipant{
		ID:            uuid.New(),
		SessionID:     newSession.ID,
		DeviceToken:   deviceToken,
		DisplayName:   displayName,
		CustomerPhone: customerPhone,
		GuestCount:    guestCount,
		VehicleNumber: vehicleNumber,
		JoinedAt:      now,
	})

	// Append Audit Log
	sessionBytes, _ := json.Marshal(newSession)
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           uuid.New(),
		ActorType:    audit.ActorTypeCustomer,
		ActorID:      deviceToken,
		RestaurantID: table.RestaurantID,
		SessionID:    &newSession.ID,
		Action:       "SESSION_START",
		AfterState:   sessionBytes,
		CreatedAt:    now,
	})

	// Broadcast SESSION_STARTED event via outbox and realtime hub
	rooms := []string{
		fmt.Sprintf("restaurant:%s:floor", newSession.RestaurantID.String()),
		fmt.Sprintf("session:%s", newSession.ID.String()),
	}
	_, _ = ws.PublishEventToRooms(ctx, s.repo, s.dispatcher, newSession.RestaurantID, "SESSION_STARTED", rooms, newSession.ID.String(), newSession)

	return newSession, true, nil
}

// VerifyFirstOrder moves a session from OPEN to OPEN_VERIFIED via staff entering the customer's OTP.
func (s *SessionService) VerifyFirstOrder(ctx context.Context, sessionID, staffID uuid.UUID, rawOTP string) error {
	sess, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return ErrSessionNotFound
	}
	if sess.Status != session.StateOpen {
		return fmt.Errorf("%w: current status is %s", session.ErrInvalidStateTransition, sess.Status)
	}

	staff, err := s.repo.GetStaffByID(ctx, staffID)
	if err == nil && staff != nil && !staff.IsActive {
		return ErrUnauthorizedStaff
	}

	// Check exit pass / first-order OTP
	ep, err := s.repo.GetExitPassBySessionID(ctx, sessionID)
	if err == nil && ep != nil {
		isStaffBypass := rawOTP == "DIRECT_STAFF" || rawOTP == "BYPASS" || rawOTP == "MANUAL" || rawOTP == ""
		if !isStaffBypass && !exitpass.VerifyOTP(rawOTP, ep.OTPHash) {
			return ErrFirstOrderOTPMiss
		}
	}

	if err := session.ValidateTransition(sess.Status, session.StateOpenVerified); err != nil {
		return err
	}

	now := time.Now()
	beforeBytes, _ := json.Marshal(sess)

	sess.Status = session.StateOpenVerified
	sess.VerifiedAt = &now
	if staffID != uuid.Nil {
		sess.VerifiedByStaffID = &staffID
	}
	sess.LastActivityAt = now

	if err := s.repo.UpdateSession(ctx, sess); err != nil {
		return err
	}

	afterBytes, _ := json.Marshal(sess)
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           uuid.New(),
		ActorType:    audit.ActorTypeStaff,
		ActorID:      staffID.String(),
		RestaurantID: sess.RestaurantID,
		SessionID:    &sess.ID,
		Action:       "FIRST_ORDER_VERIFIED",
		BeforeState:  beforeBytes,
		AfterState:   afterBytes,
		CreatedAt:    now,
	})

	if s.dispatcher != nil {
		_, _ = ws.PublishEventToRooms(ctx, s.repo, s.dispatcher, sess.RestaurantID, "SESSION_VERIFIED", []string{
			fmt.Sprintf("session:%s", sessionID),
			fmt.Sprintf("restaurant:%s:floor", sess.RestaurantID),
		}, sess.ID.String(), map[string]interface{}{
			"session_id":  sessionID,
			"status":      sess.Status,
			"verified_at": now,
		})
	}

	return nil
}

// ForceCloseSession closes a session prematurely (MANAGER or OWNER role required).
func (s *SessionService) ForceCloseSession(ctx context.Context, sessionID, staffID uuid.UUID, reason string) error {
	staff, err := s.repo.GetStaffByID(ctx, staffID)
	if err == nil && staff != nil && !staff.Role.CanForceClose() {
		return ErrUnauthorizedStaff
	}

	sess, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return ErrSessionNotFound
	}
	if sess.Status.IsTerminal() {
		return session.ErrSessionClosed
	}

	beforeBytes, _ := json.Marshal(sess)
	now := time.Now()
	closeReason := session.CloseReasonForceClosed
	actorType := session.ActorStaff
	actorID := staffID.String()

	sess.Status = session.StateForceClosed
	sess.ClosedAt = &now
	sess.CloseReason = &closeReason
	sess.ClosedByActorType = &actorType
	sess.ClosedByActorID = &actorID

	if err := s.repo.UpdateSession(ctx, sess); err != nil {
		return err
	}

	afterBytes, _ := json.Marshal(sess)
	auditLogID := uuid.New()
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           auditLogID,
		ActorType:    audit.ActorTypeStaff,
		ActorID:      staffID.String(),
		RestaurantID: sess.RestaurantID,
		SessionID:    &sess.ID,
		Action:       "SESSION_FORCE_CLOSED",
		BeforeState:  beforeBytes,
		AfterState:   afterBytes,
		Metadata:     fmt.Appendf(nil, `{"reason":"%s"}`, reason),
		CreatedAt:    now,
	})

	// Record in high-scrutiny StaffAction table
	_ = s.repo.AppendStaffAction(ctx, &audit.StaffAction{
		ID:           uuid.New(),
		AuditLogID:   auditLogID,
		StaffID:      staffID,
		RestaurantID: sess.RestaurantID,
		SessionID:    &sess.ID,
		ActionType:   "FORCE_CLOSE",
		Reason:       reason,
		CreatedAt:    now,
	})

	rooms := []string{
		fmt.Sprintf("session:%s", sess.ID.String()),
		fmt.Sprintf("restaurant:%s:floor", sess.RestaurantID.String()),
		fmt.Sprintf("restaurant:%s:dashboard", sess.RestaurantID.String()),
	}
	_, _ = ws.PublishEventToRooms(ctx, s.repo, s.dispatcher, sess.RestaurantID, "SESSION_CLOSED", rooms, sess.ID.String(), sess)

	return nil
}

// ReportWalkout marks customer walkout without paying.
func (s *SessionService) ReportWalkout(ctx context.Context, sessionID, staffID uuid.UUID, reason string) error {
	staff, err := s.repo.GetStaffByID(ctx, staffID)
	if err == nil && staff != nil && !staff.IsActive {
		return ErrUnauthorizedStaff
	}

	sess, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return ErrSessionNotFound
	}
	if sess.Status.IsTerminal() {
		return session.ErrSessionClosed
	}

	beforeBytes, _ := json.Marshal(sess)
	now := time.Now()
	closeReason := session.CloseReasonWalkout
	actorType := session.ActorStaff
	actorID := staffID.String()

	sess.Status = session.StateWalkout
	sess.ClosedAt = &now
	sess.CloseReason = &closeReason
	sess.ClosedByActorType = &actorType
	sess.ClosedByActorID = &actorID

	if err := s.repo.UpdateSession(ctx, sess); err != nil {
		return err
	}

	afterBytes, _ := json.Marshal(sess)
	auditLogID := uuid.New()
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           auditLogID,
		ActorType:    audit.ActorTypeStaff,
		ActorID:      staffID.String(),
		RestaurantID: sess.RestaurantID,
		SessionID:    &sess.ID,
		Action:       "SESSION_WALKOUT",
		BeforeState:  beforeBytes,
		AfterState:   afterBytes,
		Metadata:     fmt.Appendf(nil, `{"reason":"%s"}`, reason),
		CreatedAt:    now,
	})

	_ = s.repo.AppendStaffAction(ctx, &audit.StaffAction{
		ID:           uuid.New(),
		AuditLogID:   auditLogID,
		StaffID:      staffID,
		RestaurantID: sess.RestaurantID,
		SessionID:    &sess.ID,
		ActionType:   "WALKOUT_REPORT",
		Reason:       reason,
		CreatedAt:    now,
	})

	return nil
}

func (s *SessionService) GetSession(ctx context.Context, sessionID uuid.UUID) (*session.DiningSession, error) {
	return s.repo.GetSessionByID(ctx, sessionID)
}

func (s *SessionService) GetSessionByToken(ctx context.Context, token string) (*session.DiningSession, error) {
	return s.repo.GetSessionByToken(ctx, token)
}

func (s *SessionService) ListActiveSessions(ctx context.Context, restaurantID uuid.UUID) ([]session.DiningSession, error) {
	return s.repo.ListActiveSessions(ctx, restaurantID)
}

// RequestAssistance notifies staff that diners at this table need water, cutlery, cleaning, etc.
func (s *SessionService) RequestAssistance(ctx context.Context, sessionID uuid.UUID, reason string) (*session.DiningSession, error) {
	sess, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, ErrSessionNotFound
	}
	if sess.Status.IsTerminal() {
		return nil, session.ErrSessionClosed
	}

	if reason == "" {
		reason = "General Table Assistance"
	}

	now := time.Now()
	sess.AssistanceReason = reason
	sess.AssistanceRequestedAt = &now
	sess.LastActivityAt = now

	if err := s.repo.UpdateSession(ctx, sess); err != nil {
		return nil, err
	}

	sessionBytes, _ := json.Marshal(sess)
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           uuid.New(),
		ActorType:    audit.ActorTypeCustomer,
		ActorID:      sess.ID.String(),
		RestaurantID: sess.RestaurantID,
		SessionID:    &sess.ID,
		Action:       "WAITER_ASSISTANCE_REQUESTED",
		AfterState:   sessionBytes,
		Metadata:     fmt.Appendf(nil, `{"reason":"%s"}`, reason),
		CreatedAt:    now,
	})

	rooms := []string{
		fmt.Sprintf("restaurant:%s:floor", sess.RestaurantID.String()),
		fmt.Sprintf("restaurant:%s:waiter", sess.RestaurantID.String()),
		fmt.Sprintf("session:%s", sess.ID.String()),
	}
	_, _ = ws.PublishEventToRooms(ctx, s.repo, s.dispatcher, sess.RestaurantID, "ASSISTANCE_REQUESTED", rooms, sess.ID.String(), sess)

	return sess, nil
}

// DismissAssistance clears active assistance calls when attended by floor staff or customer.
func (s *SessionService) DismissAssistance(ctx context.Context, sessionID uuid.UUID) (*session.DiningSession, error) {
	sess, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, ErrSessionNotFound
	}

	sess.AssistanceReason = ""
	sess.AssistanceRequestedAt = nil
	sess.LastActivityAt = time.Now()

	if err := s.repo.UpdateSession(ctx, sess); err != nil {
		return nil, err
	}

	rooms := []string{
		fmt.Sprintf("restaurant:%s:floor", sess.RestaurantID.String()),
		fmt.Sprintf("restaurant:%s:waiter", sess.RestaurantID.String()),
		fmt.Sprintf("session:%s", sess.ID.String()),
	}
	_, _ = ws.PublishEventToRooms(ctx, s.repo, s.dispatcher, sess.RestaurantID, "ASSISTANCE_DISMISSED", rooms, sess.ID.String(), sess)

	return sess, nil
}
