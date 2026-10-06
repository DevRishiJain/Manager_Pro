package ws

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidTicket = errors.New("invalid or expired websocket ticket")
	ErrTicketExpired = errors.New("websocket ticket has expired")
)

type TicketClaims struct {
	RestaurantID uuid.UUID `json:"restaurant_id"`
	SessionID    uuid.UUID `json:"session_id,omitempty"`
	StaffID      uuid.UUID `json:"staff_id,omitempty"`
	Role         string    `json:"role"`
	Rooms        []string  `json:"rooms"`
	IssuedAt     time.Time `json:"issued_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type TicketManager struct {
	mu      sync.Mutex
	tickets map[string]*TicketClaims
	ttl     time.Duration
}

func NewTicketManager(ttl time.Duration) *TicketManager {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	tm := &TicketManager{
		tickets: make(map[string]*TicketClaims),
		ttl:     ttl,
	}
	return tm
}

func (tm *TicketManager) generateTicketToken() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// IssueCustomerTicket generates a single-use ticket for a dining session.
func (tm *TicketManager) IssueCustomerTicket(restaurantID, sessionID uuid.UUID) (string, error) {
	token, err := tm.generateTicketToken()
	if err != nil {
		return "", err
	}

	now := time.Now()
	rooms := []string{
		fmt.Sprintf("session:%s", sessionID.String()),
		fmt.Sprintf("restaurant:%s:menu", restaurantID.String()),
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.cleanupExpiredLocked()

	tm.tickets[token] = &TicketClaims{
		RestaurantID: restaurantID,
		SessionID:    sessionID,
		Role:         "CUSTOMER",
		Rooms:        rooms,
		IssuedAt:     now,
		ExpiresAt:    now.Add(tm.ttl),
	}
	return token, nil
}

// IssueStaffTicket generates a single-use ticket for authenticated staff with role-appropriate rooms.
func (tm *TicketManager) IssueStaffTicket(restaurantID, staffID uuid.UUID, role string) (string, error) {
	token, err := tm.generateTicketToken()
	if err != nil {
		return "", err
	}

	now := time.Now()
	restStr := restaurantID.String()

	var rooms []string
	switch role {
	case "KITCHEN":
		rooms = []string{
			fmt.Sprintf("restaurant:%s:kitchen", restStr),
		}
	case "WAITER":
		rooms = []string{
			fmt.Sprintf("restaurant:%s:floor", restStr),
			fmt.Sprintf("restaurant:%s:waiter", restStr),
			fmt.Sprintf("restaurant:%s:kitchen", restStr),
		}
	case "MANAGER", "RESTAURANT_ADMIN", "RESTAURANT_OWNER":
		rooms = []string{
			fmt.Sprintf("restaurant:%s:floor", restStr),
			fmt.Sprintf("restaurant:%s:waiter", restStr),
			fmt.Sprintf("restaurant:%s:kitchen", restStr),
			fmt.Sprintf("restaurant:%s:dashboard", restStr),
			fmt.Sprintf("restaurant:%s:manager", restStr),
		}
	case "SUPER_ADMIN":
		rooms = []string{
			"platform:superadmin",
			fmt.Sprintf("restaurant:%s:floor", restStr),
			fmt.Sprintf("restaurant:%s:dashboard", restStr),
		}
	default:
		rooms = []string{
			fmt.Sprintf("restaurant:%s:floor", restStr),
		}
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.cleanupExpiredLocked()

	tm.tickets[token] = &TicketClaims{
		RestaurantID: restaurantID,
		StaffID:      staffID,
		Role:         role,
		Rooms:        rooms,
		IssuedAt:     now,
		ExpiresAt:    now.Add(tm.ttl),
	}
	return token, nil
}

// RedeemTicket atomically retrieves and deletes a ticket token, enforcing single-use.
func (tm *TicketManager) RedeemTicket(token string) (*TicketClaims, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	claims, ok := tm.tickets[token]
	if !ok {
		return nil, ErrInvalidTicket
	}

	delete(tm.tickets, token)

	if time.Now().After(claims.ExpiresAt) {
		return nil, ErrTicketExpired
	}

	return claims, nil
}

func (tm *TicketManager) cleanupExpiredLocked() {
	now := time.Now()
	for tok, c := range tm.tickets {
		if now.After(c.ExpiresAt) {
			delete(tm.tickets, tok)
		}
	}
}
