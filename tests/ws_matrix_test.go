package tests

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/devrishijain/table-manager/internal/api"
	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/internal/ws"
	"github.com/google/uuid"
)

func TestRealtimeEmissionMatrixAndZeroTenantLeakage(t *testing.T) {
	repo := memory.NewMemoryRepository()
	jwtSecret := []byte("secret-for-emission-matrix-32b!")

	hub := ws.NewHub()
	tm := ws.NewTicketManager(30 * time.Second)
	dispatcher := ws.NewOutboxDispatcher(hub, repo, nil)

	sessionSvc := service.NewSessionService(repo)
	orderSvc := service.NewOrderService(repo)
	exitSvc := service.NewExitService(repo)
	ledgerSvc := service.NewLedgerService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "webhook-secret")
	analyticsSvc := service.NewAnalyticsService(repo, nil)
	onboardingSvc := service.NewOnboardingService(repo)
	staffSvc := service.NewStaffService(repo, jwtSecret)

	// Wire outbox dispatcher
	orderSvc.SetOutboxDispatcher(dispatcher)
	sessionSvc.SetOutboxDispatcher(dispatcher)
	paymentSvc.SetOutboxDispatcher(dispatcher)

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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Setup Restaurant 1 and Restaurant 2
	rest1 := &restaurant.Restaurant{ID: uuid.New(), Name: "Bistro 1", Slug: "bistro1"}
	rest2 := &restaurant.Restaurant{ID: uuid.New(), Name: "Bistro 2", Slug: "bistro2"}
	_ = repo.CreateRestaurant(ctx, rest1)
	_ = repo.CreateRestaurant(ctx, rest2)

	menuItem1 := &restaurant.MenuItem{
		ID:           uuid.New(),
		RestaurantID: rest1.ID,
		Name:         "Pasta 1",
		Price:        money.New(50000), // 500.00
		IsAvailable:  true,
	}
	_ = repo.CreateMenuItem(ctx, menuItem1)

	sess1 := &session.DiningSession{
		ID:           uuid.New(),
		RestaurantID: rest1.ID,
		TableID:      uuid.New(),
		Status:       session.StateOpen,
		SessionToken: "token_sess_1",
		GuestCount:   2,
		RunningTotal: money.Zero(),
		FinalTotal:   money.Zero(),
	}
	_ = repo.CreateSession(ctx, sess1)

	sess2 := &session.DiningSession{
		ID:           uuid.New(),
		RestaurantID: rest2.ID,
		TableID:      uuid.New(),
		Status:       session.StateOpen,
		SessionToken: "token_sess_2",
		GuestCount:   2,
		RunningTotal: money.Zero(),
		FinalTotal:   money.Zero(),
	}
	_ = repo.CreateSession(ctx, sess2)

	// Issue Tickets
	ticketCust1, _ := tm.IssueCustomerTicket(rest1.ID, sess1.ID)
	ticketCust2, _ := tm.IssueCustomerTicket(rest2.ID, sess2.ID)
	ticketStaff1, _ := tm.IssueStaffTicket(rest1.ID, uuid.New(), "WAITER")
	ticketStaff2, _ := tm.IssueStaffTicket(rest2.ID, uuid.New(), "WAITER")

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) + "/ws/v1"

	// Connect 4 clients
	connCust1, _, err := websocket.Dial(ctx, wsURL+"?ticket="+ticketCust1, nil)
	if err != nil {
		t.Fatalf("failed to dial connCust1: %v", err)
	}
	defer connCust1.Close(websocket.StatusNormalClosure, "")

	connCust2, _, err := websocket.Dial(ctx, wsURL+"?ticket="+ticketCust2, nil)
	if err != nil {
		t.Fatalf("failed to dial connCust2: %v", err)
	}
	defer connCust2.Close(websocket.StatusNormalClosure, "")

	connStaff1, _, err := websocket.Dial(ctx, wsURL+"?ticket="+ticketStaff1, nil)
	if err != nil {
		t.Fatalf("failed to dial connStaff1: %v", err)
	}
	defer connStaff1.Close(websocket.StatusNormalClosure, "")

	connStaff2, _, err := websocket.Dial(ctx, wsURL+"?ticket="+ticketStaff2, nil)
	if err != nil {
		t.Fatalf("failed to dial connStaff2: %v", err)
	}
	defer connStaff2.Close(websocket.StatusNormalClosure, "")

	time.Sleep(50 * time.Millisecond)

	// 2. Perform Order Placement in Restaurant 1
	cartItems := []order.CartItem{
		{MenuItemID: menuItem1.ID, Quantity: 1},
	}
	ord1, _, err := orderSvc.PlaceOrder(ctx, sess1.ID, cartItems)
	if err != nil {
		t.Fatalf("failed to place order in rest1: %v", err)
	}

	// 3. Verify Cust1 and Staff1 receive ORDER_PLACED
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()

	_, msgCust1, err := connCust1.Read(readCtx)
	if err != nil {
		t.Fatalf("connCust1 must receive ORDER_PLACED, got err: %v", err)
	}
	var envCust1 ws.Envelope
	_ = json.Unmarshal(msgCust1, &envCust1)
	if envCust1.T != "ORDER_PLACED" {
		t.Fatalf("expected ORDER_PLACED on Cust1, got: %s", envCust1.T)
	}

	_, msgStaff1, err := connStaff1.Read(readCtx)
	if err != nil {
		t.Fatalf("connStaff1 must receive ORDER_PLACED, got err: %v", err)
	}
	var envStaff1 ws.Envelope
	_ = json.Unmarshal(msgStaff1, &envStaff1)
	if envStaff1.T != "ORDER_PLACED" {
		t.Fatalf("expected ORDER_PLACED on Staff1, got: %s", envStaff1.T)
	}

	// 4. Verify ZERO cross-tenant leakage: Cust2 and Staff2 receive NOTHING
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer shortCancel()

	if _, _, err := connCust2.Read(shortCtx); err == nil {
		t.Fatal("CRITICAL LEAK: Customer in Restaurant 2 received order event from Restaurant 1!")
	}

	shortCtx2, shortCancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer shortCancel2()

	if _, _, err := connStaff2.Read(shortCtx2); err == nil {
		t.Fatal("CRITICAL LEAK: Staff in Restaurant 2 received order event from Restaurant 1!")
	}

	// 5. Test Assistance Request in Restaurant 1
	_, err = sessionSvc.RequestAssistance(ctx, sess1.ID, "Need water")
	if err != nil {
		t.Fatalf("failed to request assistance: %v", err)
	}

	// Staff1 should receive ASSISTANCE_REQUESTED
	readCtx2, readCancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel2()

	_, msgStaffAssist, err := connStaff1.Read(readCtx2)
	if err != nil {
		t.Fatalf("connStaff1 should receive ASSISTANCE_REQUESTED: %v", err)
	}
	var envAssist ws.Envelope
	_ = json.Unmarshal(msgStaffAssist, &envAssist)
	if envAssist.T != "ASSISTANCE_REQUESTED" {
		t.Fatalf("expected ASSISTANCE_REQUESTED, got %s", envAssist.T)
	}

	// Cust1 also receives ASSISTANCE_REQUESTED confirmation
	_, msgCustAssist, err := connCust1.Read(readCtx2)
	if err != nil {
		t.Fatalf("connCust1 should receive ASSISTANCE_REQUESTED confirmation: %v", err)
	}
	var envCustAssist ws.Envelope
	_ = json.Unmarshal(msgCustAssist, &envCustAssist)
	if envCustAssist.T != "ASSISTANCE_REQUESTED" {
		t.Fatalf("expected ASSISTANCE_REQUESTED on Cust1, got %s", envCustAssist.T)
	}

	// Staff2 in Restaurant 2 must receive NOTHING
	shortCtx3, shortCancel3 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer shortCancel3()

	if _, _, err := connStaff2.Read(shortCtx3); err == nil {
		t.Fatal("CRITICAL LEAK: Staff in Restaurant 2 received assistance request from Restaurant 1!")
	}

	// 6. Test Accept Order
	_, err = orderSvc.AcceptOrder(ctx, ord1.ID, uuid.New())
	if err != nil {
		t.Fatalf("failed to accept order: %v", err)
	}

	readCtx3, readCancel3 := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel3()

	_, msgCustAccept, err := connCust1.Read(readCtx3)
	if err != nil {
		t.Fatalf("connCust1 must receive ORDER_ACCEPTED: %v", err)
	}
	var envAccept ws.Envelope
	_ = json.Unmarshal(msgCustAccept, &envAccept)
	if envAccept.T != "ORDER_ACCEPTED" {
		t.Fatalf("expected ORDER_ACCEPTED, got: %s", envAccept.T)
	}
}
