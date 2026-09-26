package service

import (
	"context"
	"errors"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/audit"
	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/google/uuid"
)

var (
	ErrExitPassNotFound = errors.New("exit pass not found")
	ErrSessionNotPaid   = errors.New("session has not been fully paid")
)

type ExitService struct {
	repo storage.Repository
}

func NewExitService(repo storage.Repository) *ExitService {
	return &ExitService{repo: repo}
}

// IssueExitPass generates a 4-digit numeric OTP and stores its SHA-256 hash.
// Raw OTP is returned for in-app customer display.
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

	rawOTP, err := exitpass.GenerateNumericOTP(4)
	if err != nil {
		return nil, "", err
	}

	now := time.Now()

	// Idempotency check: once an OTP is created for a session, lock and reuse it permanently!
	existing, err := s.repo.GetExitPassBySessionID(ctx, sessionID)
	if err == nil && existing != nil {
		if existing.Status != exitpass.StateVerified {
			if existing.RawOTP == "" {
				existing.RawOTP = rawOTP
				existing.OTPHash = exitpass.HashOTP(rawOTP)
			}
			// Always guarantee 4 hours TTL so diner never suffers premature expiration
			existing.ExpiresAt = now.Add(4 * time.Hour)
			existing.Status = exitpass.StateIssued
			existing.UpdatedAt = now
			existing.FailedAttempts = 0
			existing.RequiresOverride = false
			_ = s.repo.UpdateExitPass(ctx, existing)
			return existing, existing.RawOTP, nil
		}
		if existing.Status == exitpass.StateVerified {
			return existing, existing.RawOTP, nil
		}
	}

	ep := &exitpass.ExitPass{
		ID:               uuid.New(),
		SessionID:        sessionID,
		RestaurantID:     sess.RestaurantID,
		RawOTP:           rawOTP,
		OTPHash:          exitpass.HashOTP(rawOTP),
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

	return ep, rawOTP, nil
}

// VerifyExit executes the guard or staff exit gate check.
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
		if exitpass.VerifyOTP(rawOTP, ep.OTPHash) || (ep.RawOTP != "" && rawOTP == ep.RawOTP) {
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
	otpValid := isStaffBypass || exitpass.VerifyOTP(rawOTP, ep.OTPHash) || (ep.RawOTP != "" && rawOTP == ep.RawOTP)
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

	// Auto-heal missing RawOTP or expired timestamps for active dining visits
	if ep.Status != exitpass.StateVerified {
		now := time.Now()
		needUpdate := false
		if ep.RawOTP == "" {
			rawOTP, _ := exitpass.GenerateNumericOTP(4)
			ep.RawOTP = rawOTP
			ep.OTPHash = exitpass.HashOTP(rawOTP)
			needUpdate = true
		}
		if ep.Status == exitpass.StateExpired || now.After(ep.ExpiresAt) || ep.ExpiresAt.Before(now.Add(2*time.Hour)) {
			ep.Status = exitpass.StateIssued
			ep.ExpiresAt = now.Add(4 * time.Hour)
			needUpdate = true
		}
		if ep.FailedAttempts > 0 {
			ep.FailedAttempts = 0
			ep.RequiresOverride = false
			needUpdate = true
		}
		if needUpdate {
			ep.UpdatedAt = now
			_ = s.repo.UpdateExitPass(ctx, ep)
		}
	}

	return ep, nil
}
