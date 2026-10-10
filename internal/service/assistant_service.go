package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	ErrAssistantBusy           = errors.New("assistant is busy; please try again shortly")
	ErrInvalidAssistantRequest = errors.New("invalid assistant request")
)

type AssistantMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type AssistantRequest struct {
	Message string             `json:"message"`
	Page    string             `json:"page"`
	History []AssistantMessage `json:"history,omitempty"`
}

type AssistantReply struct {
	Answer string `json:"answer"`
	Source string `json:"source"`
}

type AssistantService struct {
	apiKey     string
	model      string
	httpClient *http.Client
	slots      chan struct{}
	mu         sync.Mutex
	window     time.Time
	calls      int
}

const (
	assistantWindow      = 1 * time.Minute
	assistantWindowCalls = 30
	assistantMaxInFlight = 2
	assistantMaxMessage  = 1000
	assistantMaxHistory  = 6
	assistantMaxAnswer   = 4000
	assistantBodyLimit   = 64 * 1024
)

var assistantModelPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func NewAssistantService(apiKey, model string) *AssistantService {
	if model == "" {
		model = "gemini-2.5-flash"
	}
	return &AssistantService{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		slots:  make(chan struct{}, assistantMaxInFlight),
		window: time.Now(),
	}
}

func validateAssistantRequest(req AssistantRequest) (AssistantRequest, error) {
	msg := strings.TrimSpace(req.Message)
	n := len([]rune(msg))
	if n < 1 || n > assistantMaxMessage {
		return req, ErrInvalidAssistantRequest
	}
	req.Message = msg

	req.Page = strings.ToLower(strings.TrimSpace(req.Page))
	if req.Page == "" {
		req.Page = "general"
	}
	if _, ok := assistantHelp[req.Page]; !ok {
		return req, ErrInvalidAssistantRequest
	}

	if len(req.History) > assistantMaxHistory {
		return req, ErrInvalidAssistantRequest
	}
	for i, m := range req.History {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role != "user" && role != "assistant" {
			return req, ErrInvalidAssistantRequest
		}
		txt := strings.TrimSpace(m.Text)
		tr := len([]rune(txt))
		if tr < 1 || tr > assistantMaxMessage {
			return req, ErrInvalidAssistantRequest
		}
		wantRole := "user"
		if i%2 == 1 {
			wantRole = "assistant"
		}
		if role != wantRole {
			return req, ErrInvalidAssistantRequest
		}
		req.History[i].Role = role
		req.History[i].Text = txt
	}
	if len(req.History)%2 != 0 {
		return req, ErrInvalidAssistantRequest
	}
	return req, nil
}

func guideReply(req AssistantRequest) *AssistantReply {
	lower := strings.ToLower(req.Message)
	for _, t := range assistantTopicKeywords {
		for _, kw := range t.terms {
			if strings.Contains(lower, kw) {
				return &AssistantReply{Answer: assistantHelp[t.page], Source: "guide"}
			}
		}
	}
	if a, ok := assistantHelp[req.Page]; ok {
		return &AssistantReply{Answer: a, Source: "guide"}
	}
	return &AssistantReply{Answer: assistantHelp["general"], Source: "guide"}
}

func (s *AssistantService) tryAcquire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if now.Sub(s.window) >= assistantWindow {
		s.window = now
		s.calls = 0
	}
	if s.calls >= assistantWindowCalls {
		return false
	}
	select {
	case s.slots <- struct{}{}:
		s.calls++
		return true
	default:
		return false
	}
}

func (s *AssistantService) release() {
	<-s.slots
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Candidates []struct {
		FinishReason string `json:"finishReason"`
		Content      struct {
			Parts []struct {
				Text    string `json:"text"`
				Thought bool   `json:"thought"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

func (s *AssistantService) Reply(ctx context.Context, req AssistantRequest) (*AssistantReply, error) {
	req, err := validateAssistantRequest(req)
	if err != nil {
		return nil, err
	}
	fallback := guideReply(req)

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.apiKey == "" || !assistantModelPattern.MatchString(s.model) {
		return fallback, nil
	}
	if !s.tryAcquire() {
		return nil, ErrAssistantBusy
	}
	defer s.release()

	contents := make([]geminiContent, 0, len(req.History)+1)
	for _, m := range req.History {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: m.Text}},
		})
	}
	contents = append(contents, geminiContent{
		Role:  "user",
		Parts: []geminiPart{{Text: req.Message}},
	})

	payload := map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]string{{"text": assistantInstructions + "\nCurrent page category: " + req.Page}}},
		"contents":          contents,
		"generationConfig":  map[string]any{"maxOutputTokens": 1024, "temperature": 0.2, "responseMimeType": "text/plain"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fallback, nil
	}

	url := "https://generativelanguage.googleapis.com/v1beta/models/" + s.model + ":generateContent"
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fallback, nil
	}
	hreq.Header.Set("x-goog-api-key", s.apiKey)
	hreq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(hreq)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return fallback, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, assistantBodyLimit))
		return fallback, nil
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, assistantBodyLimit+1))
	if err != nil {
		return fallback, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) > assistantBodyLimit {
		return fallback, nil
	}

	var g geminiResponse
	if err := json.Unmarshal(raw, &g); err != nil {
		return fallback, nil
	}
	if g.PromptFeedback != nil && g.PromptFeedback.BlockReason != "" {
		return fallback, nil
	}
	if len(g.Candidates) == 0 || g.Candidates[0].FinishReason != "STOP" {
		return fallback, nil
	}

	var b strings.Builder
	for _, p := range g.Candidates[0].Content.Parts {
		if p.Thought || p.Text == "" {
			continue
		}
		b.WriteString(p.Text)
	}
	answer := strings.TrimSpace(b.String())
	if answer == "" {
		return fallback, nil
	}
	if runes := []rune(answer); len(runes) > assistantMaxAnswer {
		answer = string(runes[:assistantMaxAnswer])
	}
	return &AssistantReply{Answer: answer, Source: "gemini"}, nil
}
