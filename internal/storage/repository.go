package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/audit"
	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/devrishijain/table-manager/internal/domain/expense"
	"github.com/devrishijain/table-manager/internal/domain/inventory"
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
	Room                string          `json:"room,omitempty"`
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
	ListOrders(ctx context.Context, restaurantID uuid.UUID, limit int, startDate, endDate *time.Time) ([]order.Order, error)
	RecordOrderStatusHistory(ctx context.Context, h *order.StatusHistory) error
	GetOrderStatusHistory(ctx context.Context, orderID uuid.UUID) ([]order.StatusHistory, error)

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
	GetRestaurantBySlug(ctx context.Context, slug string) (*restaurant.Restaurant, error)
	ListRestaurants(ctx context.Context) ([]restaurant.Restaurant, error)
	UpdateRestaurant(ctx context.Context, r *restaurant.Restaurant) error

	CreateTable(ctx context.Context, t *restaurant.Table) error
	GetTableByID(ctx context.Context, id uuid.UUID) (*restaurant.Table, error)
	GetTableByToken(ctx context.Context, token string) (*restaurant.Table, error)
	ListTables(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.Table, error)
	UpdateTable(ctx context.Context, t *restaurant.Table) error

	CreateStaff(ctx context.Context, s *restaurant.StaffUser) error
	GetStaffByID(ctx context.Context, id uuid.UUID) (*restaurant.StaffUser, error)
	GetStaffByEmail(ctx context.Context, email string) (*restaurant.StaffUser, error)
	GetStaffByEmployeeID(ctx context.Context, restaurantID uuid.UUID, employeeID string) (*restaurant.StaffUser, error)
	GetStaffByEmployeeIDGlobal(ctx context.Context, employeeID string) (*restaurant.StaffUser, error)
	ListStaff(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.StaffUser, error)
	UpdateStaffPassword(ctx context.Context, staffID uuid.UUID, passwordHash string) error

	CreateGuard(ctx context.Context, g *restaurant.GuardUser) error
	GetGuardByID(ctx context.Context, id uuid.UUID) (*restaurant.GuardUser, error)
	GetGuardByPhone(ctx context.Context, phone string) (*restaurant.GuardUser, error)

	CreateCategory(ctx context.Context, c *restaurant.MenuCategory) error
	ListCategories(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.MenuCategory, error)
	CreateMenuItem(ctx context.Context, m *restaurant.MenuItem) error
	GetMenuItemByID(ctx context.Context, id uuid.UUID) (*restaurant.MenuItem, error)
	GetMenuItemsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*restaurant.MenuItem, error)
	ListMenuItems(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.MenuItem, error)
	UpdateMenuItemAvailability(ctx context.Context, id uuid.UUID, isAvailable bool) error
	ListVariantsByMenuItemIDs(ctx context.Context, menuItemIDs []uuid.UUID) (map[uuid.UUID][]restaurant.MenuItemVariant, error)
	ReplaceMenuItemVariants(ctx context.Context, menuItemID uuid.UUID, variants []restaurant.MenuItemVariant) error
	GetMenuItemVariantByID(ctx context.Context, id uuid.UUID) (*restaurant.MenuItemVariant, error)

	// Quick Billing atomic transaction
	CreateQuickBillingTransaction(ctx context.Context, s *session.DiningSession, o *order.Order, items []order.OrderItem, p *payment.Payment) error

	GetSettings(ctx context.Context, restaurantID uuid.UUID) (*restaurant.RestaurantSettings, error)
	UpdateSettings(ctx context.Context, s *restaurant.RestaurantSettings) error

	GetSubscription(ctx context.Context, restaurantID uuid.UUID) (*restaurant.Restaurant, error)
	RenewSubscription(ctx context.Context, restaurantID uuid.UUID, days int) (*restaurant.Restaurant, error)

	// Subscription activation OTPs
	CreateSubscriptionOTP(ctx context.Context, o *restaurant.SubscriptionOTP) error
	GetActiveSubscriptionOTP(ctx context.Context, restaurantID uuid.UUID) (*restaurant.SubscriptionOTP, error)
	UpdateSubscriptionOTP(ctx context.Context, o *restaurant.SubscriptionOTP) error
	ListSubscriptionOTPs(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.SubscriptionOTP, error)
	// ConsumeSubscriptionOTP atomically flips an OTP ISSUED→USED; returns false if not ISSUED.
	ConsumeSubscriptionOTP(ctx context.Context, id uuid.UUID) (bool, error)

	// Franchises
	CreateFranchise(ctx context.Context, f *restaurant.Franchise) error
	GetFranchiseByID(ctx context.Context, id uuid.UUID) (*restaurant.Franchise, error)
	UpdateFranchise(ctx context.Context, f *restaurant.Franchise) error
	ListFranchises(ctx context.Context) ([]restaurant.Franchise, error)
	ListRestaurantsByFranchise(ctx context.Context, franchiseID uuid.UUID) ([]restaurant.Restaurant, error)
	CreateFranchiseInviteCode(ctx context.Context, c *restaurant.FranchiseInviteCode) error
	GetFranchiseInviteCode(ctx context.Context, code string) (*restaurant.FranchiseInviteCode, error)
	MarkFranchiseInviteCodeUsed(ctx context.Context, code string, restaurantID uuid.UUID) error

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
	PrunePublishedOutbox(ctx context.Context, maxAge time.Duration) (int64, error)

	// Webhook Idempotency
	RecordWebhookEvent(ctx context.Context, gateway, eventID string) (bool, error) // returns true if newly inserted, false if duplicate

	// Expenses
	CreateExpense(ctx context.Context, e *expense.Expense) error
	ListExpenses(ctx context.Context, restaurantID uuid.UUID, expenseType *expense.ExpenseType, category *expense.ExpenseCategory, startDate, endDate *time.Time) ([]expense.Expense, error)
	ListExpenseLineItems(ctx context.Context, expenseID uuid.UUID) ([]expense.ExpenseLineItem, error)
	DeleteExpense(ctx context.Context, restaurantID, expenseID uuid.UUID) error

	// Inventory
	CreateInventoryItem(ctx context.Context, item *inventory.InventoryItem) error
	GetInventoryItemByID(ctx context.Context, restaurantID, id uuid.UUID) (*inventory.InventoryItem, error)
	ListInventoryItems(ctx context.Context, restaurantID uuid.UUID) ([]inventory.InventoryItem, error)
	UpdateInventoryItem(ctx context.Context, item *inventory.InventoryItem) error
	DeleteInventoryItem(ctx context.Context, restaurantID, id uuid.UUID) error
	CreateInventoryLog(ctx context.Context, log *inventory.InventoryLog) error
	ListInventoryLogs(ctx context.Context, restaurantID uuid.UUID, itemID *uuid.UUID, limit int) ([]inventory.InventoryLog, error)

	// Recipes & Costing
	SaveRecipeIngredients(ctx context.Context, restaurantID, menuItemID uuid.UUID, ingredients []inventory.RecipeIngredient) error
	GetRecipeIngredientsByMenuItemID(ctx context.Context, restaurantID, menuItemID uuid.UUID) ([]inventory.RecipeIngredient, error)
	ListDishMargins(ctx context.Context, restaurantID uuid.UUID) ([]inventory.DishMargin, error)
	ListRecipeIngredientsForOrder(ctx context.Context, orderID uuid.UUID) ([]inventory.OrderIngredientRequirement, error)

	// Password Resets
	StorePasswordResetToken(ctx context.Context, token *PasswordResetToken) error
	GetPasswordResetToken(ctx context.Context, tokenHash string) (*PasswordResetToken, error)
	MarkPasswordResetTokenUsed(ctx context.Context, id uuid.UUID) error
}

type PasswordResetToken struct {
	ID        uuid.UUID  `json:"id"`
	StaffID   uuid.UUID  `json:"staff_id"`
	TokenHash string     `json:"token_hash"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type queryCounterKey struct{}

var QueryCounterKey = queryCounterKey{}

type QueryCounter struct {
	count int64
}

func (q *QueryCounter) Inc() {
	if q != nil {
		q.count++
	}
}

func (q *QueryCounter) Value() int64 {
	if q == nil {
		return 0
	}
	return q.count
}

func RecordQuery(ctx context.Context) {
	if ctx == nil {
		return
	}
	if c, ok := ctx.Value(QueryCounterKey).(*QueryCounter); ok && c != nil {
		c.Inc()
	}
}
