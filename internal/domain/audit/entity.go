package audit

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ActorType string

const (
	ActorTypeCustomer ActorType = "CUSTOMER_SESSION"
	ActorTypeStaff    ActorType = "STAFF_USER"
	ActorTypeGuard    ActorType = "GUARD_USER"
	ActorTypeSystem   ActorType = "SYSTEM"
)

// AuditLog represents an immutable, append-only historical record of state mutations.
type AuditLog struct {
	ID           uuid.UUID       `json:"id"`
	ActorType    ActorType       `json:"actor_type"`
	ActorID      string          `json:"actor_id"`
	RestaurantID uuid.UUID       `json:"restaurant_id"`
	SessionID    *uuid.UUID      `json:"session_id,omitempty"`
	Action       string          `json:"action"`
	BeforeState  json.RawMessage `json:"before_state,omitempty"`
	AfterState   json.RawMessage `json:"after_state,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// StaffAction is a high-scrutiny queryable projection over AuditLog.
type StaffAction struct {
	ID           uuid.UUID       `json:"id"`
	AuditLogID   uuid.UUID       `json:"audit_log_id"`
	StaffID      uuid.UUID       `json:"staff_id"`
	RestaurantID uuid.UUID       `json:"restaurant_id"`
	SessionID    *uuid.UUID      `json:"session_id,omitempty"`
	ActionType   string          `json:"action_type"` // e.g. "PAYMENT_CONFIRM", "FORCE_CLOSE", "WALKOUT_FLAG"
	Reason       string          `json:"reason,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}
