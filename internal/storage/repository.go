package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/audit"
	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/devrishijain/table-manager/internal/domain/ledger"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/google/uuid"
)

var (
	ErrActiveSessionExists = errors.New("table already has an active dining session")
	ErrNotFound            = errors.New("record not found")
	ErrOptimisticLock      = errors.New("optimistic locking conflict: record has been modified")
)

type OutboxStatus string

const (
	OutboxStatusPending    OutboxStatus = "PENDING"
	OutboxStatusClaimed    OutboxStatus = "CLAIMED"
	OutboxStatusPublished  OutboxStatus = "PUBLISHED"
	OutboxStatusFailed     OutboxStatus = "FAILED"
	OutboxStatusDeadLetter OutboxStatus = "DEAD_LETTER"
)

type OutboxEvent struct {
	ID                  uuid.UUID       `json:"id"`
	RestaurantID        uuid.UUID       `json:"restaurant_id"`
	EventType           string          `json:"event_type"` // e.g. "ORDER_ACCEPTED", "PAYMENT_CONFIRMED", "EXIT_VERIFIED"
	AggregateID         string          `json:"aggregate_id"`
	Payload             json.RawMessage `json:"payload"`
	Status              OutboxStatus    `json:"status"`
	Retries             int             `json:"retries"`
	ClaimedAt           *time.Time      `json:"claimed_at,omitempty"`
	ClaimLeaseExpiresAt *time.Time      `json:"claim_lease_expires_at,omitempty"`
	NextRetryAt         *time.Time      `json:"next_retry_at,omitempty"`
	LastError           *string         `json:"last_error,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	PublishedAt         *time.Time      `json:"published_at,omitempty"`
}

type Repository interface {
	// Session
	CreateSession(ctx context.Context, s *session.DiningSession) error
	GetSessionByID(ctx context.Context, id uuid.UUID) (*session.DiningSession, error)
	GetSessionByToken(ctx context.Context, token string) (*session.DiningSession, error)
	GetActiveSessionByTableID(ctx context.Context, tableID uuid.UUID) (*session.DiningSession, error)
	UpdateSession(ctx context.Context, s *session.DiningSession) error
	ListActiveSessions(ctx context.Context, restaurantID uuid.UUID) ([]session.DiningSession, error)
	AddParticipant(ctx context.Context, p *session.SessionParticipant) error
	GetParticipants(ctx context.Context, sessionID uuid.UUID) ([]session.SessionParticipant, error)

	// Order
	CreateOrder(ctx context.Context, o *order.Order, items []order.OrderItem) error
	GetOrderByID(ctx context.Context, id uuid.UUID) (*order.Order, error)
	GetOrdersBySessionID(ctx context.Context, sessionID uuid.UUID) ([]order.Order, error)
	UpdateOrder(ctx context.Context, o *order.Order) error
	ListKitchenQueue(ctx context.Context, restaurantID uuid.UUID, statuses []order.State) ([]order.Order, error)
	ListPendingOrders(ctx context.Context, restaurantID uuid.UUID) ([]order.Order, error)

	// Payment
	CreatePayment(ctx context.Context, p *payment.Payment) error
	GetPaymentByID(ctx context.Context, id uuid.UUID) (*payment.Payment, error)
	GetPaymentsBySessionID(ctx context.Context, sessionID uuid.UUID) ([]payment.Payment, error)
	UpdatePayment(ctx context.Context, p *payment.Payment) error
	CreateRefund(ctx context.Context, r *payment.Refund) error
	GetRefundsBySessionID(ctx context.Context, sessionID uuid.UUID) ([]payment.Refund, error)
	CreateAdjustment(ctx context.Context, a *payment.Adjustment) error
	GetAdjustmentsBySessionID(ctx context.Context, sessionID uuid.UUID) ([]payment.Adjustment, error)

	// ExitPass
	CreateExitPass(ctx context.Context, ep *exitpass.ExitPass) error
	GetExitPassByID(ctx context.Context, id uuid.UUID) (*exitpass.ExitPass, error)
	GetExitPassBySessionID(ctx context.Context, sessionID uuid.UUID) (*exitpass.ExitPass, error)
	UpdateExitPass(ctx context.Context, ep *exitpass.ExitPass) error

	// Ledger
	CreatePlatformFeeEntry(ctx context.Context, entry *ledger.PlatformFeeLedgerEntry) error
	GetPlatformFeeBySessionID(ctx context.Context, sessionID uuid.UUID) (*ledger.PlatformFeeLedgerEntry, error)
	ListPlatformFees(ctx context.Context, restaurantID uuid.UUID, period string) ([]ledger.PlatformFeeLedgerEntry, error)
	CreateRefundAdjustment(ctx context.Context, adj *ledger.RefundAdjustment) error
	CreateSettlement(ctx context.Context, s *ledger.RestaurantSettlement) error
	ListSettlements(ctx context.Context, restaurantID uuid.UUID) ([]ledger.RestaurantSettlement, error)

	// Restaurant Catalog & Config
	CreateRestaurant(ctx context.Context, r *restaurant.Restaurant) error
	GetRestaurantByID(ctx context.Context, id uuid.UUID) (*restaurant.Restaurant, error)
	ListRestaurants(ctx context.Context) ([]restaurant.Restaurant, error)
	UpdateRestaurant(ctx context.Context, r *restaurant.Restaurant) error

	CreateTable(ctx context.Context, t *restaurant.Table) error
	GetTableByID(ctx context.Context, id uuid.UUID) (*restaurant.Table, error)
	GetTableByToken(ctx context.Context, token string) (*restaurant.Table, error)
	ListTables(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.Table, error)

	CreateStaff(ctx context.Context, s *restaurant.StaffUser) error
	GetStaffByID(ctx context.Context, id uuid.UUID) (*restaurant.StaffUser, error)
	GetStaffByEmail(ctx context.Context, email string) (*restaurant.StaffUser, error)
	GetStaffByEmployeeID(ctx context.Context, restaurantID uuid.UUID, employeeID string) (*restaurant.StaffUser, error)
	ListStaff(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.StaffUser, error)

	CreateGuard(ctx context.Context, g *restaurant.GuardUser) error
	GetGuardByID(ctx context.Context, id uuid.UUID) (*restaurant.GuardUser, error)
	GetGuardByPhone(ctx context.Context, phone string) (*restaurant.GuardUser, error)

	CreateCategory(ctx context.Context, c *restaurant.MenuCategory) error
	ListCategories(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.MenuCategory, error)
	CreateMenuItem(ctx context.Context, m *restaurant.MenuItem) error
	GetMenuItemByID(ctx context.Context, id uuid.UUID) (*restaurant.MenuItem, error)
	ListMenuItems(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.MenuItem, error)
	UpdateMenuItemAvailability(ctx context.Context, id uuid.UUID, isAvailable bool) error

	GetSettings(ctx context.Context, restaurantID uuid.UUID) (*restaurant.RestaurantSettings, error)
	UpdateSettings(ctx context.Context, s *restaurant.RestaurantSettings) error

	GetOnboarding(ctx context.Context, restaurantID uuid.UUID) (*restaurant.RestaurantOnboarding, error)
	UpdateOnboarding(ctx context.Context, o *restaurant.RestaurantOnboarding) error

	// Audit & Outbox
	AppendAuditLog(ctx context.Context, log *audit.AuditLog) error
	AppendStaffAction(ctx context.Context, action *audit.StaffAction) error
	ListAuditLogs(ctx context.Context, restaurantID uuid.UUID, limit, offset int) ([]audit.AuditLog, error)
	ListStaffActions(ctx context.Context, restaurantID uuid.UUID, limit, offset int) ([]audit.StaffAction, error)

	StoreOutboxEvent(ctx context.Context, event *OutboxEvent) error
	FetchPendingOutbox(ctx context.Context, batchSize int) ([]OutboxEvent, error)
	ClaimPendingOutbox(ctx context.Context, batchSize int, leaseDuration time.Duration) ([]OutboxEvent, error)
	MarkOutboxPublished(ctx context.Context, id uuid.UUID) error
	MarkOutboxFailed(ctx context.Context, id uuid.UUID, lastErr string, backoff time.Duration, maxRetries int) error
	ReplayDeadLetterOutbox(ctx context.Context, id uuid.UUID) error

	// Webhook Idempotency
	RecordWebhookEvent(ctx context.Context, gateway, eventID string) (bool, error) // returns true if newly inserted, false if duplicate
}
