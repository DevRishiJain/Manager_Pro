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

type InventoryService struct {
	repo storage.Repository
}

func NewInventoryService(repo storage.Repository) *InventoryService {
	return &InventoryService{repo: repo}
}

type CreateInventoryItemInput struct {
	RestaurantID  uuid.UUID `json:"restaurant_id"`
	Name          string    `json:"name"`
	Category      string    `json:"category"`
	Unit          string    `json:"unit"`
	CurrentStock  float64   `json:"current_stock"`
	MinThreshold  float64   `json:"min_threshold"`
	UnitCostMinor int64     `json:"unit_cost_minor"`
}

type InventorySummary struct {
	Items              []inventory.InventoryItem `json:"items"`
	TotalItemsCount    int                      `json:"total_items_count"`
	LowStockItemsCount int                      `json:"low_stock_items_count"`
	TotalValuation     money.Money              `json:"total_valuation"`
}

func (s *InventoryService) CreateItem(ctx context.Context, input CreateInventoryItemInput) (*inventory.InventoryItem, error) {
	if input.RestaurantID == uuid.Nil {
		return nil, errors.New("restaurant_id is required")
	}
	if input.Name == "" {
		return nil, inventory.ErrEmptyItemName
	}
	if input.Unit == "" {
		input.Unit = "pcs"
	}

	item := &inventory.InventoryItem{
		ID:           uuid.New(),
		RestaurantID: input.RestaurantID,
		Name:         input.Name,
		Category:     input.Category,
		Unit:         input.Unit,
		CurrentStock: input.CurrentStock,
		MinThreshold: input.MinThreshold,
		UnitCost:     money.New(input.UnitCostMinor),
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	if err := s.repo.CreateInventoryItem(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *InventoryService) UpdateItem(ctx context.Context, item *inventory.InventoryItem) error {
	return s.repo.UpdateInventoryItem(ctx, item)
}

func (s *InventoryService) DeleteItem(ctx context.Context, restaurantID, id uuid.UUID) error {
	return s.repo.DeleteInventoryItem(ctx, restaurantID, id)
}

func (s *InventoryService) GetInventorySummary(ctx context.Context, restaurantID uuid.UUID) (*InventorySummary, error) {
	items, err := s.repo.ListInventoryItems(ctx, restaurantID)
	if err != nil {
		return nil, err
	}

	var lowStockCount int
	var totalValuationMinor int64

	for _, it := range items {
		if it.IsLowStock() {
			lowStockCount++
		}
		totalValuationMinor += int64(float64(it.UnitCost.AmountMinorUnits) * it.CurrentStock)
	}

	return &InventorySummary{
		Items:              items,
		TotalItemsCount:    len(items),
		LowStockItemsCount: lowStockCount,
		TotalValuation:     money.New(totalValuationMinor),
	}, nil
}

type LogStockInput struct {
	RestaurantID    uuid.UUID            `json:"restaurant_id"`
	InventoryItemID uuid.UUID            `json:"inventory_item_id"`
	ChangeType      inventory.ChangeType `json:"change_type"`
	Quantity        float64              `json:"quantity"`
	UnitCostMinor   *int64               `json:"unit_cost_minor,omitempty"`
	Reference       string               `json:"reference"`
}

func (s *InventoryService) LogStockMovement(ctx context.Context, input LogStockInput) (*inventory.InventoryLog, error) {
	if input.RestaurantID == uuid.Nil || input.InventoryItemID == uuid.Nil {
		return nil, errors.New("restaurant_id and inventory_item_id are required")
	}
	if input.Quantity == 0 {
		return nil, inventory.ErrInvalidStockQuantity
	}

	item, err := s.repo.GetInventoryItemByID(ctx, input.RestaurantID, input.InventoryItemID)
	if err != nil {
		return nil, err
	}

	unitCost := item.UnitCost
	if input.UnitCostMinor != nil && *input.UnitCostMinor > 0 {
		unitCost = money.New(*input.UnitCostMinor)
		// Optionally update unit cost on item if stock in
		if input.ChangeType == inventory.ChangeStockIn {
			item.UnitCost = unitCost
			_ = s.repo.UpdateInventoryItem(ctx, item)
		}
	}

	totalCost := money.New(int64(float64(unitCost.AmountMinorUnits) * input.Quantity))

	log := &inventory.InventoryLog{
		ID:              uuid.New(),
		RestaurantID:    input.RestaurantID,
		InventoryItemID: input.InventoryItemID,
		ItemName:        item.Name,
		Unit:            item.Unit,
		ChangeType:      input.ChangeType,
		Quantity:        input.Quantity,
		UnitCost:        unitCost,
		TotalCost:       totalCost,
		Reference:       input.Reference,
		LoggedAt:        time.Now().UTC(),
	}

	if input.ChangeType == inventory.ChangeWastage && totalCost.AmountMinorUnits > 0 {
		refText := input.Reference
		if refText == "" {
			refText = "Kitchen Spoilage / Wastage"
		}
		wastageExpense := &expense.Expense{
			ID:              uuid.New(),
			RestaurantID:    input.RestaurantID,
			Type:            expense.TypeVariable,
			Category:        expense.CategoryFoodWastage,
			Title:           "Wastage: " + item.Name,
			Amount:          totalCost,
			PaidVia:         expense.PaidViaInventoryWriteOff,
			VendorName:      "Internal Spoilage",
			ExpenseDate:     time.Now().UTC(),
			Notes:           refText,
			IsStockPurchase: false,
			InventoryLogID:  nil,
			CreatedAt:       time.Now().UTC(),
			UpdatedAt:       time.Now().UTC(),
		}
		if err := s.repo.CreateExpense(ctx, wastageExpense); err == nil {
			log.ExpenseID = &wastageExpense.ID
		}
	}

	if err := s.repo.CreateInventoryLog(ctx, log); err != nil {
		return nil, err
	}
	return log, nil
}

func (s *InventoryService) ListLogs(ctx context.Context, restaurantID uuid.UUID, itemID *uuid.UUID, limit int) ([]inventory.InventoryLog, error) {
	return s.repo.ListInventoryLogs(ctx, restaurantID, itemID, limit)
}

func (s *InventoryService) SaveRecipe(ctx context.Context, restaurantID, menuItemID uuid.UUID, ingredients []inventory.RecipeIngredient) error {
	return s.repo.SaveRecipeIngredients(ctx, restaurantID, menuItemID, ingredients)
}

func (s *InventoryService) GetRecipe(ctx context.Context, restaurantID, menuItemID uuid.UUID) ([]inventory.RecipeIngredient, error) {
	return s.repo.GetRecipeIngredientsByMenuItemID(ctx, restaurantID, menuItemID)
}

func (s *InventoryService) ListDishMargins(ctx context.Context, restaurantID uuid.UUID) ([]inventory.DishMargin, error) {
	return s.repo.ListDishMargins(ctx, restaurantID)
}
