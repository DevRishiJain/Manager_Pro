package inventory

import (
	"errors"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/google/uuid"
)

var (
	ErrInvalidStockQuantity = errors.New("stock quantity cannot be zero")
	ErrEmptyItemName        = errors.New("inventory item name cannot be empty")
	ErrItemNotFound         = errors.New("inventory item not found")
)

type ChangeType string

const (
	ChangeStockIn          ChangeType = "STOCK_IN"
	ChangeWastage          ChangeType = "WASTAGE"
	ChangeOrderConsumption ChangeType = "ORDER_CONSUMPTION"
	ChangeAdjustment       ChangeType = "ADJUSTMENT"
)

type InventoryItem struct {
	ID           uuid.UUID   `json:"id"`
	RestaurantID uuid.UUID   `json:"restaurant_id"`
	Name         string      `json:"name"`
	Category     string      `json:"category"`
	Unit         string      `json:"unit"`
	CurrentStock float64     `json:"current_stock"`
	MinThreshold float64     `json:"min_threshold"`
	UnitCost     money.Money `json:"unit_cost"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

func (i InventoryItem) IsLowStock() bool {
	return i.MinThreshold > 0 && i.CurrentStock <= i.MinThreshold
}

type InventoryLog struct {
	ID              uuid.UUID   `json:"id"`
	RestaurantID    uuid.UUID   `json:"restaurant_id"`
	InventoryItemID uuid.UUID   `json:"inventory_item_id"`
	ItemName        string      `json:"item_name,omitempty"`
	Unit            string      `json:"unit,omitempty"`
	ChangeType      ChangeType  `json:"change_type"`
	Quantity        float64     `json:"quantity"`
	UnitCost        money.Money `json:"unit_cost"`
	TotalCost       money.Money `json:"total_cost"`
	Reference       string      `json:"reference"`
	ExpenseID       *uuid.UUID  `json:"expense_id,omitempty"`
	OrderID         *uuid.UUID  `json:"order_id,omitempty"`
	LoggedAt        time.Time   `json:"logged_at"`
}

type RecipeIngredient struct {
	ID               uuid.UUID   `json:"id"`
	RestaurantID     uuid.UUID   `json:"restaurant_id"`
	MenuItemID       uuid.UUID   `json:"menu_item_id"`
	InventoryItemID  uuid.UUID   `json:"inventory_item_id"`
	ItemName         string      `json:"item_name,omitempty"`
	Unit             string      `json:"unit,omitempty"`
	QuantityRequired float64     `json:"quantity_required"`
	UnitCost         money.Money `json:"unit_cost,omitempty"`
	CostContribution money.Money `json:"cost_contribution,omitempty"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

type DishMargin struct {
	MenuItemID   uuid.UUID   `json:"menu_item_id"`
	MenuItemName string      `json:"menu_item_name"`
	CategoryName string      `json:"category_name"`
	SellingPrice money.Money `json:"selling_price"`
	CostPrice    money.Money `json:"cost_price"`
	GrossProfit  money.Money `json:"gross_profit"`
	MarginPct    float64     `json:"margin_pct"`
}

type OrderIngredientRequirement struct {
	InventoryItemID uuid.UUID `json:"inventory_item_id"`
	Quantity        float64   `json:"quantity"`
}
