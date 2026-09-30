package expense

import (
	"errors"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/google/uuid"
)

var (
	ErrInvalidExpenseAmount = errors.New("expense amount must be greater than zero")
	ErrEmptyExpenseTitle    = errors.New("expense title cannot be empty")
)

type ExpenseType string

const (
	TypeVariable ExpenseType = "VARIABLE"
	TypeFixed    ExpenseType = "FIXED"
)

type ExpenseCategory string

const (
	CategoryVegetables    ExpenseCategory = "VEGETABLES"
	CategoryMeatPoultry   ExpenseCategory = "MEAT_POULTRY"
	CategoryDairy         ExpenseCategory = "DAIRY"
	CategoryGrocerySpices ExpenseCategory = "GROCERY_SPICES"
	CategoryPackaging     ExpenseCategory = "PACKAGING"
	CategoryGasUtility    ExpenseCategory = "GAS_UTILITY"
	CategorySalary        ExpenseCategory = "SALARY"
	CategoryRent          ExpenseCategory = "RENT"
	CategoryElectricity   ExpenseCategory = "ELECTRICITY"
	CategoryMaintenance   ExpenseCategory = "MAINTENANCE"
	CategoryFoodWastage   ExpenseCategory = "FOOD_WASTAGE"
	CategoryOther         ExpenseCategory = "OTHER"
)

const (
	PaidViaCash                = "CASH"
	PaidViaUPI                 = "UPI"
	PaidViaBankTransfer        = "BANK_TRANSFER"
	PaidViaCheque              = "CHEQUE"
	PaidViaCredit              = "CREDIT"
	PaidViaInventoryWriteOff   = "INVENTORY_WRITE_OFF"
)

type ExpenseLineItem struct {
	ID              uuid.UUID   `json:"id"`
	ExpenseID       uuid.UUID   `json:"expense_id"`
	InventoryItemID *uuid.UUID  `json:"inventory_item_id,omitempty"`
	ItemName        string      `json:"item_name"`
	Quantity        float64     `json:"quantity"`
	Unit            string      `json:"unit"`
	UnitPrice       money.Money `json:"unit_price"`
	TotalPrice      money.Money `json:"total_price"`
	CreatedAt       time.Time   `json:"created_at"`
}

type Expense struct {
	ID               uuid.UUID         `json:"id"`
	RestaurantID     uuid.UUID         `json:"restaurant_id"`
	Type             ExpenseType       `json:"type"`
	Category         ExpenseCategory   `json:"category"`
	Title            string            `json:"title"`
	Amount           money.Money       `json:"amount"`
	PaidVia          string            `json:"paid_via"`
	VendorName       string            `json:"vendor_name"`
	ExpenseDate      time.Time         `json:"expense_date"`
	Notes            string            `json:"notes"`
	IsStockPurchase  bool              `json:"is_stock_purchase"`
	InventoryLogID   *uuid.UUID        `json:"inventory_log_id,omitempty"`
	LineItems        []ExpenseLineItem `json:"line_items,omitempty"`
	CreatedByStaffID *uuid.UUID        `json:"created_by_staff_id,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}
