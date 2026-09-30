package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/devrishijain/table-manager/internal/api/middleware"
	"github.com/devrishijain/table-manager/internal/domain/inventory"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (h *APIHandler) ListInventory(w http.ResponseWriter, r *http.Request) {
	var restaurantID uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
		restaurantID = claims.RestaurantID
	}
	if restaurantID == uuid.Nil {
		if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
			restaurantID, _ = uuid.Parse(restParam)
		}
	}
	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required")
		return
	}

	summary, err := h.getInventoryService().GetInventorySummary(r.Context(), restaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, summary)
}

type CreateInventoryItemRequest struct {
	Name          string  `json:"name"`
	Category      string  `json:"category"`
	Unit          string  `json:"unit"`
	CurrentStock  float64 `json:"current_stock"`
	MinThreshold  float64 `json:"min_threshold"`
	UnitCostMinor int64   `json:"unit_cost_minor"`
}

func (h *APIHandler) CreateInventoryItem(w http.ResponseWriter, r *http.Request) {
	var restaurantID uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
		restaurantID = claims.RestaurantID
	}
	if restaurantID == uuid.Nil {
		if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
			restaurantID, _ = uuid.Parse(restParam)
		}
	}
	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required")
		return
	}

	var req CreateInventoryItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	item, err := h.getInventoryService().CreateItem(r.Context(), service.CreateInventoryItemInput{
		RestaurantID:  restaurantID,
		Name:          req.Name,
		Category:      req.Category,
		Unit:          req.Unit,
		CurrentStock:  req.CurrentStock,
		MinThreshold:  req.MinThreshold,
		UnitCostMinor: req.UnitCostMinor,
	})
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusCreated, item)
}

func (h *APIHandler) UpdateInventoryItem(w http.ResponseWriter, r *http.Request) {
	var restaurantID uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
		restaurantID = claims.RestaurantID
	}
	if restaurantID == uuid.Nil {
		if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
			restaurantID, _ = uuid.Parse(restParam)
		}
	}
	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required")
		return
	}

	itemIDStr := chi.URLParam(r, "id")
	itemID, err := uuid.Parse(itemIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid item id")
		return
	}

	var req CreateInventoryItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	item := &inventory.InventoryItem{
		ID:           itemID,
		RestaurantID: restaurantID,
		Name:         req.Name,
		Category:     req.Category,
		Unit:         req.Unit,
		CurrentStock: req.CurrentStock,
		MinThreshold: req.MinThreshold,
		UnitCost:     money.New(req.UnitCostMinor),
	}

	if err := h.getInventoryService().UpdateItem(r.Context(), item); err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, item)
}

func (h *APIHandler) DeleteInventoryItem(w http.ResponseWriter, r *http.Request) {
	var restaurantID uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
		restaurantID = claims.RestaurantID
	}
	if restaurantID == uuid.Nil {
		if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
			restaurantID, _ = uuid.Parse(restParam)
		}
	}
	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required")
		return
	}

	itemIDStr := chi.URLParam(r, "id")
	itemID, err := uuid.Parse(itemIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid item id")
		return
	}

	if err := h.getInventoryService().DeleteItem(r.Context(), restaurantID, itemID); err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
}

type LogStockRequest struct {
	ChangeType    inventory.ChangeType `json:"change_type"`
	Quantity      float64              `json:"quantity"`
	UnitCostMinor *int64               `json:"unit_cost_minor,omitempty"`
	Reference     string               `json:"reference"`
}

func (h *APIHandler) LogStockMovement(w http.ResponseWriter, r *http.Request) {
	var restaurantID uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
		restaurantID = claims.RestaurantID
	}
	if restaurantID == uuid.Nil {
		if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
			restaurantID, _ = uuid.Parse(restParam)
		}
	}
	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required")
		return
	}

	itemIDStr := chi.URLParam(r, "id")
	itemID, err := uuid.Parse(itemIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid item id")
		return
	}

	var req LogStockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	log, err := h.getInventoryService().LogStockMovement(r.Context(), service.LogStockInput{
		RestaurantID:    restaurantID,
		InventoryItemID: itemID,
		ChangeType:      req.ChangeType,
		Quantity:        req.Quantity,
		UnitCostMinor:   req.UnitCostMinor,
		Reference:       req.Reference,
	})
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusCreated, log)
}

func (h *APIHandler) ListInventoryLogs(w http.ResponseWriter, r *http.Request) {
	var restaurantID uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
		restaurantID = claims.RestaurantID
	}
	if restaurantID == uuid.Nil {
		if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
			restaurantID, _ = uuid.Parse(restParam)
		}
	}
	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required")
		return
	}

	var itemID *uuid.UUID
	if itemParam := r.URL.Query().Get("inventory_item_id"); itemParam != "" {
		if parsed, err := uuid.Parse(itemParam); err == nil {
			itemID = &parsed
		}
	}

	limit := 100
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	logs, err := h.getInventoryService().ListLogs(r.Context(), restaurantID, itemID, limit)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	if logs == nil {
		logs = []inventory.InventoryLog{}
	}

	jsonResponse(w, http.StatusOK, logs)
}

// ---------------- Recipes & Dish Margins ----------------

func (h *APIHandler) GetRecipe(w http.ResponseWriter, r *http.Request) {
	var restaurantID uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
		restaurantID = claims.RestaurantID
	}
	if restaurantID == uuid.Nil {
		if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
			restaurantID, _ = uuid.Parse(restParam)
		}
	}
	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required")
		return
	}

	menuItemIDStr := chi.URLParam(r, "menu_item_id")
	menuItemID, err := uuid.Parse(menuItemIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid menu_item_id")
		return
	}

	recipe, err := h.getInventoryService().GetRecipe(r.Context(), restaurantID, menuItemID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	if recipe == nil {
		recipe = []inventory.RecipeIngredient{}
	}

	jsonResponse(w, http.StatusOK, recipe)
}

type SaveRecipeIngredientItem struct {
	InventoryItemID  uuid.UUID `json:"inventory_item_id"`
	QuantityRequired float64   `json:"quantity_required"`
}

func (h *APIHandler) SaveRecipe(w http.ResponseWriter, r *http.Request) {
	var restaurantID uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
		restaurantID = claims.RestaurantID
	}
	if restaurantID == uuid.Nil {
		if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
			restaurantID, _ = uuid.Parse(restParam)
		}
	}
	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required")
		return
	}

	menuItemIDStr := chi.URLParam(r, "menu_item_id")
	menuItemID, err := uuid.Parse(menuItemIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid menu_item_id")
		return
	}

	var req []SaveRecipeIngredientItem
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var ingredients []inventory.RecipeIngredient
	for _, it := range req {
		ingredients = append(ingredients, inventory.RecipeIngredient{
			InventoryItemID:  it.InventoryItemID,
			QuantityRequired: it.QuantityRequired,
		})
	}

	if err := h.getInventoryService().SaveRecipe(r.Context(), restaurantID, menuItemID, ingredients); err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	recipe, _ := h.getInventoryService().GetRecipe(r.Context(), restaurantID, menuItemID)
	jsonResponse(w, http.StatusOK, recipe)
}

func (h *APIHandler) ListDishMargins(w http.ResponseWriter, r *http.Request) {
	var restaurantID uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
		restaurantID = claims.RestaurantID
	}
	if restaurantID == uuid.Nil {
		if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
			restaurantID, _ = uuid.Parse(restParam)
		}
	}
	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required")
		return
	}

	margins, err := h.getInventoryService().ListDishMargins(r.Context(), restaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	if margins == nil {
		margins = []inventory.DishMargin{}
	}

	jsonResponse(w, http.StatusOK, margins)
}
