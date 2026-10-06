package ws

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

type Server struct {
	hub            *Hub
	ticketManager  *TicketManager
	originPatterns []string
}

func NewServer(hub *Hub, tm *TicketManager, originPatterns []string) *Server {
	if len(originPatterns) == 0 {
		originPatterns = []string{"*"}
	}
	return &Server{
		hub:            hub,
		ticketManager:  tm,
		originPatterns: originPatterns,
	}
}

func (s *Server) Hub() *Hub {
	return s.hub
}

func (s *Server) TicketManager() *TicketManager {
	return s.ticketManager
}

// ServeHTTP handles the WebSocket handshake and initiates client event streaming.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Validate and redeem single-use ticket BEFORE upgrading
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing websocket ticket"})
		return
	}

	claims, err := s.ticketManager.RedeemTicket(ticket)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid or expired websocket ticket"})
		return
	}

	// 2. Accept WebSocket connection with compression & origin check
	acceptOpts := &websocket.AcceptOptions{
		OriginPatterns:  s.originPatterns,
		CompressionMode: websocket.CompressionContextTakeover,
	}
	if len(s.originPatterns) == 1 && s.originPatterns[0] == "*" {
		acceptOpts.InsecureSkipVerify = true
	}

	conn, err := websocket.Accept(w, r, acceptOpts)
	if err != nil {
		// Accept writes error to w directly
		return
	}

	// 3. Resolve remote IP for rate limiting / connection counting
	remoteIP := extractRemoteIP(r)

	// 4. Create and register client
	client := NewClient(s.hub, conn, claims, remoteIP)
	if err := s.hub.Register(client); err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}

	// 5. If client provided last_event_id, replay missed events from 5-minute resume buffer
	lastEventID := r.URL.Query().Get("last_event_id")
	if lastEventID != "" {
		for _, room := range claims.Rooms {
			s.hub.ReplayAfter(client, room, lastEventID)
		}
	}

	// 6. Run connection pumps (ReadPump blocks until disconnect)
	ctx := r.Context()
	go client.WritePump(ctx)
	client.ReadPump(ctx)
}

func extractRemoteIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return strings.TrimSpace(xrip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
