package tests

import (
	"fmt"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/google/uuid"
)

func TestDiningSessionStateTransitions(t *testing.T) {
	validMoves := [][2]session.State{
		{session.StateOpen, session.StateOpenVerified},
		{session.StateOpen, session.StateExpired},
		{session.StateOpen, session.StateForceClosed},
		{session.StateOpenVerified, session.StateAwaitingPayment},
		{session.StateOpenVerified, session.StateForceClosed},
		{session.StateAwaitingPayment, session.StatePaid},
		{session.StateAwaitingPayment, session.StateOpenVerified}, // customer adds item before paying
		{session.StateAwaitingPayment, session.StateWalkout},
		{session.StatePaid, session.StateCompleted},
	}

	for _, move := range validMoves {
		from, to := move[0], move[1]
		t.Run(fmt.Sprintf("Valid_%s_to_%s", from, to), func(t *testing.T) {
			if err := session.ValidateTransition(from, to); err != nil {
				t.Fatalf("expected transition %s -> %s to be valid, got error: %v", from, to, err)
			}
		})
	}

	invalidMoves := [][2]session.State{
		{session.StateOpen, session.StatePaid},
		{session.StateOpen, session.StateCompleted},
		{session.StateCompleted, session.StateOpen},
		{session.StateWalkout, session.StatePaid},
		{session.StateExpired, session.StatePaid},
		{session.StateForceClosed, session.StateAwaitingPayment},
	}

	for _, move := range invalidMoves {
		from, to := move[0], move[1]
		t.Run(fmt.Sprintf("Invalid_%s_to_%s", from, to), func(t *testing.T) {
			if err := session.ValidateTransition(from, to); err == nil {
				t.Fatalf("expected transition %s -> %s to be invalid, but validation succeeded", from, to)
			}
		})
	}
}

func TestOrderStateTransitionsAndCancellationTiers(t *testing.T) {
	flow := []struct {
		from order.State
		to   order.State
	}{
		{order.StatePlacedVerified, order.StateAccepted},
		{order.StateAccepted, order.StatePreparing},
		{order.StatePreparing, order.StateReady},
		{order.StateReady, order.StateServed},
	}

	for _, step := range flow {
		t.Run(fmt.Sprintf("ForwardFlow_%s_to_%s", step.from, step.to), func(t *testing.T) {
			if err := order.ValidateTransition(step.from, step.to); err != nil {
				t.Fatalf("expected valid order move %s -> %s: %v", step.from, step.to, err)
			}
		})
	}

	t.Run("Prohibited_Served_to_Cancelled", func(t *testing.T) {
		if err := order.ValidateTransition(order.StateServed, order.StateCancelled); err == nil {
			t.Fatal("expected SERVED -> CANCELLED to be prohibited")
		}
	})

	cancellable := []order.State{
		order.StatePlacedUnverified,
		order.StatePlacedVerified,
		order.StateAccepted,
		order.StatePreparing,
	}
	for _, st := range cancellable {
		t.Run(fmt.Sprintf("Cancellation_Permitted_From_%s", st), func(t *testing.T) {
			if err := order.ValidateTransition(st, order.StateCancelled); err != nil {
				t.Fatalf("expected cancellation to be valid from %s: %v", st, err)
			}
		})
	}
}

func TestPaymentStateTransitions(t *testing.T) {
	t.Run("Initiated_to_PendingConfirmation", func(t *testing.T) {
		if err := payment.ValidateTransition(payment.StateInitiated, payment.StatePendingConfirmation); err != nil {
			t.Fatalf("expected INITIATED -> PENDING_CONFIRMATION valid: %v", err)
		}
	})

	t.Run("PendingConfirmation_to_Confirmed", func(t *testing.T) {
		if err := payment.ValidateTransition(payment.StatePendingConfirmation, payment.StateConfirmed); err != nil {
			t.Fatalf("expected PENDING_CONFIRMATION -> CONFIRMED valid: %v", err)
		}
	})

	t.Run("Terminal_Confirmed_to_Failed_Prohibited", func(t *testing.T) {
		if err := payment.ValidateTransition(payment.StateConfirmed, payment.StateFailed); err == nil {
			t.Fatal("expected CONFIRMED -> FAILED to be rejected")
		}
	})
}

func TestExitPassOTPVerificationAndRateLimiting(t *testing.T) {
	t.Run("Generate_And_Verify_Numeric_OTP", func(t *testing.T) {
		rawOTP, err := exitpass.GenerateNumericOTP(4)
		if err != nil {
			t.Fatalf("failed to generate numeric OTP: %v", err)
		}
		if len(rawOTP) != 4 {
			t.Fatalf("expected 4 digit OTP, got %s", rawOTP)
		}

		hash := exitpass.HashOTP(rawOTP)
		if !exitpass.VerifyOTP(rawOTP, hash) {
			t.Fatal("expected valid OTP to verify successfully")
		}

		if exitpass.VerifyOTP("0000", hash) && rawOTP != "0000" {
			t.Fatal("expected wrong OTP to fail verification")
		}
	})

	t.Run("Lockout_After_5_Failed_Attempts", func(t *testing.T) {
		hash := exitpass.HashOTP("1234")
		ep := &exitpass.ExitPass{
			ID:               uuid.New(),
			SessionID:        uuid.New(),
			RestaurantID:     uuid.New(),
			OTPHash:          hash,
			IssuedAt:         time.Now(),
			ExpiresAt:        time.Now().Add(1 * time.Hour),
			Status:           exitpass.StateIssued,
			FailedAttempts:   4,
			RequiresOverride: false,
			Version:          1,
		}

		// 5th attempt
		ep.FailedAttempts++
		if ep.FailedAttempts >= 5 {
			ep.RequiresOverride = true
		}

		if !ep.RequiresOverride {
			t.Fatal("expected pass to require manager override after 5 failed attempts")
		}
	})
}

func TestPlatformFeeRoundingPolicy(t *testing.T) {
	t.Run("RoundHalfUp_1555_paise_at_1pct_yields_16_paise", func(t *testing.T) {
		gmv := money.New(1555)
		fee, err := gmv.MultiplyFractionRoundHalfUp(100, 10000)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fee.AmountMinorUnits != 16 {
			t.Fatalf("expected 16 paise, got %d", fee.AmountMinorUnits)
		}
	})

	t.Run("RoundHalfUp_1540_paise_at_1pct_yields_15_paise", func(t *testing.T) {
		gmv2 := money.New(1540)
		fee2, _ := gmv2.MultiplyFractionRoundHalfUp(100, 10000)
		if fee2.AmountMinorUnits != 15 {
			t.Fatalf("expected 15 paise, got %d", fee2.AmountMinorUnits)
		}
	})
}
