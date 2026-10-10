package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newTestAssistant(rt roundTripFunc) *AssistantService {
	s := NewAssistantService("test-key", "gemini-2.5-flash")
	if rt != nil {
		s.httpClient = &http.Client{
			Transport: rt,
			Timeout:   10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return s
}

func geminiOKResponse(text string) *http.Response {
	body := `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"` + text + `"}]}}]}`
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestAssistantGuideNoKeyNoNetwork(t *testing.T) {
	s := newTestAssistant(func(r *http.Request) (*http.Response, error) {
		t.Fatal("network must not be called without an API key")
		return nil, nil
	})
	s.apiKey = ""
	reply, err := s.Reply(context.Background(), AssistantRequest{Message: "How do I renew my subscription?", Page: "dashboard"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Source != "guide" || reply.Answer == "" {
		t.Fatalf("expected guide reply, got %+v", reply)
	}
	if !strings.Contains(reply.Answer, "subscription") {
		t.Fatalf("keyword topic match expected subscription guide, got: %s", reply.Answer)
	}
}

func TestAssistantInvalidModelNoNetwork(t *testing.T) {
	s := newTestAssistant(func(r *http.Request) (*http.Response, error) {
		t.Fatal("network must not be called for an invalid model name")
		return nil, nil
	})
	s.model = "bad;model?x=1"
	reply, err := s.Reply(context.Background(), AssistantRequest{Message: "hi", Page: "general"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Source != "guide" {
		t.Fatalf("expected guide fallback, got %+v", reply)
	}
}

func TestAssistantCancelBeforeKeyCheck(t *testing.T) {
	s := newTestAssistant(func(r *http.Request) (*http.Response, error) {
		t.Fatal("network must not be called after cancellation")
		return nil, nil
	})
	s.apiKey = ""
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Reply(ctx, AssistantRequest{Message: "hi", Page: "general"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestAssistantPageFallbackAndGeneral(t *testing.T) {
	s := newTestAssistant(nil)
	s.apiKey = ""

	reply, _ := s.Reply(context.Background(), AssistantRequest{Message: "hello", Page: "platform"})
	if reply.Answer != assistantHelp["platform"] {
		t.Fatalf("expected platform guide, got %q", reply.Answer)
	}
	reply, _ = s.Reply(context.Background(), AssistantRequest{Message: "zqxwv", Page: ""})
	if reply.Answer != assistantHelp["general"] {
		t.Fatalf("expected general guide")
	}
	reply, _ = s.Reply(context.Background(), AssistantRequest{Message: "generate an invite code", Page: "settings"})
	if reply.Answer != assistantHelp["franchise"] {
		t.Fatalf("expected franchise guide by keyword, got %q", reply.Answer)
	}
}

func TestAssistantValidation(t *testing.T) {
	s := newTestAssistant(func(r *http.Request) (*http.Response, error) {
		t.Fatal("network must not be called for invalid requests")
		return nil, nil
	})

	cases := []AssistantRequest{
		{Message: "  ", Page: "general"},
		{Message: strings.Repeat("x", 1001), Page: "general"},
		{Message: "hi", Page: "nonsense"},
		{Message: "hi", Page: "general", History: []AssistantMessage{
			{Role: "user", Text: "a"},
		}},
		{Message: "hi", Page: "general", History: []AssistantMessage{
			{Role: "assistant", Text: "a"}, {Role: "user", Text: "b"},
		}},
		{Message: "hi", Page: "general", History: []AssistantMessage{
			{Role: "system", Text: "a"}, {Role: "assistant", Text: "b"},
		}},
		{Message: "hi", Page: "general", History: []AssistantMessage{
			{Role: "user", Text: "   "}, {Role: "assistant", Text: "b"},
		}},
		{Message: "hi", Page: "general", History: []AssistantMessage{
			{Role: "user", Text: strings.Repeat("y", 1001)}, {Role: "assistant", Text: "b"},
		}},
		{Message: "hi", Page: "general", History: []AssistantMessage{
			{Role: "user", Text: "a"}, {Role: "assistant", Text: "b"},
			{Role: "user", Text: "a"}, {Role: "assistant", Text: "b"},
			{Role: "user", Text: "a"}, {Role: "assistant", Text: "b"},
			{Role: "user", Text: "a"}, {Role: "assistant", Text: "b"},
		}},
	}
	for i, req := range cases {
		if _, err := s.Reply(context.Background(), req); !errors.Is(err, ErrInvalidAssistantRequest) {
			t.Fatalf("case %d: expected ErrInvalidAssistantRequest, got %v", i, err)
		}
	}
}

func TestAssistantGeminiPayload(t *testing.T) {
	var captured *http.Request
	var capturedBody []byte
	s := newTestAssistant(func(r *http.Request) (*http.Response, error) {
		captured = r
		capturedBody, _ = io.ReadAll(r.Body)
		return &http.Response{
			StatusCode: 200,
			Body: io.NopCloser(strings.NewReader(
				`{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"I think.","thought":true},{"text":"Here is help."}]}}]}`)),
			Header: make(http.Header),
		}, nil
	})

	reply, err := s.Reply(context.Background(), AssistantRequest{
		Message: "How do I renew?",
		Page:    "subscription",
		History: []AssistantMessage{
			{Role: "user", Text: "hello"},
			{Role: "assistant", Text: "hi there"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Source != "gemini" || reply.Answer != "Here is help." {
		t.Fatalf("expected gemini answer without thought part, got %+v", reply)
	}

	if !strings.Contains(captured.URL.Path, "/models/gemini-2.5-flash:generateContent") {
		t.Fatalf("unexpected URL %s", captured.URL)
	}
	if strings.Contains(captured.URL.RawQuery, "key") {
		t.Fatalf("key must not be in URL: %s", captured.URL)
	}
	if captured.Header.Get("x-goog-api-key") != "test-key" {
		t.Fatal("missing x-goog-api-key header")
	}
	if captured.Header.Get("Content-Type") != "application/json" {
		t.Fatal("missing json content-type")
	}

	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatal(err)
	}
	si := payload["systemInstruction"].(map[string]any)
	siText := si["parts"].([]any)[0].(map[string]any)["text"].(string)
	if siText != assistantInstructions+"\nCurrent page category: subscription" {
		t.Fatalf("systemInstruction mismatch: %q", siText)
	}
	gc := payload["generationConfig"].(map[string]any)
	if gc["maxOutputTokens"] != float64(1024) || gc["temperature"] != 0.2 || gc["responseMimeType"] != "text/plain" {
		t.Fatalf("generationConfig mismatch: %v", gc)
	}
	contents := payload["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(contents))
	}
	if contents[0].(map[string]any)["role"] != "user" {
		t.Fatal("history user role expected")
	}
	if contents[1].(map[string]any)["role"] != "model" {
		t.Fatal("assistant history must map to model role")
	}
	last := contents[2].(map[string]any)
	if last["role"] != "user" || last["parts"].([]any)[0].(map[string]any)["text"] != "How do I renew?" {
		t.Fatalf("last user message mismatch: %v", last)
	}
}

func TestAssistantProviderFailuresFallback(t *testing.T) {
	responses := map[string]*http.Response{
		"non200":    {StatusCode: 500, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"internal x-goog-api-key-leak"}}`)), Header: make(http.Header)},
		"malformed": {StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{{{`)), Header: make(http.Header)},
		"nocand":    {StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"candidates":[]}`)), Header: make(http.Header)},
		"truncated": {StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"finishReason":"MAX_TOKENS","content":{"parts":[{"text":"x"}]}}]}`)), Header: make(http.Header)},
		"blocked":   {StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"promptFeedback":{"blockReason":"SAFETY"}}`)), Header: make(http.Header)},
		"toolarge":  {StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"ok"}]}}]}` + strings.Repeat(" ", 70000))), Header: make(http.Header)},
		"empty":     {StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"finishReason":"STOP","content":{"parts":[]}}]}`)), Header: make(http.Header)},
		"redirect":  {StatusCode: 302, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Location": {"https://evil.example/steal"}}},
	}
	for name, resp := range responses {
		s := newTestAssistant(func(r *http.Request) (*http.Response, error) { return resp, nil })
		reply, err := s.Reply(context.Background(), AssistantRequest{Message: "hi", Page: "general"})
		if err != nil {
			t.Fatalf("%s: unexpected err %v", name, err)
		}
		if reply.Source != "guide" {
			t.Fatalf("%s: expected guide fallback", name)
		}
		if strings.Contains(reply.Answer, "x-goog-api-key-leak") {
			t.Fatalf("%s: upstream error leaked", name)
		}
	}
}

func TestAssistantRedirectNotFollowed(t *testing.T) {
	var hosts []string
	var mu sync.Mutex
	s := newTestAssistant(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		hosts = append(hosts, r.URL.Host)
		mu.Unlock()
		return &http.Response{
			StatusCode: 302,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     http.Header{"Location": {"https://attacker.example/steal"}},
		}, nil
	})
	reply, err := s.Reply(context.Background(), AssistantRequest{Message: "hi", Page: "general"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Source != "guide" {
		t.Fatalf("expected guide on redirect, got %+v", reply)
	}
	if len(hosts) != 1 || hosts[0] != "generativelanguage.googleapis.com" {
		t.Fatalf("redirect must not be followed, hosts: %v", hosts)
	}
}

func TestAssistantBusyConcurrency(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var relOnce sync.Once
	free := func() { relOnce.Do(func() { close(release) }) }
	defer free()
	s := newTestAssistant(func(r *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-release
		return geminiOKResponse("ok"), nil
	})

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Reply(context.Background(), AssistantRequest{Message: "hi", Page: "general"})
			results[i] = err
		}(i)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("calls did not start in time")
		}
	}
	_, err := s.Reply(context.Background(), AssistantRequest{Message: "hi", Page: "general"})
	if !errors.Is(err, ErrAssistantBusy) {
		t.Fatalf("expected busy on saturated slots, got %v", err)
	}
	free()
	wg.Wait()
	for _, e := range results {
		if e != nil {
			t.Fatalf("unexpected error %v", e)
		}
	}
}

func TestAssistantBudgetCap(t *testing.T) {
	var calls int32
	s := newTestAssistant(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return geminiOKResponse("ok"), nil
	})
	for i := 0; i < 30; i++ {
		if _, err := s.Reply(context.Background(), AssistantRequest{Message: "hi", Page: "general"}); err != nil {
			t.Fatalf("call %d unexpected err %v", i, err)
		}
	}
	if _, err := s.Reply(context.Background(), AssistantRequest{Message: "hi", Page: "general"}); !errors.Is(err, ErrAssistantBusy) {
		t.Fatalf("31st call expected busy, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 30 {
		t.Fatalf("expected 30 upstream calls, got %d", calls)
	}
}

func TestAssistantBudgetWindowRollover(t *testing.T) {
	var calls int32
	s := newTestAssistant(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return geminiOKResponse("ok"), nil
	})
	s.mu.Lock()
	s.calls = assistantWindowCalls
	s.window = time.Now().Add(-2 * time.Minute)
	s.mu.Unlock()
	if _, err := s.Reply(context.Background(), AssistantRequest{Message: "hi", Page: "general"}); err != nil {
		t.Fatalf("expired window must reset budget, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 upstream call after rollover, got %d", calls)
	}
}

func TestAssistantCancellation(t *testing.T) {
	s := newTestAssistant(func(r *http.Request) (*http.Response, error) {
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(5 * time.Second):
			return geminiOKResponse("late"), nil
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Reply(ctx, AssistantRequest{Message: "hi", Page: "general"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
