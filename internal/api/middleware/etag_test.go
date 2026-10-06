package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/devrishijain/table-manager/internal/api/middleware"
)

func TestETagMiddleware(t *testing.T) {
	handler := middleware.ETagMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":"hello world"}`))
	}))

	// 1. Initial GET request without ETag
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr1.Code)
	}
	etag := rr1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag header to be set, got empty")
	}

	// 2. Subsequent GET with matching If-None-Match
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req2.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusNotModified {
		t.Fatalf("expected status 304 Not Modified, got %d", rr2.Code)
	}
	if rr2.Body.Len() != 0 {
		t.Fatalf("expected empty body for 304, got %d bytes", rr2.Body.Len())
	}

	// 3. Subsequent GET with non-matching If-None-Match
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req3.Header.Set("If-None-Match", `"mismatch"`)
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)

	if rr3.Code != http.StatusOK {
		t.Fatalf("expected status 200 for mismatched ETag, got %d", rr3.Code)
	}
	if rr3.Body.String() != `{"message":"hello world"}` {
		t.Fatalf("expected body content, got %s", rr3.Body.String())
	}
}
