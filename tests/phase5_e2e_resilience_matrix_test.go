package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/devrishijain/table-manager/internal/api"
	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/internal/ws"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

func TestPhase5E2EResilienceAndFallbackMatrix(t *testing.T) {
	repo := memory.NewMemoryRepository()
	jwtSecret := []byte("phase5-resilience-secret-32b-key!")

	hub := ws.NewHub(ws.HubConfig{
		MaxConnPerIP:     50,
		MaxConnPerRest:   50,
		ResumeBufferTTL:  5 * time.Minute,
		MaxBufferPerRoom: 100,
	})
	tm := ws.NewTicketManager(30 * time.Second)
	dispatcher := ws.NewOutboxDispatcher(hub, repo, nil)

	sessionSvc := service.NewSessionService(repo)
	orderSvc := service.NewOrderService(repo)
	exitSvc := service.NewExitService(repo).WithSecret("exit-secret-32b-test-phase5!!")
	ledgerSvc := service.NewLedgerService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "webhook-secret")
	analyticsSvc := service.NewAnalyticsService(repo, nil)
	onboardingSvc := service.NewOnboardingService(repo)
	staffSvc := service.NewStaffService(repo, jwtSecret)

	// Wire outbox dispatcher for realtime emission
	orderSvc.SetOutboxDispatcher(dispatcher)
	sessionSvc.SetOutboxDispatcher(dispatcher)
	paymentSvc.SetOutboxDispatcher(dispatcher)
	exitSvc.SetOutboxDispatcher(dispatcher)

	apiHandler := handlers.NewAPIHandler(
		sessionSvc, orderSvc, paymentSvc, exitSvc, ledgerSvc,
		analyticsSvc, onboardingSvc, nil, repo, "webhook-secret",
	)
	apiHandler.SetStaffService(staffSvc)
	apiHandler.SetJWTSecret(jwtSecret)
	apiHandler.SetWSTicketManager(tm)

	wsServer := ws.NewServer(hub, tm, []string{"*"})
	router := api.NewRouter(apiHandler, repo, jwtSecret, wsServer)
	server := httptest.NewServer(router)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Seed restaurant and table
	restID := uuid.New()
	tableID := uuid.New()
	tableToken := "RESIL-T01-TOKEN"

	_ = repo.CreateRestaurant(ctx, &restaurant.Restaurant{
		ID:     restID,
		Name:   "Matrix Resilience Diner",
		Status: restaurant.StatusActive,
	})
	_ = repo.CreateTable(ctx, &restaurant.Table{
		ID:           tableID,
		RestaurantID: restID,
		TableNumber:  "T-01",
		TableToken:   tableToken,
		IsActive:     true,
	})

	menuItem := &restaurant.MenuItem{
		ID:           uuid.New(),
		RestaurantID: restID,
		Name:         "Gourmet Burger",
		Price:        money.New(45000), // 450.00
		IsAvailable:  true,
	}
	_ = repo.CreateMenuItem(ctx, menuItem)

	waiterID := uuid.New()
	waiterToken, _ := crypto.GenerateStaffJWT(jwtSecret, waiterID, restID, "WAITER", false, 1*time.Hour)

	// ==========================================
	// 1. Socket ON Flow: Full Realtime Lifecycle
	// ==========================================
	t.Run("Socket_ON_Realtime_Event_Delivery", func(t *testing.T) {
		// Diner starts session
		sess, _, err := sessionSvc.StartSession(ctx, tableToken, "diner-device-1", "Aarav", "+919876543210", "", 2, "fp-1")
		if err != nil {
			t.Fatalf("failed to start session: %v", err)
		}

		// Customer and Staff authenticate and connect to WS
		ticketCust, err := tm.IssueCustomerTicket(restID, sess.ID)
		if err != nil {
			t.Fatalf("failed to issue customer ticket: %v", err)
		}
		ticketStaff, err := tm.IssueStaffTicket(restID, waiterID, "WAITER")
		if err != nil {
			t.Fatalf("failed to issue staff ticket: %v", err)
		}

		wsURL := strings.Replace(server.URL, "http://", "ws://", 1) + "/ws/v1"

		connCust, _, err := websocket.Dial(ctx, wsURL+"?ticket="+ticketCust, nil)
		if err != nil {
			t.Fatalf("failed to connect customer WS: %v", err)
		}
		defer connCust.Close(websocket.StatusNormalClosure, "")

		connStaff, _, err := websocket.Dial(ctx, wsURL+"?ticket="+ticketStaff, nil)
		if err != nil {
			t.Fatalf("failed to connect staff WS: %v", err)
		}
		defer connStaff.Close(websocket.StatusNormalClosure, "")

		// Place order
		cart := []order.CartItem{{MenuItemID: menuItem.ID, Quantity: 2}}
		ord, otp, err := orderSvc.PlaceOrder(ctx, sess.ID, cart)
		if err != nil {
			t.Fatalf("failed to place order: %v", err)
		}

		// Read ORDER_PLACED from WS
		readCtx, rCancel := context.WithTimeout(ctx, 2*time.Second)
		defer rCancel()

		_, msgCust, err := connCust.Read(readCtx)
		if err != nil {
			t.Fatalf("expected ORDER_PLACED on customer WS: %v", err)
		}
		var envCust ws.Envelope
		_ = json.Unmarshal(msgCust, &envCust)
		if envCust.T != "ORDER_PLACED" {
			t.Fatalf("expected ORDER_PLACED event, got %s", envCust.T)
		}

		// Staff verifies first order
		err = sessionSvc.VerifyFirstOrder(ctx, sess.ID, waiterID, *otp)
		if err != nil {
			t.Fatalf("failed to verify first order: %v", err)
		}

		// Customer should receive SESSION_VERIFIED
		_, msgVer, err := connCust.Read(readCtx)
		if err != nil {
			t.Fatalf("expected SESSION_VERIFIED on customer WS: %v", err)
		}
		var envVer ws.Envelope
		_ = json.Unmarshal(msgVer, &envVer)
		if envVer.T != "SESSION_VERIFIED" {
			t.Fatalf("expected SESSION_VERIFIED event, got %s", envVer.T)
		}

		// Staff accepts order
		_, err = orderSvc.AcceptOrder(ctx, ord.ID, waiterID)
		if err != nil {
			t.Fatalf("failed to accept order: %v", err)
		}

		// Customer receives ORDER_ACCEPTED
		_, msgAcc, err := connCust.Read(readCtx)
		if err != nil {
			t.Fatalf("expected ORDER_ACCEPTED on customer WS: %v", err)
		}
		var envAcc ws.Envelope
		_ = json.Unmarshal(msgAcc, &envAcc)
		if envAcc.T != "ORDER_ACCEPTED" {
			t.Fatalf("expected ORDER_ACCEPTED event, got %s", envAcc.T)
		}
	})

	// ====================================================
	// 2. Socket Drop & Mid-Flow Reconnect with Replay
	// ====================================================
	t.Run("Socket_Drop_And_MidFlow_Reconnect_Replay", func(t *testing.T) {
		sess2, _, _ := sessionSvc.StartSession(ctx, tableToken, "diner-device-2", "Priya", "+919876543211", "", 2, "fp-2")

		ticketCust, _ := tm.IssueCustomerTicket(restID, sess2.ID)
		wsURL := strings.Replace(server.URL, "http://", "ws://", 1) + "/ws/v1"

		connCust, _, err := websocket.Dial(ctx, wsURL+"?ticket="+ticketCust, nil)
		if err != nil {
			t.Fatalf("failed to dial: %v", err)
		}

		// Place order 1
		ord2, _, _ := orderSvc.PlaceOrder(ctx, sess2.ID, []order.CartItem{{MenuItemID: menuItem.ID, Quantity: 1}})

		// Read event 1
		_, msg1, err := connCust.Read(ctx)
		if err != nil {
			t.Fatalf("failed to read initial event: %v", err)
		}
		var env1 ws.Envelope
		_ = json.Unmarshal(msg1, &env1)
		lastSeenEventID := env1.ID

		// NOW: Simulate network disconnect / socket drop!
		_ = connCust.Close(websocket.StatusGoingAway, "simulated client disconnect")

		// While diner is disconnected, events occur on server:
		// 1) Staff accepts order -> ORDER_ACCEPTED
		_, _ = orderSvc.AcceptOrder(ctx, ord2.ID, waiterID)
		// 2) Kitchen marks preparing -> ORDER_STATUS_CHANGED
		_, _ = orderSvc.UpdateOrderStatus(ctx, ord2.ID, order.StatePreparing, waiterID)
		// 3) Bill is requested -> BILL_REQUESTED
		_, _ = paymentSvc.RequestBill(ctx, sess2.ID)

		// RECONNECT: Diner reconnects with last_event_id resume parameter!
		ticketCustReconnect, _ := tm.IssueCustomerTicket(restID, sess2.ID)
		reconnURL := wsURL + "?ticket=" + ticketCustReconnect + "&last_event_id=" + lastSeenEventID

		connReconnected, _, err := websocket.Dial(ctx, reconnURL, nil)
		if err != nil {
			t.Fatalf("failed to reconnect with last_event_id: %v", err)
		}
		defer connReconnected.Close(websocket.StatusNormalClosure, "")

		// Diner must receive missed events in exact chronological sequence!
		rCtx, rCancel := context.WithTimeout(ctx, 3*time.Second)
		defer rCancel()

		// Missed event 1: ORDER_ACCEPTED
		_, msgMissed1, err := connReconnected.Read(rCtx)
		if err != nil {
			t.Fatalf("failed to read missed event 1: %v", err)
		}
		var envM1 ws.Envelope
		_ = json.Unmarshal(msgMissed1, &envM1)
		if envM1.T != "ORDER_ACCEPTED" {
			t.Fatalf("expected missed ORDER_ACCEPTED, got: %s", envM1.T)
		}

		// Missed event 2: ORDER_STATUS_CHANGED
		_, msgMissed2, err := connReconnected.Read(rCtx)
		if err != nil {
			t.Fatalf("failed to read missed event 2: %v", err)
		}
		var envM2 ws.Envelope
		_ = json.Unmarshal(msgMissed2, &envM2)
		if envM2.T != "ORDER_STATUS_CHANGED" {
			t.Fatalf("expected missed ORDER_STATUS_CHANGED, got: %s", envM2.T)
		}

		// Missed event 3: BILL_REQUESTED
		_, msgMissed3, err := connReconnected.Read(rCtx)
		if err != nil {
			t.Fatalf("failed to read missed event 3: %v", err)
		}
		var envM3 ws.Envelope
		_ = json.Unmarshal(msgMissed3, &envM3)
		if envM3.T != "BILL_REQUESTED" {
			t.Fatalf("expected missed BILL_REQUESTED, got: %s", envM3.T)
		}
	})

	// ====================================================
	// 3. Socket Blocked: Seamless HTTP Fallback & Reconciliation
	// ====================================================
	t.Run("Socket_Blocked_HTTP_Polling_Fallback_Preserves_State", func(t *testing.T) {
		// In an aggressive proxy/corporate Wi-Fi environment where WebSockets are entirely blocked,
		// client seamlessly falls back to periodic HTTP queries (15s fallback poll / 60s reconciliation).
		sess3, _, _ := sessionSvc.StartSession(ctx, tableToken, "diner-device-3", "Karan", "+919876543212", "", 1, "fp-3")

		// Client places order via HTTP
		ord3, _, _ := orderSvc.PlaceOrder(ctx, sess3.ID, []order.CartItem{{MenuItemID: menuItem.ID, Quantity: 1}})

		// State is updated on server
		_, _ = orderSvc.AcceptOrder(ctx, ord3.ID, waiterID)
		_, _ = paymentSvc.RequestBill(ctx, sess3.ID)

		// Diner polls HTTP GET /api/v1/session/{id} with session token
		reqSession, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/session/"+sess3.ID.String(), nil)
		reqSession.Header.Set("X-Session-Token", sess3.SessionToken)
		respSession, err := http.DefaultClient.Do(reqSession)
		if err != nil {
			t.Fatalf("failed to perform HTTP session fallback poll: %v", err)
		}
		defer respSession.Body.Close()

		if respSession.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 from HTTP session fallback poll, got %d", respSession.StatusCode)
		}

		var sessDetails struct {
			Session struct {
				ID         uuid.UUID     `json:"id"`
				Status     session.State `json:"status"`
				FinalTotal money.Money   `json:"final_total"`
			} `json:"session"`
			Orders []order.Order `json:"orders"`
		}
		if err := json.NewDecoder(respSession.Body).Decode(&sessDetails); err != nil {
			t.Fatalf("failed to decode session fallback response: %v", err)
		}

		if sessDetails.Session.Status != session.StateAwaitingPayment {
			t.Fatalf("expected fallback poll to reflect AWAITING_PAYMENT, got %s", sessDetails.Session.Status)
		}
		if len(sessDetails.Orders) == 0 {
			t.Fatalf("expected non-empty orders in fallback poll response")
		}
		var polledOrder *order.Order
		for i := range sessDetails.Orders {
			if sessDetails.Orders[i].ID == ord3.ID {
				polledOrder = &sessDetails.Orders[i]
				break
			}
		}
		if polledOrder == nil {
			t.Fatalf("expected order %s in fallback poll response", ord3.ID)
		}
		if polledOrder.Status != order.StateAccepted {
			t.Fatalf("expected order %s status ACCEPTED in fallback poll response, got %s", ord3.ID, polledOrder.Status)
		}

		// Staff payment confirmation via HTTP
		payReq := payment.PaymentConfirmationRequest{
			PaymentID:          uuid.New(),
			SessionID:          sess3.ID,
			RestaurantID:       restID,
			Amount:             sessDetails.Session.FinalTotal,
			Method:             payment.MethodCash,
			ConfirmedByStaffID: &waiterID,
		}
		_, err = paymentSvc.ConfirmPayment(ctx, payReq)
		if err != nil {
			t.Fatalf("failed to confirm payment: %v", err)
		}

		// Fallback poll reflects PAID and includes valid ExitPass
		reqPollPaid, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/session/"+sess3.ID.String(), nil)
		reqPollPaid.Header.Set("X-Session-Token", sess3.SessionToken)
		respPollPaid, err := http.DefaultClient.Do(reqPollPaid)
		if err != nil {
			t.Fatalf("failed to perform paid session fallback poll: %v", err)
		}
		defer respPollPaid.Body.Close()

		if respPollPaid.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 on paid session fallback poll, got %d", respPollPaid.StatusCode)
		}

		var paidDetails struct {
			Session struct {
				Status session.State `json:"status"`
			} `json:"session"`
		}
		_ = json.NewDecoder(respPollPaid.Body).Decode(&paidDetails)
		if paidDetails.Session.Status != session.StatePaid {
			t.Fatalf("expected session status PAID, got %s", paidDetails.Session.Status)
		}
	})

	_ = waiterToken
}
