package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/audit"
	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/devrishijain/table-manager/internal/domain/expense"
	"github.com/devrishijain/table-manager/internal/domain/inventory"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/risk"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/google/uuid"
)

var (
	ErrEmptyCart            = errors.New("cart cannot be empty")
	ErrRiskBlocked          = errors.New("order blocked by risk evaluation rules")
	ErrOrderNotFound        = errors.New("order not found")
	ErrOrderCannotBeMutated = errors.New("order status cannot be updated in its current state")
)

type OrderService struct {
	repo storage.Repository
}

func NewOrderService(repo storage.Repository) *OrderService {
	return &OrderService{repo: repo}
}

// PlaceOrder converts ephemeral CartItems into an immutable Order with full price/tax snapshots.
func (s *OrderService) PlaceOrder(ctx context.Context, sessionID uuid.UUID, cartItems []order.CartItem) (*order.Order, *string, error) {
	if len(cartItems) == 0 {
		return nil, nil, ErrEmptyCart
	}

	sess, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, nil, ErrSessionNotFound
	}
	if sess.Status.IsTerminal() {
		return nil, nil, session.ErrSessionClosed
	}

	// If session was AWAITING_PAYMENT, reopening it to OPEN_VERIFIED
	if sess.Status == session.StateAwaitingPayment {
		sess.Status = session.StateOpenVerified
		sess.LastActivityAt = time.Now()
		_ = s.repo.UpdateSession(ctx, sess)
	}

	settings, err := s.repo.GetSettings(ctx, sess.RestaurantID)
	if err != nil {
		return nil, nil, err
	}

	now := time.Now()
	orderID := uuid.New()
	var orderItems []order.OrderItem
	var subtotalMinor int64
	var taxTotalMinor int64

	// Fetch current menu catalog & snapshot prices
	for _, item := range cartItems {
		if item.Quantity <= 0 {
			return nil, nil, errors.New("item quantity must be greater than 0")
		}

		menuItem, err := s.repo.GetMenuItemByID(ctx, item.MenuItemID)
		if err != nil {
			return nil, nil, fmt.Errorf("item %s not found: %w", item.MenuItemID, err)
		}
		if !menuItem.IsAvailable {
			return nil, nil, fmt.Errorf("%w: %s (%s)", order.ErrItemOutOfStock, menuItem.Name, menuItem.ID)
		}

		unitPrice := menuItem.Price
		// Calculate line totals
		lineTotalMinor := unitPrice.AmountMinorUnits * int64(item.Quantity)
		lineMoney := money.New(lineTotalMinor)

		// CGST & SGST calculation using Round-Half-Up
		cgstMoney, _ := lineMoney.MultiplyFractionRoundHalfUp(menuItem.CGSTRateBps, 10000)
		sgstMoney, _ := lineMoney.MultiplyFractionRoundHalfUp(menuItem.SGSTRateBps, 10000)

		subtotalMinor += lineTotalMinor
		taxTotalMinor += cgstMoney.AmountMinorUnits + sgstMoney.AmountMinorUnits

		hsnCode := menuItem.HSNSACCode
		if hsnCode == "" {
			hsnCode = "996331" // default GST SAC for restaurant services
		}

		orderItems = append(orderItems, order.OrderItem{
			ID:                  uuid.New(),
			OrderID:             orderID,
			MenuItemID:          menuItem.ID,
			VariantID:           item.VariantID,
			ItemNameSnapshot:    menuItem.Name,
			Quantity:            item.Quantity,
			UnitPriceSnapshot:   unitPrice,
			LineTotal:           lineMoney,
			HSNSACCodeSnapshot:  hsnCode,
			CGSTRateBpsSnapshot: menuItem.CGSTRateBps,
			SGSTRateBpsSnapshot: menuItem.SGSTRateBps,
			CGSTAmount:          cgstMoney,
			SGSTAmount:          sgstMoney,
			SpecialInstructions: item.SpecialInstructions,
			CreatedAt:           now,
		})
	}

	totalMoney := money.New(subtotalMinor + taxTotalMinor)

	// Fetch prior orders to check sequence and risk scoring
	priorOrders, _ := s.repo.GetOrdersBySessionID(ctx, sessionID)
	sequenceNum := len(priorOrders) + 1

	var firstOrderTotal money.Money
	if len(priorOrders) > 0 {
		firstOrderTotal = priorOrders[0].Total
	}

	// Evaluate Risk Scoring Engine
	riskContext := risk.EvaluationContext{
		CurrentRunningTotal: sess.RunningTotal,
		NewOrderTotal:       totalMoney,
		FirstOrderTotal:     firstOrderTotal,
		OrderCountLast5Min:  len(priorOrders),
		Settings:            *settings,
	}
	riskTier := risk.Evaluate(riskContext)
	if riskTier == risk.TierBlocked {
		return nil, nil, ErrRiskBlocked
	}

	var rawFirstOTP *string
	var initialOrderStatus order.State

	hasPriorAccepted := false
	for _, po := range priorOrders {
		if po.Status != order.StatePlacedUnverified && po.Status != order.StateCancelled {
			hasPriorAccepted = true
			break
		}
	}

	if sess.Status == session.StateOpen && !hasPriorAccepted {
		// First order in unverified session: requires staff verification OTP
		initialOrderStatus = order.StatePlacedUnverified
		otp, err := exitpass.GenerateNumericOTP(4)
		if err != nil {
			return nil, nil, err
		}
		rawFirstOTP = &otp

		// Save first-order verification OTP
		existingEp, err := s.repo.GetExitPassBySessionID(ctx, sess.ID)
		if err == nil && existingEp != nil {
			// Keep existing OTP permanently for this dining session
			if existingEp.RawOTP == "" {
				existingEp.RawOTP = otp
				existingEp.OTPHash = exitpass.HashOTP(otp)
			}
			existingEp.ExpiresAt = now.Add(4 * time.Hour)
			rawFirstOTP = &existingEp.RawOTP
			existingEp.Status = exitpass.StateIssued
			existingEp.UpdatedAt = now
			_ = s.repo.UpdateExitPass(ctx, existingEp)
		} else {
			ep := &exitpass.ExitPass{
				ID:               uuid.New(),
				SessionID:        sess.ID,
				RestaurantID:     sess.RestaurantID,
				RawOTP:           otp,
				OTPHash:          exitpass.HashOTP(otp),
				IssuedAt:         now,
				ExpiresAt:        now.Add(4 * time.Hour),
				Status:           exitpass.StateIssued,
				RequiresOverride: false,
				CreatedAt:        now,
				UpdatedAt:        now,
			}
			_ = s.repo.CreateExitPass(ctx, ep)
		}
	} else {
		// Table order was previously accepted or session already verified:
		// Places order directly into kitchen queue as PLACED_VERIFIED without waiter re-approval!
		initialOrderStatus = order.StatePlacedVerified
	}

	tableNumber := "Table 1"
	if tbl, err := s.repo.GetTableByID(ctx, sess.TableID); err == nil && tbl != nil && tbl.TableNumber != "" {
		tableNumber = tbl.TableNumber
	}
	customerName := sess.CustomerName
	if customerName == "" {
		customerName = "Guest Diner"
	}
	customerPhone := sess.CustomerPhone
	guestCount := sess.GuestCount
	if guestCount <= 0 {
		guestCount = 1
	}
	vehicleNumber := strings.TrimSpace(sess.VehicleNumber)
	if vehicleNumber != "" {
		tableNumber = "Car " + strings.ToUpper(vehicleNumber)
	}

	newOrder := &order.Order{
		ID:                       orderID,
		SessionID:                sessionID,
		RestaurantID:             sess.RestaurantID,
		SequenceNumber:           sequenceNum,
		TableNumber:              tableNumber,
		CustomerName:             customerName,
		CustomerPhone:            customerPhone,
		GuestCount:               guestCount,
		VehicleNumber:            vehicleNumber,
		Status:                   initialOrderStatus,
		PlacedAt:                 now,
		Subtotal:                 money.New(subtotalMinor),
		TaxTotal:                 money.New(taxTotalMinor),
		Total:                    totalMoney,
		CancellationFeeApplicable: false,
		Version:                  1,
		CreatedAt:                now,
		UpdatedAt:                now,
	}

	if err := s.repo.CreateOrder(ctx, newOrder, orderItems); err != nil {
		return nil, nil, err
	}

	// Update session running total
	sess.RunningTotal = sess.RunningTotal.MustAdd(totalMoney)
	sess.LastActivityAt = now
	_ = s.repo.UpdateSession(ctx, sess)

	// Outbox & Audit
	orderBytes, _ := json.Marshal(newOrder)
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           uuid.New(),
		ActorType:    audit.ActorTypeCustomer,
		ActorID:      sess.SessionToken,
		RestaurantID: sess.RestaurantID,
		SessionID:    &sess.ID,
		Action:       "ORDER_PLACED",
		AfterState:   orderBytes,
		CreatedAt:    now,
	})

	_ = s.repo.StoreOutboxEvent(ctx, &storage.OutboxEvent{
		ID:           uuid.New(),
		RestaurantID: sess.RestaurantID,
		EventType:    "ORDER_PLACED",
		AggregateID:  newOrder.ID.String(),
		Payload:      orderBytes,
		Status:       storage.OutboxStatusPending,
		CreatedAt:    now,
	})

	newOrder.Items = orderItems
	return newOrder, rawFirstOTP, nil
}

// AcceptOrder is called by staff to accept an order into the kitchen.
func (s *OrderService) AcceptOrder(ctx context.Context, orderID, staffID uuid.UUID) (*order.Order, error) {
	ord, err := s.repo.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, ErrOrderNotFound
	}

	if err := order.ValidateTransition(ord.Status, order.StateAccepted); err != nil {
		return nil, err
	}

	now := time.Now()
	ord.Status = order.StateAccepted
	ord.AcceptedAt = &now
	if staffID != uuid.Nil {
		ord.AcceptedByStaffID = &staffID
	}
	ord.UpdatedAt = now
	if err := s.repo.UpdateOrder(ctx, ord); err != nil {
		return nil, err
	}

	// Auto-verify session if it was still open
	sess, err := s.repo.GetSessionByID(ctx, ord.SessionID)
	if err == nil && sess.Status == session.StateOpen {
		sess.Status = session.StateOpenVerified
		sess.VerifiedAt = &now
		if staffID != uuid.Nil {
			sess.VerifiedByStaffID = &staffID
		}
		sess.UpdatedAt = now
		_ = s.repo.UpdateSession(ctx, sess)
	}

	orderBytes, _ := json.Marshal(ord)
	auditID := uuid.New()
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           auditID,
		ActorType:    audit.ActorTypeStaff,
		ActorID:      staffID.String(),
		RestaurantID: ord.RestaurantID,
		SessionID:    &ord.SessionID,
		Action:       "ORDER_ACCEPTED",
		AfterState:   orderBytes,
		CreatedAt:    now,
	})

	_ = s.repo.AppendStaffAction(ctx, &audit.StaffAction{
		ID:           uuid.New(),
		AuditLogID:   auditID,
		StaffID:      staffID,
		RestaurantID: ord.RestaurantID,
		SessionID:    &ord.SessionID,
		ActionType:   "ORDER_ACCEPTED",
		Reason:       fmt.Sprintf("Order #%d accepted by waiter", ord.SequenceNumber),
		Metadata:     orderBytes,
		CreatedAt:    now,
	})

	_ = s.repo.StoreOutboxEvent(ctx, &storage.OutboxEvent{
		ID:           uuid.New(),
		RestaurantID: ord.RestaurantID,
		EventType:    "ORDER_ACCEPTED",
		AggregateID:  ord.ID.String(),
		Payload:      orderBytes,
		Status:       storage.OutboxStatusPending,
		CreatedAt:    now,
	})

	s.depleteOrderIngredients(ctx, ord)

	return ord, nil
}

// UpdateOrderStatus transitions order through kitchen stages (PREPARING, READY, SERVED)
func (s *OrderService) UpdateOrderStatus(ctx context.Context, orderID uuid.UUID, targetState order.State, staffID uuid.UUID) (*order.Order, error) {
	ord, err := s.repo.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, ErrOrderNotFound
	}

	if err := order.ValidateTransition(ord.Status, targetState); err != nil {
		return nil, err
	}

	now := time.Now()
	ord.Status = targetState
	ord.UpdatedAt = now
	if err := s.repo.UpdateOrder(ctx, ord); err != nil {
		return nil, err
	}

	if targetState == order.StateAccepted || targetState == order.StatePreparing {
		s.depleteOrderIngredients(ctx, ord)
	}

	orderBytes, _ := json.Marshal(ord)
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           uuid.New(),
		ActorType:    audit.ActorTypeStaff,
		ActorID:      staffID.String(),
		RestaurantID: ord.RestaurantID,
		SessionID:    &ord.SessionID,
		Action:       fmt.Sprintf("ORDER_STATUS_%s", targetState),
		AfterState:   orderBytes,
		CreatedAt:    now,
	})

	return ord, nil
}

// CancelOrder handles cancellation across all 3 stages.
func (s *OrderService) CancelOrder(ctx context.Context, orderID, actorID uuid.UUID, actorType audit.ActorType, reason string) (*order.Order, error) {
	ord, err := s.repo.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, ErrOrderNotFound
	}

	if err := order.ValidateTransition(ord.Status, order.StateCancelled); err != nil {
		return nil, err
	}

	now := time.Now()
	var stage order.CancellationStage
	switch ord.Status {
	case order.StatePlacedUnverified, order.StatePlacedVerified:
		stage = order.CancellationPreAcceptance
	case order.StateAccepted:
		stage = order.CancellationPostAcceptancePrePrep
	case order.StatePreparing, order.StateReady:
		stage = order.CancellationPostPrepStart
	default:
		return nil, ErrOrderCannotBeMutated
	}

	ord.Status = order.StateCancelled
	ord.CancelledAt = &now
	ord.CancellationStage = &stage

	if err := s.repo.UpdateOrder(ctx, ord); err != nil {
		return nil, err
	}

	// Roll back session running total for PRE_ACCEPTANCE and POST_ACCEPTANCE_PRE_PREP
	// For POST_PREP_START: platform fee still applies so GMV is retained!
	if stage != order.CancellationPostPrepStart {
		sess, err := s.repo.GetSessionByID(ctx, ord.SessionID)
		if err == nil {
			sess.RunningTotal, _ = sess.RunningTotal.Sub(ord.Total)
			_ = s.repo.UpdateSession(ctx, sess)
		}
		s.restoreOrderIngredients(ctx, ord)
	} else {
		// POST_PREP_START: Active cooking or prepared food was force-closed/cancelled!
		// Automatically log as food wastage in the financial expense ledger
		wastageReason := reason
		if wastageReason == "" {
			wastageReason = "Kitchen food wastage: order cancelled during cooking or after preparation"
		}
		if ord.Total.AmountMinorUnits > 0 {
			wastageExpense := &expense.Expense{
				ID:              uuid.New(),
				RestaurantID:    ord.RestaurantID,
				Type:            expense.TypeVariable,
				Category:        expense.CategoryFoodWastage,
				Title:           fmt.Sprintf("Wastage: Order #%d (Force-closed)", ord.SequenceNumber),
				Amount:          ord.Total,
				PaidVia:         expense.PaidViaInventoryWriteOff,
				VendorName:      "Kitchen Wastage",
				ExpenseDate:     now,
				Notes:           wastageReason,
				IsStockPurchase: false,
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			_ = s.repo.CreateExpense(ctx, wastageExpense)
		}
	}

	orderBytes, _ := json.Marshal(ord)
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           uuid.New(),
		ActorType:    actorType,
		ActorID:      actorID.String(),
		RestaurantID: ord.RestaurantID,
		SessionID:    &ord.SessionID,
		Action:       "ORDER_CANCELLED",
		AfterState:   orderBytes,
		Metadata:     fmt.Appendf(nil, `{"reason":"%s","stage":"%s"}`, reason, stage),
		CreatedAt:    now,
	})

	_ = s.repo.StoreOutboxEvent(ctx, &storage.OutboxEvent{
		ID:           uuid.New(),
		RestaurantID: ord.RestaurantID,
		EventType:    "ORDER_CANCELLED",
		AggregateID:  ord.ID.String(),
		Payload:      orderBytes,
		Status:       storage.OutboxStatusPending,
		CreatedAt:    now,
	})

	return ord, nil
}

func (s *OrderService) depleteOrderIngredients(ctx context.Context, ord *order.Order) {
	if ord == nil {
		return
	}
	existingLogs, err := s.repo.ListInventoryLogs(ctx, ord.RestaurantID, nil, 100)
	if err == nil {
		for _, l := range existingLogs {
			if l.OrderID != nil && *l.OrderID == ord.ID && l.ChangeType == inventory.ChangeOrderConsumption {
				return // already depleted
			}
		}
	}

	reqs, err := s.repo.ListRecipeIngredientsForOrder(ctx, ord.ID)
	if err != nil || len(reqs) == 0 {
		return
	}

	now := time.Now().UTC()
	for _, req := range reqs {
		if req.Quantity <= 0 {
			continue
		}
		invLog := &inventory.InventoryLog{
			ID:              uuid.New(),
			RestaurantID:    ord.RestaurantID,
			InventoryItemID: req.InventoryItemID,
			ChangeType:      inventory.ChangeOrderConsumption,
			Quantity:        req.Quantity,
			Reference:       fmt.Sprintf("Order #%d Table %s", ord.SequenceNumber, ord.TableNumber),
			OrderID:         &ord.ID,
			LoggedAt:        now,
		}
		_ = s.repo.CreateInventoryLog(ctx, invLog)
	}
}

func (s *OrderService) restoreOrderIngredients(ctx context.Context, ord *order.Order) {
	if ord == nil {
		return
	}
	existingLogs, err := s.repo.ListInventoryLogs(ctx, ord.RestaurantID, nil, 100)
	if err != nil {
		return
	}
	wasDepleted := false
	for _, l := range existingLogs {
		if l.OrderID != nil && *l.OrderID == ord.ID && l.ChangeType == inventory.ChangeOrderConsumption {
			wasDepleted = true
			break
		}
	}
	if !wasDepleted {
		return
	}

	reqs, err := s.repo.ListRecipeIngredientsForOrder(ctx, ord.ID)
	if err != nil || len(reqs) == 0 {
		return
	}

	now := time.Now().UTC()
	for _, req := range reqs {
		if req.Quantity <= 0 {
			continue
		}
		invLog := &inventory.InventoryLog{
			ID:              uuid.New(),
			RestaurantID:    ord.RestaurantID,
			InventoryItemID: req.InventoryItemID,
			ChangeType:      inventory.ChangeStockIn,
			Quantity:        req.Quantity,
			Reference:       fmt.Sprintf("Restored from cancelled Order #%d", ord.SequenceNumber),
			OrderID:         &ord.ID,
			LoggedAt:        now,
		}
		_ = s.repo.CreateInventoryLog(ctx, invLog)
	}
}

func (s *OrderService) GetOrdersBySessionID(ctx context.Context, sessionID uuid.UUID) ([]order.Order, error) {
	return s.repo.GetOrdersBySessionID(ctx, sessionID)
}

func (s *OrderService) ListKitchenQueue(ctx context.Context, restaurantID uuid.UUID) ([]order.Order, error) {
	statuses := []order.State{order.StatePlacedVerified, order.StateAccepted, order.StatePreparing, order.StateReady, order.StateServed}
	return s.repo.ListKitchenQueue(ctx, restaurantID, statuses)
}

func (s *OrderService) ListPendingOrders(ctx context.Context, restaurantID uuid.UUID) ([]order.Order, error) {
	return s.repo.ListPendingOrders(ctx, restaurantID)
}

func (s *OrderService) ListOrders(ctx context.Context, restaurantID uuid.UUID, limit int, startDate, endDate *time.Time) ([]order.Order, error) {
	return s.repo.ListOrders(ctx, restaurantID, limit, startDate, endDate)
}

