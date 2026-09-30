package service

import (
	"context"
	"errors"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/expense"
	"github.com/devrishijain/table-manager/internal/domain/inventory"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/google/uuid"
)

type ExpenseService struct {
	repo storage.Repository
}

func NewExpenseService(repo storage.Repository) *ExpenseService {
	return &ExpenseService{repo: repo}
}

type CreateExpenseLineItemInput struct {
	InventoryItemID *uuid.UUID `json:"inventory_item_id,omitempty"`
	ItemName        string     `json:"item_name"`
	Quantity        float64    `json:"quantity"`
	Unit            string     `json:"unit"`
	UnitPriceMinor  int64      `json:"unit_price_minor"`
	TotalPriceMinor int64      `json:"total_price_minor"`
}

type CreateExpenseInput struct {
	RestaurantID     uuid.UUID                    `json:"restaurant_id"`
	Type             expense.ExpenseType          `json:"type"`
	Category         expense.ExpenseCategory      `json:"category"`
	Title            string                       `json:"title"`
	AmountMinor      int64                        `json:"amount_minor"`
	Currency         string                       `json:"currency"`
	PaidVia          string                       `json:"paid_via"`
	VendorName       string                       `json:"vendor_name"`
	ExpenseDate      time.Time                    `json:"expense_date"`
	Notes            string                       `json:"notes"`
	IsStockPurchase  bool                         `json:"is_stock_purchase"`
	InventoryLogID   *uuid.UUID                   `json:"inventory_log_id,omitempty"`
	LineItems        []CreateExpenseLineItemInput `json:"line_items,omitempty"`
	CreatedByStaffID *uuid.UUID                   `json:"created_by_staff_id,omitempty"`
}

func (s *ExpenseService) CreateExpense(ctx context.Context, input CreateExpenseInput) (*expense.Expense, error) {
	if input.RestaurantID == uuid.Nil {
		return nil, errors.New("restaurant_id is required")
	}
	if input.Title == "" {
		return nil, expense.ErrEmptyExpenseTitle
	}
	if input.AmountMinor <= 0 {
		return nil, expense.ErrInvalidExpenseAmount
	}
	if input.Currency == "" {
		input.Currency = "INR"
	}
	if input.PaidVia == "" {
		input.PaidVia = "CASH"
	}
	if input.ExpenseDate.IsZero() {
		input.ExpenseDate = time.Now().UTC()
	}

	expenseID := uuid.New()
	now := time.Now().UTC()

	var domainLineItems []expense.ExpenseLineItem
	for _, li := range input.LineItems {
		itemTotal := li.TotalPriceMinor
		unitPrice := li.UnitPriceMinor
		if unitPrice == 0 && li.Quantity > 0 {
			unitPrice = itemTotal / int64(li.Quantity)
		}
		if itemTotal == 0 && li.Quantity > 0 {
			itemTotal = int64(float64(unitPrice) * li.Quantity)
		}
		domainLineItems = append(domainLineItems, expense.ExpenseLineItem{
			ID:              uuid.New(),
			ExpenseID:       expenseID,
			InventoryItemID: li.InventoryItemID,
			ItemName:        li.ItemName,
			Quantity:        li.Quantity,
			Unit:            li.Unit,
			UnitPrice:       money.NewWithCurrency(unitPrice, input.Currency),
			TotalPrice:      money.NewWithCurrency(itemTotal, input.Currency),
			CreatedAt:       now,
		})
	}

	e := &expense.Expense{
		ID:               expenseID,
		RestaurantID:     input.RestaurantID,
		Type:             input.Type,
		Category:         input.Category,
		Title:            input.Title,
		Amount:           money.NewWithCurrency(input.AmountMinor, input.Currency),
		PaidVia:          input.PaidVia,
		VendorName:       input.VendorName,
		ExpenseDate:      input.ExpenseDate,
		Notes:            input.Notes,
		IsStockPurchase:  input.IsStockPurchase || len(domainLineItems) > 0,
		InventoryLogID:   input.InventoryLogID,
		LineItems:        domainLineItems,
		CreatedByStaffID: input.CreatedByStaffID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := s.repo.CreateExpense(ctx, e); err != nil {
		return nil, err
	}

	// Auto stock-in for linked inventory items
	for _, li := range domainLineItems {
		if li.InventoryItemID != nil && *li.InventoryItemID != uuid.Nil && li.Quantity > 0 {
			invLog := &inventory.InventoryLog{
				ID:              uuid.New(),
				RestaurantID:    e.RestaurantID,
				InventoryItemID: *li.InventoryItemID,
				ChangeType:      inventory.ChangeStockIn,
				Quantity:        li.Quantity,
				UnitCost:        li.UnitPrice,
				TotalCost:       li.TotalPrice,
				Reference:       "Expense: " + e.Title,
				ExpenseID:       &e.ID,
				LoggedAt:        now,
			}
			_ = s.repo.CreateInventoryLog(ctx, invLog)
		}
	}

	return e, nil
}

func (s *ExpenseService) ListExpenses(ctx context.Context, restaurantID uuid.UUID, expType *expense.ExpenseType, category *expense.ExpenseCategory, startDate, endDate *time.Time) ([]expense.Expense, error) {
	return s.repo.ListExpenses(ctx, restaurantID, expType, category, startDate, endDate)
}

func (s *ExpenseService) ListExpenseLineItems(ctx context.Context, expenseID uuid.UUID) ([]expense.ExpenseLineItem, error) {
	return s.repo.ListExpenseLineItems(ctx, expenseID)
}

func (s *ExpenseService) DeleteExpense(ctx context.Context, restaurantID, expenseID uuid.UUID) error {
	return s.repo.DeleteExpense(ctx, restaurantID, expenseID)
}
