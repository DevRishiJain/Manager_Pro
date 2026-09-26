package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"github.com/devrishijain/table-manager/internal/domain/money"

	"github.com/devrishijain/table-manager/internal/domain/audit"
	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/devrishijain/table-manager/internal/domain/ledger"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/google/uuid"
)

var (
	ErrNotFound           = storage.ErrNotFound
	ErrActiveSessionExists = storage.ErrActiveSessionExists
	ErrOptimisticLock      = storage.ErrOptimisticLock
	ErrDuplicateKey       = errors.New("duplicate key violation")
)

type MemoryRepository struct {
	mu sync.RWMutex

	restaurants   map[uuid.UUID]*restaurant.Restaurant
	tables        map[uuid.UUID]*restaurant.Table
	tablesByToken map[string]*restaurant.Table
	staff             map[uuid.UUID]*restaurant.StaffUser
	staffByEmail      map[string]*restaurant.StaffUser
	staffByEmployeeID map[string]*restaurant.StaffUser
	guards            map[uuid.UUID]*restaurant.GuardUser
	guardsByPhone map[string]*restaurant.GuardUser

	categories map[uuid.UUID]*restaurant.MenuCategory
	menuItems  map[uuid.UUID]*restaurant.MenuItem

	settings   map[uuid.UUID]*restaurant.RestaurantSettings
	onboarding map[uuid.UUID]*restaurant.RestaurantOnboarding

	sessions         map[uuid.UUID]*session.DiningSession
	sessionsByToken  map[string]*session.DiningSession
	participants     map[uuid.UUID][]session.SessionParticipant

	orders      map[uuid.UUID]*order.Order
	orderItems  map[uuid.UUID][]order.OrderItem

	payments    map[uuid.UUID]*payment.Payment
	refunds     map[uuid.UUID][]payment.Refund
	adjustments map[uuid.UUID][]payment.Adjustment

	exitPasses       map[uuid.UUID]*exitpass.ExitPass
	exitPassBySession map[uuid.UUID]*exitpass.ExitPass

	platformFees map[uuid.UUID]*ledger.PlatformFeeLedgerEntry
	feeBySession map[uuid.UUID]*ledger.PlatformFeeLedgerEntry
	refundAdjs   map[uuid.UUID]*ledger.RefundAdjustment
	settlements  map[uuid.UUID]*ledger.RestaurantSettlement

	auditLogs    []audit.AuditLog
	staffActions []audit.StaffAction

	outboxEvents  map[uuid.UUID]*storage.OutboxEvent
	webhookEvents map[string]time.Time
}

func NewMemoryRepository() *MemoryRepository {
	repo := &MemoryRepository{
		restaurants:       make(map[uuid.UUID]*restaurant.Restaurant),
		tables:            make(map[uuid.UUID]*restaurant.Table),
		tablesByToken:     make(map[string]*restaurant.Table),
		staff:             make(map[uuid.UUID]*restaurant.StaffUser),
		staffByEmail:      make(map[string]*restaurant.StaffUser),
		staffByEmployeeID: make(map[string]*restaurant.StaffUser),
		guards:            make(map[uuid.UUID]*restaurant.GuardUser),
		guardsByPhone:     make(map[string]*restaurant.GuardUser),
		categories:        make(map[uuid.UUID]*restaurant.MenuCategory),
		menuItems:         make(map[uuid.UUID]*restaurant.MenuItem),
		settings:          make(map[uuid.UUID]*restaurant.RestaurantSettings),
		onboarding:        make(map[uuid.UUID]*restaurant.RestaurantOnboarding),
		sessions:          make(map[uuid.UUID]*session.DiningSession),
		sessionsByToken:   make(map[string]*session.DiningSession),
		participants:      make(map[uuid.UUID][]session.SessionParticipant),
		orders:            make(map[uuid.UUID]*order.Order),
		orderItems:        make(map[uuid.UUID][]order.OrderItem),
		payments:          make(map[uuid.UUID]*payment.Payment),
		refunds:           make(map[uuid.UUID][]payment.Refund),
		adjustments:       make(map[uuid.UUID][]payment.Adjustment),
		exitPasses:        make(map[uuid.UUID]*exitpass.ExitPass),
		exitPassBySession: make(map[uuid.UUID]*exitpass.ExitPass),
		platformFees:      make(map[uuid.UUID]*ledger.PlatformFeeLedgerEntry),
		feeBySession:      make(map[uuid.UUID]*ledger.PlatformFeeLedgerEntry),
		refundAdjs:        make(map[uuid.UUID]*ledger.RefundAdjustment),
		settlements:       make(map[uuid.UUID]*ledger.RestaurantSettlement),
		auditLogs:         make([]audit.AuditLog, 0),
		staffActions:      make([]audit.StaffAction, 0),
		outboxEvents:      make(map[uuid.UUID]*storage.OutboxEvent),
		webhookEvents:     make(map[string]time.Time),
	}
	repo.seedDefaultData()
	return repo
}

// Ensure MemoryRepository implements storage.Repository
var _ storage.Repository = (*MemoryRepository)(nil)

// ---------------- Session Methods ----------------

func (m *MemoryRepository) CreateSession(ctx context.Context, s *session.DiningSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Enforce table uniqueness constraint: at most one non-terminal session per table
	for _, existing := range m.sessions {
		if existing.TableID == s.TableID && !existing.Status.IsTerminal() {
			return ErrActiveSessionExists
		}
	}

	cpy := *s
	m.sessions[s.ID] = &cpy
	m.sessionsByToken[s.SessionToken] = &cpy
	return nil
}

func (m *MemoryRepository) GetSessionByID(ctx context.Context, id uuid.UUID) (*session.DiningSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *s
	return &cpy, nil
}

func (m *MemoryRepository) GetSessionByToken(ctx context.Context, token string) (*session.DiningSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.sessionsByToken[token]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *s
	return &cpy, nil
}

func (m *MemoryRepository) GetActiveSessionByTableID(ctx context.Context, tableID uuid.UUID) (*session.DiningSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.sessions {
		if s.TableID == tableID && !s.Status.IsTerminal() {
			cpy := *s
			return &cpy, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryRepository) UpdateSession(ctx context.Context, s *session.DiningSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.sessions[s.ID]
	if !ok {
		return ErrNotFound
	}
	if existing.Version != s.Version {
		return ErrOptimisticLock
	}

	s.Version++
	s.UpdatedAt = time.Now()
	cpy := *s
	m.sessions[s.ID] = &cpy
	m.sessionsByToken[s.SessionToken] = &cpy
	return nil
}

func (m *MemoryRepository) ListActiveSessions(ctx context.Context, restaurantID uuid.UUID) ([]session.DiningSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []session.DiningSession
	for _, s := range m.sessions {
		if s.RestaurantID == restaurantID && !s.Status.IsTerminal() {
			result = append(result, *s)
		}
	}
	return result, nil
}

func (m *MemoryRepository) AddParticipant(ctx context.Context, p *session.SessionParticipant) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := *p
	m.participants[p.SessionID] = append(m.participants[p.SessionID], cpy)
	return nil
}

func (m *MemoryRepository) GetParticipants(ctx context.Context, sessionID uuid.UUID) ([]session.SessionParticipant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := m.participants[sessionID]
	res := make([]session.SessionParticipant, len(list))
	copy(res, list)
	return res, nil
}

// ---------------- Order Methods ----------------

func (m *MemoryRepository) CreateOrder(ctx context.Context, o *order.Order, items []order.OrderItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	orderCpy := *o
	m.orders[o.ID] = &orderCpy

	itemsCpy := make([]order.OrderItem, len(items))
	copy(itemsCpy, items)
	m.orderItems[o.ID] = itemsCpy
	return nil
}

func (m *MemoryRepository) GetOrderByID(ctx context.Context, id uuid.UUID) (*order.Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	o, ok := m.orders[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *o
	cpy.Items = m.orderItems[id]
	return &cpy, nil
}

func (m *MemoryRepository) GetOrdersBySessionID(ctx context.Context, sessionID uuid.UUID) ([]order.Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []order.Order
	for _, o := range m.orders {
		if o.SessionID == sessionID {
			cpy := *o
			cpy.Items = m.orderItems[o.ID]
			res = append(res, cpy)
		}
	}
	return res, nil
}

func (m *MemoryRepository) UpdateOrder(ctx context.Context, o *order.Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.orders[o.ID]
	if !ok {
		return ErrNotFound
	}
	if existing.Version != o.Version {
		return ErrOptimisticLock
	}

	o.Version++
	o.UpdatedAt = time.Now()
	cpy := *o
	m.orders[o.ID] = &cpy
	return nil
}

func (m *MemoryRepository) ListKitchenQueue(ctx context.Context, restaurantID uuid.UUID, statuses []order.State) ([]order.Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statusMap := make(map[order.State]bool)
	for _, st := range statuses {
		statusMap[st] = true
	}

	var res []order.Order
	for _, o := range m.orders {
		if o.RestaurantID == restaurantID && statusMap[o.Status] && time.Since(o.PlacedAt) <= 12*time.Hour {
			cpy := *o
			cpy.Items = m.orderItems[o.ID]
			if sess, exists := m.sessions[o.SessionID]; exists {
				if tbl, tblExists := m.tables[sess.TableID]; tblExists {
					cpy.TableNumber = tbl.TableNumber
				}
			}
			res = append(res, cpy)
		}
	}
	return res, nil
}

func (m *MemoryRepository) ListPendingOrders(ctx context.Context, restaurantID uuid.UUID) ([]order.Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []order.Order
	for _, o := range m.orders {
		if o.RestaurantID == restaurantID && (o.Status == order.StatePlacedUnverified || o.Status == order.StatePlacedVerified) && time.Since(o.PlacedAt) <= 12*time.Hour {
			cpy := *o
			cpy.Items = m.orderItems[o.ID]
			if sess, exists := m.sessions[o.SessionID]; exists {
				if tbl, tblExists := m.tables[sess.TableID]; tblExists {
					cpy.TableNumber = tbl.TableNumber
				}
			}
			res = append(res, cpy)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].PlacedAt.Before(res[j].PlacedAt)
	})
	return res, nil
}

// ---------------- Payment Methods ----------------

func (m *MemoryRepository) CreatePayment(ctx context.Context, p *payment.Payment) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := *p
	m.payments[p.ID] = &cpy
	return nil
}

func (m *MemoryRepository) GetPaymentByID(ctx context.Context, id uuid.UUID) (*payment.Payment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.payments[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *p
	return &cpy, nil
}

func (m *MemoryRepository) GetPaymentsBySessionID(ctx context.Context, sessionID uuid.UUID) ([]payment.Payment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []payment.Payment
	for _, p := range m.payments {
		if p.SessionID == sessionID {
			res = append(res, *p)
		}
	}
	return res, nil
}

func (m *MemoryRepository) UpdatePayment(ctx context.Context, p *payment.Payment) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.payments[p.ID]
	if !ok {
		return ErrNotFound
	}
	if existing.Version != p.Version {
		return ErrOptimisticLock
	}

	p.Version++
	p.UpdatedAt = time.Now()
	cpy := *p
	m.payments[p.ID] = &cpy
	return nil
}

func (m *MemoryRepository) CreateRefund(ctx context.Context, r *payment.Refund) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := *r
	m.refunds[r.SessionID] = append(m.refunds[r.SessionID], cpy)
	return nil
}

func (m *MemoryRepository) GetRefundsBySessionID(ctx context.Context, sessionID uuid.UUID) ([]payment.Refund, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := m.refunds[sessionID]
	res := make([]payment.Refund, len(list))
	copy(res, list)
	return res, nil
}

func (m *MemoryRepository) CreateAdjustment(ctx context.Context, a *payment.Adjustment) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if a.Type == payment.AdjustmentTypeOverpaymentCredit {
		for _, existing := range m.adjustments[a.SessionID] {
			if existing.Type == payment.AdjustmentTypeOverpaymentCredit {
				return nil
			}
		}
	}

	cpy := *a
	m.adjustments[a.SessionID] = append(m.adjustments[a.SessionID], cpy)
	return nil
}

func (m *MemoryRepository) GetAdjustmentsBySessionID(ctx context.Context, sessionID uuid.UUID) ([]payment.Adjustment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := m.adjustments[sessionID]
	res := make([]payment.Adjustment, len(list))
	copy(res, list)
	return res, nil
}

// ---------------- ExitPass Methods ----------------

func (m *MemoryRepository) CreateExitPass(ctx context.Context, ep *exitpass.ExitPass) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.exitPassBySession[ep.SessionID]; exists {
		return ErrDuplicateKey
	}

	cpy := *ep
	m.exitPasses[ep.ID] = &cpy
	m.exitPassBySession[ep.SessionID] = &cpy
	return nil
}

func (m *MemoryRepository) GetExitPassByID(ctx context.Context, id uuid.UUID) (*exitpass.ExitPass, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ep, ok := m.exitPasses[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *ep
	return &cpy, nil
}

func (m *MemoryRepository) GetExitPassBySessionID(ctx context.Context, sessionID uuid.UUID) (*exitpass.ExitPass, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ep, ok := m.exitPassBySession[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *ep
	return &cpy, nil
}

func (m *MemoryRepository) UpdateExitPass(ctx context.Context, ep *exitpass.ExitPass) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.exitPasses[ep.ID]
	if !ok {
		return ErrNotFound
	}
	if existing.Version != ep.Version {
		return ErrOptimisticLock
	}

	ep.Version++
	ep.UpdatedAt = time.Now()
	cpy := *ep
	m.exitPasses[ep.ID] = &cpy
	m.exitPassBySession[ep.SessionID] = &cpy
	return nil
}

// ---------------- Ledger Methods ----------------

func (m *MemoryRepository) CreatePlatformFeeEntry(ctx context.Context, entry *ledger.PlatformFeeLedgerEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.feeBySession[entry.SessionID]; exists {
		return ErrDuplicateKey
	}

	cpy := *entry
	m.platformFees[entry.ID] = &cpy
	m.feeBySession[entry.SessionID] = &cpy
	return nil
}

func (m *MemoryRepository) GetPlatformFeeBySessionID(ctx context.Context, sessionID uuid.UUID) (*ledger.PlatformFeeLedgerEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.feeBySession[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *entry
	return &cpy, nil
}

func (m *MemoryRepository) ListPlatformFees(ctx context.Context, restaurantID uuid.UUID, period string) ([]ledger.PlatformFeeLedgerEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []ledger.PlatformFeeLedgerEntry
	for _, entry := range m.platformFees {
		if entry.RestaurantID == restaurantID {
			if period == "" || entry.BillingPeriod == period {
				res = append(res, *entry)
			}
		}
	}
	return res, nil
}

func (m *MemoryRepository) CreateRefundAdjustment(ctx context.Context, adj *ledger.RefundAdjustment) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := *adj
	m.refundAdjs[adj.ID] = &cpy
	return nil
}

func (m *MemoryRepository) CreateSettlement(ctx context.Context, s *ledger.RestaurantSettlement) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := *s
	m.settlements[s.ID] = &cpy
	return nil
}

func (m *MemoryRepository) ListSettlements(ctx context.Context, restaurantID uuid.UUID) ([]ledger.RestaurantSettlement, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []ledger.RestaurantSettlement
	for _, s := range m.settlements {
		if s.RestaurantID == restaurantID {
			res = append(res, *s)
		}
	}
	return res, nil
}

// ---------------- Restaurant Catalog & Config Methods ----------------

func (m *MemoryRepository) CreateRestaurant(ctx context.Context, r *restaurant.Restaurant) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := *r
	m.restaurants[r.ID] = &cpy
	return nil
}

func (m *MemoryRepository) GetRestaurantByID(ctx context.Context, id uuid.UUID) (*restaurant.Restaurant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.restaurants[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *r
	return &cpy, nil
}

func (m *MemoryRepository) ListRestaurants(ctx context.Context) ([]restaurant.Restaurant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []restaurant.Restaurant
	for _, r := range m.restaurants {
		res = append(res, *r)
	}
	return res, nil
}

func (m *MemoryRepository) UpdateRestaurant(ctx context.Context, r *restaurant.Restaurant) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	r.UpdatedAt = time.Now()
	cpy := *r
	m.restaurants[r.ID] = &cpy
	return nil
}

func (m *MemoryRepository) CreateTable(ctx context.Context, t *restaurant.Table) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := *t
	m.tables[t.ID] = &cpy
	m.tablesByToken[t.TableToken] = &cpy
	return nil
}

func (m *MemoryRepository) GetTableByID(ctx context.Context, id uuid.UUID) (*restaurant.Table, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, ok := m.tables[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *t
	return &cpy, nil
}

func (m *MemoryRepository) GetTableByToken(ctx context.Context, token string) (*restaurant.Table, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, ok := m.tablesByToken[token]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *t
	return &cpy, nil
}

func (m *MemoryRepository) ListTables(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.Table, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []restaurant.Table
	for _, t := range m.tables {
		if t.RestaurantID == restaurantID {
			res = append(res, *t)
		}
	}
	return res, nil
}

func (m *MemoryRepository) CreateStaff(ctx context.Context, s *restaurant.StaffUser) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.staffByEmail[s.Email]; exists {
		return ErrDuplicateKey
	}

	cpy := *s
	m.staff[s.ID] = &cpy
	m.staffByEmail[s.Email] = &cpy
	if s.EmployeeID != "" {
		m.staffByEmployeeID[s.RestaurantID.String()+":"+s.EmployeeID] = &cpy
	}
	return nil
}

func (m *MemoryRepository) GetStaffByID(ctx context.Context, id uuid.UUID) (*restaurant.StaffUser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.staff[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *s
	return &cpy, nil
}

func (m *MemoryRepository) GetStaffByEmail(ctx context.Context, email string) (*restaurant.StaffUser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.staffByEmail[email]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *s
	return &cpy, nil
}

func (m *MemoryRepository) GetStaffByEmployeeID(ctx context.Context, restaurantID uuid.UUID, employeeID string) (*restaurant.StaffUser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := restaurantID.String() + ":" + employeeID
	if s, ok := m.staffByEmployeeID[key]; ok {
		cpy := *s
		return &cpy, nil
	}
	for _, st := range m.staff {
		if st.RestaurantID == restaurantID && st.EmployeeID == employeeID {
			cpy := *st
			return &cpy, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryRepository) GetStaffByEmployeeIDGlobal(ctx context.Context, employeeID string) (*restaurant.StaffUser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	upper := strings.ToUpper(strings.TrimSpace(employeeID))
	for _, st := range m.staff {
		if strings.ToUpper(st.EmployeeID) == upper {
			cpy := *st
			return &cpy, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryRepository) seedDefaultData() {
	restID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	now := time.Now()

	// 1. Default Restaurant
	rest := &restaurant.Restaurant{
		ID:                   restID,
		Name:                 "The Spice Route",
		GSTIN:                "07AABCG1234F1Z5",
		CommissionRateBps:    100,
		SettlementBankDetails: "HDFC Bank • A/C 50200012345678 • IFSC HDFC0000128",
		Status:               restaurant.StatusActive,
		Timezone:             "Asia/Kolkata",
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	m.restaurants[restID] = rest

	// 2. Settings
	settings := restaurant.DefaultSettings(restID)
	m.settings[restID] = &settings

	// 3. Tables (12 tables with permanent tokens and demo tokens)
	for i := 1; i <= 12; i++ {
		tblID := uuid.New()
		tblNum := fmt.Sprintf("Table %d", i)
		token := fmt.Sprintf("TBL-%03d", i)
		t := &restaurant.Table{
			ID:           tblID,
			RestaurantID: restID,
			TableNumber:  tblNum,
			TableToken:   token,
			IsActive:     true,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		m.tables[tblID] = t
		m.tablesByToken[token] = t

		if i == 1 {
			m.tablesByToken["table-qr-token-spice-route-01"] = t
		} else if i == 2 {
			m.tablesByToken["table-qr-token-spice-route-02"] = t
		}
	}

	// 4. Menu Categories
	catStartersID := uuid.New()
	catMainID := uuid.New()
	catBreadsID := uuid.New()
	catDessertsID := uuid.New()

	m.categories[catStartersID] = &restaurant.MenuCategory{
		ID: catStartersID, RestaurantID: restID, Name: "Starters", DisplayOrder: 1, CreatedAt: now, UpdatedAt: now,
	}
	m.categories[catMainID] = &restaurant.MenuCategory{
		ID: catMainID, RestaurantID: restID, Name: "Main Course", DisplayOrder: 2, CreatedAt: now, UpdatedAt: now,
	}
	m.categories[catBreadsID] = &restaurant.MenuCategory{
		ID: catBreadsID, RestaurantID: restID, Name: "Breads", DisplayOrder: 3, CreatedAt: now, UpdatedAt: now,
	}
	m.categories[catDessertsID] = &restaurant.MenuCategory{
		ID: catDessertsID, RestaurantID: restID, Name: "Desserts", DisplayOrder: 4, CreatedAt: now, UpdatedAt: now,
	}

	// 5. Menu Items
	dishes := []struct {
		catID uuid.UUID
		name  string
		desc  string
		price int64
	}{
		{catStartersID, "Murgh Tikka Angara", "Charcoal grilled chicken skewers in hung curd marinade", 44000},
		{catStartersID, "Crispy Corn Kernels", "Spiced batter-fried golden sweet corn with lime zest", 26000},
		{catMainID, "Paneer Butter Masala", "Cottage cheese cubes in velvety rich spiced tomato butter gravy", 36000},
		{catMainID, "Dal Makhani", "Slow-cooked overnight black lentils infused with white churned butter", 28000},
		{catMainID, "Dum Biryani Awadhi", "Aromatic basmati rice cooked on dum with whole spices and saffron", 39000},
		{catBreadsID, "Garlic Butter Naan", "Clay oven flatbread infused with roasted garlic flakes and butter", 8000},
		{catBreadsID, "Laccha Paratha", "Multi-layered flaky whole wheat bread baked in tandoor", 7000},
		{catDessertsID, "Classic Mango Kulfi", "Slow-reduced milk ice cream flavored with Alphonso mango puree", 18000},
		{catDessertsID, "Gulab Jamun with Rabri", "Warm milk dumplings steeped in rose cardamom syrup with thickened rabri", 19000},
	}

	for _, d := range dishes {
		miID := uuid.New()
		m.menuItems[miID] = &restaurant.MenuItem{
			ID:           miID,
			RestaurantID: restID,
			CategoryID:   d.catID,
			Name:         d.name,
			Description:  d.desc,
			Price:        money.New(d.price),
			IsAvailable:  true,
			HSNSACCode:   "996331",
			CGSTRateBps:  250,
			SGSTRateBps:  250,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
	}

	// 6. Default Staff Roster with password 'password123'
	pwHashBytes, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	pwHash := string(pwHashBytes)

	staffMembers := []struct {
		empID string
		name  string
		email string
		role  restaurant.Role
	}{
		{"EMP-WTR-001", "Aman Verma", "aman.waiter@goldenspoon.com", restaurant.RoleWaiter},
		{"EMP-CHF-001", "Chef Rajesh", "rajesh.chef@goldenspoon.com", restaurant.RoleKitchen},
		{"EMP-KIT-001", "Chef Kitchen", "kitchen@goldenspoon.com", restaurant.RoleKitchen},
		{"EMP-CSH-001", "Sunil Grover", "sunil.cashier@goldenspoon.com", restaurant.RoleCashier},
		{"EMP-MGR-001", "Priya Nair", "priya.manager@goldenspoon.com", restaurant.RoleManager},
		{"EMP-ADM-001", "Vikram Malhotra", "owner@goldenspoon.com", restaurant.RoleRestaurantAdmin},
		{"EMP-GRD-001", "Ramesh Singh", "guard@goldenspoon.com", restaurant.RoleGuard},
	}

	for _, st := range staffMembers {
		sID := uuid.New()
		s := &restaurant.StaffUser{
			ID:           sID,
			RestaurantID: restID,
			EmployeeID:   st.empID,
			Name:         st.name,
			Phone:        "+91 98765 43210",
			Email:        st.email,
			PasswordHash: pwHash,
			Role:         st.role,
			IsActive:     true,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		m.staff[sID] = s
		m.staffByEmail[st.email] = s
		m.staffByEmployeeID[restID.String()+":"+st.empID] = s
	}

	// 7. Onboarding Complete
	m.onboarding[restID] = &restaurant.RestaurantOnboarding{
		RestaurantID:   restID,
		CurrentStep:    restaurant.StepGoLive,
		StepsCompleted: []restaurant.OnboardingStep{restaurant.StepProfileSetup, restaurant.StepTableSetup, restaurant.StepMenuSetup, restaurant.StepStaffSetup, restaurant.StepPaymentSetup, restaurant.StepPolicySetup, restaurant.StepTestOrder, restaurant.StepGoLive},
		StartedAt:      now,
		CompletedAt:    &now,
		UpdatedAt:      now,
	}
}

func (m *MemoryRepository) ListStaff(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.StaffUser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []restaurant.StaffUser
	for _, s := range m.staff {
		if s.RestaurantID == restaurantID {
			res = append(res, *s)
		}
	}
	return res, nil
}

func (m *MemoryRepository) CreateGuard(ctx context.Context, g *restaurant.GuardUser) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.guardsByPhone[g.Phone]; exists {
		return ErrDuplicateKey
	}

	cpy := *g
	m.guards[g.ID] = &cpy
	m.guardsByPhone[g.Phone] = &cpy
	return nil
}

func (m *MemoryRepository) GetGuardByID(ctx context.Context, id uuid.UUID) (*restaurant.GuardUser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	g, ok := m.guards[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *g
	return &cpy, nil
}

func (m *MemoryRepository) GetGuardByPhone(ctx context.Context, phone string) (*restaurant.GuardUser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	g, ok := m.guardsByPhone[phone]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *g
	return &cpy, nil
}

func (m *MemoryRepository) CreateCategory(ctx context.Context, c *restaurant.MenuCategory) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := *c
	m.categories[c.ID] = &cpy
	return nil
}

func (m *MemoryRepository) ListCategories(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.MenuCategory, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []restaurant.MenuCategory
	for _, c := range m.categories {
		if c.RestaurantID == restaurantID {
			res = append(res, *c)
		}
	}
	return res, nil
}

func (m *MemoryRepository) CreateMenuItem(ctx context.Context, mi *restaurant.MenuItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := *mi
	m.menuItems[mi.ID] = &cpy
	return nil
}

func (m *MemoryRepository) GetMenuItemByID(ctx context.Context, id uuid.UUID) (*restaurant.MenuItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mi, ok := m.menuItems[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *mi
	return &cpy, nil
}

func (m *MemoryRepository) ListMenuItems(ctx context.Context, restaurantID uuid.UUID) ([]restaurant.MenuItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []restaurant.MenuItem
	for _, mi := range m.menuItems {
		if mi.RestaurantID == restaurantID {
			res = append(res, *mi)
		}
	}
	return res, nil
}

func (m *MemoryRepository) UpdateMenuItemAvailability(ctx context.Context, id uuid.UUID, isAvailable bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mi, ok := m.menuItems[id]
	if !ok {
		return ErrNotFound
	}
	mi.IsAvailable = isAvailable
	mi.UpdatedAt = time.Now()
	return nil
}

func (m *MemoryRepository) GetSettings(ctx context.Context, restaurantID uuid.UUID) (*restaurant.RestaurantSettings, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.settings[restaurantID]
	if !ok {
		def := restaurant.DefaultSettings(restaurantID)
		return &def, nil
	}
	cpy := *s
	return &cpy, nil
}

func (m *MemoryRepository) UpdateSettings(ctx context.Context, s *restaurant.RestaurantSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s.UpdatedAt = time.Now()
	cpy := *s
	m.settings[s.RestaurantID] = &cpy
	return nil
}

func (m *MemoryRepository) GetOnboarding(ctx context.Context, restaurantID uuid.UUID) (*restaurant.RestaurantOnboarding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	o, ok := m.onboarding[restaurantID]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *o
	return &cpy, nil
}

func (m *MemoryRepository) UpdateOnboarding(ctx context.Context, o *restaurant.RestaurantOnboarding) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	o.UpdatedAt = time.Now()
	cpy := *o
	m.onboarding[o.RestaurantID] = &cpy
	return nil
}

// ---------------- Audit & Outbox Methods ----------------

func (m *MemoryRepository) AppendAuditLog(ctx context.Context, log *audit.AuditLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := *log
	if cpy.CreatedAt.IsZero() {
		cpy.CreatedAt = time.Now()
	}
	m.auditLogs = append(m.auditLogs, cpy)
	return nil
}

func (m *MemoryRepository) AppendStaffAction(ctx context.Context, action *audit.StaffAction) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := *action
	if cpy.CreatedAt.IsZero() {
		cpy.CreatedAt = time.Now()
	}
	m.staffActions = append(m.staffActions, cpy)
	return nil
}

func (m *MemoryRepository) ListAuditLogs(ctx context.Context, restaurantID uuid.UUID, limit, offset int) ([]audit.AuditLog, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var filtered []audit.AuditLog
	for _, l := range m.auditLogs {
		if l.RestaurantID == restaurantID {
			filtered = append(filtered, l)
		}
	}

	if offset >= len(filtered) {
		return []audit.AuditLog{}, nil
	}
	end := offset + limit
	if limit <= 0 || end > len(filtered) {
		end = len(filtered)
	}
	return filtered[offset:end], nil
}

func (m *MemoryRepository) ListStaffActions(ctx context.Context, restaurantID uuid.UUID, limit, offset int) ([]audit.StaffAction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var filtered []audit.StaffAction
	for _, a := range m.staffActions {
		if a.RestaurantID == restaurantID {
			filtered = append(filtered, a)
		}
	}

	if offset >= len(filtered) {
		return []audit.StaffAction{}, nil
	}
	end := offset + limit
	if limit <= 0 || end > len(filtered) {
		end = len(filtered)
	}
	return filtered[offset:end], nil
}

func (m *MemoryRepository) StoreOutboxEvent(ctx context.Context, event *storage.OutboxEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cpy := *event
	if cpy.CreatedAt.IsZero() {
		cpy.CreatedAt = time.Now()
	}
	m.outboxEvents[event.ID] = &cpy
	return nil
}

func (m *MemoryRepository) FetchPendingOutbox(ctx context.Context, batchSize int) ([]storage.OutboxEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []storage.OutboxEvent
	for _, e := range m.outboxEvents {
		if e.Status == storage.OutboxStatusPending {
			res = append(res, *e)
			if batchSize > 0 && len(res) >= batchSize {
				break
			}
		}
	}
	return res, nil
}

func (m *MemoryRepository) ClaimPendingOutbox(ctx context.Context, batchSize int, leaseDuration time.Duration) ([]storage.OutboxEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	var res []storage.OutboxEvent
	for _, e := range m.outboxEvents {
		isPendingAndReady := e.Status == storage.OutboxStatusPending && (e.NextRetryAt == nil || now.After(*e.NextRetryAt) || now.Equal(*e.NextRetryAt))
		isClaimExpired := e.Status == storage.OutboxStatusClaimed && e.ClaimLeaseExpiresAt != nil && now.After(*e.ClaimLeaseExpiresAt)

		if isPendingAndReady || isClaimExpired {
			e.Status = storage.OutboxStatusClaimed
			e.ClaimedAt = &now
			leaseExpires := now.Add(leaseDuration)
			e.ClaimLeaseExpiresAt = &leaseExpires

			res = append(res, *e)
			if batchSize > 0 && len(res) >= batchSize {
				break
			}
		}
	}
	return res, nil
}

func (m *MemoryRepository) MarkOutboxPublished(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.outboxEvents[id]
	if !ok {
		return ErrNotFound
	}
	now := time.Now()
	e.Status = storage.OutboxStatusPublished
	e.PublishedAt = &now
	e.ClaimLeaseExpiresAt = nil
	return nil
}

func (m *MemoryRepository) MarkOutboxFailed(ctx context.Context, id uuid.UUID, lastErr string, backoff time.Duration, maxRetries int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.outboxEvents[id]
	if !ok {
		return ErrNotFound
	}

	e.Retries++
	e.LastError = &lastErr
	e.ClaimLeaseExpiresAt = nil
	now := time.Now()

	if maxRetries > 0 && e.Retries >= maxRetries {
		e.Status = storage.OutboxStatusDeadLetter
	} else {
		e.Status = storage.OutboxStatusPending
		nextRetry := now.Add(backoff)
		e.NextRetryAt = &nextRetry
	}
	return nil
}

func (m *MemoryRepository) ReplayDeadLetterOutbox(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.outboxEvents[id]
	if !ok {
		return ErrNotFound
	}
	if e.Status != storage.OutboxStatusDeadLetter && e.Status != storage.OutboxStatusFailed {
		return fmt.Errorf("outbox event is not in dead letter or failed status: current %s", e.Status)
	}

	e.Status = storage.OutboxStatusPending
	e.Retries = 0
	e.NextRetryAt = nil
	e.LastError = nil
	e.ClaimLeaseExpiresAt = nil
	return nil
}

func (m *MemoryRepository) RecordWebhookEvent(ctx context.Context, gateway, eventID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s:%s", gateway, eventID)
	if _, exists := m.webhookEvents[key]; exists {
		return false, nil // duplicate webhook event
	}
	m.webhookEvents[key] = time.Now()
	return true, nil // newly recorded
}
