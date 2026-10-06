package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

func TestPhase5SecurityHardening(t *testing.T) {
	router, repo, jwtSecret := setupTestRouter(t)
	ctx := context.Background()

	restA := uuid.New()
	restB := uuid.New()

	_ = repo.CreateRestaurant(ctx, &restaurant.Restaurant{
		ID:     restA,
		Name:   "Restaurant Alpha",
		Status: restaurant.StatusActive,
	})
	_ = repo.CreateRestaurant(ctx, &restaurant.Restaurant{
		ID:     restB,
		Name:   "Restaurant Beta",
		Status: restaurant.StatusActive,
	})

	waiterA := uuid.New()
	kitchenA := uuid.New()
	managerA := uuid.New()
	guardA := uuid.New()

	waiterTokenA, _ := crypto.GenerateStaffJWT(jwtSecret, waiterA, restA, string(restaurant.RoleWaiter), false, 1*time.Hour)
	kitchenTokenA, _ := crypto.GenerateStaffJWT(jwtSecret, kitchenA, restA, "KITCHEN", false, 1*time.Hour)
	managerTokenA, _ := crypto.GenerateStaffJWT(jwtSecret, managerA, restA, string(restaurant.RoleManager), false, 1*time.Hour)
	guardTokenA, _ := crypto.GenerateGuardJWT(jwtSecret, guardA, restA, 1*time.Hour)

	// 1. Kitchen Routes: Role checks
	t.Run("Kitchen_Route_Requires_Staff_JWT_With_Allowed_Role", func(t *testing.T) {
		// No auth -> 401
		reqNoAuth := httptest.NewRequest(http.MethodGet, "/api/v1/kitchen/orders/queue", nil)
		wNoAuth := httptest.NewRecorder()
		router.ServeHTTP(wNoAuth, reqNoAuth)
		if wNoAuth.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for unauthenticated kitchen queue, got %d", wNoAuth.Code)
		}

		// Guard token -> rejected on staff route (401)
		reqGuard := httptest.NewRequest(http.MethodGet, "/api/v1/kitchen/orders/queue", nil)
		reqGuard.Header.Set("Authorization", "Bearer "+guardTokenA)
		wGuard := httptest.NewRecorder()
		router.ServeHTTP(wGuard, reqGuard)
		if wGuard.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for guard token on kitchen route, got %d", wGuard.Code)
		}

		// Kitchen staff token -> 200 OK
		reqKitchen := httptest.NewRequest(http.MethodGet, "/api/v1/kitchen/orders/queue", nil)
		reqKitchen.Header.Set("Authorization", "Bearer "+kitchenTokenA)
		wKitchen := httptest.NewRecorder()
		router.ServeHTTP(wKitchen, reqKitchen)
		if wKitchen.Code != http.StatusOK {
			t.Fatalf("expected 200 for kitchen role on kitchen queue, got %d", wKitchen.Code)
		}

		// Waiter token -> 200 OK (allowed role)
		reqWaiter := httptest.NewRequest(http.MethodGet, "/api/v1/kitchen/orders/queue", nil)
		reqWaiter.Header.Set("Authorization", "Bearer "+waiterTokenA)
		wWaiter := httptest.NewRecorder()
		router.ServeHTTP(wWaiter, reqWaiter)
		if wWaiter.Code != http.StatusOK {
			t.Fatalf("expected 200 for waiter role on kitchen queue, got %d", wWaiter.Code)
		}
	})

	// 2. Tenant ID strictly from claims (Cross-tenant probing blocked)
	t.Run("Tenant_ID_Enforced_Strictly_From_Claims_Blocks_Cross_Tenant_Access", func(t *testing.T) {
		// Staff from Restaurant Alpha tries to query kitchen queue of Restaurant Beta via query param
		reqCrossKitchen := httptest.NewRequest(http.MethodGet, "/api/v1/kitchen/orders/queue?restaurant_id="+restB.String(), nil)
		reqCrossKitchen.Header.Set("Authorization", "Bearer "+kitchenTokenA)
		wCrossKitchen := httptest.NewRecorder()
		router.ServeHTTP(wCrossKitchen, reqCrossKitchen)
		if wCrossKitchen.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for cross-tenant kitchen queue access, got %d", wCrossKitchen.Code)
		}

		// Staff from Restaurant Alpha tries to access table dashboard of Restaurant Beta via query param
		reqCrossTables := httptest.NewRequest(http.MethodGet, "/api/v1/staff/dashboard/tables?restaurant_id="+restB.String(), nil)
		reqCrossTables.Header.Set("Authorization", "Bearer "+waiterTokenA)
		wCrossTables := httptest.NewRecorder()
		router.ServeHTTP(wCrossTables, reqCrossTables)
		if wCrossTables.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for cross-tenant table dashboard access, got %d", wCrossTables.Code)
		}

		// Staff from Restaurant Alpha tries to access orders of Restaurant Beta via query param
		reqCrossOrders := httptest.NewRequest(http.MethodGet, "/api/v1/restaurant/orders?restaurant_id="+restB.String(), nil)
		reqCrossOrders.Header.Set("Authorization", "Bearer "+managerTokenA)
		wCrossOrders := httptest.NewRecorder()
		router.ServeHTTP(wCrossOrders, reqCrossOrders)
		if wCrossOrders.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for cross-tenant restaurant orders access, got %d", wCrossOrders.Code)
		}

		// Staff from Restaurant Alpha tries to update kitchen order status belonging to Restaurant Beta
		orderB := &order.Order{
			ID:           uuid.New(),
			RestaurantID: restB,
			Status:       order.StatePlacedVerified,
			Total:        money.New(1000),
			CreatedAt:    time.Now(),
		}
		_ = repo.CreateOrder(ctx, orderB, nil)

		updatePayload, _ := json.Marshal(map[string]string{"status": string(order.StatePreparing)})
		reqCrossUpdate := httptest.NewRequest(http.MethodPost, "/api/v1/kitchen/orders/"+orderB.ID.String()+"/status", bytes.NewBuffer(updatePayload))
		reqCrossUpdate.Header.Set("Authorization", "Bearer "+kitchenTokenA)
		reqCrossUpdate.Header.Set("Content-Type", "application/json")
		wCrossUpdate := httptest.NewRecorder()
		router.ServeHTTP(wCrossUpdate, reqCrossUpdate)
		if wCrossUpdate.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden when staff updates order of another restaurant, got %d", wCrossUpdate.Code)
		}
	})

	// 3. Server-side table token generation
	t.Run("Server_Side_Table_Token_Generation", func(t *testing.T) {
		// Test dedicated generation endpoint
		reqGen := httptest.NewRequest(http.MethodPost, "/api/v1/restaurant/tables/generate-token", nil)
		reqGen.Header.Set("Authorization", "Bearer "+managerTokenA)
		wGen := httptest.NewRecorder()
		router.ServeHTTP(wGen, reqGen)
		if wGen.Code != http.StatusOK {
			t.Fatalf("expected 200 OK from table token generator, got %d: %s", wGen.Code, wGen.Body.String())
		}

		var genResp map[string]interface{}
		_ = json.Unmarshal(wGen.Body.Bytes(), &genResp)
		tokenStr, ok := genResp["table_token"].(string)
		if !ok || len(tokenStr) < 16 {
			t.Fatalf("expected valid high-entropy server table token, got %v", genResp["table_token"])
		}

		// Test CreateTable auto-generates server-side token if none provided
		createPayload := []byte(`{"table_number":"Table 99","capacity":6}`)
		reqCreate := httptest.NewRequest(http.MethodPost, "/api/v1/restaurant/tables", bytes.NewBuffer(createPayload))
		reqCreate.Header.Set("Authorization", "Bearer "+managerTokenA)
		reqCreate.Header.Set("Content-Type", "application/json")
		wCreate := httptest.NewRecorder()
		router.ServeHTTP(wCreate, reqCreate)
		if wCreate.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created from CreateTable, got %d: %s", wCreate.Code, wCreate.Body.String())
		}

		var createdTable map[string]interface{}
		_ = json.Unmarshal(wCreate.Body.Bytes(), &createdTable)
		createdToken, ok := createdTable["table_token"].(string)
		if !ok || len(createdToken) < 16 {
			t.Fatalf("expected auto-generated high-entropy table token, got %v", createdTable["table_token"])
		}
	})
}
