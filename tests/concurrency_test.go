package tests

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/google/uuid"
)

func TestConcurrentTableQRScansOneSessionWinner(t *testing.T) {
	_, repo, _ := setupTestRouter(t)
	sessionSvc := service.NewSessionService(repo)

	restID := uuid.New()
	tableID := uuid.New()
	tableToken := "table-qr-token-unique-123"

	_ = repo.CreateRestaurant(context.Background(), &restaurant.Restaurant{
		ID:     restID,
		Name:   "Rush Hour Cafe",
		Status: restaurant.StatusActive,
	})

	_ = repo.CreateTable(context.Background(), &restaurant.Table{
		ID:           tableID,
		RestaurantID: restID,
		TableNumber:  "T1",
		TableToken:   tableToken,
		IsActive:     true,
	})

	// Simulate 20 concurrent devices scanning the exact same table QR at the exact same millisecond (§9.1, §9.20)
	concurrency := 20
	var wg sync.WaitGroup
	wg.Add(concurrency)

	type scanResult struct {
		session *session.DiningSession
		isNew   bool
		err     error
	}
	results := make([]scanResult, concurrency)

	for i := 0; i < concurrency; i++ {
		idx := i
		go func() {
			defer wg.Done()
			devToken := fmt.Sprintf("device-%d", idx)
			s, isNew, err := sessionSvc.StartSession(context.Background(), tableToken, devToken, fmt.Sprintf("Guest %d", idx), "+919876543210", 2, "fp-mock")
			results[idx] = scanResult{session: s, isNew: isNew, err: err}
		}()
	}
	wg.Wait()

	// Verify results
	var newSessionCount int
	var winningSessionID uuid.UUID

	for _, res := range results {
		if res.err != nil {
			t.Fatalf("unexpected scan error: %v", res.err)
		}
		if res.isNew {
			newSessionCount++
			winningSessionID = res.session.ID
		}
	}

	if newSessionCount != 1 {
		t.Fatalf("expected exactly 1 new session created under concurrent scans, got %d", newSessionCount)
	}

	// Verify all 20 participants joined the EXACT same session ID!
	for i, res := range results {
		if res.session.ID != winningSessionID {
			t.Errorf("device %d assigned to wrong session ID: got %s, expected %s", i, res.session.ID, winningSessionID)
		}
	}

	// Verify database state has strictly 1 active session for table
	active, err := repo.GetActiveSessionByTableID(context.Background(), tableID)
	if err != nil || active == nil {
		t.Fatal("expected active session in DB for table")
	}
	if active.ID != winningSessionID {
		t.Errorf("expected DB active session to be %s, got %s", winningSessionID, active.ID)
	}
}

func TestOptimisticLockingVersionConflict(t *testing.T) {
	_, repo, _ := setupTestRouter(t)

	sess := &session.DiningSession{
		ID:           uuid.New(),
		RestaurantID: uuid.New(),
		TableID:      uuid.New(),
		Status:       session.StateOpen,
		SessionToken: "token-1",
		Version:      1,
	}
	_ = repo.CreateSession(context.Background(), sess)

	// Thread 1 reads version 1
	tx1Copy, _ := repo.GetSessionByID(context.Background(), sess.ID)
	// Thread 2 reads version 1
	tx2Copy, _ := repo.GetSessionByID(context.Background(), sess.ID)

	// Thread 1 updates successfully (bumps to version 2)
	tx1Copy.Status = session.StateOpenVerified
	if err := repo.UpdateSession(context.Background(), tx1Copy); err != nil {
		t.Fatalf("thread 1 update failed: %v", err)
	}

	// Thread 2 tries to update with stale version 1 -> MUST FAIL with optimistic lock conflict!
	tx2Copy.Status = session.StateForceClosed
	err := repo.UpdateSession(context.Background(), tx2Copy)
	if err == nil {
		t.Fatal("expected optimistic lock conflict for stale version, but update succeeded")
	}
}

func TestWebhookIdempotency(t *testing.T) {
	router, _, _ := setupTestRouter(t)

	eventID := "evt_test_razorpay_retry_999"
	body := []byte(`{
		"event": "payment.captured",
		"payload": {
			"payment": {
				"entity": {
					"id": "pay_test_123",
					"amount": 50000,
					"status": "captured",
					"order_id": "order_test_123",
					"notes": {"session_id": "00000000-0000-0000-0000-000000000000"}
				}
			}
		}
	}`)

	// Delivery 1: Processed or recorded
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/razorpay", bytes.NewBuffer(body))
	req1.Header.Set("X-Razorpay-Event-Id", eventID)
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Errorf("expected webhook delivery 1 to return 200, got %d", w1.Code)
	}

	// Delivery 2 (Network Retry with exact same Event-ID): Must return 200 duplicate_ignored
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/razorpay", bytes.NewBuffer(body))
	req2.Header.Set("X-Razorpay-Event-Id", eventID)
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("expected webhook retry 2 to return 200, got %d", w2.Code)
	}
	if !bytes.Contains(w2.Body.Bytes(), []byte("duplicate_ignored")) {
		t.Errorf("expected duplicate_ignored response body, got %s", w2.Body.String())
	}
}

func TestIdempotencyKeyPayloadTampering(t *testing.T) {
	router, _, _ := setupTestRouter(t)

	idempotencyKey := "idem-key-customer-order-44"

	// Request 1 with payload A
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/session/start", bytes.NewBuffer([]byte(`{"table_token":"t1","device_token":"d1"}`)))
	req1.Header.Set("Idempotency-Key", idempotencyKey)
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	// Request 2 reusing same Idempotency-Key with MODIFIED payload B -> MUST return 409 Conflict (§9.17)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/session/start", bytes.NewBuffer([]byte(`{"table_token":"t2-tampered","device_token":"d1"}`)))
	req2.Header.Set("Idempotency-Key", idempotencyKey)
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict for tampered payload under same idempotency key, got %d", w2.Code)
	}
}
