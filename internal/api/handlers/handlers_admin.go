package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devrishijain/table-manager/internal/api/middleware"
	"github.com/devrishijain/table-manager/internal/domain/audit"
	"github.com/devrishijain/table-manager/internal/domain/exitpass"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// GetAdminOverview returns platform-wide aggregate counts and today's volume.
func (h *APIHandler) GetAdminOverview(w http.ResponseWriter, r *http.Request) {
	nowUTC := time.Now().UTC()
	dayStart := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)

	var (
		allRests     []restaurant.Restaurant
		franchises   []restaurant.Franchise
		liveSessions int
		todayOrders  []order.Order
		platformGMV  int64
		platformFee  int64
	)
	var wg sync.WaitGroup
	wg.Add(5)
	go func() { defer wg.Done(); allRests, _ = h.repo.ListRestaurants(r.Context()) }()
	go func() { defer wg.Done(); franchises, _ = h.repo.ListFranchises(r.Context()) }()
	go func() { defer wg.Done(); liveSessions, _ = h.repo.CountActiveSessionsAll(r.Context()) }()
	go func() {
		defer wg.Done()
		todayOrders, _ = h.repo.ListRecentOrdersAllRestaurants(r.Context(), 500, &dayStart, nil)
	}()
	go func() { defer wg.Done(); platformGMV, platformFee, _ = h.repo.SumPlatformFeesAll(r.Context()) }()
	wg.Wait()

	total, active, suspended := 0, 0, 0
	franchiseOutlets, single := 0, 0
	activeSubs, expiredSubs := 0, 0

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
	}

	ordersToday := 0
	var revenueToday int64
	for _, o := range todayOrders {
		if o.Status == order.StateCancelled || o.Status == order.StateRejected {
			continue
		}
		ordersToday++
		revenueToday += o.Total.AmountMinorUnits
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

	// Batch fetch all restaurants and group by franchise (1 query instead of N)
	allRests, _ := h.repo.ListRestaurants(r.Context())
	restsByFranchise := make(map[uuid.UUID][]restaurant.Restaurant)
	for _, rest := range allRests {
		if rest.ID == restaurant.PlatformRestaurantID || rest.FranchiseID == nil {
			continue
		}
		restsByFranchise[*rest.FranchiseID] = append(restsByFranchise[*rest.FranchiseID], rest)
	}

	// Batch fetch all outlet enrichment data (parallel with owner fetch)
	var allOutlets []restaurant.Restaurant
	for _, rests := range restsByFranchise {
		allOutlets = append(allOutlets, rests...)
	}
	enrichedByRestID := make(map[uuid.UUID]map[string]interface{})
	ownerByID := make(map[uuid.UUID]*restaurant.StaffUser)

	ownerIDs := make(map[uuid.UUID]bool)
	for _, fr := range franchises {
		if fr.OwnerStaffID != nil {
			ownerIDs[*fr.OwnerStaffID] = true
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if len(allOutlets) > 0 {
			enrichedOutlets := h.enrichOutletsBatch(r.Context(), allOutlets)
			for _, e := range enrichedOutlets {
				if id, ok := e["id"].(uuid.UUID); ok {
					enrichedByRestID[id] = e
				}
			}
		}
	}()
	go func() {
		defer wg.Done()
		if len(ownerIDs) > 0 {
			ids := make([]uuid.UUID, 0, len(ownerIDs))
			for id := range ownerIDs {
				ids = append(ids, id)
			}
			ownerByID, _ = h.repo.GetStaffByIDs(r.Context(), ids)
		}
	}()
	wg.Wait()

	out := make([]map[string]interface{}, 0, len(franchises))
	for _, fr := range franchises {
		outlets := restsByFranchise[fr.ID]
		enrichedOutlets := make([]map[string]interface{}, 0, len(outlets))
		for _, rest := range outlets {
			if e, ok := enrichedByRestID[rest.ID]; ok {
				enrichedOutlets = append(enrichedOutlets, e)
			}
		}

		ownerName, ownerEmail := "", ""
		if fr.OwnerStaffID != nil {
			if staff, ok := ownerByID[*fr.OwnerStaffID]; ok && staff != nil {
				ownerName = staff.Name
				ownerEmail = staff.Email
			}
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

	// Parallelize all independent queries — outletInfo is replaced by direct batch fetches
	// to avoid redundant ListTables/ListActiveSessions/ListOrders calls
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	thirtyDaysAgo := dayStart.AddDate(0, 0, -30)

	var (
		tables         []restaurant.Table
		activeSessions []session.DiningSession
		ordersToday    []order.Order
		orders30d      []order.Order
		orders         []order.Order
		staffList      []restaurant.StaffUser
		auditLogs      []audit.AuditLog
	)
	var wg sync.WaitGroup
	wg.Add(7)

	go func() { defer wg.Done(); tables, _ = h.repo.ListTables(r.Context(), restID) }()
	go func() { defer wg.Done(); activeSessions, _ = h.repo.ListActiveSessions(r.Context(), restID) }()
	go func() {
		defer wg.Done()
		ordersToday, _ = h.orderService.ListOrders(r.Context(), restID, 500, &dayStart, nil)
	}()
	go func() {
		defer wg.Done()
		orders30d, _ = h.orderService.ListOrders(r.Context(), restID, 500, &thirtyDaysAgo, nil)
	}()
	go func() { defer wg.Done(); orders, _ = h.orderService.ListOrders(r.Context(), restID, 50, nil, nil) }()
	go func() { defer wg.Done(); staffList, _ = h.repo.ListStaff(r.Context(), restID) }()
	go func() { defer wg.Done(); auditLogs, _ = h.repo.ListAuditLogs(r.Context(), restID, 30, 0) }()
	wg.Wait()

	// Build enriched outlet info from fetched data (no extra queries)
	ordersTodayCount := 0
	var revenueToday int64
	for _, o := range ordersToday {
		if o.Status != order.StateCancelled && o.Status != order.StateRejected {
			ordersTodayCount++
			revenueToday += o.Total.AmountMinorUnits
		}
	}
	var revenue30d int64
	for _, o := range orders30d {
		if o.Status != order.StateCancelled && o.Status != order.StateRejected {
			revenue30d += o.Total.AmountMinorUnits
		}
	}
	enriched := map[string]interface{}{
		"id":                     rest.ID,
		"name":                   rest.Name,
		"slug":                   rest.Slug,
		"theme":                  rest.Theme,
		"venue_type":             rest.VenueType,
		"status":                 rest.Status,
		"subscription_plan":      rest.SubscriptionPlan,
		"subscription_status":    rest.SubscriptionStatus,
		"subscription_end_at":    rest.SubscriptionEndAt,
		"days_remaining":         rest.DaysRemaining(),
		"is_active":              rest.IsSubscriptionActive(),
		"is_subscription_active": rest.IsSubscriptionActive(),
		"franchise_id":           rest.FranchiseID,
		"franchise_name":         rest.FranchiseName,
		"ownership_type":         rest.OwnershipType,
		"table_count":            len(tables),
		"active_sessions":        len(activeSessions),
		"orders_today":           ordersTodayCount,
		"revenue_today_minor":    revenueToday,
		"revenue_30d_minor":      revenue30d,
		"created_at":             rest.CreatedAt,
	}

	subscription := map[string]interface{}{
		"plan":           rest.SubscriptionPlan,
		"status":         rest.SubscriptionStatus,
		"end_at":         rest.SubscriptionEndAt,
		"days_remaining": rest.DaysRemaining(),
		"is_active":      rest.IsSubscriptionActive(),
	}

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

	// Batch fetch staff names instead of N+1 per-order queries
	staffIDs := make(map[uuid.UUID]bool)
	for _, o := range orders {
		if o.AcceptedByStaffID != nil {
			staffIDs[*o.AcceptedByStaffID] = true
		}
	}
	staffByID := make(map[uuid.UUID]*restaurant.StaffUser)
	if len(staffIDs) > 0 {
		ids := make([]uuid.UUID, 0, len(staffIDs))
		for id := range staffIDs {
			ids = append(ids, id)
		}
		staffByID, _ = h.repo.GetStaffByIDs(r.Context(), ids)
	}

	recentOrders := make([]map[string]interface{}, 0, len(orders))
	for _, o := range orders {
		acceptedByName := ""
		if o.AcceptedByStaffID != nil {
			if st, ok := staffByID[*o.AcceptedByStaffID]; ok && st != nil {
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
	restNameByID := make(map[uuid.UUID]string, len(rests))
	for _, rest := range rests {
		if rest.ID == restaurant.PlatformRestaurantID {
			continue
		}
		restNameByID[rest.ID] = rest.Name
	}

	type feedItem struct {
		RestaurantID   uuid.UUID `json:"restaurant_id"`
		RestaurantName string    `json:"restaurant_name"`
		TableNumber    string    `json:"table_number"`
		Status         string    `json:"status"`
		Total          int64     `json:"total"`
		PlacedAt       time.Time `json:"placed_at"`
	}

	// Single query across all restaurants instead of N queries
	orders, err := h.repo.ListRecentOrdersAllRestaurants(r.Context(), limit, nil, nil)
	if err != nil || len(orders) == 0 {
		jsonResponse(w, http.StatusOK, []feedItem{})
		return
	}

	items := make([]feedItem, 0, len(orders))
	for _, o := range orders {
		name, ok := restNameByID[o.RestaurantID]
		if !ok {
			continue
		}
		items = append(items, feedItem{
			RestaurantID:   o.RestaurantID,
			RestaurantName: name,
			TableNumber:    o.TableNumber,
			Status:         string(o.Status),
			Total:          o.Total.AmountMinorUnits,
			PlacedAt:       o.PlacedAt,
		})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].PlacedAt.After(items[j].PlacedAt) })
	if len(items) > limit {
		items = items[:limit]
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
