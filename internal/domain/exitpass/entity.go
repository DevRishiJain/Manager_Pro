package exitpass

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidExitPassTransition = errors.New("invalid exit pass state transition")
	ErrExitPassExpired           = errors.New("exit pass has expired")
	ErrExitPassAlreadyUsed       = errors.New("exit pass has already been verified")
	ErrExitPassRevoked           = errors.New("exit pass has been revoked")
	ErrMaxAttemptsExceeded       = errors.New("maximum OTP verification attempts exceeded; manager override required")
	ErrInvalidOTP                = errors.New("invalid OTP code")
)

type State string

const (
	StateIssued   State = "ISSUED"
	StateVerified State = "VERIFIED"
	StateExpired  State = "EXPIRED"
	StateRevoked  State = "REVOKED"
)

var AllowedExitPassTransitions = map[State][]State{
	StateIssued: {
		StateVerified,
		StateExpired,
		StateRevoked,
	},
	StateVerified: {},
	StateExpired:  {},
	StateRevoked:  {},
}

func ValidateTransition(current, target State) error {
	allowed, ok := AllowedExitPassTransitions[current]
	if !ok {
		return fmt.Errorf("%w: unknown state %s", ErrInvalidExitPassTransition, current)
	}
	for _, next := range allowed {
		if next == target {
			return nil
		}
	}
	return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidExitPassTransition, current, target)
}

type ExitPass struct {
	ID                uuid.UUID  `json:"id"`
	SessionID         uuid.UUID  `json:"session_id"`
	RestaurantID      uuid.UUID  `json:"restaurant_id"`
	RawOTP            string     `json:"otp,omitempty"`
	OTPHash           string     `json:"-"` // SHA-256 hex string, never plaintext
	IssuedAt          time.Time  `json:"issued_at"`
	ExpiresAt         time.Time  `json:"expires_at"`
	UsedAt            *time.Time `json:"used_at,omitempty"`
	UsedByGuardID     *uuid.UUID `json:"used_by_guard_id,omitempty"`
	Status            State      `json:"status"`
	FailedAttempts    int        `json:"failed_attempts"`
	LockedUntil       *time.Time `json:"locked_until,omitempty"`
	RequiresOverride  bool       `json:"requires_override"`
	Version           int        `json:"version"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// HashOTP computes the SHA-256 hex digest of a raw OTP.
func HashOTP(rawOTP string) string {
	sum := sha256.Sum256([]byte(rawOTP))
	return hex.EncodeToString(sum[:])
}

// VerifyOTP compares a raw OTP with the stored hash in constant time.
func VerifyOTP(rawOTP, storedHash string) bool {
	candidateHash := HashOTP(rawOTP)
	return subtle.ConstantTimeCompare([]byte(candidateHash), []byte(storedHash)) == 1
}

// GenerateNumericOTP generates a cryptographically secure N-digit numeric string.
func GenerateNumericOTP(digits int) (string, error) {
	if digits <= 0 || digits > 10 {
		return "", errors.New("digits must be between 1 and 10")
	}
	maxVal := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	n, err := rand.Int(rand.Reader, maxVal)
	if err != nil {
		return "", err
	}
	format := fmt.Sprintf("%%0%dd", digits)
	return fmt.Sprintf(format, n.Int64()), nil
}

// DeriveExitOTP deterministically computes a 4-digit numeric OTP using HMAC-SHA256 from sessionID and secret.
func DeriveExitOTP(sessionID uuid.UUID, secret string) string {
	if secret == "" {
		secret = "tableos-default-exit-secret-key-32b"
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(sessionID.String()))
	sum := mac.Sum(nil)
	val := binary.BigEndian.Uint32(sum[:4])
	otpNum := val % 10000
	return fmt.Sprintf("%04d", otpNum)
}

type GuardVerificationResult string

const (
	GuardResultApproved GuardVerificationResult = "APPROVED"
	GuardResultDenied   GuardVerificationResult = "DENIED"
)

type GuardVerificationResponse struct {
	Result GuardVerificationResult `json:"result"`
	Reason string                  `json:"reason"`
}
