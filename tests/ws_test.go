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
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/internal/ws"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

func setupWSTestEnv(t *testing.T) (*httptest.Server, *ws.Hub, *ws.TicketManager, *ws.OutboxDispatcher, *memory.MemoryRepository, []byte) {
	t.Helper()
	repo := memory.NewMemoryRepository()
	jwtSecret := []byte("super-secure-ws-test-secret-32b!")

	sessionSvc := service.NewSessionService(repo)
	orderSvc := service.NewOrderService(repo)
	exitSvc := service.NewExitService(repo)
	ledgerSvc := service.NewLedgerService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "webhook-secret")
	analyticsSvc := service.NewAnalyticsService(repo, nil)
	onboardingSvc := service.NewOnboardingService(repo)
	staffSvc := service.NewStaffService(repo, jwtSecret)

	apiHandler := handlers.NewAPIHandler(
		sessionSvc, orderSvc, paymentSvc, exitSvc, ledgerSvc,
		analyticsSvc, onboardingSvc, nil, repo, "webhook-secret",
	)
	apiHandler.SetStaffService(staffSvc)
	apiHandler.SetJWTSecret(jwtSecret)

	hub := ws.NewHub(ws.HubConfig{
		MaxConnPerIP:     100,
		MaxConnPerRest:   100,
		ResumeBufferTTL:  5 * time.Minute,
		MaxBufferPerRoom: 50,
	})
	tm := ws.NewTicketManager(5 * time.Second)
	apiHandler.SetWSTicketManager(tm)

	wsServer := ws.NewServer(hub, tm, []string{"*"})
	dispatcher := ws.NewOutboxDispatcher(hub, repo, nil)

	router := api.NewRouter(apiHandler, repo, jwtSecret, wsServer)
	server := httptest.NewServer(router)

	return server, hub, tm, dispatcher, repo, jwtSecret
}

func TestWSTicketIssuanceAndSingleUse(t *testing.T) {
	server, _, tm, _, repo, jwtSecret := setupWSTestEnv(t)
	defer server.Close()

	restID := uuid.New()
	staffID := uuid.New()
	staffToken, err := crypto.GenerateStaffJWT(jwtSecret, staffID, restID, "WAITER", false, 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to generate staff token: %v", err)
	}

	// 1. Request ticket with valid staff token
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/ws/ticket", nil)
	req.Header.Set("Authorization", "Bearer "+staffToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to request ticket: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var ticketBody struct {
		Ticket    string `json:"ticket"`
		ExpiresIn int    `json:"expires_in"`
		Role      string `json:"role"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ticketBody); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if ticketBody.Ticket == "" {
		t.Fatal("expected non-empty ticket")
	}
	if ticketBody.Role != "WAITER" {
		t.Fatalf("expected WAITER role, got %s", ticketBody.Role)
	}

	// 2. Redeem ticket once - should succeed
	claims, err := tm.RedeemTicket(ticketBody.Ticket)
	if err != nil {
		t.Fatalf("first redemption should succeed, got: %v", err)
	}
	if claims.StaffID != staffID {
		t.Fatalf("expected staffID %s, got %s", staffID, claims.StaffID)
	}

	// 3. Redeem ticket second time - MUST FAIL (Single-use enforcement)
	_, err = tm.RedeemTicket(ticketBody.Ticket)
	if err == nil {
		t.Fatal("second redemption MUST fail for single-use ticket")
	}

	// 4. Test Customer Ticket with dining session
	sessID := uuid.New()
	testSession := &session.DiningSession{
		ID:           sessID,
		RestaurantID: restID,
		TableID:      uuid.New(),
		Status:       session.StateOpen,
		SessionToken: "sess_tok_abc_123",
		GuestCount:   2,
	}
	_ = repo.CreateSession(context.Background(), testSession)

	custReq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/ws/ticket", nil)
	custReq.Header.Set("X-Session-Token", "sess_tok_abc_123")
	custResp, err := http.DefaultClient.Do(custReq)
	if err != nil {
		t.Fatalf("failed to request customer ticket: %v", err)
	}
	defer custResp.Body.Close()

	if custResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for customer ticket, got %d", custResp.StatusCode)
	}

	var custTicketBody struct {
		Ticket string `json:"ticket"`
		Role   string `json:"role"`
	}
	_ = json.NewDecoder(custResp.Body).Decode(&custTicketBody)
	if custTicketBody.Role != "CUSTOMER" {
		t.Fatalf("expected CUSTOMER role, got %s", custTicketBody.Role)
	}
}

func TestWebSocketConnectAndRoomIsolation(t *testing.T) {
	server, hub, tm, _, _, _ := setupWSTestEnv(t)
	defer server.Close()

	restID := uuid.New()
	sessionA := uuid.New()
	sessionB := uuid.New()

	ticketA, err := tm.IssueCustomerTicket(restID, sessionA)
	if err != nil {
		t.Fatalf("failed to issue ticket A: %v", err)
	}
	ticketB, err := tm.IssueCustomerTicket(restID, sessionB)
	if err != nil {
		t.Fatalf("failed to issue ticket B: %v", err)
	}

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) + "/ws/v1"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect Client A (subscribed to session:sessionA)
	connA, _, err := websocket.Dial(ctx, wsURL+"?ticket="+ticketA, nil)
	if err != nil {
		t.Fatalf("failed to dial connA: %v", err)
	}
	defer connA.Close(websocket.StatusNormalClosure, "")

	// Connect Client B (subscribed to session:sessionB)
	connB, _, err := websocket.Dial(ctx, wsURL+"?ticket="+ticketB, nil)
	if err != nil {
		t.Fatalf("failed to dial connB: %v", err)
	}
	defer connB.Close(websocket.StatusNormalClosure, "")

	// Give a moment for registration
	time.Sleep(50 * time.Millisecond)

	if hub.ActiveClientCount() != 2 {
		t.Fatalf("expected 2 active clients in hub, got %d", hub.ActiveClientCount())
	}

	// Broadcast an event to sessionA
	roomA := "session:" + sessionA.String()
	_, err = hub.Broadcast(roomA, "ORDER_PLACED", map[string]string{
		"session_id": sessionA.String(),
		"status":     "PLACED",
	})
	if err != nil {
		t.Fatalf("failed to broadcast: %v", err)
	}

	// Client A must receive the message
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()

	_, msgA, err := connA.Read(readCtx)
	if err != nil {
		t.Fatalf("connA should have received message, got error: %v", err)
	}

	var envA ws.Envelope
	if err := json.Unmarshal(msgA, &envA); err != nil {
		t.Fatalf("failed to unmarshal envA: %v", err)
	}
	if envA.T != "ORDER_PLACED" || envA.Room != roomA {
		t.Fatalf("unexpected envelope on connA: %+v", envA)
	}

	// Client B MUST NOT receive anything (Cross-session isolation)
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer shortCancel()

	_, _, errB := connB.Read(shortCtx)
	if errB == nil {
		t.Fatal("connB received message intended ONLY for connA! Cross-room leak!")
	}
}

func TestResumeBufferReplay(t *testing.T) {
	server, hub, tm, _, _, _ := setupWSTestEnv(t)
	defer server.Close()

	restID := uuid.New()
	sessID := uuid.New()
	room := "session:" + sessID.String()

	// Broadcast 3 events before client connects
	env1, _ := hub.Broadcast(room, "EVENT_1", map[string]int{"num": 1})
	env2, _ := hub.Broadcast(room, "EVENT_2", map[string]int{"num": 2})
	env3, _ := hub.Broadcast(room, "EVENT_3", map[string]int{"num": 3})

	// Client missed EVENT_2 and EVENT_3, so client passes last_event_id = env1.ID
	ticket, err := tm.IssueCustomerTicket(restID, sessID)
	if err != nil {
		t.Fatalf("failed to issue ticket: %v", err)
	}

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) + "/ws/v1"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL+"?ticket="+ticket+"&last_event_id="+env1.ID, nil)
	if err != nil {
		t.Fatalf("failed to connect with last_event_id: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read first replayed event - should be EVENT_2
	_, msg1, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read replayed event 2: %v", err)
	}
	var replayed2 ws.Envelope
	_ = json.Unmarshal(msg1, &replayed2)
	if replayed2.ID != env2.ID || replayed2.T != "EVENT_2" {
		t.Fatalf("expected EVENT_2 with ID %s, got %+v", env2.ID, replayed2)
	}

	// Read second replayed event - should be EVENT_3
	_, msg2, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read replayed event 3: %v", err)
	}
	var replayed3 ws.Envelope
	_ = json.Unmarshal(msg2, &replayed3)
	if replayed3.ID != env3.ID || replayed3.T != "EVENT_3" {
		t.Fatalf("expected EVENT_3 with ID %s, got %+v", env3.ID, replayed3)
	}
}

func TestOutboxDispatcherSweep(t *testing.T) {
	_, hub, tm, dispatcher, repo, _ := setupWSTestEnv(t)

	restID := uuid.New()
	sessID := uuid.New()
	room := "session:" + sessID.String()

	// Store outbox event without immediate dispatcher to test asynchronous sweep
	evt1, err := ws.PublishEvent(context.Background(), repo, nil, restID, "ORDER_CREATED", room, sessID.String(), map[string]string{
		"session_id": sessID.String(),
		"action":     "ORDER_CREATED",
	})
	if err != nil {
		t.Fatalf("failed to publish outbox event: %v", err)
	}

	// Dispatch pending events
	dispatched := dispatcher.DispatchPending(context.Background())
	if dispatched != 1 {
		t.Fatalf("expected 1 event dispatched, got %d", dispatched)
	}

	// Check that the hub's room buffer now has the event
	ticket, _ := tm.IssueCustomerTicket(restID, sessID)
	claims, _ := tm.RedeemTicket(ticket)
	mockClient := ws.NewClient(hub, nil, claims, "127.0.0.1")

	replayedCount := hub.ReplayAfter(mockClient, room, "")
	if replayedCount != 1 {
		t.Fatalf("expected 1 event replayed from outbox dispatch, got %d", replayedCount)
	}

	select {
	case msg := <-mockClient.Send:
		var env ws.Envelope
		_ = json.Unmarshal(msg, &env)
		if env.ID != evt1.ID.String() || env.T != "ORDER_CREATED" {
			t.Fatalf("unexpected envelope content: %+v", env)
		}
	default:
		t.Fatal("expected message in mockClient.Send channel")
	}
}
