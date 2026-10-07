package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/devrishijain/table-manager/internal/api/middleware"
	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// GetAdminOverview returns platform-wide aggregate counts and today's volume.
func (h *APIHandler) GetAdminOverview(w http.ResponseWriter, r *http.Request) {
	allRests, _ := h.repo.ListRestaurants(r.Context())
	franchises, _ := h.repo.ListFranchises(r.Context())

	nowUTC := time.Now().UTC()
	dayStart := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)

	total, active, suspended := 0, 0, 0
	franchiseOutlets, single := 0, 0
	activeSubs, expiredSubs := 0, 0
	liveSessions := 0
	ordersToday := 0
	var revenueToday int64
	var platformGMV int64
	var platformFee int64

	for _, rest := range allRests {
		if rest.ID == restaurant.PlatformRestaurantID {
			continue
		}
		total++
		if rest.Status == restaurant.StatusSuspended {
			suspended++
		} else {
			active++
		}
		if rest.FranchiseID != nil {
			franchiseOutlets++
		} else {
			single++
		}
		if rest.IsSubscriptionActive() {
			activeSubs++
		} else {
			expiredSubs++
		}

		if sessions, err := h.repo.ListActiveSessions(r.Context(), rest.ID); err == nil {
			liveSessions += len(sessions)
		}
		if orders, err := h.orderService.ListOrders(r.Context(), rest.ID, 500, &dayStart, nil); err == nil {
			for _, o := range orders {
				if o.Status == order.StateCancelled || o.Status == order.StateRejected {
					continue
				}
				ordersToday++
				revenueToday += o.Total.AmountMinorUnits
			}
		}
		if fees, err := h.repo.ListPlatformFees(r.Context(), rest.ID, ""); err == nil {
			for _, f := range fees {
				platformGMV += f.GMVAmount.AmountMinorUnits
				platformFee += f.FeeAmount.AmountMinorUnits
			}
		}
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"total_restaurants":          total,
		"active_restaurants":         active,
		"suspended_restaurants":      suspended,
		"franchise_count":            len(franchises),
		"franchise_outlets":          franchiseOutlets,
		"single_restaurants":         single,
		"active_subscriptions":       activeSubs,
		"expired_subscriptions":      expiredSubs,
		"live_sessions":              liveSessions,
		"orders_today":               ordersToday,
		"revenue_today_minor":        revenueToday,
		"platform_gross_sales_minor": platformGMV,
		"platform_fee_revenue_minor": platformFee,
	})
}

// GetAdminFranchises lists every franchise with its enriched outlet set.
func (h *APIHandler) GetAdminFranchises(w http.ResponseWriter, r *http.Request) {
	franchises, err := h.repo.ListFranchises(r.Context())
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]map[string]interface{}, 0, len(franchises))
	for _, fr := range franchises {
		outlets, _ := h.repo.ListRestaurantsByFranchise(r.Context(), fr.ID)

		ownerName, ownerEmail := "", ""
		if fr.OwnerStaffID != nil {
			if staff, err := h.repo.GetStaffByID(r.Context(), *fr.OwnerStaffID); err == nil && staff != nil {
				ownerName = staff.Name
				ownerEmail = staff.Email
			}
		}

		enrichedOutlets := make([]map[string]interface{}, 0, len(outlets))
		for _, rest := range outlets {
			if rest.ID == restaurant.PlatformRestaurantID {
				continue
			}
			enrichedOutlets = append(enrichedOutlets, h.outletInfo(r.Context(), rest))
		}

		out = append(out, map[string]interface{}{
			"id":           fr.ID,
			"name":         fr.Name,
			"owner_name":   ownerName,
			"owner_email":  ownerEmail,
			"outlet_count": len(enrichedOutlets),
			"created_at":   fr.CreatedAt,
			"outlets":      enrichedOutlets,
		})
	}

	jsonResponse(w, http.StatusOK, out)
}

// GetRestaurantActivity returns a deep inspection view of a single restaurant.
func (h *APIHandler) GetRestaurantActivity(w http.ResponseWriter, r *http.Request) {
	restID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid restaurant ID")
		return
	}

	rest, err := h.repo.GetRestaurantByID(r.Context(), restID)
	if err != nil || rest == nil || rest.ID == restaurant.PlatformRestaurantID {
		errorResponse(w, http.StatusNotFound, "restaurant not found")
		return
	}

	enriched := h.outletInfo(r.Context(), *rest)

	subscription := map[string]interface{}{
		"plan":           rest.SubscriptionPlan,
		"status":         rest.SubscriptionStatus,
		"end_at":         rest.SubscriptionEndAt,
		"days_remaining": rest.DaysRemaining(),
		"is_active":      rest.IsSubscriptionActive(),
	}

	tables, _ := h.repo.ListTables(r.Context(), restID)
	activeSessions, _ := h.repo.ListActiveSessions(r.Context(), restID)
	sessionsByTable := make(map[uuid.UUID]int, len(activeSessions))
	for _, s := range activeSessions {
		sessionsByTable[s.TableID] = 1
	}
	tableView := make([]map[string]interface{}, 0, len(tables))
	for _, t := range tables {
		entry := map[string]interface{}{
			"table_id":     t.ID,
			"table_number": t.TableNumber,
			"capacity":     t.Capacity,
			"is_active":    t.IsActive,
			"is_occupied":  sessionsByTable[t.ID] == 1,
		}
		for _, s := range activeSessions {
			if s.TableID == t.ID {
				entry["active_session_id"] = s.ID
				entry["session_status"] = s.Status
				entry["customer_name"] = s.CustomerName
				entry["customer_phone"] = s.CustomerPhone
				entry["guest_count"] = s.GuestCount
				entry["running_total_minor"] = s.RunningTotal.AmountMinorUnits
				entry["opened_at"] = s.OpenedAt
				entry["assigned_waiter_id"] = s.AssignedWaiterID
				entry["assigned_waiter_name"] = s.AssignedWaiterName
				break
			}
		}
		tableView = append(tableView, entry)
	}

	orders, _ := h.orderService.ListOrders(r.Context(), restID, 50, nil, nil)
	recentOrders := make([]map[string]interface{}, 0, len(orders))
	for _, o := range orders {
		acceptedByName := ""
		if o.AcceptedByStaffID != nil {
			if st, err := h.repo.GetStaffByID(r.Context(), *o.AcceptedByStaffID); err == nil && st != nil {
				acceptedByName = st.Name
			}
		}
		recentOrders = append(recentOrders, map[string]interface{}{
			"id":                   o.ID,
			"sequence_number":      o.SequenceNumber,
			"table_number":         o.TableNumber,
			"status":               o.Status,
			"total":                o.Total,
			"placed_at":            o.PlacedAt,
			"accepted_by_staff_id": o.AcceptedByStaffID,
			"accepted_by_name":     acceptedByName,
			"customer_name":        o.CustomerName,
		})
	}

	staffList, _ := h.repo.ListStaff(r.Context(), restID)
	staffView := make([]map[string]interface{}, 0, len(staffList))
	for _, st := range staffList {
		staffView = append(staffView, map[string]interface{}{
			"id":          st.ID,
			"name":        st.Name,
			"role":        st.Role,
			"employee_id": st.EmployeeID,
			"email":       st.Email,
			"phone":       st.Phone,
			"is_active":   st.IsActive,
		})
	}

	auditLogs, _ := h.repo.ListAuditLogs(r.Context(), restID, 30, 0)

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"restaurant":      enriched,
		"subscription":    subscription,
		"tables":          tableView,
		"active_sessions": activeSessions,
		"recent_orders":   recentOrders,
		"staff":           staffView,
		"recent_audit":    auditLogs,
	})
}

// GetActivityFeed merges the latest orders across all restaurants, sorted desc.
func (h *APIHandler) GetActivityFeed(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if parsed, err := strconv.Atoi(lStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	rests, _ := h.repo.ListRestaurants(r.Context())

	type feedItem struct {
		RestaurantID   uuid.UUID `json:"restaurant_id"`
		RestaurantName string    `json:"restaurant_name"`
		TableNumber    string    `json:"table_number"`
		Status         string    `json:"status"`
		Total          int64     `json:"total"`
		PlacedAt       time.Time `json:"placed_at"`
	}

	var items []feedItem
	for _, rest := range rests {
		if rest.ID == restaurant.PlatformRestaurantID {
			continue
		}
		orders, err := h.orderService.ListOrders(r.Context(), rest.ID, limit, nil, nil)
		if err != nil {
			continue
		}
		for _, o := range orders {
			items = append(items, feedItem{
				RestaurantID:   rest.ID,
				RestaurantName: rest.Name,
				TableNumber:    o.TableNumber,
				Status:         string(o.Status),
				Total:          o.Total.AmountMinorUnits,
				PlacedAt:       o.PlacedAt,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool { return items[i].PlacedAt.After(items[j].PlacedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	if items == nil {
		items = []feedItem{}
	}

	jsonResponse(w, http.StatusOK, items)
}

// AdminGenerateSubscriptionOTP issues a 24h one-time activation code.
func (h *APIHandler) AdminGenerateSubscriptionOTP(w http.ResponseWriter, r *http.Request) {
	restID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid restaurant ID")
		return
	}
	rest, err := h.repo.GetRestaurantByID(r.Context(), restID)
	if err != nil || rest == nil {
		errorResponse(w, http.StatusNotFound, "restaurant not found")
		return
	}

	var req struct {
		Days int    `json:"days"`
		Plan string `json:"plan"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Days < 1 || req.Days > 730 {
		errorResponse(w, http.StatusBadRequest, "days is required and must be between 1 and 730")
		return
	}
	plan := strings.ToUpper(strings.TrimSpace(req.Plan))
	if plan == "" {
		plan = "PRO"
	}

	// Revoke previously issued OTPs for this restaurant
	if prev, err := h.repo.ListSubscriptionOTPs(r.Context(), restID); err == nil {
		for _, o := range prev {
			if o.Status == "ISSUED" {
				o.Status = "REVOKED"
				_ = h.repo.UpdateSubscriptionOTP(r.Context(), &o)
			}
		}
	}

	rawOTP, err := exitpass.GenerateNumericOTP(6)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to generate OTP")
		return
	}

	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)
	var createdBy *uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims != nil {
		createdBy = &claims.StaffID
	}

	otp := &restaurant.SubscriptionOTP{
		ID:               uuid.New(),
		RestaurantID:     restID,
		OTPHash:          exitpass.HashOTP(rawOTP),
		Days:             req.Days,
		Plan:             plan,
		Status:           "ISSUED",
		Attempts:         0,
		ExpiresAt:        expiresAt,
		CreatedByStaffID: createdBy,
		CreatedAt:        now,
	}
	if err := h.repo.CreateSubscriptionOTP(r.Context(), otp); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to store OTP")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"otp":             rawOTP,
		"restaurant_id":   restID,
		"restaurant_name": rest.Name,
		"days":            req.Days,
		"plan":            plan,
		"expires_at":      expiresAt,
	})
}

// AdminListSubscriptionOTPs returns OTP history without hashes or raw codes.
func (h *APIHandler) AdminListSubscriptionOTPs(w http.ResponseWriter, r *http.Request) {
	restID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid restaurant ID")
		return
	}
	if _, err := h.repo.GetRestaurantByID(r.Context(), restID); err != nil {
		errorResponse(w, http.StatusNotFound, "restaurant not found")
		return
	}

	otps, err := h.repo.ListSubscriptionOTPs(r.Context(), restID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	if otps == nil {
		otps = []restaurant.SubscriptionOTP{}
	}
	jsonResponse(w, http.StatusOK, otps)
}
