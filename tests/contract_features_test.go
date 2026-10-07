package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/api"
	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var testJWTSecret = []byte("contract-test-jwt-secret-32bytes!")

type contractFixture struct {
	repo       *memory.MemoryRepository
	router     http.Handler
	sessionSvc *service.SessionService
	orderSvc   *service.OrderService
	jwtSecret  []byte
}

func newContractFixture(t *testing.T) *contractFixture {
	repo := memory.NewMemoryRepository()
	jwtSecret := testJWTSecret

	sessionSvc := service.NewSessionService(repo)
	orderSvc := service.NewOrderService(repo)
	ledgerSvc := service.NewLedgerService(repo)
	exitSvc := service.NewExitService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "whsec")
	analyticsSvc := service.NewAnalyticsService(repo, nil)
	onboardingSvc := service.NewOnboardingService(repo)
	staffSvc := service.NewStaffService(repo, jwtSecret)

	apiHandler := handlers.NewAPIHandler(
		sessionSvc, orderSvc, paymentSvc, exitSvc, ledgerSvc,
		analyticsSvc, onboardingSvc, nil, repo, "whsec",
	)
	apiHandler.SetStaffService(staffSvc)
	apiHandler.SetJWTSecret(jwtSecret)

	return &contractFixture{
		repo:       repo,
		router:     api.NewRouter(apiHandler, repo, jwtSecret),
		sessionSvc: sessionSvc,
		orderSvc:   orderSvc,
		jwtSecret:  jwtSecret,
	}
}

func (f *contractFixture) serve(method, path, token string, body interface{}) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func mustStaffToken(t *testing.T, f *contractFixture, staffID, restID uuid.UUID, role string) string {
	tok, err := crypto.GenerateStaffJWT(f.jwtSecret, staffID, restID, role, role == "SUPER_ADMIN", 24*time.Hour)
	if err != nil {
		t.Fatalf("failed to generate %s token: %v", role, err)
	}
	return tok
}

func seedRestaurant(t *testing.T, f *contractFixture) uuid.UUID {
	restID := uuid.New()
	if err := f.repo.CreateRestaurant(context.Background(), &restaurant.Restaurant{
		ID:                 restID,
		Name:               "Contract Bistro " + restID.String()[:6],
		Slug:               "bistro-" + restID.String()[:6],
		Status:             restaurant.StatusActive,
		SubscriptionPlan:   "PRO",
		SubscriptionStatus: "ACTIVE",
		SubscriptionEndAt:  time.Now().UTC().Add(10 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("failed to seed restaurant: %v", err)
	}
	return restID
}

func seedStaff(t *testing.T, f *contractFixture, restID uuid.UUID, role restaurant.Role, password string) *restaurant.StaffUser {
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	st := &restaurant.StaffUser{
		ID:           uuid.New(),
		RestaurantID: restID,
		EmployeeID:   "EMP-" + uuid.New().String()[:8],
		Name:         "Staff " + uuid.New().String()[:6],
		Email:        uuid.New().String()[:8] + "@test.com",
		PasswordHash: string(hash),
		Role:         role,
		IsActive:     true,
	}
	if err := f.repo.CreateStaff(context.Background(), st); err != nil {
		t.Fatalf("failed to seed staff: %v", err)
	}
	return st
}

func seedTable(t *testing.T, f *contractFixture, restID uuid.UUID, num string, cap int) *restaurant.Table {
	tbl := &restaurant.Table{
		ID:           uuid.New(),
		RestaurantID: restID,
		TableNumber:  num,
		TableToken:   "tok-" + uuid.New().String()[:8],
		Capacity:     cap,
		IsActive:     true,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := f.repo.CreateTable(context.Background(), tbl); err != nil {
		t.Fatalf("failed to seed table: %v", err)
	}
	return tbl
}

func seedMenuItem(t *testing.T, f *contractFixture, restID uuid.UUID, name string, price int64) *restaurant.MenuItem {
	mi := &restaurant.MenuItem{
		ID:           uuid.New(),
		RestaurantID: restID,
		CategoryID:   uuid.New(),
		Name:         name,
		Price:        money.New(price),
		IsAvailable:  true,
		CGSTRateBps:  250,
		SGSTRateBps:  250,
	}
	if err := f.repo.CreateMenuItem(context.Background(), mi); err != nil {
		t.Fatalf("failed to seed menu item: %v", err)
	}
	return mi
}

// ---------- §1 Subscription OTP ----------

func TestSubscriptionOTPFlow(t *testing.T) {
	f := newContractFixture(t)
	restID := uuid.New()
	_ = f.repo.CreateRestaurant(context.Background(), &restaurant.Restaurant{
		ID:                 restID,
		Name:               "OTP Bistro",
		Slug:               "otp-bistro",
		Status:             restaurant.StatusActive,
		SubscriptionPlan:   "PRO",
		SubscriptionStatus: "EXPIRED",
		SubscriptionEndAt:  time.Now().UTC().Add(-24 * time.Hour),
	})
	admin := seedStaff(t, f, restID, restaurant.RoleRestaurantAdmin, "Password123!")
	waiter := seedStaff(t, f, restID, restaurant.RoleWaiter, "Password123!")

	adminToken := mustStaffToken(t, f, admin.ID, restID, "RESTAURANT_ADMIN")
	waiterToken := mustStaffToken(t, f, waiter.ID, restID, "WAITER")
	platformToken := mustStaffToken(t, f, uuid.New(), uuid.New(), "SUPER_ADMIN")

	// renew without OTP → 400
	w := f.serve("POST", "/api/v1/restaurant/subscription/renew", adminToken, map[string]int{"days": 30})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("renew without OTP: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "otp is required") {
		t.Fatalf("expected otp-required message, got: %s", w.Body.String())
	}

	// waiter → 403
	w = f.serve("POST", "/api/v1/restaurant/subscription/renew", waiterToken, map[string]string{"otp": "123456"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("renew as waiter: expected 403, got %d: %s", w.Code, w.Body.String())
	}

	// no active OTP yet → 400
	w = f.serve("POST", "/api/v1/restaurant/subscription/renew", adminToken, map[string]string{"otp": "123456"})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "no active activation OTP") {
		t.Fatalf("expected no-active-OTP 400, got %d: %s", w.Code, w.Body.String())
	}

	// platform admin generates OTP
	w = f.serve("POST", "/api/v1/admin/restaurants/"+restID.String()+"/subscription-otp", platformToken, map[string]interface{}{"days": 30, "plan": "ENTERPRISE"})
	if w.Code != http.StatusOK {
		t.Fatalf("generate OTP: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var otpResp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &otpResp)
	rawOTP, _ := otpResp["otp"].(string)
	if len(rawOTP) != 6 {
		t.Fatalf("expected 6-digit OTP, got %v", otpResp)
	}
	if otpResp["days"].(float64) != 30 || otpResp["plan"] != "ENTERPRISE" {
		t.Fatalf("unexpected OTP response fields: %v", otpResp)
	}

	// OTP history listed without hash
	w = f.serve("GET", "/api/v1/admin/restaurants/"+restID.String()+"/subscription-otps", platformToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list OTPs: expected 200, got %d", w.Code)
	}
	var otps []map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &otps)
	if len(otps) != 1 {
		t.Fatalf("expected 1 OTP in history, got %d", len(otps))
	}
	if _, leaked := otps[0]["otp_hash"]; leaked {
		t.Fatalf("OTP hash must not be exposed")
	}
	if otps[0]["status"] != "ISSUED" {
		t.Fatalf("expected ISSUED status, got %v", otps[0])
	}

	wrongOTP := "000000"
	if rawOTP == wrongOTP {
		wrongOTP = "000001"
	}

	// wrong OTP → 400 + attempts increment
	w = f.serve("POST", "/api/v1/restaurant/subscription/renew", adminToken, map[string]string{"otp": wrongOTP})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid OTP") {
		t.Fatalf("wrong OTP: expected 400 invalid OTP, got %d: %s", w.Code, w.Body.String())
	}
	w = f.serve("GET", "/api/v1/admin/restaurants/"+restID.String()+"/subscription-otps", platformToken, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &otps)
	if otps[0]["attempts"].(float64) != 1 {
		t.Fatalf("expected attempts=1 after wrong OTP, got %v", otps[0])
	}

	// days mismatch → 400 (not counted as attempt)
	w = f.serve("POST", "/api/v1/restaurant/subscription/renew", adminToken, map[string]interface{}{"otp": rawOTP, "days": 7})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "30-day plan") {
		t.Fatalf("days mismatch: expected 400 plan message, got %d: %s", w.Code, w.Body.String())
	}
	w = f.serve("GET", "/api/v1/admin/restaurants/"+restID.String()+"/subscription-otps", platformToken, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &otps)
	if otps[0]["attempts"].(float64) != 1 {
		t.Fatalf("days mismatch must not count as an attempt, attempts=%v", otps[0])
	}

	// correct OTP → 200, days extended, plan set
	w = f.serve("POST", "/api/v1/restaurant/subscription/renew", adminToken, map[string]interface{}{"otp": rawOTP, "days": 30})
	if w.Code != http.StatusOK {
		t.Fatalf("correct OTP: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var renewResp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &renewResp)
	if renewResp["subscription_plan"] != "ENTERPRISE" {
		t.Fatalf("expected plan ENTERPRISE, got %v", renewResp)
	}
	if renewResp["days"].(float64) != 30 {
		t.Fatalf("expected days=30, got %v", renewResp)
	}
	if renewResp["days_remaining"].(float64) < 29 {
		t.Fatalf("expected ~30 days remaining, got %v", renewResp)
	}

	// reuse → 400
	w = f.serve("POST", "/api/v1/restaurant/subscription/renew", adminToken, map[string]string{"otp": rawOTP})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("reuse OTP: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// lockout: new OTP, 5 wrong attempts → locked (429 then revoked)
	w = f.serve("POST", "/api/v1/admin/restaurants/"+restID.String()+"/subscription-otp", platformToken, map[string]interface{}{"days": 30})
	var otp2 map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &otp2)
	rawOTP2, _ := otp2["otp"].(string)
	wrong2 := "999999"
	if rawOTP2 == wrong2 {
		wrong2 = "999998"
	}
	for i := 0; i < 5; i++ {
		w = f.serve("POST", "/api/v1/restaurant/subscription/renew", adminToken, map[string]string{"otp": wrong2})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("wrong attempt %d: expected 400, got %d", i+1, w.Code)
		}
	}
	w = f.serve("POST", "/api/v1/restaurant/subscription/renew", adminToken, map[string]string{"otp": wrong2})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked OTP: expected 429, got %d: %s", w.Code, w.Body.String())
	}
	w = f.serve("POST", "/api/v1/restaurant/subscription/renew", adminToken, map[string]string{"otp": rawOTP2})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("correct OTP after lockout must fail (revoked), got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubscriptionOTPConcurrentDoubleUse(t *testing.T) {
	f := newContractFixture(t)
	restID := uuid.New()
	_ = f.repo.CreateRestaurant(context.Background(), &restaurant.Restaurant{
		ID: restID, Name: "Race Bistro", Slug: "race-bistro", Status: restaurant.StatusActive,
		SubscriptionStatus: "ACTIVE", SubscriptionEndAt: time.Now().UTC().Add(-time.Hour),
	})
	admin := seedStaff(t, f, restID, restaurant.RoleRestaurantAdmin, "Password123!")
	adminToken := mustStaffToken(t, f, admin.ID, restID, "RESTAURANT_ADMIN")
	platformToken := mustStaffToken(t, f, uuid.New(), uuid.New(), "SUPER_ADMIN")

	w := f.serve("POST", "/api/v1/admin/restaurants/"+restID.String()+"/subscription-otp", platformToken, map[string]interface{}{"days": 30})
	var otpResp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &otpResp)
	rawOTP, _ := otpResp["otp"].(string)

	const n = 8
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := f.serve("POST", "/api/v1/restaurant/subscription/renew", adminToken, map[string]string{"otp": rawOTP})
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)

	successes, conflicts := 0, 0
	for code := range codes {
		if code == http.StatusOK {
			successes++
		} else if code == http.StatusBadRequest {
			conflicts++
		} else {
			t.Fatalf("unexpected status %d in concurrent renew", code)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly 1 successful OTP consumption, got %d", successes)
	}
}

// ---------- §2 Table capacity ----------

func TestTableCapacityContract(t *testing.T) {
	f := newContractFixture(t)
	restID := seedRestaurant(t, f)
	admin := seedStaff(t, f, restID, restaurant.RoleRestaurantAdmin, "Password123!")
	adminToken := mustStaffToken(t, f, admin.ID, restID, "RESTAURANT_ADMIN")

	// create without capacity → 400
	w := f.serve("POST", "/api/v1/restaurant/tables", adminToken, map[string]string{"table_number": "T1"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create without capacity: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// create with capacity 6 → stored
	w = f.serve("POST", "/api/v1/restaurant/tables", adminToken, map[string]interface{}{"table_number": "T2", "capacity": 6})
	if w.Code != http.StatusCreated {
		t.Fatalf("create with capacity: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var tbl map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &tbl)
	if tbl["capacity"].(float64) != 6 {
		t.Fatalf("expected capacity 6, got %v", tbl)
	}
	tblID := tbl["id"].(string)

	// PUT updates capacity
	w = f.serve("PUT", "/api/v1/restaurant/tables/"+tblID, adminToken, map[string]int{"capacity": 8})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT capacity: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tbl)
	if tbl["capacity"].(float64) != 8 {
		t.Fatalf("expected capacity 8 after PUT, got %v", tbl)
	}

	// invalid PUT capacity → 400
	w = f.serve("PUT", "/api/v1/restaurant/tables/"+tblID, adminToken, map[string]int{"capacity": 99})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid capacity: expected 400, got %d", w.Code)
	}

	// onboarding: default_capacity=2, explicit table capacity=6 wins
	onboard := map[string]interface{}{
		"restaurant_name":  "Cap Onboard",
		"default_capacity": 2,
		"tables": []map[string]interface{}{
			{"table_number": "A1", "capacity": 6},
			{"table_number": "A2"},
		},
		"admin": map[string]string{"name": "Admin", "email": "cap-onboard@test.com", "password": "Password123!"},
	}
	w = f.serve("POST", "/api/v1/public/onboard", "", onboard)
	if w.Code != http.StatusCreated {
		t.Fatalf("onboard: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var ob map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &ob)
	newRestID, _ := uuid.Parse(ob["restaurant_id"].(string))
	tables, _ := f.repo.ListTables(context.Background(), newRestID)
	capByNum := map[string]int{}
	for _, tb := range tables {
		capByNum[tb.TableNumber] = tb.Capacity
	}
	if capByNum["A1"] != 6 || capByNum["A2"] != 2 {
		t.Fatalf("onboarding capacity resolution wrong: %v", capByNum)
	}
}

// ---------- §3 Waiter ownership ----------

func TestWaiterOwnership(t *testing.T) {
	ctx := context.Background()
	f := newContractFixture(t)
	restID := seedRestaurant(t, f)
	tbl := seedTable(t, f, restID, "Table 5", 4)
	mi := seedMenuItem(t, f, restID, "Paneer", 25000)

	waiterA := seedStaff(t, f, restID, restaurant.RoleWaiter, "Password123!")
	waiterB := seedStaff(t, f, restID, restaurant.RoleWaiter, "Password123!")
	manager := seedStaff(t, f, restID, restaurant.RoleManager, "Password123!")

	tokenA := mustStaffToken(t, f, waiterA.ID, restID, "WAITER")
	tokenB := mustStaffToken(t, f, waiterB.ID, restID, "WAITER")
	tokenM := mustStaffToken(t, f, manager.ID, restID, "MANAGER")

	sess, _, err := f.sessionSvc.StartSession(ctx, tbl.TableToken, "dev-1", "Customer", "+91999", "", 2, "fp")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	ord1, _, err := f.orderSvc.PlaceOrder(ctx, sess.ID, []order.CartItem{{MenuItemID: mi.ID, Quantity: 1}})
	if err != nil {
		t.Fatalf("place order1: %v", err)
	}
	ord2, _, err := f.orderSvc.PlaceOrder(ctx, sess.ID, []order.CartItem{{MenuItemID: mi.ID, Quantity: 2}})
	if err != nil {
		t.Fatalf("place order2: %v", err)
	}

	// Waiter A accepts the first order → session assigned to A
	w := f.serve("POST", "/api/v1/staff/orders/"+ord1.ID.String()+"/accept", tokenA, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("waiter A accept: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	sess2, _ := f.repo.GetSessionByID(ctx, sess.ID)
	if sess2.AssignedWaiterID == nil || *sess2.AssignedWaiterID != waiterA.ID {
		t.Fatalf("session not assigned to waiter A: %+v", sess2.AssignedWaiterID)
	}
	if sess2.AssignedWaiterName == "" {
		t.Fatalf("assigned waiter name should be set")
	}

	// Waiter B pending list excludes the session's other pending order
	w = f.serve("GET", "/api/v1/staff/orders/pending", tokenB, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("pending list: expected 200, got %d", w.Code)
	}
	var pending []map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &pending)
	for _, p := range pending {
		if p["order"].(map[string]interface{})["id"] == ord2.ID.String() {
			t.Fatalf("waiter B must not see order from waiter A's table")
		}
	}

	// Waiter B tries to accept order on A's table → 403
	w = f.serve("POST", "/api/v1/staff/orders/"+ord2.ID.String()+"/accept", tokenB, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("waiter B accept on A's table: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "being served by") {
		t.Fatalf("expected 'being served by' message, got: %s", w.Body.String())
	}

	// Kitchen queue for waiter B also excludes it
	w = f.serve("GET", "/api/v1/kitchen/orders/queue", tokenB, nil)
	var queue []map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &queue)
	for _, q := range queue {
		if q["id"] == ord1.ID.String() || q["id"] == ord2.ID.String() {
			t.Fatalf("waiter B kitchen queue must exclude A's table orders")
		}
	}

	// Manager can accept (bypass)
	w = f.serve("POST", "/api/v1/staff/orders/"+ord2.ID.String()+"/accept", tokenM, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("manager accept: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Dashboard flags
	w = f.serve("GET", "/api/v1/staff/dashboard/tables", tokenA, nil)
	var boardA []map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &boardA)
	found := false
	for _, e := range boardA {
		if e["table_id"] == tbl.ID.String() {
			found = true
			if e["is_assigned_to_me"] != true {
				t.Fatalf("A's dashboard should flag is_assigned_to_me")
			}
			if e["assigned_waiter_name"] == "" {
				t.Fatalf("dashboard should include assigned waiter name")
			}
		}
	}
	if !found {
		t.Fatalf("table not found in dashboard")
	}

	w = f.serve("GET", "/api/v1/staff/dashboard/tables", tokenB, nil)
	var boardB []map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &boardB)
	for _, e := range boardB {
		if e["table_id"] == tbl.ID.String() {
			if e["is_assigned_to_me"] != false {
				t.Fatalf("B's dashboard should not flag is_assigned_to_me")
			}
			if e["customer_phone"] != "" {
				t.Fatalf("B should see redacted customer_phone on A's table")
			}
		}
	}

	// assign-waiter: manager reassigns to B
	w = f.serve("POST", "/api/v1/staff/sessions/"+sess.ID.String()+"/assign-waiter", tokenM, map[string]string{"staff_id": waiterB.ID.String()})
	if w.Code != http.StatusOK {
		t.Fatalf("assign-waiter: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	sess3, _ := f.repo.GetSessionByID(ctx, sess.ID)
	if sess3.AssignedWaiterID == nil || *sess3.AssignedWaiterID != waiterB.ID {
		t.Fatalf("session not reassigned to waiter B")
	}

	// unassign via null
	w = f.serve("POST", "/api/v1/staff/sessions/"+sess.ID.String()+"/assign-waiter", tokenM, map[string]interface{}{"staff_id": nil})
	if w.Code != http.StatusOK {
		t.Fatalf("unassign-waiter: expected 200, got %d", w.Code)
	}
	sess4, _ := f.repo.GetSessionByID(ctx, sess.ID)
	if sess4.AssignedWaiterID != nil {
		t.Fatalf("session should be unassigned")
	}
}

// ---------- §4 Menu variants ----------

func TestMenuVariants(t *testing.T) {
	ctx := context.Background()
	f := newContractFixture(t)
	restID := seedRestaurant(t, f)
	tbl := seedTable(t, f, restID, "Table 1", 4)
	admin := seedStaff(t, f, restID, restaurant.RoleRestaurantAdmin, "Password123!")
	adminToken := mustStaffToken(t, f, admin.ID, restID, "RESTAURANT_ADMIN")

	// Create item with variants
	w := f.serve("POST", "/api/v1/restaurant/menu/items", adminToken, map[string]interface{}{
		"category_id": uuid.New().String(),
		"name":        "Butter Chicken",
		"price_minor": 250,
		"variants": []map[string]interface{}{
			{"name": "Half", "price_minor": 150},
			{"name": "Full", "price_minor": 250},
		},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create item: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var item map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &item)
	itemID := item["id"].(string)
	variants, _ := item["variants"].([]interface{})
	if len(variants) != 2 {
		t.Fatalf("expected 2 variants in create response, got %v", item)
	}
	halfID := variants[0].(map[string]interface{})["id"].(string)

	// List returns variants
	w = f.serve("GET", "/api/v1/restaurant/menu/items?restaurant_id="+restID.String(), adminToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list items: expected 200, got %d", w.Code)
	}
	var items []map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &items)
	found := false
	for _, it := range items {
		if it["id"] == itemID {
			found = true
			if v, _ := it["variants"].([]interface{}); len(v) != 2 {
				t.Fatalf("list must return variants, got %v", it)
			}
		}
	}
	if !found {
		t.Fatalf("created item missing from list")
	}

	// Order with Half variant → price 150, name "Butter Chicken (Half)"
	sess, _, err := f.sessionSvc.StartSession(ctx, tbl.TableToken, "dev", "Cust", "+9111", "", 2, "fp")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	halfUUID, _ := uuid.Parse(halfID)
	itemUUID, _ := uuid.Parse(itemID)
	ord, _, err := f.orderSvc.PlaceOrder(ctx, sess.ID, []order.CartItem{{MenuItemID: itemUUID, VariantID: &halfUUID, Quantity: 1}})
	if err != nil {
		t.Fatalf("place order with variant: %v", err)
	}
	if len(ord.Items) != 1 {
		t.Fatalf("expected 1 order item")
	}
	got := ord.Items[0]
	if got.UnitPriceSnapshot.AmountMinorUnits != 150 {
		t.Fatalf("expected unit price 150, got %d", got.UnitPriceSnapshot.AmountMinorUnits)
	}
	if got.ItemNameSnapshot != "Butter Chicken (Half)" {
		t.Fatalf("expected 'Butter Chicken (Half)', got %q", got.ItemNameSnapshot)
	}

	// Foreign variant ID rejected
	otherItem := seedMenuItem(t, f, restID, "Dal", 100)
	otherVarID := uuid.New()
	_ = f.repo.ReplaceMenuItemVariants(ctx, otherItem.ID, []restaurant.MenuItemVariant{
		{ID: otherVarID, Name: "Small", Price: money.New(50), IsAvailable: true},
	})
	_, _, err = f.orderSvc.PlaceOrder(ctx, sess.ID, []order.CartItem{{MenuItemID: itemUUID, VariantID: &otherVarID, Quantity: 1}})
	if err == nil {
		t.Fatalf("foreign variant must be rejected")
	}

	// PUT variants replaces the set
	w = f.serve("PUT", "/api/v1/restaurant/menu/items/"+itemID+"/variants", adminToken, map[string]interface{}{
		"variants": []map[string]interface{}{{"name": "Regular", "price_minor": 99}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT variants: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &item)
	if v, _ := item["variants"].([]interface{}); len(v) != 1 {
		t.Fatalf("expected 1 variant after PUT, got %v", item)
	}
	miAfter, _ := f.repo.GetMenuItemByID(ctx, itemUUID)
	if len(miAfter.Variants) != 1 || miAfter.Variants[0].Name != "Regular" {
		t.Fatalf("variants not replaced: %+v", miAfter.Variants)
	}
}

// ---------- §5 Franchise model ----------

func TestFranchiseModel(t *testing.T) {
	ctx := context.Background()
	f := newContractFixture(t)

	onboard := func(body map[string]interface{}) *httptest.ResponseRecorder {
		return f.serve("POST", "/api/v1/public/onboard", "", body)
	}

	// Onboard a FRANCHISE
	w := onboard(map[string]interface{}{
		"restaurant_name": "Spice Route Central",
		"slug":            "spice-central",
		"ownership_type":  "FRANCHISE",
		"franchise_name":  "Spice Route Group",
		"admin":           map[string]string{"name": "FR Owner", "email": "fr1@test.com", "password": "Password123!"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("franchise onboard: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var fr1 map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &fr1)
	franchiseToken, _ := fr1["token"].(string)
	if franchiseToken == "" {
		t.Fatalf("expected token in onboard response")
	}
	franchiseID, _ := fr1["franchise_id"].(string)
	if franchiseID == "" {
		t.Fatalf("expected franchise_id in onboard response: %v", fr1)
	}
	if fr1["admin"].(map[string]interface{})["role"] != "FRANCHISE_OWNER" {
		t.Fatalf("expected FRANCHISE_OWNER admin role: %v", fr1)
	}

	// Generate invite code
	w = f.serve("POST", "/api/v1/franchise/invite-code", franchiseToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("invite code: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var inv map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &inv)
	code, _ := inv["code"].(string)
	if !strings.HasPrefix(code, "FRN-") {
		t.Fatalf("expected FRN- code, got %v", inv)
	}

	// Public invite lookup valid
	w = f.serve("GET", "/api/v1/public/franchise/invite/"+code, "", nil)
	var lookup map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &lookup)
	if lookup["valid"] != true || lookup["franchise_name"] != "Spice Route Group" {
		t.Fatalf("invite lookup wrong: %v", lookup)
	}
	// Unknown code → valid:false (still 200)
	w = f.serve("GET", "/api/v1/public/franchise/invite/FRN-000000", "", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &lookup)
	if lookup["valid"] != false {
		t.Fatalf("unknown code should be valid:false")
	}

	// Onboard FRANCHISE_OUTLET with the code
	w = onboard(map[string]interface{}{
		"restaurant_name":       "Spice Route Noida",
		"slug":                  "spice-noida",
		"ownership_type":        "FRANCHISE_OUTLET",
		"franchise_invite_code": code,
		"admin":                 map[string]string{"name": "Outlet Admin", "email": "outlet1@test.com", "password": "Password123!"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("outlet onboard: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var ob map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &ob)
	outletID, _ := ob["restaurant_id"].(string)
	if ob["franchise_id"] != franchiseID {
		t.Fatalf("outlet not linked to franchise: %v", ob)
	}

	// Reuse invite code → 400
	w = onboard(map[string]interface{}{
		"restaurant_name":       "Spice Route Third",
		"ownership_type":        "FRANCHISE_OUTLET",
		"franchise_invite_code": code,
		"admin":                 map[string]string{"name": "A", "email": "outlet2@test.com", "password": "Password123!"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invite reuse: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// Outlets list has exactly 2
	w = f.serve("GET", "/api/v1/franchise/outlets", franchiseToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("outlets list: expected 200, got %d", w.Code)
	}
	var outlets []map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &outlets)
	if len(outlets) != 2 {
		t.Fatalf("expected exactly 2 outlets, got %d: %v", len(outlets), outlets)
	}
	for _, o := range outlets {
		if o["franchise_name"] != "Spice Route Group" || o["ownership_type"] != "FRANCHISE" {
			t.Fatalf("outlet missing franchise metadata: %v", o)
		}
	}

	// Seed a paid order into the outlet → summary revenue computed
	outletUUID, _ := uuid.Parse(outletID)
	_ = f.repo.CreateOrder(ctx, &order.Order{
		ID:           uuid.New(),
		SessionID:    uuid.New(),
		RestaurantID: outletUUID,
		TableNumber:  "Table 1",
		Status:       order.StateServed,
		Total:        money.New(50000),
		PlacedAt:     time.Now(),
	}, nil)
	w = f.serve("GET", "/api/v1/franchise/summary", franchiseToken, nil)
	var summary map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &summary)
	if summary["total_outlets"].(float64) != 2 {
		t.Fatalf("summary total_outlets: %v", summary)
	}
	if summary["total_revenue_minor"].(float64) != 50000 {
		t.Fatalf("summary revenue should be computed (50000), got %v", summary)
	}
	if summary["top_performing_outlet"] != "Spice Route Noida" {
		t.Fatalf("top outlet should be Spice Route Noida, got %v", summary)
	}

	// Onboard a SECOND independent franchise — must not see the first's outlets
	w = onboard(map[string]interface{}{
		"restaurant_name": "Rival Foods",
		"slug":            "rival-foods",
		"ownership_type":  "FRANCHISE",
		"franchise_name":  "Rival Group",
		"admin":           map[string]string{"name": "Rival", "email": "rival@test.com", "password": "Password123!"},
	})
	var fr2 map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &fr2)
	rivalToken := fr2["token"].(string)

	w = f.serve("GET", "/api/v1/franchise/outlets", rivalToken, nil)
	var rivalOutlets []map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &rivalOutlets)
	if len(rivalOutlets) != 1 {
		t.Fatalf("rival should see only its own outlet, got %d", len(rivalOutlets))
	}

	// Cross-franchise ?restaurant_id access → 403
	w = f.serve("GET", "/api/v1/staff/dashboard/tables?restaurant_id="+outletID, rivalToken, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-franchise dashboard: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	w = f.serve("GET", "/api/v1/restaurant/orders?restaurant_id="+outletID, rivalToken, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-franchise orders: expected 403, got %d: %s", w.Code, w.Body.String())
	}

	// Create outlet via endpoint → persists and appears in outlets
	w = f.serve("POST", "/api/v1/franchise/outlets/create", franchiseToken, map[string]interface{}{
		"name":       "Spice Route BKC",
		"slug":       "spice-bkc",
		"admin_name": "BKC Admin",
		"email":      "bkc@test.com",
		"password":   "Password123!",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create outlet: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var created map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	newOutletID := created["restaurant_id"].(string)
	if _, err := f.repo.GetRestaurantByID(ctx, uuid.MustParse(newOutletID)); err != nil {
		t.Fatalf("created outlet not persisted: %v", err)
	}
	w = f.serve("GET", "/api/v1/franchise/outlets", franchiseToken, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &outlets)
	if len(outlets) != 3 {
		t.Fatalf("expected 3 outlets after create, got %d", len(outlets))
	}
}

// ---------- §6 Super admin ----------

func TestSuperAdminPlatform(t *testing.T) {
	ctx := context.Background()
	f := newContractFixture(t)

	// EnsureSuperAdmin idempotent
	email := "root@tableos.com"
	if err := service.EnsureSuperAdmin(ctx, f.repo, email, "RootPass123!"); err != nil {
		t.Fatalf("EnsureSuperAdmin: %v", err)
	}
	platRest, err := f.repo.GetRestaurantByID(ctx, restaurant.PlatformRestaurantID)
	if err != nil || platRest == nil {
		t.Fatalf("platform restaurant not created")
	}
	if err := service.EnsureSuperAdmin(ctx, f.repo, email, "RootPass123!"); err != nil {
		t.Fatalf("EnsureSuperAdmin must be idempotent: %v", err)
	}

	// Login via email → platform token
	w := f.serve("POST", "/api/v1/staff/login", "", map[string]string{"identifier": email, "password": "RootPass123!"})
	if w.Code != http.StatusOK {
		t.Fatalf("super admin login: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var login map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &login)
	platformToken := login["token"].(string)

	// Seed a franchise restaurant + a single restaurant
	restID := seedRestaurant(t, f)
	frID := uuid.New()
	frRestID := uuid.New()
	_ = f.repo.CreateFranchise(ctx, &restaurant.Franchise{ID: frID, Name: "Test Chain", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	_ = f.repo.CreateRestaurant(ctx, &restaurant.Restaurant{
		ID: frRestID, Name: "Chain Outlet", Slug: "chain-outlet", FranchiseID: &frID,
		Status: restaurant.StatusActive, SubscriptionStatus: "ACTIVE", SubscriptionEndAt: time.Now().UTC().Add(24 * time.Hour),
	})

	// /admin/restaurants excludes platform restaurant
	w = f.serve("GET", "/api/v1/admin/restaurants", platformToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("admin restaurants: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var rests []map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &rests)
	for _, rest := range rests {
		if rest["id"] == restaurant.PlatformRestaurantID.String() {
			t.Fatalf("platform restaurant must be excluded from /admin/restaurants")
		}
	}

	// ?type filter
	w = f.serve("GET", "/api/v1/admin/restaurants?type=FRANCHISE", platformToken, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &rests)
	for _, rest := range rests {
		if rest["ownership_type"] != "FRANCHISE" {
			t.Fatalf("type=FRANCHISE filter failed: %v", rest)
		}
	}
	w = f.serve("GET", "/api/v1/admin/restaurants?q=chain", platformToken, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &rests)
	if len(rests) != 1 || rests[0]["name"] != "Chain Outlet" {
		t.Fatalf("?q filter failed: %v", rests)
	}

	// /admin/franchises groups outlets
	w = f.serve("GET", "/api/v1/admin/franchises", platformToken, nil)
	var frs []map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &frs)
	if len(frs) != 1 || frs[0]["name"] != "Test Chain" {
		t.Fatalf("admin franchises wrong: %v", frs)
	}
	if outs, _ := frs[0]["outlets"].([]interface{}); len(outs) != 1 {
		t.Fatalf("franchise must group its outlets: %v", frs)
	}

	// /admin/overview shape
	w = f.serve("GET", "/api/v1/admin/overview", platformToken, nil)
	var ov map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &ov)
	for _, k := range []string{"total_restaurants", "franchise_count", "live_sessions", "revenue_today_minor", "platform_gross_sales_minor"} {
		if _, ok := ov[k]; !ok {
			t.Fatalf("overview missing key %q: %v", k, ov)
		}
	}
	if ov["total_restaurants"].(float64) < 2 {
		t.Fatalf("overview total_restaurants wrong: %v", ov)
	}

	// activity endpoint: tables + staff, no password hashes
	seedTable(t, f, restID, "T1", 4)
	seedStaff(t, f, restID, restaurant.RoleWaiter, "Password123!")
	w = f.serve("GET", "/api/v1/admin/restaurants/"+restID.String()+"/activity", platformToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("activity: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "password_hash") || strings.Contains(w.Body.String(), "$2a$") {
		t.Fatalf("activity must never expose password hashes")
	}
	var act map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &act)
	if tbls, _ := act["tables"].([]interface{}); len(tbls) != 1 {
		t.Fatalf("activity missing tables: %v", act)
	}
	if stf, _ := act["staff"].([]interface{}); len(stf) != 1 {
		t.Fatalf("activity missing staff: %v", act)
	}
	if _, ok := act["subscription"]; !ok {
		t.Fatalf("activity missing subscription details")
	}

	// activity feed merges orders across restaurants
	_ = f.repo.CreateOrder(ctx, &order.Order{
		ID: uuid.New(), SessionID: uuid.New(), RestaurantID: restID,
		TableNumber: "T1", Status: order.StateAccepted, Total: money.New(12300), PlacedAt: time.Now(),
	}, nil)
	w = f.serve("GET", "/api/v1/admin/activity/feed?limit=10", platformToken, nil)
	var feed []map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &feed)
	if len(feed) != 1 || feed[0]["restaurant_id"] != restID.String() {
		t.Fatalf("activity feed wrong: %v", feed)
	}
	if _, ok := feed[0]["restaurant_name"]; !ok {
		t.Fatalf("feed missing restaurant_name")
	}
}

var _ = fmt.Sprintf // silence unused import if needed
