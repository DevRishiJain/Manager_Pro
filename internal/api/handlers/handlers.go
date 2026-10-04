package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"github.com/devrishijain/table-manager/pkg/crypto"

	objstore "github.com/devrishijain/table-manager/internal/adapter/storage"
	"github.com/devrishijain/table-manager/internal/api/middleware"
	"github.com/devrishijain/table-manager/internal/domain/audit"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
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
	expenseService    *service.ExpenseService
	inventoryService  *service.InventoryService
	objectStore       objstore.ObjectStore
	repo              storage.Repository
	webhookSecret     string
	jwtSecret         []byte
}

func (h *APIHandler) SetJWTSecret(secret []byte) {
	h.jwtSecret = secret
}

func (h *APIHandler) getJWTSecret() []byte {
	if len(h.jwtSecret) == 0 {
		return []byte("table-manager-staff-secret-key-32b")
	}
	return h.jwtSecret
}

func (h *APIHandler) SetAICatalogService(aiSvc *service.AICatalogService) {
	h.aiCatalogService = aiSvc
}

func (h *APIHandler) SetStaffService(staffSvc *service.StaffService) {
	h.staffService = staffSvc
}

func (h *APIHandler) SetExpenseService(expSvc *service.ExpenseService) {
	h.expenseService = expSvc
}

func (h *APIHandler) getExpenseService() *service.ExpenseService {
	if h.expenseService == nil {
		h.expenseService = service.NewExpenseService(h.repo)
	}
	return h.expenseService
}

func (h *APIHandler) SetInventoryService(invSvc *service.InventoryService) {
	h.inventoryService = invSvc
}

func (h *APIHandler) getInventoryService() *service.InventoryService {
	if h.inventoryService == nil {
		h.inventoryService = service.NewInventoryService(h.repo)
	}
	return h.inventoryService
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
	CustomerName      string `json:"customer_name"`
	CustomerPhone     string `json:"customer_phone"`
	PhoneNumber       string `json:"phone_number"`
	GuestCount        int    `json:"guest_count"`
	NumberOfGuests    int    `json:"no_of_guests"`
	VehicleNumber     string `json:"vehicle_number"`
	CarNumber         string `json:"car_number"`
	DeviceFingerprint string `json:"device_fingerprint"`
}

func (h *APIHandler) StartSession(w http.ResponseWriter, r *http.Request) {
	var req StartSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" && strings.TrimSpace(req.CustomerName) != "" {
		displayName = strings.TrimSpace(req.CustomerName)
	}
	if displayName == "" {
		displayName = "Guest Diner"
	}

	customerPhone := strings.TrimSpace(req.CustomerPhone)
	if customerPhone == "" {
		customerPhone = strings.TrimSpace(req.PhoneNumber)
	}

	guestCount := req.GuestCount
	if guestCount <= 0 && req.NumberOfGuests > 0 {
		guestCount = req.NumberOfGuests
	}
	if guestCount <= 0 {
		guestCount = 2
	}

	vehicleNumber := strings.TrimSpace(req.VehicleNumber)
	if vehicleNumber == "" {
		vehicleNumber = strings.TrimSpace(req.CarNumber)
	}

	sess, isNew, err := h.sessionService.StartSession(r.Context(), req.TableToken, req.DeviceToken, displayName, customerPhone, vehicleNumber, guestCount, req.DeviceFingerprint)
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

	// If order is placed by authenticated staff (waiter/manager), auto-accept it directly to the kitchen
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok {
		if acceptedOrd, err := h.orderService.AcceptOrder(r.Context(), ord.ID, claims.StaffID); err == nil && acceptedOrd != nil {
			ord = acceptedOrd
			firstOTP = nil
		}
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

	resp := map[string]interface{}{
		"session":  sess,
		"orders":   orders,
		"payments": payments,
	}

	if h.exitService != nil {
		if ep, err := h.exitService.GetExitPass(r.Context(), sessionID); err == nil && ep != nil {
			resp["exit_pass"] = ep
			resp["exit_otp"] = ep.RawOTP
		}
	}

	jsonResponse(w, http.StatusOK, resp)
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

type RequestAssistancePayload struct {
	Reason string `json:"reason"`
}

func (h *APIHandler) RequestAssistance(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid session ID")
		return
	}

	var req RequestAssistancePayload
	_ = json.NewDecoder(r.Body).Decode(&req)

	sess, err := h.sessionService.RequestAssistance(r.Context(), sessionID, req.Reason)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":                 "ASSISTANCE_REQUESTED",
		"assistance_reason":      sess.AssistanceReason,
		"assistance_requested_at": sess.AssistanceRequestedAt,
	})
}

func (h *APIHandler) DismissAssistance(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid session ID")
		return
	}

	_, err = h.sessionService.DismissAssistance(r.Context(), sessionID)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status": "DISMISSED",
	})
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

func (h *APIHandler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	orderIDStr := chi.URLParam(r, "orderId")
	if orderIDStr == "" {
		orderIDStr = chi.URLParam(r, "id")
	}
	orderID, err := uuid.Parse(orderIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid order ID")
		return
	}

	actorID := uuid.Nil
	actorType := audit.ActorTypeCustomer
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok {
		actorID = claims.StaffID
		actorType = audit.ActorTypeStaff
	}

	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	reason := req.Reason
	if reason == "" {
		reason = "Customer cancelled uncooked item before kitchen started cooking"
	}

	ord, err := h.orderService.CancelOrder(r.Context(), orderID, actorID, actorType, reason)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, ord)
}

type ConfirmPaymentRequest struct {
	PaymentID             *uuid.UUID     `json:"payment_id,omitempty"`
	SessionID             *uuid.UUID     `json:"session_id,omitempty"`
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
		errorResponse(w, http.StatusBadRequest, "invalid payload: "+err.Error())
		return
	}

	var sessionID uuid.UUID
	if req.SessionID != nil && *req.SessionID != uuid.Nil {
		sessionID = *req.SessionID
	} else if urlID := chi.URLParam(r, "id"); urlID != "" {
		if parsed, err := uuid.Parse(urlID); err == nil {
			sessionID = parsed
		}
	}

	if sessionID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "session_id is required")
		return
	}

	var paymentID uuid.UUID
	if req.PaymentID != nil {
		paymentID = *req.PaymentID
	}

	now := time.Now()
	confirmReq := payment.PaymentConfirmationRequest{
		PaymentID:             paymentID,
		SessionID:             sessionID,
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

	// When bill is settled, close all remaining active items:
	// - If not cooking (PLACED_UNVERIFIED, PLACED_VERIFIED, ACCEPTED): cancel the order and notify kitchen
	// - If cooking (PREPARING) or prepared (READY): force-close and record as food wastage, notify kitchen
	orders, _ := h.orderService.GetOrdersBySessionID(r.Context(), sessionID)
	for _, ord := range orders {
		switch ord.Status {
		case order.StatePlacedUnverified, order.StatePlacedVerified, order.StateAccepted:
			_, _ = h.orderService.CancelOrder(r.Context(), ord.ID, claims.StaffID, audit.ActorTypeStaff, "Bill settled: cancelled before kitchen cooking")
		case order.StatePreparing:
			_, _ = h.orderService.CancelOrder(r.Context(), ord.ID, claims.StaffID, audit.ActorTypeStaff, "Bill settled: force-closed active cooking item (food wastage)")
		case order.StateReady:
			_, _ = h.orderService.CancelOrder(r.Context(), ord.ID, claims.StaffID, audit.ActorTypeStaff, "Bill settled: force-closed prepared item (food wastage)")
		}
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

func (h *APIHandler) resolveRestaurantID(ctx context.Context, param string) uuid.UUID {
	trimmed := strings.TrimSpace(param)
	if trimmed == "" {
		return uuid.Nil
	}
	if parsed, err := uuid.Parse(trimmed); err == nil && parsed != uuid.Nil {
		return parsed
	}
	cleanSlug := strings.ToLower(strings.TrimPrefix(trimmed, "@"))
	if rest, err := h.repo.GetRestaurantBySlug(ctx, cleanSlug); err == nil && rest != nil {
		return rest.ID
	}
	return uuid.Nil
}

func (h *APIHandler) GetTableDashboard(w http.ResponseWriter, r *http.Request) {
	var restaurantID uuid.UUID
	if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
		restaurantID = h.resolveRestaurantID(r.Context(), restParam)
	}
	if restaurantID == uuid.Nil {
		if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
			restaurantID = claims.RestaurantID
		}
	}
	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required")
		return
	}

	tables, _ := h.repo.ListTables(r.Context(), restaurantID)
	activeSessions, _ := h.sessionService.ListActiveSessions(r.Context(), restaurantID)

	sessionByTable := make(map[uuid.UUID]session.DiningSession)
	for _, s := range activeSessions {
		sessionByTable[s.TableID] = s
	}

	var board []map[string]interface{}
	for _, t := range tables {
		var isOccupied bool
		var sessID, sessStatus, openedAt, custName, custPhone string
		var runMinor int64
		var guestCount int

		var assistReason string
		var assistAt *string
		var sessObj interface{}
		if s, ok := sessionByTable[t.ID]; ok {
			isOccupied = true
			sessID = s.ID.String()
			sessStatus = string(s.Status)
			openedAt = s.OpenedAt.Format(time.RFC3339)
			runMinor = s.RunningTotal.AmountMinorUnits
			custName = s.CustomerName
			custPhone = s.CustomerPhone
			guestCount = s.GuestCount
			sessObj = s
			assistReason = s.AssistanceReason
			if s.AssistanceRequestedAt != nil {
				formatted := s.AssistanceRequestedAt.Format(time.RFC3339)
				assistAt = &formatted
			}
		}

		cap := t.Capacity
		if cap <= 0 {
			cap = 4
		}

		entry := map[string]interface{}{
			"table_id":                t.ID,
			"table_number":            t.TableNumber,
			"capacity":                cap,
			"is_occupied":             isOccupied,
			"active_session_id":       sessID,
			"session_status":          sessStatus,
			"opened_at":               openedAt,
			"running_total_minor":     runMinor,
			"customer_name":           custName,
			"customer_phone":          custPhone,
			"guest_count":             guestCount,
			"assistance_reason":       assistReason,
			"assistance_requested_at": assistAt,
			"table":                   t,
			"session":                 sessObj,
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

func (h *APIHandler) StaffVerifyExit(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	sessionIDStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid session ID")
		return
	}

	var req struct {
		OTPCode string `json:"otp_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	resp := h.exitService.VerifyExit(r.Context(), sessionID, req.OTPCode, claims.StaffID)
	jsonResponse(w, http.StatusOK, resp)
}

// ---------------- Kitchen KDS Handlers ----------------

func (h *APIHandler) GetKitchenQueue(w http.ResponseWriter, r *http.Request) {
	var restaurantID uuid.UUID
	if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
		restaurantID = h.resolveRestaurantID(r.Context(), restParam)
	}
	if restaurantID == uuid.Nil {
		if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
			restaurantID = claims.RestaurantID
		}
	}

	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required either from staff authentication or query parameter")
		return
	}

	queue, err := h.orderService.ListKitchenQueue(r.Context(), restaurantID)
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

	var staffID uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.StaffID != uuid.Nil {
		staffID = claims.StaffID
	}

	var req UpdateKitchenStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid payload")
		return
	}

	ord, err := h.orderService.UpdateOrderStatus(r.Context(), orderID, req.Status, staffID)
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

func (h *APIHandler) GetExecutiveDashboardAnalytics(w http.ResponseWriter, r *http.Request) {
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

	analytics, err := h.analyticsService.GetExecutiveAnalytics(r.Context(), restaurantID, startDate, endDate)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, analytics)
}

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
	var restaurantID uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
		restaurantID = claims.RestaurantID
	} else if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
		if parsed, err := uuid.Parse(restParam); err == nil {
			restaurantID = parsed
		}
	}

	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required either from staff authentication or query parameter")
		return
	}

	cats, err := h.repo.ListCategories(r.Context(), restaurantID)
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
	var restaurantID uuid.UUID
	if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
		restaurantID = claims.RestaurantID
	} else if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
		if parsed, err := uuid.Parse(restParam); err == nil {
			restaurantID = parsed
		}
	}

	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required either from staff authentication or query parameter")
		return
	}

	items, err := h.repo.ListMenuItems(r.Context(), restaurantID)
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
		Price       int64     `json:"price"`
		CGSTRateBps int64     `json:"cgst_rate_bps"`
		SGSTRateBps int64     `json:"sgst_rate_bps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil || strings.TrimSpace(item.Name) == "" {
		errorResponse(w, http.StatusBadRequest, "valid menu item payload required")
		return
	}
	priceMinor := item.PriceMinor
	if priceMinor == 0 && item.Price != 0 {
		priceMinor = item.Price
	}
	cgst := item.CGSTRateBps
	if cgst == 0 {
		cgst = 250 // 2.5% CGST
	}
	sgst := item.SGSTRateBps
	if sgst == 0 {
		sgst = 250 // 2.5% SGST
	}
	mi := &restaurant.MenuItem{
		ID:           uuid.New(),
		RestaurantID: claims.RestaurantID,
		CategoryID:   item.CategoryID,
		Name:         strings.TrimSpace(item.Name),
		Description:  strings.TrimSpace(item.Description),
		Price:        money.New(priceMinor),
		IsAvailable:  true,
		HSNSACCode:   "996331",
		CGSTRateBps:  cgst,
		SGSTRateBps:  sgst,
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
	Identifier   string `json:"identifier"`
	Password     string `json:"password"`
	RestaurantID string `json:"restaurant_id,omitempty"`
}

func (h *APIHandler) CheckHandleAvailability(w http.ResponseWriter, r *http.Request) {
	handle := strings.TrimSpace(r.URL.Query().Get("handle"))
	handle = strings.ToLower(strings.TrimPrefix(handle, "@"))

	if handle == "" {
		errorResponse(w, http.StatusBadRequest, "handle query parameter is required")
		return
	}

	if len(handle) < 2 || len(handle) > 32 {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"handle":    handle,
			"available": false,
			"exists":    false,
			"message":   "Handle must be between 2 and 32 characters",
		})
		return
	}

	rest, err := h.repo.GetRestaurantBySlug(r.Context(), handle)
	exists := (err == nil && rest != nil)

	res := map[string]interface{}{
		"handle":    handle,
		"available": !exists,
		"exists":    exists,
	}

	if exists && rest != nil {
		res["restaurant"] = map[string]interface{}{
			"id":         rest.ID,
			"name":       rest.Name,
			"slug":       rest.Slug,
			"theme":      rest.Theme,
			"venue_type": rest.VenueType,
			"status":     rest.Status,
		}
	}

	jsonResponse(w, http.StatusOK, res)
}

func (h *APIHandler) LookupRestaurantPublic(w http.ResponseWriter, r *http.Request) {
	identifier := strings.TrimSpace(chi.URLParam(r, "identifier"))
	identifier = strings.TrimPrefix(identifier, "@")
	if identifier == "" {
		errorResponse(w, http.StatusBadRequest, "restaurant identifier is required")
		return
	}

	var rest *restaurant.Restaurant
	var err error

	if parsed, parseErr := uuid.Parse(identifier); parseErr == nil && parsed != uuid.Nil {
		rest, err = h.repo.GetRestaurantByID(r.Context(), parsed)
	}
	if rest == nil {
		rest, err = h.repo.GetRestaurantBySlug(r.Context(), identifier)
	}

	if err != nil || rest == nil {
		errorResponse(w, http.StatusNotFound, "restaurant not found")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"id":         rest.ID,
		"name":       rest.Name,
		"slug":       rest.Slug,
		"theme":      rest.Theme,
		"venue_type": rest.VenueType,
		"status":     rest.Status,
	})
}

type UpdateStaffPasswordRequest struct {
	Password string `json:"password"`
}

func (h *APIHandler) UpdateStaffPassword(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	targetStaffIDStr := chi.URLParam(r, "id")
	targetStaffID, err := uuid.Parse(targetStaffIDStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid staff id")
		return
	}

	var req UpdateStaffPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	targetStaff, err := h.repo.GetStaffByID(r.Context(), targetStaffID)
	if err != nil || targetStaff == nil {
		errorResponse(w, http.StatusNotFound, "staff member not found")
		return
	}

	if !claims.IsPlatform && targetStaff.RestaurantID != claims.RestaurantID {
		errorResponse(w, http.StatusForbidden, "cannot modify staff from another restaurant")
		return
	}

	if err := h.getStaffService().UpdateStaffPassword(r.Context(), targetStaffID, req.Password, claims.StaffID); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("Password updated for %s", targetStaff.Name),
	})
}

func (h *APIHandler) StaffLogin(w http.ResponseWriter, r *http.Request) {
	var req StaffLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	var restID *uuid.UUID
	trimmedRest := strings.TrimSpace(req.RestaurantID)
	if trimmedRest != "" {
		if parsed, err := uuid.Parse(trimmedRest); err == nil && parsed != uuid.Nil {
			restID = &parsed
		} else {
			// Resolve by slug
			if rest, err := h.repo.GetRestaurantBySlug(r.Context(), trimmedRest); err == nil && rest != nil {
				restID = &rest.ID
			}
		}
	}

	result, err := h.getStaffService().Authenticate(r.Context(), req.Identifier, req.Password, restID)
	if err != nil {
		errorResponse(w, http.StatusUnauthorized, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, result)
}

type StaffStartSessionRequest struct {
	TableID        string `json:"table_id,omitempty"`
	TableNumber    string `json:"table_number,omitempty"`
	TableToken     string `json:"table_token,omitempty"`
	CustomerName   string `json:"customer_name,omitempty"`
	CustomerPhone  string `json:"customer_phone,omitempty"`
	GuestCount     int    `json:"guest_count,omitempty"`
	VehicleNumber  string `json:"vehicle_number,omitempty"`
}

func (h *APIHandler) StaffStartSession(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	var req StaffStartSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	var targetTable *restaurant.Table
	// 1. Try finding by token
	if strings.TrimSpace(req.TableToken) != "" {
		if t, err := h.repo.GetTableByToken(r.Context(), strings.TrimSpace(req.TableToken)); err == nil && t != nil {
			targetTable = t
		}
	}
	// 2. Try finding by ID
	if targetTable == nil && strings.TrimSpace(req.TableID) != "" {
		if parsed, err := uuid.Parse(strings.TrimSpace(req.TableID)); err == nil {
			if t, err := h.repo.GetTableByID(r.Context(), parsed); err == nil && t != nil {
				targetTable = t
			}
		}
	}
	// 3. Try finding by table number within staff's restaurant (supports "1", "T1", "Table 1")
	if targetTable == nil && strings.TrimSpace(req.TableNumber) != "" {
		tables, _ := h.repo.ListTables(r.Context(), claims.RestaurantID)
		cleanReq := strings.TrimSpace(strings.ToLower(req.TableNumber))
		cleanReqNum := strings.TrimPrefix(strings.TrimPrefix(cleanReq, "table "), "t")

		for _, t := range tables {
			cleanT := strings.TrimSpace(strings.ToLower(t.TableNumber))
			cleanTNum := strings.TrimPrefix(strings.TrimPrefix(cleanT, "table "), "t")

			if cleanT == cleanReq || (cleanReqNum != "" && cleanTNum == cleanReqNum) {
				targetTable = &t
				break
			}
		}
	}

	if targetTable == nil {
		errorResponse(w, http.StatusBadRequest, "could not locate specified dining table")
		return
	}

	// Verify table belongs to staff's restaurant
	if targetTable.RestaurantID != claims.RestaurantID && !claims.IsPlatform {
		errorResponse(w, http.StatusForbidden, "table belongs to another restaurant tenant")
		return
	}

	name := strings.TrimSpace(req.CustomerName)
	if name == "" {
		name = "Walk-in Guest"
	}
	guestCount := req.GuestCount
	if guestCount <= 0 {
		guestCount = 2
	}

	if targetTable.Capacity > 0 && guestCount > targetTable.Capacity {
		errorResponse(w, http.StatusBadRequest, fmt.Sprintf("guest count (%d) exceeds table seating capacity (%d seats)", guestCount, targetTable.Capacity))
		return
	}

	sess, _, err := h.sessionService.StartSession(
		r.Context(),
		targetTable.TableToken,
		"STAFF_TERMINAL",
		name,
		strings.TrimSpace(req.CustomerPhone),
		strings.TrimSpace(req.VehicleNumber),
		guestCount,
		"STAFF_POS_"+claims.StaffID.String(),
	)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "failed to start session: "+err.Error())
		return
	}

	// Auto-verify since staff is physically seating the walk-in guests
	now := time.Now()
	sess.Status = session.StateOpenVerified
	sess.VerifiedAt = &now
	sess.VerifiedByStaffID = &claims.StaffID
	_ = h.repo.UpdateSession(r.Context(), sess)

	jsonResponse(w, http.StatusCreated, sess)
}

type PendingOrderResponse struct {
	Order         order.Order `json:"order"`
	TableNumber   string      `json:"table_number"`
	CustomerName  string      `json:"customer_name,omitempty"`
	CustomerPhone string      `json:"customer_phone,omitempty"`
	GuestCount    int         `json:"guest_count,omitempty"`
	VehicleNumber string      `json:"vehicle_number,omitempty"`
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
			tableNum = "Table 1"
		}
		custName := ord.CustomerName
		if custName == "" {
			custName = "Guest Diner"
		}
		guestCount := ord.GuestCount
		if guestCount <= 0 {
			guestCount = 1
		}
		res[i] = PendingOrderResponse{
			Order:         ord,
			TableNumber:   tableNum,
			CustomerName:  custName,
			CustomerPhone: ord.CustomerPhone,
			GuestCount:    guestCount,
			VehicleNumber: ord.VehicleNumber,
		}
	}

	jsonResponse(w, http.StatusOK, res)
}

func (h *APIHandler) GetRestaurantOrders(w http.ResponseWriter, r *http.Request) {
	var restaurantID uuid.UUID
	if restParam := r.URL.Query().Get("restaurant_id"); restParam != "" {
		restaurantID = h.resolveRestaurantID(r.Context(), restParam)
	}
	if restaurantID == uuid.Nil {
		if claims, ok := middleware.GetStaffClaimsFromContext(r.Context()); ok && claims.RestaurantID != uuid.Nil {
			restaurantID = claims.RestaurantID
		}
	}

	if restaurantID == uuid.Nil {
		errorResponse(w, http.StatusBadRequest, "restaurant_id is required")
		return
	}

	limit := 100
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if parsed, err := strconv.Atoi(lStr); err == nil && parsed > 0 {
			limit = parsed
		}
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

	orders, err := h.orderService.ListOrders(r.Context(), restaurantID, limit, startDate, endDate)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	if orders == nil {
		orders = []order.Order{}
	}

	jsonResponse(w, http.StatusOK, orders)
}

func parseDateFilter(val string, endOfDay bool) (*time.Time, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, val); err == nil {
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", val); err == nil {
		if endOfDay {
			t = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, time.UTC)
		} else {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		}
		return &t, nil
	}
	return nil, fmt.Errorf("invalid date format: %s", val)
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



// ---------------- Public Restaurant Onboarding & Tables ----------------

type OnboardRestaurantRequest struct {
	RestaurantName string `json:"restaurant_name"`
	VenueType      string `json:"venue_type"`
	Slug           string `json:"slug"`
	Theme          string `json:"theme"`
	LegalName      string `json:"legal_name"`
	GSTIN          string `json:"gstin"`
	Phone          string `json:"phone"`
	Email          string `json:"email"`
	Address        string `json:"address"`
	Cuisine        string `json:"cuisine"`
	Currency       string `json:"currency"`
	Admin          struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Phone    string `json:"phone"`
	} `json:"admin"`
	TableCount int `json:"table_count"`
	Tables     []struct {
		TableNumber string `json:"table_number"`
		TableToken  string `json:"table_token"`
		Capacity    int    `json:"capacity"`
	} `json:"tables"`
	MenuItems []struct {
		Name        string `json:"name"`
		Category    string `json:"category"`
		Price       int64  `json:"price"`
		PriceMinor  int64  `json:"price_minor"`
		Dietary     string `json:"dietary"`
		Description string `json:"description"`
	} `json:"menu_items"`
	Staff []struct {
		Name       string          `json:"name"`
		Email      string          `json:"email"`
		Role       restaurant.Role `json:"role"`
		EmployeeID string          `json:"employee_id"`
		Password   string          `json:"password"`
		Phone      string          `json:"phone"`
	} `json:"staff"`
}

func (h *APIHandler) OnboardRestaurant(w http.ResponseWriter, r *http.Request) {
	var req OnboardRestaurantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	name := strings.TrimSpace(req.RestaurantName)
	if name == "" {
		errorResponse(w, http.StatusBadRequest, "restaurant name is required")
		return
	}

	adminEmail := strings.ToLower(strings.TrimSpace(req.Admin.Email))
	if adminEmail == "" {
		errorResponse(w, http.StatusBadRequest, "admin email is required")
		return
	}

	adminPW := strings.TrimSpace(req.Admin.Password)
	if adminPW == "" {
		adminPW = "AdminPass123!"
	}

	restID := uuid.New()
	gstin := strings.TrimSpace(req.GSTIN)
	if gstin == "" {
		gstin = "07AABCG1234F1Z5"
	}
	curr := strings.TrimSpace(req.Currency)
	if curr == "" {
		curr = "INR"
	}

	now := time.Now()
	venueType := restaurant.VenueTypeFineDine
	switch strings.ToUpper(strings.TrimSpace(req.VenueType)) {
	case "HOTEL", "ROOM_SERVICE":
		venueType = restaurant.VenueTypeHotel
	case "DRIVE_IN", "CAR_O_BAR", "CAR_BAR", "DRIVE_THRU":
		venueType = restaurant.VenueTypeDriveIn
	case "CAFE", "QSR":
		venueType = restaurant.VenueTypeCafe
	default:
		venueType = restaurant.VenueTypeFineDine
	}

	slug := strings.ToLower(strings.TrimSpace(req.Slug))
	slug = strings.TrimPrefix(slug, "@")
	if slug == "" {
		slug = strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	}
	theme := strings.ToLower(strings.TrimSpace(req.Theme))
	if theme == "" {
		theme = "gold"
	}

	rest := &restaurant.Restaurant{
		ID:                    restID,
		Name:                  name,
		Slug:                  slug,
		Theme:                 theme,
		VenueType:             venueType,
		GSTIN:                 gstin,
		CommissionRateBps:     100,
		SettlementBankDetails: strings.TrimSpace(req.LegalName),
		Status:                restaurant.StatusActive,
		Timezone:              "Asia/Kolkata",
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	if err := h.repo.CreateRestaurant(r.Context(), rest); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to register restaurant: "+err.Error())
		return
	}

	// 1. Settings
	settings := restaurant.DefaultSettings(restID)
	_ = h.repo.UpdateSettings(r.Context(), &settings)

	// 2. Admin User
	hash, err := bcrypt.GenerateFromPassword([]byte(adminPW), bcrypt.DefaultCost)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to hash admin password")
		return
	}

	adminID := uuid.New()
	adminName := strings.TrimSpace(req.Admin.Name)
	if adminName == "" {
		adminName = "Restaurant Owner"
	}

	adminUser := &restaurant.StaffUser{
		ID:           adminID,
		RestaurantID: restID,
		EmployeeID:   "EMP-ADM-001",
		Name:         adminName,
		Phone:        strings.TrimSpace(req.Admin.Phone),
		Email:        adminEmail,
		PasswordHash: string(hash),
		Role:         restaurant.RoleRestaurantAdmin,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.repo.CreateStaff(r.Context(), adminUser); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to provision admin: "+err.Error())
		return
	}

	// 3. Tables / Rooms / Universal Drive-In Station
	createdTables := make([]restaurant.Table, 0)
	if len(req.Tables) > 0 {
		for _, t := range req.Tables {
			tblToken := strings.TrimSpace(t.TableToken)
			if tblToken == "" {
				prefix := "TBL"
				if venueType == restaurant.VenueTypeHotel {
					prefix = "ROOM"
				} else if venueType == restaurant.VenueTypeDriveIn {
					prefix = "DRIVE"
				}
				tblToken = fmt.Sprintf("%s-%s-%03d", prefix, restID.String()[:4], len(createdTables)+1)
			}
			tblNum := strings.TrimSpace(t.TableNumber)
			if tblNum == "" {
				if venueType == restaurant.VenueTypeHotel {
					tblNum = fmt.Sprintf("Room %d", 100+len(createdTables)+1)
				} else if venueType == restaurant.VenueTypeDriveIn {
					tblNum = fmt.Sprintf("Drive-In Bay %d", len(createdTables)+1)
				} else {
					tblNum = fmt.Sprintf("Table %d", len(createdTables)+1)
				}
			}
			tbl := &restaurant.Table{
				ID:           uuid.New(),
				RestaurantID: restID,
				TableNumber:  tblNum,
				TableToken:   tblToken,
				IsActive:     true,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			_ = h.repo.CreateTable(r.Context(), tbl)
			createdTables = append(createdTables, *tbl)
		}
	} else if venueType == restaurant.VenueTypeDriveIn {
		// Drive-In / Car-O-Bar: Single common static QR standee for vehicle ordering
		tblToken := fmt.Sprintf("DRIVE-%s", restID.String()[:4])
		tbl := &restaurant.Table{
			ID:           uuid.New(),
			RestaurantID: restID,
			TableNumber:  "Drive-In Universal",
			TableToken:   tblToken,
			IsActive:     true,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		_ = h.repo.CreateTable(r.Context(), tbl)
		createdTables = append(createdTables, *tbl)
	} else if venueType == restaurant.VenueTypeHotel {
		// Hotel: Rooms 101 to 100+count
		count := req.TableCount
		if count <= 0 {
			count = 10
		}
		for i := 1; i <= count; i++ {
			tblToken := fmt.Sprintf("ROOM-%s-%03d", restID.String()[:4], i)
			tbl := &restaurant.Table{
				ID:           uuid.New(),
				RestaurantID: restID,
				TableNumber:  fmt.Sprintf("Room %d", 100+i),
				TableToken:   tblToken,
				IsActive:     true,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			_ = h.repo.CreateTable(r.Context(), tbl)
			createdTables = append(createdTables, *tbl)
		}
	} else {
		// Fine Dine / Cafe: Tables 1 to count
		count := req.TableCount
		if count <= 0 {
			count = 8
		}
		for i := 1; i <= count; i++ {
			tblToken := fmt.Sprintf("TBL-%s-%03d", restID.String()[:4], i)
			tbl := &restaurant.Table{
				ID:           uuid.New(),
				RestaurantID: restID,
				TableNumber:  fmt.Sprintf("Table %d", i),
				TableToken:   tblToken,
				IsActive:     true,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			_ = h.repo.CreateTable(r.Context(), tbl)
			createdTables = append(createdTables, *tbl)
		}
	}

	// 4. Categories & Menu Items
	categoryMap := make(map[string]uuid.UUID)
	dishesCount := 0
	for i, item := range req.MenuItems {
		catName := strings.TrimSpace(item.Category)
		if catName == "" {
			catName = "Main Course"
		}
		catKey := strings.ToLower(catName)
		catID, exists := categoryMap[catKey]
		if !exists {
			catID = uuid.New()
			cat := &restaurant.MenuCategory{
				ID:           catID,
				RestaurantID: restID,
				Name:         catName,
				DisplayOrder: len(categoryMap) + 1,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			_ = h.repo.CreateCategory(r.Context(), cat)
			categoryMap[catKey] = catID
		}

		priceMinor := item.PriceMinor
		if priceMinor == 0 && item.Price > 0 {
			if item.Price < 10000 {
				priceMinor = item.Price * 100
			} else {
				priceMinor = item.Price
			}
		}
		if priceMinor <= 0 {
			priceMinor = 29900
		}

		desc := strings.TrimSpace(item.Description)
		if desc == "" {
			desc = "Chef special handcrafted dish"
		}

		mi := &restaurant.MenuItem{
			ID:           uuid.New(),
			RestaurantID: restID,
			CategoryID:   catID,
			Name:         strings.TrimSpace(item.Name),
			Description:  desc,
			Price:        money.New(priceMinor),
			IsAvailable:  true,
			HSNSACCode:   "996331",
			CGSTRateBps:  250,
			SGSTRateBps:  250,
			CreatedAt:    now.Add(time.Duration(i) * time.Millisecond),
			UpdatedAt:    now,
		}
		_ = h.repo.CreateMenuItem(r.Context(), mi)
		dishesCount++
	}

	// 5. Staff Roster
	staffCount := 0
	for _, st := range req.Staff {
		stEmail := strings.ToLower(strings.TrimSpace(st.Email))
		if stEmail == "" {
			continue
		}
		stPW := strings.TrimSpace(st.Password)
		if stPW == "" {
			stPW = "password123"
		}
		stHash, err := bcrypt.GenerateFromPassword([]byte(stPW), bcrypt.DefaultCost)
		if err != nil {
			continue
		}

		empID := strings.TrimSpace(st.EmployeeID)
		if empID == "" {
			prefix := "WTR"
			if st.Role == restaurant.RoleKitchen {
				prefix = "KIT"
			} else if st.Role == restaurant.RoleCashier {
				prefix = "CSH"
			} else if st.Role == restaurant.RoleManager {
				prefix = "MGR"
			} else if st.Role == restaurant.RoleGuard {
				prefix = "GRD"
			}
			empID = fmt.Sprintf("EMP-%s-%03d", prefix, staffCount+1)
		}

		stName := strings.TrimSpace(st.Name)
		if stName == "" {
			stName = "Floor Operator"
		}

		stUser := &restaurant.StaffUser{
			ID:           uuid.New(),
			RestaurantID: restID,
			EmployeeID:   empID,
			Name:         stName,
			Phone:        strings.TrimSpace(st.Phone),
			Email:        stEmail,
			PasswordHash: string(stHash),
			Role:         st.Role,
			IsActive:     true,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		_ = h.repo.CreateStaff(r.Context(), stUser)
		staffCount++
	}

	// 6. Complete Onboarding
	_ = h.repo.UpdateOnboarding(r.Context(), &restaurant.RestaurantOnboarding{
		RestaurantID:   restID,
		CurrentStep:    restaurant.StepGoLive,
		StepsCompleted: []restaurant.OnboardingStep{restaurant.StepProfileSetup, restaurant.StepTableSetup, restaurant.StepMenuSetup, restaurant.StepStaffSetup, restaurant.StepPaymentSetup, restaurant.StepPolicySetup, restaurant.StepTestOrder, restaurant.StepGoLive},
		StartedAt:      now,
		CompletedAt:    &now,
		UpdatedAt:      now,
	})

	// 7. Generate Admin JWT Token
	token, _ := crypto.GenerateFullStaffJWT(
		h.getJWTSecret(),
		adminUser.ID,
		restID,
		adminUser.EmployeeID,
		adminUser.Name,
		string(adminUser.Role),
		false,
		7*24*time.Hour,
	)

	jsonResponse(w, http.StatusCreated, map[string]interface{}{
		"token":           token,
		"restaurant_id":   restID,
		"restaurant_name": rest.Name,
		"venue_type":      rest.VenueType,
		"restaurant":      rest,
		"admin": map[string]interface{}{
			"id":          adminUser.ID,
			"name":        adminUser.Name,
			"email":       adminUser.Email,
			"employee_id": adminUser.EmployeeID,
			"role":        adminUser.Role,
		},
		"tables_count": len(createdTables),
		"dishes_count": dishesCount,
		"staff_count":  staffCount,
		"tables":       createdTables,
	})
}

func (h *APIHandler) CreateTable(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	var req struct {
		TableNumber string `json:"table_number"`
		TableToken  string `json:"table_token"`
		Capacity    int    `json:"capacity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.TableNumber) == "" {
		errorResponse(w, http.StatusBadRequest, "valid table number required")
		return
	}

	token := strings.TrimSpace(req.TableToken)
	if token == "" {
		randToken, err := crypto.GenerateRandomToken(16)
		if err != nil {
			token = fmt.Sprintf("TBL-%s-%d", claims.RestaurantID.String()[:4], time.Now().UnixNano()%10000)
		} else {
			token = randToken
		}
	}

	now := time.Now()
	tbl := &restaurant.Table{
		ID:           uuid.New(),
		RestaurantID: claims.RestaurantID,
		TableNumber:  strings.TrimSpace(req.TableNumber),
		TableToken:   token,
		Capacity:     req.Capacity,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := h.repo.CreateTable(r.Context(), tbl); err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusCreated, tbl)
}

func (h *APIHandler) ListTables(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetStaffClaimsFromContext(r.Context())
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "unauthorized staff")
		return
	}

	tables, err := h.repo.ListTables(r.Context(), claims.RestaurantID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, tables)
}
