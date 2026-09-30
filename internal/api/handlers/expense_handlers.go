package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/devrishijain/table-manager/internal/api/middleware"
	"github.com/devrishijain/table-manager/internal/domain/expense"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type CreateExpenseLineItemRequest struct {
	InventoryItemID *uuid.UUID `json:"inventory_item_id,omitempty"`
	ItemName        string     `json:"item_name"`
	Quantity        float64    `json:"quantity"`
	Unit            string     `json:"unit"`
	UnitPriceMinor  int64      `json:"unit_price_minor"`
	TotalPriceMinor int64      `json:"total_price_minor"`
}

type CreateExpenseRequest struct {
	Type            expense.ExpenseType            `json:"type"`
	Category        expense.ExpenseCategory        `json:"category"`
	Title           string                         `json:"title"`
	AmountMinor     int64                          `json:"amount_minor"`
	Currency        string                         `json:"currency"`
	PaidVia         string                         `json:"paid_via"`
	VendorName      string                         `json:"vendor_name"`
	ExpenseDate     string                         `json:"expense_date"`
	Notes           string                         `json:"notes"`
	IsStockPurchase bool                           `json:"is_stock_purchase"`
	LineItems       []CreateExpenseLineItemRequest `json:"line_items,omitempty"`
}

func (h *APIHandler) CreateExpense(w http.ResponseWriter, r *http.Request) {
	var restaurantID uuid.UUID
	var staffID *uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
		restaurantID = claims.RestaurantID
		staffID = &claims.StaffID
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

	var req CreateExpenseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var expDate time.Time
	if req.ExpenseDate != "" {
		if t, err := time.Parse(time.RFC3339, req.ExpenseDate); err == nil {
			expDate = t
		} else if t, err := time.Parse("2006-01-02", req.ExpenseDate); err == nil {
			expDate = t
		}
	}
	if expDate.IsZero() {
		expDate = time.Now().UTC()
	}

	var serviceItems []service.CreateExpenseLineItemInput
	for _, li := range req.LineItems {
		serviceItems = append(serviceItems, service.CreateExpenseLineItemInput{
			InventoryItemID: li.InventoryItemID,
			ItemName:        li.ItemName,
			Quantity:        li.Quantity,
			Unit:            li.Unit,
			UnitPriceMinor:  li.UnitPriceMinor,
			TotalPriceMinor: li.TotalPriceMinor,
		})
	}

	exp, err := h.getExpenseService().CreateExpense(r.Context(), service.CreateExpenseInput{
		RestaurantID:     restaurantID,
		Type:             req.Type,
		Category:         req.Category,
		Title:            req.Title,
		AmountMinor:      req.AmountMinor,
		Currency:         req.Currency,
		PaidVia:          req.PaidVia,
		VendorName:       req.VendorName,
		ExpenseDate:      expDate,
		Notes:            req.Notes,
		IsStockPurchase:  req.IsStockPurchase,
		LineItems:        serviceItems,
		CreatedByStaffID: staffID,
	})
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusCreated, exp)
}

func (h *APIHandler) ListExpenses(w http.ResponseWriter, r *http.Request) {
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

	var expType *expense.ExpenseType
	if tStr := r.URL.Query().Get("type"); tStr != "" {
		t := expense.ExpenseType(tStr)
		expType = &t
	}

	var cat *expense.ExpenseCategory
	if cStr := r.URL.Query().Get("category"); cStr != "" {
		c := expense.ExpenseCategory(cStr)
		cat = &c
	}

	var startDate, endDate *time.Time
	if sStr := r.URL.Query().Get("start_date"); sStr != "" {
		if t, err := parseDateFilter(sStr, false); err == nil {
			startDate = t
		}
	}
	if eStr := r.URL.Query().Get("end_date"); eStr != "" {
		if t, err := parseDateFilter(eStr, true); err == nil {
			endDate = t
		}
	}

	expenses, err := h.getExpenseService().ListExpenses(r.Context(), restaurantID, expType, cat, startDate, endDate)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	if expenses == nil {
		expenses = []expense.Expense{}
	}

	jsonResponse(w, http.StatusOK, expenses)
}

func (h *APIHandler) DeleteExpense(w http.ResponseWriter, r *http.Request) {
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

	expenseIDStr := chi.URLParam(r, "id")
	expenseID, err := uuid.Parse(expenseIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid expense id")
		return
	}

	if err := h.getExpenseService().DeleteExpense(r.Context(), restaurantID, expenseID); err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *APIHandler) GetExpenseLineItems(w http.ResponseWriter, r *http.Request) {
	expenseIDStr := chi.URLParam(r, "id")
	expenseID, err := uuid.Parse(expenseIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid expense id")
		return
	}

	items, err := h.getExpenseService().ListExpenseLineItems(r.Context(), expenseID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []expense.ExpenseLineItem{}
	}

	jsonResponse(w, http.StatusOK, items)
}
