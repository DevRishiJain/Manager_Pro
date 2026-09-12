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
	ttlMinutes := 120
	if settings != nil && settings.ExitPassOTPTTLMinutes > 0 {
		ttlMinutes = settings.ExitPassOTPTTLMinutes
	}

	rawOTP, err := exitpass.GenerateNumericOTP(4)
	if err != nil {
		return nil, "", err
	}

	now := time.Now()

	// Idempotency check: if active pass already exists for session, rotate OTP and return
	existing, err := s.repo.GetExitPassBySessionID(ctx, sessionID)
	if err == nil && existing != nil {
		if existing.Status == exitpass.StateIssued && !existing.RequiresOverride {
			existing.OTPHash = exitpass.HashOTP(rawOTP)
			existing.ExpiresAt = now.Add(time.Duration(ttlMinutes) * time.Minute)
			existing.UpdatedAt = now
			_ = s.repo.UpdateExitPass(ctx, existing)
			return existing, rawOTP, nil
		}
		if existing.Status == exitpass.StateVerified {
			return nil, "", exitpass.ErrExitPassAlreadyUsed
		}
	}

	ep := &exitpass.ExitPass{
		ID:               uuid.New(),
		SessionID:        sessionID,
		RestaurantID:     sess.RestaurantID,
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

// VerifyExit executes the guard exit gate check.
// Returns strictly APPROVED / DENIED + reason. Never leaks order/session details to guard UI.
func (s *ExitService) VerifyExit(ctx context.Context, sessionID uuid.UUID, rawOTP string, guardID uuid.UUID) exitpass.GuardVerificationResponse {
	ep, err := s.repo.GetExitPassBySessionID(ctx, sessionID)
	if err != nil || ep == nil {
		return exitpass.GuardVerificationResponse{
			Result: exitpass.GuardResultDenied,
			Reason: "PASS_NOT_FOUND",
		}
	}

	if ep.Status == exitpass.StateVerified {
		return exitpass.GuardVerificationResponse{
			Result: exitpass.GuardResultDenied,
			Reason: "PASS_ALREADY_USED",
		}
	}

	if ep.Status == exitpass.StateRevoked {
		return exitpass.GuardVerificationResponse{
			Result: exitpass.GuardResultDenied,
			Reason: "PASS_REVOKED",
		}
	}

	now := time.Now()
	if now.After(ep.ExpiresAt) {
		ep.Status = exitpass.StateExpired
		_ = s.repo.UpdateExitPass(ctx, ep)
		return exitpass.GuardVerificationResponse{
			Result: exitpass.GuardResultDenied,
			Reason: "PASS_EXPIRED",
		}
	}

	if ep.RequiresOverride {
		return exitpass.GuardVerificationResponse{
			Result: exitpass.GuardResultDenied,
			Reason: "MAX_ATTEMPTS_EXCEEDED_MANAGER_OVERRIDE_REQUIRED",
		}
	}

	// Constant-time OTP comparison
	if !exitpass.VerifyOTP(rawOTP, ep.OTPHash) {
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

	// Verify underlying session status is PAID
	sess, err := s.repo.GetSessionByID(ctx, ep.SessionID)
	if err != nil || sess == nil || (sess.Status != session.StatePaid && sess.Status != session.StateCompleted) {
		return exitpass.GuardVerificationResponse{
			Result: exitpass.GuardResultDenied,
			Reason: "SESSION_NOT_PAID",
		}
	}

	// Atomic successful verification with optimistic locking check
	ep.Status = exitpass.StateVerified
	ep.UsedAt = &now
	ep.UsedByGuardID = &guardID
	if err := s.repo.UpdateExitPass(ctx, ep); err != nil {
		return exitpass.GuardVerificationResponse{
			Result: exitpass.GuardResultDenied,
			Reason: "CONCURRENT_VERIFICATION_CONFLICT",
		}
	}

	// Move session to terminal COMPLETED
	sess.Status = session.StateCompleted
	sess.ClosedAt = &now
	_ = s.repo.UpdateSession(ctx, sess)

	// Audit Log
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           uuid.New(),
		ActorType:    audit.ActorTypeGuard,
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
	return s.repo.GetExitPassBySessionID(ctx, sessionID)
}
