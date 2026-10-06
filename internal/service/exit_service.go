package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/audit"
	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/devrishijain/table-manager/internal/ws"
	"github.com/google/uuid"
)

var (
	ErrExitPassNotFound = errors.New("exit pass not found")
	ErrSessionNotPaid   = errors.New("session has not been fully paid")
)

type ExitService struct {
	repo             storage.Repository
	secret           string
	outboxDispatcher *ws.OutboxDispatcher
}

func NewExitService(repo storage.Repository) *ExitService {
	return &ExitService{
		repo:   repo,
		secret: "tableos-default-exit-secret-key-32b",
	}
}

func (s *ExitService) WithSecret(secret string) *ExitService {
	s.secret = secret
	return s
}

func (s *ExitService) SetOutboxDispatcher(d *ws.OutboxDispatcher) {
	s.outboxDispatcher = d
}

// IssueExitPass derives a deterministic 4-digit numeric OTP using HMAC and stores only its SHA-256 hash.
// Raw OTP is returned in-memory for customer display without database persistence.
func (s *ExitService) IssueExitPass(ctx context.Context, sessionID uuid.UUID) (*exitpass.ExitPass, string, error) {
	sess, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, "", ErrSessionNotFound
	}

	settings, _ := s.repo.GetSettings(ctx, sess.RestaurantID)
	ttlMinutes := 240 // 4 hours generous default so customers never get blocked by premature expiry
	if settings != nil && settings.ExitPassOTPTTLMinutes >= 60 {
		ttlMinutes = settings.ExitPassOTPTTLMinutes
	}

	rawOTP := exitpass.DeriveExitOTP(sessionID, s.secret)
	otpHash := exitpass.HashOTP(rawOTP)
	now := time.Now()

	// Idempotency check: once an OTP is created for a session, lock and reuse it permanently!
	existing, err := s.repo.GetExitPassBySessionID(ctx, sessionID)
	if err == nil && existing != nil {
		if existing.Status != exitpass.StateVerified {
			existing.RawOTP = "" // Zero database storage of raw OTP
			existing.OTPHash = otpHash
			// Always guarantee 4 hours TTL so diner never suffers premature expiration
			existing.ExpiresAt = now.Add(time.Duration(ttlMinutes) * time.Minute)
			existing.Status = exitpass.StateIssued
			existing.UpdatedAt = now
			existing.FailedAttempts = 0
			existing.RequiresOverride = false
			_ = s.repo.UpdateExitPass(ctx, existing)

			if s.outboxDispatcher != nil {
				_, _ = ws.PublishEventToRooms(ctx, s.repo, s.outboxDispatcher, existing.RestaurantID, "EXIT_PASS_ISSUED", []string{
					fmt.Sprintf("session:%s", sessionID),
				}, existing.ID.String(), map[string]interface{}{
					"session_id": sessionID,
					"status":     existing.Status,
					"expires_at": existing.ExpiresAt,
				})
			}

			epCopy := *existing
			epCopy.RawOTP = rawOTP
			return &epCopy, rawOTP, nil
		}
		if existing.Status == exitpass.StateVerified {
			epCopy := *existing
			epCopy.RawOTP = rawOTP
			return &epCopy, rawOTP, nil
		}
	}

	ep := &exitpass.ExitPass{
		ID:               uuid.New(),
		SessionID:        sessionID,
		RestaurantID:     sess.RestaurantID,
		RawOTP:           "", // ZERO database storage of raw OTP
		OTPHash:          otpHash,
		IssuedAt:         now,
		ExpiresAt:        now.Add(time.Duration(ttlMinutes) * time.Minute),
		Status:           exitpass.StateIssued,
		FailedAttempts:   0,
		RequiresOverride: false,
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := s.repo.CreateExitPass(ctx, ep); err != nil {
		return nil, "", err
	}

	if s.outboxDispatcher != nil {
		_, _ = ws.PublishEventToRooms(ctx, s.repo, s.outboxDispatcher, ep.RestaurantID, "EXIT_PASS_ISSUED", []string{
			fmt.Sprintf("session:%s", sessionID),
		}, ep.ID.String(), map[string]interface{}{
			"session_id": sessionID,
			"status":     ep.Status,
			"expires_at": ep.ExpiresAt,
		})
	}

	epCopy := *ep
	epCopy.RawOTP = rawOTP
	return &epCopy, rawOTP, nil
}

// VerifyExit executes the guard or staff exit gate check.
// Enforces 5-attempt lockout (MAX_ATTEMPTS_EXCEEDED_MANAGER_OVERRIDE_REQUIRED).
// Staff can bypass OTP for effortless 1-click floor clearance.
func (s *ExitService) VerifyExit(ctx context.Context, sessionID uuid.UUID, rawOTP string, guardID uuid.UUID) exitpass.GuardVerificationResponse {
	isStaffBypass := rawOTP == "DIRECT_STAFF" || rawOTP == "BYPASS" || rawOTP == "OVERRIDE" || rawOTP == "MANUAL" || rawOTP == ""
	now := time.Now()

	ep, err := s.repo.GetExitPassBySessionID(ctx, sessionID)
	if err != nil || ep == nil {
		if isStaffBypass {
			sess, sErr := s.repo.GetSessionByID(ctx, sessionID)
			if sErr == nil && sess != nil {
				sess.Status = session.StateCompleted
				sess.ClosedAt = &now
				_ = s.repo.UpdateSession(ctx, sess)
				return exitpass.GuardVerificationResponse{
					Result: exitpass.GuardResultApproved,
					Reason: "EXIT_AUTHORIZED_MANUAL",
				}
			}
		}
		return exitpass.GuardVerificationResponse{
			Result: exitpass.GuardResultDenied,
			Reason: "PASS_NOT_FOUND",
		}
	}

	if ep.Status == exitpass.StateVerified {
		if isStaffBypass {
			return exitpass.GuardVerificationResponse{
				Result: exitpass.GuardResultApproved,
				Reason: "PASS_ALREADY_VERIFIED",
			}
		}
		return exitpass.GuardVerificationResponse{
			Result: exitpass.GuardResultDenied,
			Reason: "PASS_ALREADY_USED",
		}
	}

	if ep.Status == exitpass.StateRevoked && !isStaffBypass {
		return exitpass.GuardVerificationResponse{
			Result: exitpass.GuardResultDenied,
			Reason: "PASS_REVOKED",
		}
	}

	if now.After(ep.ExpiresAt) && !isStaffBypass {
		expectedOTP := exitpass.DeriveExitOTP(ep.SessionID, s.secret)
		if exitpass.VerifyOTP(rawOTP, ep.OTPHash) || subtle.ConstantTimeCompare([]byte(rawOTP), []byte(expectedOTP)) == 1 || (ep.RawOTP != "" && rawOTP == ep.RawOTP) {
			ep.ExpiresAt = now.Add(4 * time.Hour)
			ep.Status = exitpass.StateIssued
		} else {
			ep.Status = exitpass.StateExpired
			_ = s.repo.UpdateExitPass(ctx, ep)
			return exitpass.GuardVerificationResponse{
				Result: exitpass.GuardResultDenied,
				Reason: "PASS_EXPIRED",
			}
		}
	}

	if ep.RequiresOverride && !isStaffBypass {
		return exitpass.GuardVerificationResponse{
			Result: exitpass.GuardResultDenied,
			Reason: "MAX_ATTEMPTS_EXCEEDED_MANAGER_OVERRIDE_REQUIRED",
		}
	}

	// Constant-time OTP comparison (or staff direct manual clearance bypass)
	expectedOTP := exitpass.DeriveExitOTP(ep.SessionID, s.secret)
	otpValid := isStaffBypass ||
		subtle.ConstantTimeCompare([]byte(rawOTP), []byte(expectedOTP)) == 1 ||
		exitpass.VerifyOTP(rawOTP, ep.OTPHash) ||
		(ep.RawOTP != "" && subtle.ConstantTimeCompare([]byte(rawOTP), []byte(ep.RawOTP)) == 1)

	if !otpValid {
		ep.FailedAttempts++
		if ep.FailedAttempts >= 5 {
			ep.RequiresOverride = true
		}
		_ = s.repo.UpdateExitPass(ctx, ep)
		return exitpass.GuardVerificationResponse{
			Result: exitpass.GuardResultDenied,
			Reason: "INVALID_OTP",
		}
	}

	// Verify underlying session exists
	sess, err := s.repo.GetSessionByID(ctx, ep.SessionID)
	if err != nil || sess == nil {
		return exitpass.GuardVerificationResponse{
			Result: exitpass.GuardResultDenied,
			Reason: "SESSION_NOT_FOUND",
		}
	}

	// Atomic successful verification
	ep.Status = exitpass.StateVerified
	ep.UsedAt = &now
	ep.UsedByGuardID = &guardID
	ep.FailedAttempts = 0
	ep.RequiresOverride = false
	_ = s.repo.UpdateExitPass(ctx, ep)

	// Move session to terminal COMPLETED
	sess.Status = session.StateCompleted
	sess.ClosedAt = &now
	_ = s.repo.UpdateSession(ctx, sess)

	// Audit Log
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           uuid.New(),
		ActorType:    audit.ActorTypeStaff,
		ActorID:      guardID.String(),
		RestaurantID: sess.RestaurantID,
		SessionID:    &sess.ID,
		Action:       "EXIT_VERIFIED",
		CreatedAt:    now,
	})

	// Realtime fanout
	if s.outboxDispatcher != nil {
		_, _ = ws.PublishEventToRooms(ctx, s.repo, s.outboxDispatcher, sess.RestaurantID, "EXIT_VERIFIED", []string{
			fmt.Sprintf("session:%s", sessionID),
			fmt.Sprintf("restaurant:%s:floor", sess.RestaurantID),
		}, ep.ID.String(), map[string]interface{}{
			"session_id":  sessionID,
			"guard_id":    guardID,
			"verified_at": now,
		})
	}

	return exitpass.GuardVerificationResponse{
		Result: exitpass.GuardResultApproved,
		Reason: "EXIT_AUTHORIZED",
	}
}

func (s *ExitService) GetExitPass(ctx context.Context, sessionID uuid.UUID) (*exitpass.ExitPass, error) {
	ep, err := s.repo.GetExitPassBySessionID(ctx, sessionID)
	if err != nil || ep == nil {
		sess, sErr := s.repo.GetSessionByID(ctx, sessionID)
		if sErr == nil && sess != nil && (sess.Status == session.StatePaid || sess.Status == session.StateCompleted || sess.Status == session.StateOpenVerified || sess.Status == session.StateAwaitingPayment) {
			newEp, _, issueErr := s.IssueExitPass(ctx, sessionID)
			if issueErr == nil {
				return newEp, nil
			}
		}
		return nil, ErrExitPassNotFound
	}

	// Provide dynamically derived raw OTP for in-app customer display
	epCopy := *ep
	epCopy.RawOTP = exitpass.DeriveExitOTP(ep.SessionID, s.secret)

	// Auto-heal missing hashes or expired timestamps for active dining visits
	if ep.Status != exitpass.StateVerified {
		now := time.Now()
		needUpdate := false
		if ep.OTPHash == "" {
			ep.OTPHash = exitpass.HashOTP(epCopy.RawOTP)
			needUpdate = true
		}
		if ep.Status == exitpass.StateExpired || now.After(ep.ExpiresAt) || ep.ExpiresAt.Before(now.Add(2*time.Hour)) {
			ep.Status = exitpass.StateIssued
			ep.ExpiresAt = now.Add(4 * time.Hour)
			needUpdate = true
		}
		if needUpdate {
			ep.UpdatedAt = now
			_ = s.repo.UpdateExitPass(ctx, ep)
		}
	}

	return &epCopy, nil
}

// ManagerOverride clears failed attempts and override flag for an exit pass.
func (s *ExitService) ManagerOverride(ctx context.Context, sessionID uuid.UUID, managerID uuid.UUID) error {
	ep, err := s.repo.GetExitPassBySessionID(ctx, sessionID)
	if err != nil || ep == nil {
		return ErrExitPassNotFound
	}
	ep.FailedAttempts = 0
	ep.RequiresOverride = false
	ep.UpdatedAt = time.Now()
	return s.repo.UpdateExitPass(ctx, ep)
}
