package crypto

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrInvalidToken = errors.New("invalid or expired authorization token")
)

type StaffClaims struct {
	StaffID      uuid.UUID `json:"staff_id"`
	RestaurantID uuid.UUID `json:"restaurant_id"`
	EmployeeID   string    `json:"employee_id,omitempty"`
	Name         string    `json:"name,omitempty"`
	Role         string    `json:"role"`
	IsPlatform   bool      `json:"is_platform"`
	jwt.RegisteredClaims
}

// UnmarshalJSON provides backward compatibility for string IDs (e.g. "s-admin-001") by deriving a deterministic UUID.
func (c *StaffClaims) UnmarshalJSON(data []byte) error {
	type Alias StaffClaims
	aux := struct {
		RawStaffID      json.RawMessage `json:"staff_id"`
		RawRestaurantID json.RawMessage `json:"restaurant_id"`
		*Alias
	}{
		Alias: (*Alias)(c),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if len(aux.RawStaffID) > 0 {
		var str string
		if err := json.Unmarshal(aux.RawStaffID, &str); err == nil && str != "" {
			if parsed, err := uuid.Parse(str); err == nil {
				c.StaffID = parsed
			} else {
				c.StaffID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(str))
			}
		}
	}

	if len(aux.RawRestaurantID) > 0 {
		var str string
		if err := json.Unmarshal(aux.RawRestaurantID, &str); err == nil && str != "" {
			if parsed, err := uuid.Parse(str); err == nil {
				c.RestaurantID = parsed
			} else {
				c.RestaurantID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(str))
			}
		}
	}

	return nil
}

type GuardClaims struct {
	GuardID      uuid.UUID `json:"guard_id"`
	RestaurantID uuid.UUID `json:"restaurant_id"`
	jwt.RegisteredClaims
}

// UnmarshalJSON provides backward compatibility for guard string IDs by deriving a deterministic UUID.
func (c *GuardClaims) UnmarshalJSON(data []byte) error {
	type Alias GuardClaims
	aux := struct {
		RawGuardID      json.RawMessage `json:"guard_id"`
		RawRestaurantID json.RawMessage `json:"restaurant_id"`
		*Alias
	}{
		Alias: (*Alias)(c),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if len(aux.RawGuardID) > 0 {
		var str string
		if err := json.Unmarshal(aux.RawGuardID, &str); err == nil && str != "" {
			if parsed, err := uuid.Parse(str); err == nil {
				c.GuardID = parsed
			} else {
				c.GuardID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(str))
			}
		}
	}

	if len(aux.RawRestaurantID) > 0 {
		var str string
		if err := json.Unmarshal(aux.RawRestaurantID, &str); err == nil && str != "" {
			if parsed, err := uuid.Parse(str); err == nil {
				c.RestaurantID = parsed
			} else {
				c.RestaurantID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(str))
			}
		}
	}

	return nil
}

// GenerateStaffJWT creates a signed JWT for staff or platform admin users.
func GenerateStaffJWT(secret []byte, staffID, restaurantID uuid.UUID, role string, isPlatform bool, ttl time.Duration) (string, error) {
	return GenerateFullStaffJWT(secret, staffID, restaurantID, "", "", role, isPlatform, ttl)
}

// GenerateFullStaffJWT creates a signed JWT with full employee profile claims.
func GenerateFullStaffJWT(secret []byte, staffID, restaurantID uuid.UUID, employeeID, name, role string, isPlatform bool, ttl time.Duration) (string, error) {
	claims := StaffClaims{
		StaffID:      staffID,
		RestaurantID: restaurantID,
		EmployeeID:   employeeID,
		Name:         name,
		Role:         role,
		IsPlatform:   isPlatform,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   staffID.String(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ParseStaffJWT verifies and extracts StaffClaims from a token.
func ParseStaffJWT(secret []byte, tokenStr string) (*StaffClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &StaffClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*StaffClaims)
	if !ok {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// GenerateGuardJWT creates a signed JWT for guard exit verification.
func GenerateGuardJWT(secret []byte, guardID, restaurantID uuid.UUID, ttl time.Duration) (string, error) {
	claims := GuardClaims{
		GuardID:      guardID,
		RestaurantID: restaurantID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   guardID.String(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ParseGuardJWT verifies and extracts GuardClaims.
func ParseGuardJWT(secret []byte, tokenStr string) (*GuardClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &GuardClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*GuardClaims)
	if !ok {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
