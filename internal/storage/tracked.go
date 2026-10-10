package storage

import (
	"context"
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

// TrackedRepository wraps any Repository to intercept and count DB queries per request.
type TrackedRepository struct {
	underlying Repository
}

func NewTrackedRepository(underlying Repository) *TrackedRepository {
	return &TrackedRepository{underlying: underlying}
}

func (tr *TrackedRepository) CreateSession(ctx context.Context, s *session.DiningSession) error {
	RecordQuery(ctx)
	return tr.underlying.CreateSession(ctx, s)
}

func (tr *TrackedRepository) GetSessionByID(ctx context.Context, id uuid.UUID) (*session.DiningSession, error) {
	RecordQuery(ctx)
	return tr.underlying.GetSessionByID(ctx, id)
}

func (tr *TrackedRepository) GetSessionByToken(ctx context.Context, token string) (*session.DiningSession, error) {
	RecordQuery(ctx)
	return tr.underlying.GetSessionByToken(ctx, token)
}

func (tr *TrackedRepository) GetActiveSessionByTableID(ctx context.Context, tableID uuid.UUID) (*session.DiningSession, error) {
	RecordQuery(ctx)
	return tr.underlying.GetActiveSessionByTableID(ctx, tableID)
}

func (tr *TrackedRepository) UpdateSession(ctx context.Context, s *session.DiningSession) error {
	RecordQuery(ctx)
	return tr.underlying.UpdateSession(ctx, s)
}

func (tr *TrackedRepository) ListActiveSessions(ctx context.Context, restaurantID uuid.UUID) ([]session.DiningSession, error) {
	RecordQuery(ctx)
	return tr.underlying.ListActiveSessions(ctx, restaurantID)
}

func (tr *TrackedRepository) ListActiveSessionsAll(ctx context.Context) ([]session.DiningSession, error) {
	RecordQuery(ctx)
	return tr.underlying.ListActiveSessionsAll(ctx)
}

func (tr *TrackedRepository) GetSessionsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*session.DiningSession, error) {
	RecordQuery(ctx)
	return tr.underlying.GetSessionsByIDs(ctx, ids)
}

func (tr *TrackedRepository) AddParticipant(ctx context.Context, p *session.SessionParticipant) error {
	RecordQuery(ctx)
	return tr.underlying.AddParticipant(ctx, p)
}

func (tr *TrackedRepository) GetParticipants(ctx context.Context, sessionID uuid.UUID) ([]session.SessionParticipant, error) {
	RecordQuery(ctx)
	return tr.underlying.GetParticipants(ctx, sessionID)
}

func (tr *TrackedRepository) CreateOrder(ctx context.Context, o *order.Order, items []order.OrderItem) error {
	RecordQuery(ctx)
	return tr.underlying.CreateOrder(ctx, o, items)
}

func (tr *TrackedRepository) GetOrderByID(ctx context.Context, id uuid.UUID) (*order.Order, error) {
	RecordQuery(ctx)
	return tr.underlying.GetOrderByID(ctx, id)
}

func (tr *TrackedRepository) GetOrdersBySessionID(ctx context.Context, sessionID uuid.UUID) ([]order.Order, error) {
	RecordQuery(ctx)
	return tr.underlying.GetOrdersBySessionID(ctx, sessionID)
}

func (tr *TrackedRepository) GetOrdersBySessionIDs(ctx context.Context, sessionIDs []uuid.UUID) (map[uuid.UUID][]order.Order, error) {
	RecordQuery(ctx)
	return tr.underlying.GetOrdersBySessionIDs(ctx, sessionIDs)
}

func (tr *TrackedRepository) UpdateOrder(ctx context.Context, o *order.Order) error {
	RecordQuery(ctx)
	return tr.underlying.UpdateOrder(ctx, o)
}

func (tr *TrackedRepository) ListKitchenQueue(ctx context.Context, restaurantID uuid.UUID, statuses []order.State) ([]order.Order, error) {
	RecordQuery(ctx)
	return tr.underlying.ListKitchenQueue(ctx, restaurantID, statuses)
}

func (tr *TrackedRepository) ListPendingOrders(ctx context.Context, restaurantID uuid.UUID) ([]order.Order, error) {
	RecordQuery(ctx)
	return tr.underlying.ListPendingOrders(ctx, restaurantID)
}

func (tr *TrackedRepository) ListOrders(ctx context.Context, restaurantID uuid.UUID, limit int, startDate, endDate *time.Time) ([]order.Order, error) {
	RecordQuery(ctx)
	return tr.underlying.ListOrders(ctx, restaurantID, limit, startDate, endDate)
}

func (tr *TrackedRepository) ListRecentOrdersAllRestaurants(ctx context.Context, limit int, startDate, endDate *time.Time) ([]order.Order, error) {
	RecordQuery(ctx)
	return tr.underlying.ListRecentOrdersAllRestaurants(ctx, limit, startDate, endDate)
}

func (tr *TrackedRepository) CountActiveSessionsAll(ctx context.Context) (int, error) {
	RecordQuery(ctx)
	return tr.underlying.CountActiveSessionsAll(ctx)
}

func (tr *TrackedRepository) SumPlatformFeesAll(ctx context.Context) (int64, int64, error) {
	RecordQuery(ctx)
	return tr.underlying.SumPlatformFeesAll(ctx)
}

func (tr *TrackedRepository) RecordOrderStatusHistory(ctx context.Context, h *order.StatusHistory) error {
	RecordQuery(ctx)
	return tr.underlying.RecordOrderStatusHistory(ctx, h)
}

func (tr *TrackedRepository) GetOrderStatusHistory(ctx context.Context, orderID uuid.UUID) ([]order.StatusHistory, error) {
	RecordQuery(ctx)
	return tr.underlying.GetOrderStatusHistory(ctx, orderID)
}

func (tr *TrackedRepository) CreatePayment(ctx context.Context, p *payment.Payment) error {
	RecordQuery(ctx)
	return tr.underlying.CreatePayment(ctx, p)
}

func (tr *TrackedRepository) GetPaymentByID(ctx context.Context, id uuid.UUID) (*payment.Payment, error) {
	RecordQuery(ctx)
	return tr.underlying.GetPaymentByID(ctx, id)
}

func (tr *TrackedRepository) GetPaymentsBySessionID(ctx context.Context, sessionID uuid.UUID) ([]payment.Payment, error) {
	RecordQuery(ctx)
	return tr.underlying.GetPaymentsBySessionID(ctx, sessionID)
}

func (tr *TrackedRepository) GetPaymentsBySessionIDs(ctx context.Context, sessionIDs []uuid.UUID) (map[uuid.UUID][]payment.Payment, error) {
	RecordQuery(ctx)
	return tr.underlying.GetPaymentsBySessionIDs(ctx, sessionIDs)
}

func (tr *TrackedRepository) UpdatePayment(ctx context.Context, p *payment.Payment) error {
	RecordQuery(ctx)
	return tr.underlying.UpdatePayment(ctx, p)
}

func (tr *TrackedRepository) CreateRefund(ctx context.Context, r *payment.Refund) error {
	RecordQuery(ctx)
	return tr.underlying.CreateRefund(ctx, r)
}

func (tr *TrackedRepository) GetRefundsBySessionID(ctx context.Context, sessionID uuid.UUID) ([]payment.Refund, error) {
	RecordQuery(ctx)
	return tr.underlying.GetRefundsBySessionID(ctx, sessionID)
}

func (tr *TrackedRepository) CreateAdjustment(ctx context.Context, a *payment.Adjustment) error {
	RecordQuery(ctx)
	return tr.underlying.CreateAdjustment(ctx, a)
}

func (tr *TrackedRepository) GetAdjustmentsBySessionID(ctx context.Context, sessionID uuid.UUID) ([]payment.Adjustment, error) {
	RecordQuery(ctx)
	return tr.underlying.GetAdjustmentsBySessionID(ctx, sessionID)
}

func (tr *TrackedRepository) CreateExitPass(ctx context.Context, ep *exitpass.ExitPass) error {
	RecordQuery(ctx)
	return tr.underlying.CreateExitPass(ctx, ep)
}

func (tr *TrackedRepository) GetExitPassByID(ctx context.Context, id uuid.UUID) (*exitpass.ExitPass, error) {
	RecordQuery(ctx)
	return tr.underlying.GetExitPassByID(ctx, id)
}

func (tr *TrackedRepository) GetExitPassBySessionID(ctx context.Context, sessionID uuid.UUID) (*exitpass.ExitPass, error) {
	RecordQuery(ctx)
	return tr.underlying.GetExitPassBySessionID(ctx, sessionID)
}

func (tr *TrackedRepository) UpdateExitPass(ctx context.Context, ep *exitpass.ExitPass) error {
	RecordQuery(ctx)
	return tr.underlying.UpdateExitPass(ctx, ep)
}

func (tr *TrackedRepository) CreatePlatformFeeEntry(ctx context.Context, entry *ledger.PlatformFeeLedgerEntry) error {
	RecordQuery(ctx)
	return tr.underlying.CreatePlatformFeeEntry(ctx, entry)
}

func (tr *TrackedRepository) GetPlatformFeeBySessionID(ctx context.Context, sessionID uuid.UUID) (*ledger.PlatformFeeLedgerEntry, error) {
	RecordQuery(ctx)
	return tr.underlying.GetPlatformFeeBySessionID(ctx, sessionID)
}

func (tr *TrackedRepository) ListPlatformFees(ctx context.Context, restaurantID uuid.UUID, period string) ([]ledger.PlatformFeeLedgerEntry, error) {
	RecordQuery(ctx)
	return tr.underlying.ListPlatformFees(ctx, restaurantID, period)
}

func (tr *TrackedRepository) CreateRefundAdjustment(ctx context.Context, adj *ledger.RefundAdjustment) error {
	RecordQuery(ctx)
	return tr.underlying.CreateRefundAdjustment(ctx, adj)
}

func (tr *TrackedRepository) CreateSettlement(ctx context.Context, s *ledger.RestaurantSettlement) error {
	RecordQuery(ctx)
	return tr.underlying.CreateSettlement(ctx, s)
}

func (tr *TrackedRepository) ListSettlements(ctx context.Context, restaurantID uuid.UUID) ([]ledger.RestaurantSettlement, error) {
	RecordQuery(ctx)
	return tr.underlying.ListSettlements(ctx, restaurantID)
}

func (tr *TrackedRepository) CreateRestaurant(ctx context.Context, r *restaurant.Restaurant) error {
	RecordQuery(ctx)
	return tr.underlying.CreateRestaurant(ctx, r)
}

func (tr *TrackedRepository) GetRestaurantByID(ctx context.Context, id uuid.UUID) (*restaurant.Restaurant, error) {
	RecordQuery(ctx)
	return tr.underlying.GetRestaurantByID(ctx, id)
}

func (tr *TrackedRepository) GetRestaurantBySlug(ctx context.Context, slug string) (*restaurant.Restaurant, error) {
	RecordQuery(ctx)
	return tr.underlying.GetRestaurantBySlug(ctx, slug)
}

func (tr *TrackedRepository) ListRestaurants(ctx context.Context) ([]restaurant.Restaurant, error) {
	RecordQuery(ctx)
	return tr.underlying.ListRestaurants(ctx)
}

func (tr *TrackedRepository) UpdateRestaurant(ctx context.Context, r *restaurant.Restaurant) error {
	RecordQuery(ctx)
	return tr.underlying.UpdateRestaurant(ctx, r)
}

func (tr *TrackedRepository) CreateTable(ctx context.Context, t *restaurant.Table) error {
	RecordQuery(ctx)
	return tr.underlying.CreateTable(ctx, t)
}

func (tr *TrackedRepository) GetTableByID(ctx context.Context, id uuid.UUID) (*restaurant.Table, error) {
	RecordQuery(ctx)
	return tr.underlying.GetTableByID(ctx, id)
}

func (tr *TrackedRepository) GetTableByToken(ctx context.Context, token string) (*restaurant.Table, error) {
	RecordQuery(ctx)
	return tr.underlying.GetTableByToken(ctx, token)
}

func (tr *TrackedRepository) ListTables(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.Table, error) {
	RecordQuery(ctx)
	return tr.underlying.ListTables(ctx, restaurantID)
}

func (tr *TrackedRepository) ListTablesAll(ctx context.Context) ([]restaurant.Table, error) {
	RecordQuery(ctx)
	return tr.underlying.ListTablesAll(ctx)
}

func (tr *TrackedRepository) UpdateTable(ctx context.Context, t *restaurant.Table) error {
	RecordQuery(ctx)
	return tr.underlying.UpdateTable(ctx, t)
}

func (tr *TrackedRepository) CreateStaff(ctx context.Context, s *restaurant.StaffUser) error {
	RecordQuery(ctx)
	return tr.underlying.CreateStaff(ctx, s)
}

func (tr *TrackedRepository) GetStaffByID(ctx context.Context, id uuid.UUID) (*restaurant.StaffUser, error) {
	RecordQuery(ctx)
	return tr.underlying.GetStaffByID(ctx, id)
}

func (tr *TrackedRepository) GetStaffByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*restaurant.StaffUser, error) {
	RecordQuery(ctx)
	return tr.underlying.GetStaffByIDs(ctx, ids)
}

func (tr *TrackedRepository) GetStaffByEmail(ctx context.Context, email string) (*restaurant.StaffUser, error) {
	RecordQuery(ctx)
	return tr.underlying.GetStaffByEmail(ctx, email)
}

func (tr *TrackedRepository) GetStaffByEmployeeID(ctx context.Context, restaurantID uuid.UUID, employeeID string) (*restaurant.StaffUser, error) {
	RecordQuery(ctx)
	return tr.underlying.GetStaffByEmployeeID(ctx, restaurantID, employeeID)
}

func (tr *TrackedRepository) GetStaffByEmployeeIDGlobal(ctx context.Context, employeeID string) (*restaurant.StaffUser, error) {
	RecordQuery(ctx)
	return tr.underlying.GetStaffByEmployeeIDGlobal(ctx, employeeID)
}

func (tr *TrackedRepository) ListStaff(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.StaffUser, error) {
	RecordQuery(ctx)
	return tr.underlying.ListStaff(ctx, restaurantID)
}

func (tr *TrackedRepository) UpdateStaffPassword(ctx context.Context, staffID uuid.UUID, passwordHash string) error {
	RecordQuery(ctx)
	return tr.underlying.UpdateStaffPassword(ctx, staffID, passwordHash)
}

func (tr *TrackedRepository) CreateGuard(ctx context.Context, g *restaurant.GuardUser) error {
	RecordQuery(ctx)
	return tr.underlying.CreateGuard(ctx, g)
}

func (tr *TrackedRepository) GetGuardByID(ctx context.Context, id uuid.UUID) (*restaurant.GuardUser, error) {
	RecordQuery(ctx)
	return tr.underlying.GetGuardByID(ctx, id)
}

func (tr *TrackedRepository) GetGuardByPhone(ctx context.Context, phone string) (*restaurant.GuardUser, error) {
	RecordQuery(ctx)
	return tr.underlying.GetGuardByPhone(ctx, phone)
}

func (tr *TrackedRepository) CreateCategory(ctx context.Context, c *restaurant.MenuCategory) error {
	RecordQuery(ctx)
	return tr.underlying.CreateCategory(ctx, c)
}

func (tr *TrackedRepository) ListCategories(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.MenuCategory, error) {
	RecordQuery(ctx)
	return tr.underlying.ListCategories(ctx, restaurantID)
}

func (tr *TrackedRepository) CreateMenuItem(ctx context.Context, m *restaurant.MenuItem) error {
	RecordQuery(ctx)
	return tr.underlying.CreateMenuItem(ctx, m)
}

func (tr *TrackedRepository) GetMenuItemByID(ctx context.Context, id uuid.UUID) (*restaurant.MenuItem, error) {
	RecordQuery(ctx)
	return tr.underlying.GetMenuItemByID(ctx, id)
}

func (tr *TrackedRepository) GetMenuItemsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*restaurant.MenuItem, error) {
	RecordQuery(ctx)
	return tr.underlying.GetMenuItemsByIDs(ctx, ids)
}

func (tr *TrackedRepository) ListMenuItems(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.MenuItem, error) {
	RecordQuery(ctx)
	return tr.underlying.ListMenuItems(ctx, restaurantID)
}

func (tr *TrackedRepository) UpdateMenuItemAvailability(ctx context.Context, id uuid.UUID, isAvailable bool) error {
	RecordQuery(ctx)
	return tr.underlying.UpdateMenuItemAvailability(ctx, id, isAvailable)
}

func (tr *TrackedRepository) ListVariantsByMenuItemIDs(ctx context.Context, menuItemIDs []uuid.UUID) (map[uuid.UUID][]restaurant.MenuItemVariant, error) {
	RecordQuery(ctx)
	return tr.underlying.ListVariantsByMenuItemIDs(ctx, menuItemIDs)
}

func (tr *TrackedRepository) ReplaceMenuItemVariants(ctx context.Context, menuItemID uuid.UUID, variants []restaurant.MenuItemVariant) error {
	RecordQuery(ctx)
	return tr.underlying.ReplaceMenuItemVariants(ctx, menuItemID, variants)
}

func (tr *TrackedRepository) GetMenuItemVariantByID(ctx context.Context, id uuid.UUID) (*restaurant.MenuItemVariant, error) {
	RecordQuery(ctx)
	return tr.underlying.GetMenuItemVariantByID(ctx, id)
}

func (tr *TrackedRepository) GetMenuItemVariantsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*restaurant.MenuItemVariant, error) {
	RecordQuery(ctx)
	return tr.underlying.GetMenuItemVariantsByIDs(ctx, ids)
}

func (tr *TrackedRepository) GetSettings(ctx context.Context, restaurantID uuid.UUID) (*restaurant.RestaurantSettings, error) {
	RecordQuery(ctx)
	return tr.underlying.GetSettings(ctx, restaurantID)
}

func (tr *TrackedRepository) UpdateSettings(ctx context.Context, s *restaurant.RestaurantSettings) error {
	RecordQuery(ctx)
	return tr.underlying.UpdateSettings(ctx, s)
}

func (tr *TrackedRepository) GetOnboarding(ctx context.Context, restaurantID uuid.UUID) (*restaurant.RestaurantOnboarding, error) {
	RecordQuery(ctx)
	return tr.underlying.GetOnboarding(ctx, restaurantID)
}

func (tr *TrackedRepository) UpdateOnboarding(ctx context.Context, o *restaurant.RestaurantOnboarding) error {
	RecordQuery(ctx)
	return tr.underlying.UpdateOnboarding(ctx, o)
}

func (tr *TrackedRepository) AppendAuditLog(ctx context.Context, log *audit.AuditLog) error {
	RecordQuery(ctx)
	return tr.underlying.AppendAuditLog(ctx, log)
}

func (tr *TrackedRepository) AppendStaffAction(ctx context.Context, action *audit.StaffAction) error {
	RecordQuery(ctx)
	return tr.underlying.AppendStaffAction(ctx, action)
}

func (tr *TrackedRepository) ListAuditLogs(ctx context.Context, restaurantID uuid.UUID, limit, offset int) ([]audit.AuditLog, error) {
	RecordQuery(ctx)
	return tr.underlying.ListAuditLogs(ctx, restaurantID, limit, offset)
}

func (tr *TrackedRepository) ListStaffActions(ctx context.Context, restaurantID uuid.UUID, limit, offset int) ([]audit.StaffAction, error) {
	RecordQuery(ctx)
	return tr.underlying.ListStaffActions(ctx, restaurantID, limit, offset)
}

func (tr *TrackedRepository) StoreOutboxEvent(ctx context.Context, event *OutboxEvent) error {
	RecordQuery(ctx)
	return tr.underlying.StoreOutboxEvent(ctx, event)
}

func (tr *TrackedRepository) FetchPendingOutbox(ctx context.Context, batchSize int) ([]OutboxEvent, error) {
	RecordQuery(ctx)
	return tr.underlying.FetchPendingOutbox(ctx, batchSize)
}

func (tr *TrackedRepository) ClaimPendingOutbox(ctx context.Context, batchSize int, leaseDuration time.Duration) ([]OutboxEvent, error) {
	RecordQuery(ctx)
	return tr.underlying.ClaimPendingOutbox(ctx, batchSize, leaseDuration)
}

func (tr *TrackedRepository) MarkOutboxPublished(ctx context.Context, id uuid.UUID) error {
	RecordQuery(ctx)
	return tr.underlying.MarkOutboxPublished(ctx, id)
}

func (tr *TrackedRepository) MarkOutboxFailed(ctx context.Context, id uuid.UUID, lastErr string, backoff time.Duration, maxRetries int) error {
	RecordQuery(ctx)
	return tr.underlying.MarkOutboxFailed(ctx, id, lastErr, backoff, maxRetries)
}

func (tr *TrackedRepository) ReplayDeadLetterOutbox(ctx context.Context, id uuid.UUID) error {
	RecordQuery(ctx)
	return tr.underlying.ReplayDeadLetterOutbox(ctx, id)
}

func (tr *TrackedRepository) PrunePublishedOutbox(ctx context.Context, maxAge time.Duration) (int64, error) {
	RecordQuery(ctx)
	return tr.underlying.PrunePublishedOutbox(ctx, maxAge)
}

func (tr *TrackedRepository) RecordWebhookEvent(ctx context.Context, gateway, eventID string) (bool, error) {
	RecordQuery(ctx)
	return tr.underlying.RecordWebhookEvent(ctx, gateway, eventID)
}

func (tr *TrackedRepository) CreateExpense(ctx context.Context, e *expense.Expense) error {
	RecordQuery(ctx)
	return tr.underlying.CreateExpense(ctx, e)
}

func (tr *TrackedRepository) ListExpenses(ctx context.Context, restaurantID uuid.UUID, expenseType *expense.ExpenseType, category *expense.ExpenseCategory, startDate, endDate *time.Time) ([]expense.Expense, error) {
	RecordQuery(ctx)
	return tr.underlying.ListExpenses(ctx, restaurantID, expenseType, category, startDate, endDate)
}

func (tr *TrackedRepository) ListExpenseLineItems(ctx context.Context, expenseID uuid.UUID) ([]expense.ExpenseLineItem, error) {
	RecordQuery(ctx)
	return tr.underlying.ListExpenseLineItems(ctx, expenseID)
}

func (tr *TrackedRepository) DeleteExpense(ctx context.Context, restaurantID, expenseID uuid.UUID) error {
	RecordQuery(ctx)
	return tr.underlying.DeleteExpense(ctx, restaurantID, expenseID)
}

func (tr *TrackedRepository) CreateInventoryItem(ctx context.Context, item *inventory.InventoryItem) error {
	RecordQuery(ctx)
	return tr.underlying.CreateInventoryItem(ctx, item)
}

func (tr *TrackedRepository) GetInventoryItemByID(ctx context.Context, restaurantID, id uuid.UUID) (*inventory.InventoryItem, error) {
	RecordQuery(ctx)
	return tr.underlying.GetInventoryItemByID(ctx, restaurantID, id)
}

func (tr *TrackedRepository) ListInventoryItems(ctx context.Context, restaurantID uuid.UUID) ([]inventory.InventoryItem, error) {
	RecordQuery(ctx)
	return tr.underlying.ListInventoryItems(ctx, restaurantID)
}

func (tr *TrackedRepository) UpdateInventoryItem(ctx context.Context, item *inventory.InventoryItem) error {
	RecordQuery(ctx)
	return tr.underlying.UpdateInventoryItem(ctx, item)
}

func (tr *TrackedRepository) DeleteInventoryItem(ctx context.Context, restaurantID, id uuid.UUID) error {
	RecordQuery(ctx)
	return tr.underlying.DeleteInventoryItem(ctx, restaurantID, id)
}

func (tr *TrackedRepository) CreateInventoryLog(ctx context.Context, log *inventory.InventoryLog) error {
	RecordQuery(ctx)
	return tr.underlying.CreateInventoryLog(ctx, log)
}

func (tr *TrackedRepository) CreateInventoryLogs(ctx context.Context, logs []*inventory.InventoryLog) error {
	RecordQuery(ctx)
	return tr.underlying.CreateInventoryLogs(ctx, logs)
}

func (tr *TrackedRepository) ListInventoryLogs(ctx context.Context, restaurantID uuid.UUID, itemID *uuid.UUID, limit int) ([]inventory.InventoryLog, error) {
	RecordQuery(ctx)
	return tr.underlying.ListInventoryLogs(ctx, restaurantID, itemID, limit)
}

func (tr *TrackedRepository) SaveRecipeIngredients(ctx context.Context, restaurantID, menuItemID uuid.UUID, ingredients []inventory.RecipeIngredient) error {
	RecordQuery(ctx)
	return tr.underlying.SaveRecipeIngredients(ctx, restaurantID, menuItemID, ingredients)
}

func (tr *TrackedRepository) GetRecipeIngredientsByMenuItemID(ctx context.Context, restaurantID, menuItemID uuid.UUID) ([]inventory.RecipeIngredient, error) {
	RecordQuery(ctx)
	return tr.underlying.GetRecipeIngredientsByMenuItemID(ctx, restaurantID, menuItemID)
}

func (tr *TrackedRepository) ListDishMargins(ctx context.Context, restaurantID uuid.UUID) ([]inventory.DishMargin, error) {
	RecordQuery(ctx)
	return tr.underlying.ListDishMargins(ctx, restaurantID)
}

func (tr *TrackedRepository) StorePasswordResetToken(ctx context.Context, token *PasswordResetToken) error {
	RecordQuery(ctx)
	return tr.underlying.StorePasswordResetToken(ctx, token)
}

func (tr *TrackedRepository) GetPasswordResetToken(ctx context.Context, tokenHash string) (*PasswordResetToken, error) {
	RecordQuery(ctx)
	return tr.underlying.GetPasswordResetToken(ctx, tokenHash)
}

func (tr *TrackedRepository) MarkPasswordResetTokenUsed(ctx context.Context, id uuid.UUID) error {
	RecordQuery(ctx)
	return tr.underlying.MarkPasswordResetTokenUsed(ctx, id)
}

func (tr *TrackedRepository) ListRecipeIngredientsForOrder(ctx context.Context, orderID uuid.UUID) ([]inventory.OrderIngredientRequirement, error) {
	RecordQuery(ctx)
	return tr.underlying.ListRecipeIngredientsForOrder(ctx, orderID)
}

func (tr *TrackedRepository) GetSubscription(ctx context.Context, restaurantID uuid.UUID) (*restaurant.Restaurant, error) {
	RecordQuery(ctx)
	return tr.underlying.GetSubscription(ctx, restaurantID)
}

func (tr *TrackedRepository) RenewSubscription(ctx context.Context, restaurantID uuid.UUID, days int) (*restaurant.Restaurant, error) {
	RecordQuery(ctx)
	return tr.underlying.RenewSubscription(ctx, restaurantID, days)
}

func (tr *TrackedRepository) CreateSubscriptionOTP(ctx context.Context, o *restaurant.SubscriptionOTP) error {
	RecordQuery(ctx)
	return tr.underlying.CreateSubscriptionOTP(ctx, o)
}

func (tr *TrackedRepository) GetActiveSubscriptionOTP(ctx context.Context, restaurantID uuid.UUID) (*restaurant.SubscriptionOTP, error) {
	RecordQuery(ctx)
	return tr.underlying.GetActiveSubscriptionOTP(ctx, restaurantID)
}

func (tr *TrackedRepository) UpdateSubscriptionOTP(ctx context.Context, o *restaurant.SubscriptionOTP) error {
	RecordQuery(ctx)
	return tr.underlying.UpdateSubscriptionOTP(ctx, o)
}

func (tr *TrackedRepository) ListSubscriptionOTPs(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.SubscriptionOTP, error) {
	RecordQuery(ctx)
	return tr.underlying.ListSubscriptionOTPs(ctx, restaurantID)
}

func (tr *TrackedRepository) ConsumeSubscriptionOTP(ctx context.Context, id uuid.UUID) (bool, error) {
	RecordQuery(ctx)
	return tr.underlying.ConsumeSubscriptionOTP(ctx, id)
}

func (tr *TrackedRepository) CreateFranchise(ctx context.Context, f *restaurant.Franchise) error {
	RecordQuery(ctx)
	return tr.underlying.CreateFranchise(ctx, f)
}

func (tr *TrackedRepository) GetFranchiseByID(ctx context.Context, id uuid.UUID) (*restaurant.Franchise, error) {
	RecordQuery(ctx)
	return tr.underlying.GetFranchiseByID(ctx, id)
}

func (tr *TrackedRepository) UpdateFranchise(ctx context.Context, f *restaurant.Franchise) error {
	RecordQuery(ctx)
	return tr.underlying.UpdateFranchise(ctx, f)
}

func (tr *TrackedRepository) ListFranchises(ctx context.Context) ([]restaurant.Franchise, error) {
	RecordQuery(ctx)
	return tr.underlying.ListFranchises(ctx)
}

func (tr *TrackedRepository) ListRestaurantsByFranchise(ctx context.Context, franchiseID uuid.UUID) ([]restaurant.Restaurant, error) {
	RecordQuery(ctx)
	return tr.underlying.ListRestaurantsByFranchise(ctx, franchiseID)
}

func (tr *TrackedRepository) CreateFranchiseInviteCode(ctx context.Context, c *restaurant.FranchiseInviteCode) error {
	RecordQuery(ctx)
	return tr.underlying.CreateFranchiseInviteCode(ctx, c)
}

func (tr *TrackedRepository) GetFranchiseInviteCode(ctx context.Context, code string) (*restaurant.FranchiseInviteCode, error) {
	RecordQuery(ctx)
	return tr.underlying.GetFranchiseInviteCode(ctx, code)
}

func (tr *TrackedRepository) MarkFranchiseInviteCodeUsed(ctx context.Context, code string, restaurantID uuid.UUID) error {
	RecordQuery(ctx)
	return tr.underlying.MarkFranchiseInviteCodeUsed(ctx, code, restaurantID)
}
