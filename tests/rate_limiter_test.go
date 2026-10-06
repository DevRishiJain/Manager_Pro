package tests

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/api/middleware"
	"github.com/go-chi/chi/v5"
)

func TestRateLimiterMiddlewareExceeded(t *testing.T) {
	r := chi.NewRouter()
	limiter := middleware.NewRateLimiter(2, 1*time.Minute)
	r.Use(limiter.Middleware)
	r.Get("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on request %d, got %d", i+1, w.Code)
		}
	}

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests on request 3, got %d", w.Code)
	}
}
