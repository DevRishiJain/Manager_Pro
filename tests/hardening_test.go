package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/adapter/forecast"
	s3adapter "github.com/devrishijain/table-manager/internal/adapter/storage"
	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/api/middleware"
	"github.com/devrishijain/table-manager/internal/domain/money"
	domainPayment "github.com/devrishijain/table-manager/internal/domain/payment"
	domainRestaurant "github.com/devrishijain/table-manager/internal/domain/restaurant"
	domainSession "github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/internal/worker"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestIdempotencyReplayAndConflict(t *testing.T) {
	mgr := middleware.NewIdempotencyManager()
	callCount := 0

	handler := mgr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Custom-Server-Header", "TableOS-v1")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"order_id":"123","execution_count":%d}`, callCount)))
	}))

	idempKey := "idem-test-uuid-999"
	bodyPayload := []byte(`{"table_id":"T1","amount":1500}`)

	// 1. Initial request
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewReader(bodyPayload))
	req1.Header.Set("Idempotency-Key", idempKey)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected initial request to return 201, got %d", rec1.Code)
	}
	if callCount != 1 {
		t.Fatalf("expected handler called once, got %d", callCount)
	}
	expectedBody := `{"order_id":"123","execution_count":1}`
	if rec1.Body.String() != expectedBody {
		t.Fatalf("expected body %s, got %s", expectedBody, rec1.Body.String())
	}

	// 2. Exact Replay: Same key + Identical payload
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewReader(bodyPayload))
	req2.Header.Set("Idempotency-Key", idempKey)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusCreated {
		t.Fatalf("expected replay request to return 201, got %d", rec2.Code)
	}
	if rec2.Header().Get("X-Cache") != "IDEMPOTENT-REPLAY" {
		t.Fatalf("expected X-Cache: IDEMPOTENT-REPLAY, got %s", rec2.Header().Get("X-Cache"))
	}
	if rec2.Header().Get("Custom-Server-Header") != "TableOS-v1" {
		t.Fatalf("expected cached header Custom-Server-Header to be preserved")
	}
	if rec2.Body.String() != expectedBody {
		t.Fatalf("expected replayed body to match original exactly, got %s", rec2.Body.String())
	}
	if callCount != 1 {
		t.Fatalf("handler must NOT have been executed on replay, but callCount is %d", callCount)
	}

	// 3. Conflict: Same key + Differing payload
	alteredPayload := []byte(`{"table_id":"T1","amount":2500}`)
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewReader(alteredPayload))
	req3.Header.Set("Idempotency-Key", idempKey)
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusConflict {
		t.Fatalf("expected payload mismatch to return 409 Conflict, got %d", rec3.Code)
	}
	var conflictResp map[string]string
	_ = json.Unmarshal(rec3.Body.Bytes(), &conflictResp)
	if conflictResp["code"] != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("expected error code IDEMPOTENCY_KEY_REUSED, got %s", conflictResp["code"])
	}
	if callCount != 1 {
		t.Fatalf("handler must NOT have been executed on conflict, but callCount is %d", callCount)
	}
}

func TestTransactionalOutboxWorkerReliabilityAndDLQ(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	restID := uuid.New()

	// Seed outbox event
	eventID := uuid.New()
	event := &storage.OutboxEvent{
		ID:           eventID,
		RestaurantID: restID,
		EventType:    "SMS_OTP_DISPATCH",
		AggregateID:  "session-100",
		Payload:      json.RawMessage(`{"phone":"+919876543210","otp":"123456"}`),
		Status:       storage.OutboxStatusPending,
		CreatedAt:    time.Now(),
	}
	if err := repo.StoreOutboxEvent(ctx, event); err != nil {
		t.Fatalf("failed to store outbox event: %v", err)
	}

	w := worker.NewWorker(repo, nil)
	w.SetLeaseDuration(2 * time.Second)
	w.SetMaxRetries(3)
	w.SetBaseBackoff(10 * time.Millisecond)

	// Simulated downstream dispatcher that fails transiently
	dispatchAttempts := 0
	shouldFail := true
	w.SetDispatcher(func(ctx context.Context, e storage.OutboxEvent) error {
		dispatchAttempts++
		if shouldFail {
			return errors.New("simulated external gateway timeout (504)")
		}
		return nil
	})

	// 1. Process failure #1 -> should increment retries to 1, schedule backoff
	w.ProcessOutboxOnce(ctx)
	if dispatchAttempts != 1 {
		t.Fatalf("expected 1 dispatch attempt, got %d", dispatchAttempts)
	}

	// Immediate re-check before backoff expires: should NOT claim because next_retry_at is in future
	claimedImmediate, _ := repo.ClaimPendingOutbox(ctx, 10, 2*time.Second)
	if len(claimedImmediate) != 0 {
		t.Fatalf("expected 0 events claimed during backoff window, got %d", len(claimedImmediate))
	}

	// Wait for backoff to elapse
	time.Sleep(25 * time.Millisecond)

	// 2. Process failure #2
	w.ProcessOutboxOnce(ctx)
	if dispatchAttempts != 2 {
		t.Fatalf("expected 2 dispatch attempts, got %d", dispatchAttempts)
	}

	time.Sleep(50 * time.Millisecond)

	// 3. Process failure #3 -> hits maxRetries (3), transitions to DEAD_LETTER
	w.ProcessOutboxOnce(ctx)
	if dispatchAttempts != 3 {
		t.Fatalf("expected 3 dispatch attempts, got %d", dispatchAttempts)
	}

	// Verify DEAD_LETTER state
	events, _ := repo.ClaimPendingOutbox(ctx, 10, 2*time.Second)
	if len(events) != 0 {
		t.Fatalf("dead-lettered event must NOT be claimed as pending, got %d", len(events))
	}

	// 4. Test Poison-Event Replay Capability
	if err := repo.ReplayDeadLetterOutbox(ctx, eventID); err != nil {
		t.Fatalf("failed to replay dead letter outbox: %v", err)
	}

	// Now fix simulated downstream issue and process once more
	shouldFail = false
	publishedCount := w.ProcessOutboxOnce(ctx)
	if publishedCount != 1 {
		t.Fatalf("expected replayed event to be published successfully, got %d", publishedCount)
	}
}

func TestS3PrivateEvidenceAndPreSignedURL(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	jwtSecret := []byte("secret-key-32-bytes-long-dining-os")
	restID := uuid.New()
	staffID := uuid.New()

	// Seed restaurant
	_ = repo.CreateRestaurant(ctx, &domainRestaurant.Restaurant{
		ID:        restID,
		Name:      "Test Restaurant",
		Timezone:  "Asia/Kolkata",
		CreatedAt: time.Now(),
	})

	// Seed dining session
	sessID := uuid.New()
	_ = repo.CreateSession(ctx, &domainSession.DiningSession{
		ID:             sessID,
		RestaurantID:   restID,
		TableID:        uuid.New(),
		Status:         domainSession.StateAwaitingPayment,
		FinalTotal:     money.New(150000), // ₹1,500
		CreatedAt:      time.Now(),
		ExpiryDeadline: time.Now().Add(2 * time.Hour),
	})

	s3Store := s3adapter.NewS3ObjectStore(s3adapter.S3Config{
		Region:          "ap-south-1",
		BucketName:      "tableos-private-evidence",
		AccessKeyID:     "mock_test_key",
		SecretAccessKey: "mock_test_secret",
	})

	// 1. Upload payment evidence receipt JPEG
	jpegData := []byte("\xFF\xD8\xFF\xE0\x00\x10JFIF\x00\x01\x01\x01\x00`\x00`\x00\x00Payment Receipt Proof Valid Image")
	paymentID := uuid.New()
	stored, err := s3Store.UploadProof(ctx, restID, paymentID, jpegData)
	if err != nil {
		t.Fatalf("failed to upload proof: %v", err)
	}
	if stored.Key == "" || stored.SHA256Hash == "" {
		t.Fatalf("expected stored file key and sha256 hash to be populated")
	}

	// 2. Confirm external platform payment using S3 coordinates
	sessionSvc := service.NewSessionService(repo)
	orderSvc := service.NewOrderService(repo)
	exitSvc := service.NewExitService(repo)
	ledgerSvc := service.NewLedgerService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "test-webhook-secret")
	forecastProv := forecast.NewWeightedMovingAverageForecast()
	analyticsSvc := service.NewAnalyticsService(repo, forecastProv)
	onboardSvc := service.NewOnboardingService(repo)

	platformName := "EAZYDINER"
	txID := "EAZY-TXN-998877"
	bucketName := s3Store.Config().BucketName
	p, err := paymentSvc.ConfirmPayment(ctx, domainPayment.PaymentConfirmationRequest{
		PaymentID:             paymentID,
		SessionID:             sessID,
		RestaurantID:          restID,
		Amount:                money.New(150000),
		Method:                domainPayment.MethodExternalPlatform,
		ExternalPlatformName:  &platformName,
		EvidenceTransactionID: &txID,
		EvidenceBucket:        &bucketName,
		EvidenceObjectKey:     &stored.Key,
		EvidenceContentType:   &stored.MIMEType,
		EvidenceSizeBytes:     &stored.SizeBytes,
		EvidenceSHA256:        &stored.SHA256Hash,
		ConfirmedByStaffID:    &staffID,
	})
	if err != nil {
		t.Fatalf("failed to confirm payment with S3 evidence: %v", err)
	}
	if p.EvidenceObjectKey == nil || *p.EvidenceObjectKey != stored.Key {
		t.Fatalf("expected payment to store private S3 object key")
	}

	// 3. Request temporary signed URL via handler
	apiHandler := handlers.NewAPIHandler(
		sessionSvc,
		orderSvc,
		paymentSvc,
		exitSvc,
		ledgerSvc,
		analyticsSvc,
		onboardSvc,
		s3Store,
		repo,
		"test-webhook-secret",
	)

	staffToken, err := crypto.GenerateStaffJWT(jwtSecret, staffID, restID, "MANAGER", false, 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to generate staff token: %v", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.StaffAuth(jwtSecret))
	r.Get("/api/v1/restaurant/payments/{id}/evidence-url", apiHandler.GetPaymentEvidenceURL)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/restaurant/payments/"+paymentID.String()+"/evidence-url", nil)
	req.Header.Set("Authorization", "Bearer "+staffToken)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected evidence URL endpoint to return 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var signedURLResp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &signedURLResp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	signedURL, ok := signedURLResp["signed_url"].(string)
	if !ok || signedURL == "" {
		t.Fatalf("expected signed_url string in response")
	}
	if !bytes.Contains([]byte(signedURL), []byte("expires=")) {
		t.Fatalf("expected signed URL to contain expires query parameter: %s", signedURL)
	}
}
