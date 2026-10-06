package exitpass_test

import (
	"testing"

	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/google/uuid"
)

func TestDeriveExitOTP(t *testing.T) {
	sessionID := uuid.New()
	secret := "test-secret-key-for-hmac-32bytes!"

	// 1. Deterministic output for same inputs
	otp1 := exitpass.DeriveExitOTP(sessionID, secret)
	otp2 := exitpass.DeriveExitOTP(sessionID, secret)
	if otp1 != otp2 {
		t.Fatalf("expected deterministic OTP generation: got %s and %s", otp1, otp2)
	}

	// 2. Exactly 4 numeric digits
	if len(otp1) != 4 {
		t.Fatalf("expected 4-digit OTP, got length %d (%s)", len(otp1), otp1)
	}
	for _, c := range otp1 {
		if c < '0' || c > '9' {
			t.Fatalf("expected numeric digits only, got char %c in %s", c, otp1)
		}
	}

	// 3. Different secrets produce different OTPs
	otpDiffSecret := exitpass.DeriveExitOTP(sessionID, "different-secret-key-32bytes!!")
	// Very high probability they differ
	if otp1 == otpDiffSecret {
		// Test another session to rule out 1/10000 collision
		s2 := uuid.New()
		if exitpass.DeriveExitOTP(s2, secret) == exitpass.DeriveExitOTP(s2, "diff") {
			t.Fatalf("expected different secrets to generate different OTPs")
		}
	}

	// 4. Different session IDs produce different OTPs
	otherSession := uuid.New()
	otpOther := exitpass.DeriveExitOTP(otherSession, secret)
	if otp1 == otpOther {
		// In case of 1/10000 birthday collision, test a batch
		collisions := 0
		for i := 0; i < 50; i++ {
			if exitpass.DeriveExitOTP(uuid.New(), secret) == otp1 {
				collisions++
			}
		}
		if collisions > 5 {
			t.Fatalf("excessive collisions detected in HMAC OTP derivation")
		}
	}
}

func TestVerifyOTP(t *testing.T) {
	rawOTP := "4321"
	hash := exitpass.HashOTP(rawOTP)

	if !exitpass.VerifyOTP(rawOTP, hash) {
		t.Fatalf("expected VerifyOTP to succeed with matching raw OTP and hash")
	}

	if exitpass.VerifyOTP("1234", hash) {
		t.Fatalf("expected VerifyOTP to fail with incorrect raw OTP")
	}

	if exitpass.VerifyOTP("", hash) {
		t.Fatalf("expected VerifyOTP to fail with empty OTP")
	}
}
