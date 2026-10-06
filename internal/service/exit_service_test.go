package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/google/uuid"
)

func TestExitService_HMACAndZeroStorageAndLockout(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	exitSvc := service.NewExitService(repo).WithSecret("my-secret-key-32-bytes-test!!")

	restID := uuid.New()
	guardID := uuid.New()
	managerID := uuid.New()
	sessID := uuid.New()

	_ = repo.CreateRestaurant(ctx, &restaurant.Restaurant{
		ID:     restID,
		Name:   "The Grand Taj",
		Status: restaurant.StatusActive,
	})

	sess := &session.DiningSession{
		ID:           sessID,
		RestaurantID: restID,
		Status:       session.StatePaid,
		OpenedAt:     time.Now(),
	}
	_ = repo.CreateSession(ctx, sess)

	// 1. Issue Exit Pass
	epResponse, rawOTP, err := exitSvc.IssueExitPass(ctx, sessID)
	if err != nil {
		t.Fatalf("failed to issue exit pass: %v", err)
	}

	if len(rawOTP) != 4 {
		t.Fatalf("expected 4-digit numeric raw OTP, got %s", rawOTP)
	}
	if epResponse.RawOTP != rawOTP {
		t.Fatalf("expected response object to contain raw OTP for customer display")
	}

	// Verify ZERO raw OTP stored in repository
	storedEP, err := repo.GetExitPassBySessionID(ctx, sessID)
	if err != nil {
		t.Fatalf("failed to get exit pass from repo: %v", err)
	}
	if storedEP.RawOTP != "" {
		t.Fatalf("SECURITY VIOLATION: raw OTP must NOT be stored in repository, found: %s", storedEP.RawOTP)
	}
	if storedEP.OTPHash == "" {
		t.Fatalf("expected stored OTP hash to be populated")
	}
	if !exitpass.VerifyOTP(rawOTP, storedEP.OTPHash) {
		t.Fatalf("expected stored hash to match raw OTP")
	}

	// 2. Test Lockout after 5 failed attempts
	invalidOTP := "9999"
	if invalidOTP == rawOTP {
		invalidOTP = "8888"
	}

	// Attempts 1 to 4
	for i := 1; i <= 4; i++ {
		res := exitSvc.VerifyExit(ctx, sessID, invalidOTP, guardID)
		if res.Result != exitpass.GuardResultDenied || res.Reason != "INVALID_OTP" {
			t.Fatalf("attempt %d: expected INVALID_OTP, got %s (%s)", i, res.Reason, res.Result)
		}
		epCheck, _ := repo.GetExitPassBySessionID(ctx, sessID)
		if epCheck.FailedAttempts != i {
			t.Fatalf("expected failed attempts %d, got %d", i, epCheck.FailedAttempts)
		}
		if epCheck.RequiresOverride {
			t.Fatalf("attempt %d: override should not yet be required", i)
		}
	}

	// 5th attempt (triggers lockout)
	res5 := exitSvc.VerifyExit(ctx, sessID, invalidOTP, guardID)
	if res5.Result != exitpass.GuardResultDenied || res5.Reason != "INVALID_OTP" {
		t.Fatalf("attempt 5: expected INVALID_OTP, got %s (%s)", res5.Reason, res5.Result)
	}
	epCheck5, _ := repo.GetExitPassBySessionID(ctx, sessID)
	if epCheck5.FailedAttempts != 5 {
		t.Fatalf("expected 5 failed attempts, got %d", epCheck5.FailedAttempts)
	}
	if !epCheck5.RequiresOverride {
		t.Fatalf("expected RequiresOverride to be true after 5 failed attempts")
	}

	// 6th attempt (even with CORRECT raw OTP, must be rejected with MAX_ATTEMPTS_EXCEEDED_MANAGER_OVERRIDE_REQUIRED)
	res6 := exitSvc.VerifyExit(ctx, sessID, rawOTP, guardID)
	if res6.Result != exitpass.GuardResultDenied || res6.Reason != "MAX_ATTEMPTS_EXCEEDED_MANAGER_OVERRIDE_REQUIRED" {
		t.Fatalf("attempt 6: expected lockout reason MAX_ATTEMPTS_EXCEEDED_MANAGER_OVERRIDE_REQUIRED, got %s", res6.Reason)
	}

	// 3. Manager Override
	if err := exitSvc.ManagerOverride(ctx, sessID, managerID); err != nil {
		t.Fatalf("failed to apply manager override: %v", err)
	}
	epAfterOverride, _ := repo.GetExitPassBySessionID(ctx, sessID)
	if epAfterOverride.FailedAttempts != 0 || epAfterOverride.RequiresOverride {
		t.Fatalf("expected manager override to reset failed attempts and override flag")
	}

	// 4. Verification with correct raw OTP succeeds
	resSuccess := exitSvc.VerifyExit(ctx, sessID, rawOTP, guardID)
	if resSuccess.Result != exitpass.GuardResultApproved || resSuccess.Reason != "EXIT_AUTHORIZED" {
		t.Fatalf("expected EXIT_AUTHORIZED, got %s (%s)", resSuccess.Reason, resSuccess.Result)
	}

	finalEP, _ := repo.GetExitPassBySessionID(ctx, sessID)
	if finalEP.Status != exitpass.StateVerified {
		t.Fatalf("expected exit pass status VERIFIED, got %s", finalEP.Status)
	}

	finalSess, _ := repo.GetSessionByID(ctx, sessID)
	if finalSess.Status != session.StateCompleted {
		t.Fatalf("expected session status COMPLETED, got %s", finalSess.Status)
	}

	// 5. Re-verification should be denied as PASS_ALREADY_USED
	resReused := exitSvc.VerifyExit(ctx, sessID, rawOTP, guardID)
	if resReused.Result != exitpass.GuardResultDenied || resReused.Reason != "PASS_ALREADY_USED" {
		t.Fatalf("expected PASS_ALREADY_USED, got %s", resReused.Reason)
	}
}
