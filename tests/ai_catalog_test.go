package tests

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/devrishijain/table-manager/internal/adapter/forecast"
	"github.com/devrishijain/table-manager/internal/adapter/storage"
	"github.com/devrishijain/table-manager/internal/api"
	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/service"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/google/uuid"
)

func TestAICatalogRoute_ValidationAndHandling(t *testing.T) {
	repo := memory.NewMemoryRepository()
	objStore := storage.NewMemoryObjectStore()
	forecastProvider := forecast.NewWeightedMovingAverageForecast()

	sessionSvc := service.NewSessionService(repo)
	orderSvc := service.NewOrderService(repo)
	exitSvc := service.NewExitService(repo)
	ledgerSvc := service.NewLedgerService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, "secret")
	analyticsSvc := service.NewAnalyticsService(repo, forecastProvider)
	onboardingSvc := service.NewOnboardingService(repo)

	h := handlers.NewAPIHandler(
		sessionSvc, orderSvc, paymentSvc, exitSvc, ledgerSvc, analyticsSvc, onboardingSvc, objStore, repo, "secret",
	)

	router := api.NewRouter(h, repo, []byte("super-secure-dining-os-jwt-secret-key-32b"))

	// 1. Service unavailable when AI Catalog service not set
	req := httptest.NewRequest("POST", "/api/v1/public/menu/ai-catalog", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when AI service is nil, got %d", rec.Code)
	}

	// 2. Set AI Catalog service
	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey == "" {
		geminiKey = "mock-gemini-key"
	}
	aiSvc := service.NewAICatalogService(repo, objStore, geminiKey, "gemini-3.6-flash")
	h.SetAICatalogService(aiSvc)

	// 3. Missing restaurant_id
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.Close()
	req = httptest.NewRequest("POST", "/api/v1/public/menu/ai-catalog", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing restaurant_id, got %d", rec.Code)
	}

	// 4. Missing menu_image form file
	body = &bytes.Buffer{}
	writer = multipart.NewWriter(body)
	_ = writer.WriteField("restaurant_id", uuid.New().String())
	_ = writer.Close()
	req = httptest.NewRequest("POST", "/api/v1/public/menu/ai-catalog", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing form file, got %d", rec.Code)
	}
}

func TestAICatalogService_ProcessAndCatalog_EmptyImage(t *testing.T) {
	repo := memory.NewMemoryRepository()
	objStore := storage.NewMemoryObjectStore()
	aiSvc := service.NewAICatalogService(repo, objStore, "test-key", "gemini-2.5-flash")

	_, err := aiSvc.ProcessAndCatalogMenu(context.Background(), uuid.New(), []byte{}, "image/jpeg")
	if err != service.ErrEmptyMenuImage {
		t.Fatalf("expected ErrEmptyMenuImage, got %v", err)
	}
}
