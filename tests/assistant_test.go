package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const assistantPath = "/api/v1/public/assistant/chat"

func assistantCall(t *testing.T, f *contractFixture, contentType, key string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, assistantPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func TestAssistantChatPublicNoKey(t *testing.T) {
	f := newContractFixture(t)
	rec := assistantCall(t, f, "application/json", "", []byte(`{"message":"How do I renew my subscription?"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing Cache-Control: no-store")
	}
	var reply struct {
		Answer string `json:"answer"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Source != "guide" || reply.Answer == "" {
		t.Fatalf("expected guide reply, got %+v", reply)
	}
}

func TestAssistantChatBadRequests(t *testing.T) {
	f := newContractFixture(t)
	malformed := [][]byte{
		[]byte(`{not json`),
		[]byte(`{"message":"hi","bogus":1}`),
		[]byte(`{"message":"hi"} {"message":"again"}`),
		[]byte(`{"message":""}`),
		[]byte(`{"message":"hi","page":"bogus"}`),
	}
	for i, body := range malformed {
		rec := assistantCall(t, f, "application/json", "", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("case %d: expected 400, got %d (%s)", i, rec.Code, rec.Body.String())
		}
	}
}

func TestAssistantChatContentType(t *testing.T) {
	f := newContractFixture(t)
	rec := assistantCall(t, f, "text/plain", "", []byte(`{"message":"hi"}`))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", rec.Code)
	}
	rec = assistantCall(t, f, "application/json; charset=utf-8", "", []byte(`{"message":"hi"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with charset, got %d", rec.Code)
	}
}

func TestAssistantChatOversize(t *testing.T) {
	f := newContractFixture(t)
	big := `{"message":"` + strings.Repeat("x", 40<<10) + `"}`
	rec := assistantCall(t, f, "application/json", "", []byte(big))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
}

func TestAssistantChatTrailingOversize(t *testing.T) {
	f := newContractFixture(t)
	body := []byte(`{"message":"hi"}` + strings.Repeat(" ", 40<<10))
	rec := assistantCall(t, f, "application/json", "", body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for trailing oversized bytes, got %d", rec.Code)
	}
}

func TestAssistantChatRateLimit(t *testing.T) {
	f := newContractFixture(t)
	var last *httptest.ResponseRecorder
	for i := 0; i < 9; i++ {
		last = assistantCall(t, f, "application/json", "", []byte(`{"message":"hi"}`))
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("9th call expected 429, got %d: %s", last.Code, last.Body.String())
	}
}

func TestAssistantChatIdempotencyBypass(t *testing.T) {
	f := newContractFixture(t)
	rec1 := assistantCall(t, f, "application/json", "key-abc", []byte(`{"message":"How do I renew?"}`))
	rec2 := assistantCall(t, f, "application/json", "key-abc", []byte(`{"message":"How do tables work?"}`))
	if rec1.Code != http.StatusOK || rec2.Code != http.StatusOK {
		t.Fatalf("expected 200s, got %d and %d", rec1.Code, rec2.Code)
	}
	if rec1.Body.String() == rec2.Body.String() {
		t.Fatal("idempotency replay detected: same reply for different messages")
	}
}

func TestAssistantChatNoImpactOnOtherRoutes(t *testing.T) {
	f := newContractFixture(t)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz broken: %d", rec.Code)
	}
}
