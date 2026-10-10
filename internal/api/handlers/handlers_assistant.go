package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/devrishijain/table-manager/internal/service"
)

func (h *APIHandler) SetAssistantService(s *service.AssistantService) {
	h.assistantService = s
}

func (h *APIHandler) AssistantChat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		errorResponse(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return
	}

	svc := h.assistantService
	if svc == nil {
		svc = service.NewAssistantService("", "")
	}

	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req service.AssistantRequest
	if err := dec.Decode(&req); err != nil {
		var mbErr *http.MaxBytesError
		if errors.As(err, &mbErr) {
			errorResponse(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		errorResponse(w, http.StatusBadRequest, "invalid request payload")
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		var mbErr *http.MaxBytesError
		if errors.As(err, &mbErr) {
			errorResponse(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		errorResponse(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	reply, err := svc.Reply(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidAssistantRequest):
			errorResponse(w, http.StatusBadRequest, "invalid assistant request")
		case errors.Is(err, service.ErrAssistantBusy):
			w.Header().Set("Retry-After", "60")
			errorResponse(w, http.StatusTooManyRequests, "assistant is busy; please try again shortly")
		case errors.Is(err, context.DeadlineExceeded):
			errorResponse(w, http.StatusGatewayTimeout, "request timed out")
		case errors.Is(err, context.Canceled):
			errorResponse(w, http.StatusRequestTimeout, "request canceled")
		default:
			errorResponse(w, http.StatusInternalServerError, "assistant unavailable")
		}
		return
	}

	jsonResponse(w, http.StatusOK, reply)
}
