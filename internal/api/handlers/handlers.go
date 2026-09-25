package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	objstore "github.com/devrishijain/table-manager/internal/adapter/storage"
	"github.com/devrishijain/table-manager/internal/api/middleware"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type APIHandler struct {
	sessionService    *service.SessionService
	orderService      *service.OrderService
	paymentService    *service.PaymentService
	exitService       *service.ExitService
	ledgerService     *service.LedgerService
	analyticsService  *service.AnalyticsService
	onboardingService *service.OnboardingService
	aiCatalogService  *service.AICatalogService
	staffService      *service.StaffService
	objectStore       objstore.ObjectStore
	repo              storage.Repository
	webhookSecret     string
}

func (h *APIHandler) SetAICatalogService(aiSvc *service.AICatalogService) {
	h.aiCatalogService = aiSvc
}

func (h *APIHandler) SetStaffService(staffSvc *service.StaffService) {
	h.staffService = staffSvc
}

func (h *APIHandler) getStaffService() *service.StaffService {
	if h.staffService == nil {
		h.staffService = service.NewStaffService(h.repo, []byte("table-manager-staff-secret-key-32b"))
	}
	return h.staffService
}

func NewAPIHandler(
	sessionService *service.SessionService,
	orderService *service.OrderService,
	paymentService *service.PaymentService,
	exitService *service.ExitService,
	ledgerService *service.LedgerService,
	analyticsService *service.AnalyticsService,
	onboardingService *service.OnboardingService,
	objectStore objstore.ObjectStore,
	repo storage.Repository,
	webhookSecret string,
) *APIHandler {
	return &APIHandler{
		sessionService:    sessionService,
		orderService:      orderService,
		paymentService:    paymentService,
		exitService:       exitService,
		ledgerService:     ledgerService,
		analyticsService:  analyticsService,
		onboardingService: onboardingService,
		objectStore:       objectStore,
		repo:              repo,
		webhookSecret:     webhookSecret,
	}
}

// ---------------- Helper JSON responses ----------------

func jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func errorResponse(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}

// ---------------- Customer Handlers ----------------

type StartSessionRequest struct {
	TableToken        string `json:"table_token"`
	DeviceToken       string `json:"device_token"`
	DisplayName       string `json:"display_name"`
	DeviceFingerprint string `json:"device_fingerprint"`
}

func (h *APIHandler) StartSession(w http.ResponseWriter, r *http.Request) {
	var req StartSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	sess, isNew, err := h.sessionService.StartSession(r.Context(), req.TableToken, req.DeviceToken, req.DisplayName, req.DeviceFingerprint)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	status := http.StatusOK
	if isNew {
		status = http.StatusCreated
	}
	jsonResponse(w, status, sess)
}

type PlaceOrderRequest struct {
	Items []order.CartItem `json:"items"`
}

func (h *APIHandler) PlaceOrder(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid session ID")
		return
	}

	var req PlaceOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	ord, firstOTP, err := h.orderService.PlaceOrder(r.Context(), sessionID, req.Items)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	resp := map[string]interface{}{
		"order": ord,
	}
	if firstOTP != nil {
		resp["first_order_verification_otp"] = *firstOTP
	}
	jsonResponse(w, http.StatusCreated, resp)
}

func (h *APIHandler) GetSessionDetails(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid session ID")
		return
	}

	sess, err := h.sessionService.GetSession(r.Context(), sessionID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "session not found")
		return
	}

	orders, _ := h.orderService.GetOrdersBySessionID(r.Context(), sessionID)
	payments, _ := h.paymentService.GetPaymentsBySession(r.Context(), sessionID)

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"session":  sess,
		"orders":   orders,
		"payments": payments,
	})
}

type CustomerPayRequest struct {
	Method               payment.Method `json:"method"`
	AmountMinor          int64          `json:"amount_minor"`
	ExternalPlatformName *string        `json:"external_platform_name,omitempty"`
}

func (h *APIHandler) CustomerPay(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid session ID")
		return
	}

	var req CustomerPayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid payload")
		return
	}

	// Move session to AWAITING_PAYMENT bill requested
	sess, err := h.paymentService.RequestBill(r.Context(), sessionID)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	amt := money.New(req.AmountMinor)
	if amt.IsZero() {
		amt = sess.FinalTotal
	}

	p, err := h.paymentService.InitiatePayment(r.Context(), sessionID, req.Method, amt, req.ExternalPlatformName)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, p)
}

func (h *APIHandler) GetExitPass(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid session ID")
		return
	}

	ep, err := h.exitService.GetExitPass(r.Context(), sessionID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "exit pass not found or session not paid")
		return
	}

	jsonResponse(w, http.StatusOK, ep)
}

// ---------------- Staff Handlers ----------------

type VerifyFirstOrderRequest struct {
	OTP string `json:"otp"`
}

func (h *APIHandler) VerifyFirstOrder(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid session ID")
		return
	}

	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	var req VerifyFirstOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid payload")
		return
	}

	if err := h.sessionService.VerifyFirstOrder(r.Context(), sessionID, claims.StaffID, req.OTP); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "OPEN_VERIFIED"})
}

func (h *APIHandler) AcceptOrder(w http.ResponseWriter, r *http.Request) {
	orderIDStr := chi.URLParam(r, "id")
	orderID, err := uuid.Parse(orderIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid order ID")
		return
	}

	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	ord, err := h.orderService.AcceptOrder(r.Context(), orderID, claims.StaffID)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, ord)
}

type ConfirmPaymentRequest struct {
	PaymentID             uuid.UUID      `json:"payment_id"`
	SessionID             uuid.UUID      `json:"session_id"`
	AmountMinor           int64          `json:"amount_minor"`
	Method                payment.Method `json:"method"`
	ExternalPlatformName  *string        `json:"external_platform_name,omitempty"`
	EvidenceTransactionID *string        `json:"evidence_transaction_id,omitempty"`
	EvidenceBucket        *string        `json:"evidence_bucket,omitempty"`
	EvidenceObjectKey     *string        `json:"evidence_object_key,omitempty"`
	EvidenceContentType   *string        `json:"evidence_content_type,omitempty"`
	EvidenceSizeBytes     *int64         `json:"evidence_size_bytes,omitempty"`
	EvidenceSHA256        *string        `json:"evidence_sha256,omitempty"`
	EvidencePhotoURL      *string        `json:"evidence_photo_url,omitempty"`
}

// StaffConfirmPayment enforces that staff ID is extracted from authenticated context, never request body.
func (h *APIHandler) StaffConfirmPayment(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	// Guard cannot confirm payment!
	if claims.Role == "GUARD" {
		errorResponse(w, http.StatusForbidden, "guards are strictly unauthorized to confirm payments")
		return
	}

	var req ConfirmPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid payload")
		return
	}

	now := time.Now()
	confirmReq := payment.PaymentConfirmationRequest{
		PaymentID:             req.PaymentID,
		SessionID:             req.SessionID,
		RestaurantID:          claims.RestaurantID,
		Amount:                money.New(req.AmountMinor),
		Method:                req.Method,
		ExternalPlatformName:  req.ExternalPlatformName,
		EvidenceTransactionID: req.EvidenceTransactionID,
		EvidenceBucket:        req.EvidenceBucket,
		EvidenceObjectKey:     req.EvidenceObjectKey,
		EvidenceContentType:   req.EvidenceContentType,
		EvidenceSizeBytes:     req.EvidenceSizeBytes,
		EvidenceSHA256:        req.EvidenceSHA256,
		EvidenceUploadedAt:    &now,
		EvidencePhotoURL:      req.EvidencePhotoURL,
		ConfirmedByStaffID:    &claims.StaffID, // resolved from JWT
	}

	p, err := h.paymentService.ConfirmPayment(r.Context(), confirmReq)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, p)
}

type ForceCloseRequest struct {
	Reason string `json:"reason"`
}

func (h *APIHandler) ForceCloseSession(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid session ID")
		return
	}

	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	if claims.Role != string(restaurant.RoleManager) &&
		claims.Role != string(restaurant.RoleRestaurantAdmin) &&
		claims.Role != string(restaurant.RoleRestaurantOwner) &&
		claims.Role != string(restaurant.RoleSuperAdmin) {
		errorResponse(w, http.StatusForbidden, "forbidden: insufficient permissions to force close session")
		return
	}

	var req ForceCloseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Reason == "" {
		errorResponse(w, http.StatusBadRequest, "mandatory reason required for force close")
		return
	}

	if err := h.sessionService.ForceCloseSession(r.Context(), sessionID, claims.StaffID, req.Reason); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "FORCE_CLOSED"})
}

func (h *APIHandler) GetTableDashboard(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	tables, _ := h.repo.ListTables(r.Context(), claims.RestaurantID)
	activeSessions, _ := h.sessionService.ListActiveSessions(r.Context(), claims.RestaurantID)

	sessionByTable := make(map[uuid.UUID]interface{})
	for _, s := range activeSessions {
		sessionByTable[s.TableID] = s
	}

	var board []map[string]interface{}
	for _, t := range tables {
		entry := map[string]interface{}{
			"table":   t,
			"session": sessionByTable[t.ID],
		}
		board = append(board, entry)
	}

	jsonResponse(w, http.StatusOK, board)
}

// ---------------- Guard Handlers ----------------

type GuardVerifyRequest struct {
	SessionID uuid.UUID `json:"session_id"`
	OTPCode   string    `json:"otp_code"`
}

func (h *APIHandler) GuardVerifyExit(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetGuardClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized guard")
		return
	}

	var req GuardVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid guard verification request")
		return
	}

	resp := h.exitService.VerifyExit(r.Context(), req.SessionID, req.OTPCode, claims.GuardID)
	jsonResponse(w, http.StatusOK, resp)
}

// ---------------- Kitchen KDS Handlers ----------------

func (h *APIHandler) GetKitchenQueue(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	queue, err := h.orderService.ListKitchenQueue(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, queue)
}

type UpdateKitchenStatusRequest struct {
	Status order.State `json:"status"`
}

func (h *APIHandler) UpdateKitchenStatus(w http.ResponseWriter, r *http.Request) {
	orderIDStr := chi.URLParam(r, "id")
	orderID, err := uuid.Parse(orderIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid order ID")
		return
	}

	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	var req UpdateKitchenStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid payload")
		return
	}

	ord, err := h.orderService.UpdateOrderStatus(r.Context(), orderID, req.Status, claims.StaffID)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, ord)
}

// ---------------- Webhook Handler ----------------

type RazorpayWebhookPayload struct {
	Event   string `json:"event"`
	Payload struct {
		Payment struct {
			Entity struct {
				ID          string `json:"id"`
				Amount      int64  `json:"amount"`
				Status      string `json:"status"`
				OrderID     string `json:"order_id"`
				Notes       map[string]string `json:"notes"`
			} `json:"entity"`
		} `json:"payment"`
	} `json:"payload"`
}

func (h *APIHandler) RazorpayWebhook(w http.ResponseWriter, r *http.Request) {
	eventID := r.Header.Get("X-Razorpay-Event-Id")
	if eventID == "" {
		errorResponse(w, http.StatusBadRequest, "missing webhook event id")
		return
	}

	// Webhook idempotency check
	isNew, err := h.repo.RecordWebhookEvent(r.Context(), "RAZORPAY", eventID)
	if err != nil || !isNew {
		// Duplicate webhook retry: return 200 OK without double-processing
		jsonResponse(w, http.StatusOK, map[string]string{"status": "duplicate_ignored"})
		return
	}

	var payload RazorpayWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}

	sessionIDStr := payload.Payload.Payment.Entity.Notes["session_id"]
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		// Event unrelated to active session
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	sess, err := h.sessionService.GetSession(r.Context(), sessionID)
	if err != nil {
		jsonResponse(w, http.StatusOK, map[string]string{"status": "session_not_found"})
		return
	}

	// Auto-confirm gateway payment
	gatewayRef := payload.Payload.Payment.Entity.ID
	confirmReq := payment.PaymentConfirmationRequest{
		PaymentID:          uuid.New(),
		SessionID:          sess.ID,
		RestaurantID:       sess.RestaurantID,
		Amount:             money.New(payload.Payload.Payment.Entity.Amount),
		Method:             payment.MethodOwnGateway,
		GatewayReferenceID: &gatewayRef,
	}

	_, _ = h.paymentService.ConfirmPayment(r.Context(), confirmReq)
	jsonResponse(w, http.StatusOK, map[string]string{"status": "processed"})
}

// ---------------- Tenant Admin & Analytics Handlers ----------------

func (h *APIHandler) GetTodayAnalytics(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	analytics, err := h.analyticsService.GetTodayAnalytics(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, analytics)
}

func (h *APIHandler) GetSalesForecast(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	horizonStr := r.URL.Query().Get("horizon")
	horizon := 7
	if h, err := strconv.Atoi(horizonStr); err == nil && h > 0 {
		horizon = h
	}

	forecasts, err := h.analyticsService.GetSalesForecast(r.Context(), claims.RestaurantID, horizon)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	mtd, _ := h.analyticsService.GetMonthToDateAnalytics(r.Context(), claims.RestaurantID)
	actualSales := money.Zero()
	if mtd != nil {
		actualSales = mtd.TotalGMV
	}

	var totalForecastMinor int64
	for _, f := range forecasts {
		totalForecastMinor += f.ExpectedGMV.AmountMinorUnits
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"metric_type":                "PROJECTION_ANALYTICS_NOT_FINANCIAL_TRUTH",
		"disclaimer":                 "Forecasted figures are moving-average projections for operational planning, not settled financial statements.",
		"actual_month_to_date_sales": actualSales,
		"projected_horizon_sales":    money.New(totalForecastMinor),
		"horizon_days":               horizon,
		"daily_projections":          forecasts,
	})
}

func (h *APIHandler) GetLedgerPayable(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	payable, err := h.ledgerService.GetRunningPayable(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	settlements, _ := h.ledgerService.ListSettlements(r.Context(), claims.RestaurantID)

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"running_platform_payable": payable,
		"settlements":              settlements,
	})
}

// ---------------- Platform Super Admin Handlers ----------------

func (h *APIHandler) ListAllRestaurants(w http.ResponseWriter, r *http.Request) {
	list, err := h.repo.ListRestaurants(r.Context())
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, list)
}

type CommissionOverrideRequest struct {
	NewRateBps int64  `json:"new_rate_bps"`
	Reason     string `json:"reason"`
}

func (h *APIHandler) OverrideCommissionRate(w http.ResponseWriter, r *http.Request) {
	restaurantIDStr := chi.URLParam(r, "id")
	restaurantID, err := uuid.Parse(restaurantIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid restaurant ID")
		return
	}

	var req CommissionOverrideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Reason == "" {
		errorResponse(w, http.StatusBadRequest, "valid rate and mandatory reason required")
		return
	}

	rest, err := h.repo.GetRestaurantByID(r.Context(), restaurantID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "restaurant not found")
		return
	}

	rest.CommissionRateBps = req.NewRateBps
	if err := h.repo.UpdateRestaurant(r.Context(), rest); err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, rest)
}

func (h *APIHandler) ExportTableQRs(w http.ResponseWriter, r *http.Request) {
	restaurantIDStr := chi.URLParam(r, "id")
	restaurantID, err := uuid.Parse(restaurantIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid restaurant ID")
		return
	}

	tables, _ := h.repo.ListTables(r.Context(), restaurantID)
	var exports []service.TableQRExport
	for _, t := range tables {
		exports = append(exports, service.TableQRExport{
			TableID:     t.ID,
			TableNumber: t.TableNumber,
			TableToken:  t.TableToken,
			QRURL:       "https://dine.table-manager.internal/r/" + restaurantIDStr + "/t/" + t.TableToken,
		})
	}

	jsonResponse(w, http.StatusOK, exports)
}

func (h *APIHandler) GetMonthToDateAnalytics(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	mtd, err := h.analyticsService.GetMonthToDateAnalytics(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, mtd)
}

func (h *APIHandler) GetPeriodComparison(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	comp, err := h.analyticsService.GetPeriodComparison(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, comp)
}

func (h *APIHandler) GetPeakHours(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	peak, err := h.analyticsService.GetPeakHours(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, peak)
}

func (h *APIHandler) GetSettlements(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	settlements, err := h.ledgerService.ListSettlements(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, settlements)
}

// ---------------- Menu & Staff Management Handlers ----------------

func (h *APIHandler) ListCategories(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	cats, err := h.repo.ListCategories(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, cats)
}

func (h *APIHandler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	var cat struct {
		Name         string `json:"name"`
		DisplayOrder int    `json:"display_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&cat); err != nil || cat.Name == "" {
		errorResponse(w, http.StatusBadRequest, "valid category name required")
		return
	}
	newCat := &restaurant.MenuCategory{
		ID:           uuid.New(),
		RestaurantID: claims.RestaurantID,
		Name:         cat.Name,
		DisplayOrder: cat.DisplayOrder,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := h.repo.CreateCategory(r.Context(), newCat); err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusCreated, newCat)
}

func (h *APIHandler) ListMenuItems(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	items, err := h.repo.ListMenuItems(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, items)
}

func (h *APIHandler) CreateMenuItem(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	var item struct {
		CategoryID  uuid.UUID `json:"category_id"`
		Name        string    `json:"name"`
		Description string    `json:"description"`
		PriceMinor  int64     `json:"price_minor"`
		CGSTRateBps int64     `json:"cgst_rate_bps"`
		SGSTRateBps int64     `json:"sgst_rate_bps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil || item.Name == "" {
		errorResponse(w, http.StatusBadRequest, "valid menu item payload required")
		return
	}
	mi := &restaurant.MenuItem{
		ID:           uuid.New(),
		RestaurantID: claims.RestaurantID,
		CategoryID:   item.CategoryID,
		Name:         item.Name,
		Description:  item.Description,
		Price:        money.New(item.PriceMinor),
		IsAvailable:  true,
		CGSTRateBps:  item.CGSTRateBps,
		SGSTRateBps:  item.SGSTRateBps,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := h.repo.CreateMenuItem(r.Context(), mi); err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusCreated, mi)
}

func (h *APIHandler) ListStaff(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	staffList, err := h.repo.ListStaff(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, staffList)
}

type CreateStaffRequest struct {
	Name     string          `json:"name"`
	Phone    string          `json:"phone"`
	Email    string          `json:"email"`
	Password string          `json:"password"`
	Role     restaurant.Role `json:"role"`
}

func (h *APIHandler) CreateStaff(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	var req CreateStaffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	staff, err := h.getStaffService().CreateStaff(r.Context(), service.CreateStaffInput{
		RestaurantID: claims.RestaurantID,
		Name:         req.Name,
		Phone:        req.Phone,
		Email:        req.Email,
		Password:     req.Password,
		Role:         req.Role,
	}, claims.StaffID)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusCreated, staff)
}

type StaffLoginRequest struct {
	Identifier   string     `json:"identifier"`
	Password     string     `json:"password"`
	RestaurantID *uuid.UUID `json:"restaurant_id,omitempty"`
}

func (h *APIHandler) StaffLogin(w http.ResponseWriter, r *http.Request) {
	var req StaffLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	result, err := h.getStaffService().Authenticate(r.Context(), req.Identifier, req.Password, req.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusUnauthorized, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, result)
}

type PendingOrderResponse struct {
	Order       order.Order `json:"order"`
	TableNumber string      `json:"table_number"`
}

func (h *APIHandler) GetPendingOrders(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	orders, err := h.orderService.ListPendingOrders(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	res := make([]PendingOrderResponse, len(orders))
	for i, ord := range orders {
		tableNum := ord.TableNumber
		if tableNum == "" {
			tableNum = "Table"
		}
		res[i] = PendingOrderResponse{
			Order:       ord,
			TableNumber: tableNum,
		}
	}

	jsonResponse(w, http.StatusOK, res)
}

func (h *APIHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	settings, err := h.repo.GetSettings(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, settings)
}

func (h *APIHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	var s restaurant.RestaurantSettings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid settings payload")
		return
	}
	s.RestaurantID = claims.RestaurantID
	s.UpdatedAt = time.Now()
	if err := h.repo.UpdateSettings(r.Context(), &s); err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, s)
}

func (h *APIHandler) GetOnboarding(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	onboarding, err := h.repo.GetOnboarding(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "onboarding record not found")
		return
	}
	jsonResponse(w, http.StatusOK, onboarding)
}

func (h *APIHandler) CloneMenu(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	var req struct {
		TemplateType string `json:"template_type"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := h.onboardingService.CloneMenuFromTemplate(r.Context(), claims.RestaurantID, req.TemplateType); err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"status": "menu_template_cloned"})
}

func (h *APIHandler) GoLive(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	if err := h.onboardingService.CompleteGoLive(r.Context(), claims.RestaurantID); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"status": "ACTIVE"})
}

func (h *APIHandler) UploadPaymentProof(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	paymentIDStr := r.URL.Query().Get("payment_id")
	paymentID, err := uuid.Parse(paymentIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "valid payment_id query param required")
		return
	}

	// Read raw body bytes (max 5MB enforced by ObjectStore)
	fileBytes, err := io.ReadAll(r.Body)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "failed to read file payload")
		return
	}

	stored, err := h.objectStore.UploadProof(r.Context(), claims.RestaurantID, paymentID, fileBytes)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	if pay, err := h.repo.GetPaymentByID(r.Context(), paymentID); err == nil && pay != nil {
		pay.EvidenceObjectKey = &stored.Key
		pay.EvidenceSizeBytes = &stored.SizeBytes
		pay.EvidenceContentType = &stored.MIMEType
		pay.EvidenceSHA256 = &stored.SHA256Hash
		pay.EvidenceUploadedAt = &stored.UploadedAt
		_ = h.repo.UpdatePayment(r.Context(), pay)
	}

	jsonResponse(w, http.StatusCreated, stored)
}

func (h *APIHandler) GetPaymentEvidenceURL(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	paymentIDStr := chi.URLParam(r, "id")
	paymentID, err := uuid.Parse(paymentIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid payment ID")
		return
	}

	pay, err := h.repo.GetPaymentByID(r.Context(), paymentID)
	if err != nil || pay == nil {
		errorResponse(w, http.StatusNotFound, "payment not found")
		return
	}

	if !claims.IsPlatform && pay.RestaurantID != claims.RestaurantID {
		errorResponse(w, http.StatusForbidden, "forbidden: payment belongs to another restaurant")
		return
	}

	var objectKey string
	if pay.EvidenceObjectKey != nil && *pay.EvidenceObjectKey != "" {
		objectKey = *pay.EvidenceObjectKey
	} else if pay.EvidencePhotoURL != nil && *pay.EvidencePhotoURL != "" {
		objectKey = *pay.EvidencePhotoURL
	} else {
		errorResponse(w, http.StatusNotFound, "no evidence file uploaded for this payment")
		return
	}

	// Generate 15-minute temporary pre-signed URL
	signedURL, err := h.objectStore.GetSignedURL(r.Context(), objectKey, 15*time.Minute)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to generate signed URL")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"payment_id":         paymentID,
		"signed_url":         signedURL,
		"expires_in_seconds": 900,
		"content_type":       pay.EvidenceContentType,
		"size_bytes":         pay.EvidenceSizeBytes,
		"sha256":             pay.EvidenceSHA256,
	})
}

// ---------------- Platform Admin Additional Handlers ----------------

func (h *APIHandler) GetRestaurantDetails(w http.ResponseWriter, r *http.Request) {
	restaurantIDStr := chi.URLParam(r, "id")
	restaurantID, err := uuid.Parse(restaurantIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid restaurant ID")
		return
	}

	rest, err := h.repo.GetRestaurantByID(r.Context(), restaurantID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "restaurant not found")
		return
	}

	settings, _ := h.repo.GetSettings(r.Context(), restaurantID)
	onboarding, _ := h.repo.GetOnboarding(r.Context(), restaurantID)
	tables, _ := h.repo.ListTables(r.Context(), restaurantID)
	activeSessions, _ := h.repo.ListActiveSessions(r.Context(), restaurantID)

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"restaurant":      rest,
		"settings":        settings,
		"onboarding":      onboarding,
		"table_count":     len(tables),
		"active_sessions": len(activeSessions),
	})
}

func (h *APIHandler) GetPlatformAnalytics(w http.ResponseWriter, r *http.Request) {
	restaurants, err := h.repo.ListRestaurants(r.Context())
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	var totalPlatformGMVMinor int64
	var totalPlatformFeeMinor int64
	activeRestaurantCount := 0

	for _, rest := range restaurants {
		if rest.Status == restaurant.StatusActive {
			activeRestaurantCount++
		}
		fees, _ := h.repo.ListPlatformFees(r.Context(), rest.ID, "")
		for _, f := range fees {
			totalPlatformGMVMinor += f.GMVAmount.AmountMinorUnits
			totalPlatformFeeMinor += f.FeeAmount.AmountMinorUnits
		}
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"total_restaurants":         len(restaurants),
		"active_restaurants":        activeRestaurantCount,
		"platform_gross_sales":      money.New(totalPlatformGMVMinor),
		"platform_fee_revenue":      money.New(totalPlatformFeeMinor),
	})
}

func (h *APIHandler) GetFraudReviewQueue(w http.ResponseWriter, r *http.Request) {
	restaurants, err := h.repo.ListRestaurants(r.Context())
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	type FraudFlag struct {
		RestaurantID   uuid.UUID `json:"restaurant_id"`
		RestaurantName string    `json:"restaurant_name"`
		FlagType       string    `json:"flag_type"`
		Description    string    `json:"description"`
	}

	var flags []FraudFlag
	for _, rest := range restaurants {
		staffActions, _ := h.repo.ListStaffActions(r.Context(), rest.ID, 100, 0)
		walkoutCount := 0
		forceCloseCount := 0
		for _, a := range staffActions {
			if a.ActionType == "WALKOUT_REPORT" {
				walkoutCount++
			}
			if a.ActionType == "FORCE_CLOSE" {
				forceCloseCount++
			}
		}

		if walkoutCount >= 3 {
			flags = append(flags, FraudFlag{
				RestaurantID:   rest.ID,
				RestaurantName: rest.Name,
				FlagType:       "HIGH_WALKOUT_RATE",
				Description:    "Anomalous number of reported walkouts detected",
			})
		}
		if forceCloseCount >= 5 {
			flags = append(flags, FraudFlag{
				RestaurantID:   rest.ID,
				RestaurantName: rest.Name,
				FlagType:       "HIGH_FORCE_CLOSE_RATE",
				Description:    "Unusual frequency of manual force-closes detected",
			})
		}
	}

	jsonResponse(w, http.StatusOK, flags)
}

func (h *APIHandler) SuspendRestaurant(w http.ResponseWriter, r *http.Request) {
	restaurantIDStr := chi.URLParam(r, "id")
	restaurantID, err := uuid.Parse(restaurantIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid restaurant ID")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Reason == "" {
		errorResponse(w, http.StatusBadRequest, "mandatory suspension reason required")
		return
	}

	rest, err := h.repo.GetRestaurantByID(r.Context(), restaurantID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "restaurant not found")
		return
	}

	rest.Status = restaurant.StatusSuspended
	if err := h.repo.UpdateRestaurant(r.Context(), rest); err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, rest)
}

func (h *APIHandler) ReactivateRestaurant(w http.ResponseWriter, r *http.Request) {
	restaurantIDStr := chi.URLParam(r, "id")
	restaurantID, err := uuid.Parse(restaurantIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid restaurant ID")
		return
	}

	rest, err := h.repo.GetRestaurantByID(r.Context(), restaurantID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "restaurant not found")
		return
	}

	rest.Status = restaurant.StatusActive
	if err := h.repo.UpdateRestaurant(r.Context(), rest); err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, rest)
}

func (h *APIHandler) GetRestaurantOnboardingAdmin(w http.ResponseWriter, r *http.Request) {
	restaurantIDStr := chi.URLParam(r, "id")
	restaurantID, err := uuid.Parse(restaurantIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid restaurant ID")
		return
	}
	onboard, err := h.repo.GetOnboarding(r.Context(), restaurantID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "onboarding record not found")
		return
	}
	jsonResponse(w, http.StatusOK, onboard)
}

func (h *APIHandler) GetDashboardOverview(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	overview, err := h.analyticsService.GetDashboardOverview(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, overview)
}

func (h *APIHandler) GetTablePerformance(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	stats, err := h.analyticsService.GetTablePerformance(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, stats)
}

func (h *APIHandler) GetMenuPerformance(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	stats, err := h.analyticsService.GetMenuPerformance(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, stats)
}

func (h *APIHandler) GetOnboardingProgress(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}
	progress, err := h.onboardingService.GetOnboardingProgress(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, progress)
}

// AICatalogMenu processes an uploaded menu image with Gemini AI Vision and saves extracted categories/dishes.
func (h *APIHandler) AICatalogMenu(w http.ResponseWriter, r *http.Request) {
	if h.aiCatalogService == nil {
		errorResponse(w, http.StatusServiceUnavailable, "AI catalog service is not initialized")
		return
	}

	var restaurantID uuid.UUID
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if ok {
		restaurantID = claims.RestaurantID
	} else {
		// Allow fallback restaurant_id in query or form
		restIDStr := r.URL.Query().Get("restaurant_id")
		if restIDStr == "" {
			restIDStr = r.FormValue("restaurant_id")
		}
		if restIDStr != "" {
			parsedID, err := uuid.Parse(restIDStr)
			if err == nil {
				restaurantID = parsedID
			}
		}
	}

	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required")
		return
	}

	// Parse file from multipart form
	err := r.ParseMultipartForm(10 << 20) // 10 MB max
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "failed to parse multipart form: "+err.Error())
		return
	}

	file, header, err := r.FormFile("menu_image")
	if err != nil {
		file, header, err = r.FormFile("file")
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "menu_image form file is required")
			return
		}
	}
	defer file.Close()

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to read uploaded file: "+err.Error())
		return
	}

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}

	result, err := h.aiCatalogService.ProcessAndCatalogMenu(r.Context(), restaurantID, fileBytes, mimeType)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "AI menu extraction failed: "+err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":        "success",
		"message":       "Menu cataloged successfully via Gemini AI",
		"restaurant_id": restaurantID,
		"catalog":       result,
	})
}

// AIQueryMenu handles natural language questions and semantic data retrieval over stored menu items using Gemini AI.
func (h *APIHandler) AIQueryMenu(w http.ResponseWriter, r *http.Request) {
	if h.aiCatalogService == nil {
		errorResponse(w, http.StatusServiceUnavailable, "AI service is not initialized")
		return
	}

	var restaurantID uuid.UUID
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if ok {
		restaurantID = claims.RestaurantID
	}

	var req struct {
		RestaurantID string `json:"restaurant_id"`
		Query        string `json:"query"`
	}

	if r.Header.Get("Content-Type") == "application/json" {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if restaurantID == uuid.Nil && req.RestaurantID != "" {
		if parsed, err := uuid.Parse(req.RestaurantID); err == nil {
			restaurantID = parsed
		}
	}
	if restaurantID == uuid.Nil {
		if queryRestID := r.URL.Query().Get("restaurant_id"); queryRestID != "" {
			if parsed, err := uuid.Parse(queryRestID); err == nil {
				restaurantID = parsed
			}
		}
	}

	userQuery := req.Query
	if userQuery == "" {
		userQuery = r.URL.Query().Get("query")
	}

	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required")
		return
	}

	if strings.TrimSpace(userQuery) == "" {
		errorResponse(w, http.StatusBadRequest, "query is required")
		return
	}

	result, err := h.aiCatalogService.QueryMenuWithAI(r.Context(), restaurantID, userQuery)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "AI data retrieval failed: "+err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":        "success",
		"restaurant_id": restaurantID,
		"retrieval":     result,
	})
}


