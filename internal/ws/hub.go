package ws

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrTooManyConnectionsIP   = errors.New("too many websocket connections from this IP")
	ErrTooManyConnectionsRest = errors.New("too many websocket connections for this restaurant")
	ErrClientNil              = errors.New("client cannot be nil")
)

type HubConfig struct {
	MaxConnPerIP     int
	MaxConnPerRest   int
	ResumeBufferTTL  time.Duration
	MaxBufferPerRoom int
}

func DefaultHubConfig() HubConfig {
	return HubConfig{
		MaxConnPerIP:     50,
		MaxConnPerRest:   500,
		ResumeBufferTTL:  5 * time.Minute,
		MaxBufferPerRoom: 200,
	}
}

type Hub struct {
	mu          sync.RWMutex
	clients     map[*Client]bool
	rooms       map[string]map[*Client]bool
	ipConns     map[string]int
	restConns   map[string]int
	roomBuffers map[string][]*Envelope
	config      HubConfig
	seqCounter  atomic.Int64
	closed      bool
}

func NewHub(cfg ...HubConfig) *Hub {
	c := DefaultHubConfig()
	if len(cfg) > 0 {
		c = cfg[0]
	}
	return &Hub{
		clients:     make(map[*Client]bool),
		rooms:       make(map[string]map[*Client]bool),
		ipConns:     make(map[string]int),
		restConns:   make(map[string]int),
		roomBuffers: make(map[string][]*Envelope),
		config:      c,
	}
}

// StartCleanup starts a background routine that purges events older than ResumeBufferTTL.
func (h *Hub) StartCleanup(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.purgeExpiredBuffers()
			}
		}
	}()
}

// Register adds a client to the hub and subscribes it to all its authorized rooms.
func (h *Hub) Register(c *Client) error {
	if c == nil {
		return ErrClientNil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return errors.New("hub is closed")
	}

	// 1. Enforce IP connection limit
	if h.config.MaxConnPerIP > 0 && h.ipConns[c.RemoteIP] >= h.config.MaxConnPerIP {
		return ErrTooManyConnectionsIP
	}

	// 2. Enforce Restaurant connection limit
	restID := c.Claims.RestaurantID.String()
	if h.config.MaxConnPerRest > 0 && h.restConns[restID] >= h.config.MaxConnPerRest {
		return ErrTooManyConnectionsRest
	}

	h.clients[c] = true
	h.ipConns[c.RemoteIP]++
	h.restConns[restID]++

	for room := range c.Rooms {
		if h.rooms[room] == nil {
			h.rooms[room] = make(map[*Client]bool)
		}
		h.rooms[room][c] = true
	}

	return nil
}

// Unregister removes a client from the hub and all rooms.
func (h *Hub) Unregister(c *Client) {
	if c == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.clients[c] {
		return
	}

	delete(h.clients, c)

	h.ipConns[c.RemoteIP]--
	if h.ipConns[c.RemoteIP] <= 0 {
		delete(h.ipConns, c.RemoteIP)
	}

	restID := c.Claims.RestaurantID.String()
	h.restConns[restID]--
	if h.restConns[restID] <= 0 {
		delete(h.restConns, restID)
	}

	for room := range c.Rooms {
		if roomClients, ok := h.rooms[room]; ok {
			delete(roomClients, c)
			if len(roomClients) == 0 {
				delete(h.rooms, room)
			}
		}
	}
}

// Broadcast creates a canonical Envelope, appends it to the room buffer, and pushes to all room clients.
func (h *Hub) Broadcast(room string, eventType string, payload interface{}) (*Envelope, error) {
	seq := h.seqCounter.Add(1)
	env, err := NewEnvelope(eventType, room, payload, seq)
	if err != nil {
		return nil, err
	}

	if err := h.BroadcastEnvelope(env); err != nil {
		return nil, err
	}

	return env, nil
}

// BroadcastEnvelope pushes an existing Envelope to room subscribers and records it in the resume buffer.
func (h *Hub) BroadcastEnvelope(env *Envelope) error {
	if env == nil {
		return errors.New("envelope cannot be nil")
	}

	data, err := json.Marshal(env)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Append to room resume buffer
	h.appendToBufferLocked(env)

	// Dispatch to subscribers
	subscribers, ok := h.rooms[env.Room]
	if !ok || len(subscribers) == 0 {
		return nil
	}

	for client := range subscribers {
		select {
		case client.Send <- data:
		default:
			// Bounded queue full: drop or disconnect slow client
			// Dropping prevents slow consumer from blocking all other subscribers
		}
	}

	return nil
}

// BroadcastToRooms pushes an Envelope to subscribers across multiple rooms, ensuring each client receives it at most once.
func (h *Hub) BroadcastToRooms(rooms []string, env *Envelope) error {
	if env == nil {
		return errors.New("envelope cannot be nil")
	}

	data, err := json.Marshal(env)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	uniqueClients := make(map[*Client]bool)
	for _, room := range rooms {
		roomEnv := *env
		roomEnv.Room = room
		h.appendToBufferLocked(&roomEnv)

		if subscribers, ok := h.rooms[room]; ok {
			for client := range subscribers {
				uniqueClients[client] = true
			}
		}
	}

	for client := range uniqueClients {
		select {
		case client.Send <- data:
		default:
		}
	}

	return nil
}


// ReplayAfter finds all events in the room buffer after lastEventID and queues them to client.
func (h *Hub) ReplayAfter(c *Client, room string, lastEventID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	buffer, ok := h.roomBuffers[room]
	if !ok || len(buffer) == 0 {
		return 0
	}

	startIndex := -1
	for i, env := range buffer {
		if env.ID == lastEventID {
			startIndex = i + 1
			break
		}
	}

	// If lastEventID was not found (e.g. purged from ring buffer or client provided unknown ID),
	// replay the whole buffer if lastEventID is empty, else replay all retained events.
	if startIndex == -1 {
		if lastEventID == "" {
			startIndex = 0
		} else {
			// ID too old / evicted from 5-minute buffer; replay everything retained
			startIndex = 0
		}
	}

	count := 0
	for i := startIndex; i < len(buffer); i++ {
		env := buffer[i]
		data, err := json.Marshal(env)
		if err != nil {
			continue
		}
		select {
		case c.Send <- data:
			count++
		default:
			// Client queue full
			return count
		}
	}
	return count
}

func (h *Hub) appendToBufferLocked(env *Envelope) {
	buf := h.roomBuffers[env.Room]
	buf = append(buf, env)

	// Keep buffer within MaxBufferPerRoom
	if len(buf) > h.config.MaxBufferPerRoom {
		buf = buf[len(buf)-h.config.MaxBufferPerRoom:]
	}

	// Prune events older than ResumeBufferTTL
	cutoff := time.Now().Add(-h.config.ResumeBufferTTL).UnixMilli()
	validIdx := 0
	for validIdx < len(buf) && buf[validIdx].TS < cutoff {
		validIdx++
	}
	if validIdx > 0 {
		buf = buf[validIdx:]
	}

	h.roomBuffers[env.Room] = buf
}

func (h *Hub) purgeExpiredBuffers() {
	h.mu.Lock()
	defer h.mu.Unlock()

	cutoff := time.Now().Add(-h.config.ResumeBufferTTL).UnixMilli()

	for room, buf := range h.roomBuffers {
		validIdx := 0
		for validIdx < len(buf) && buf[validIdx].TS < cutoff {
			validIdx++
		}
		if validIdx >= len(buf) {
			delete(h.roomBuffers, room)
		} else if validIdx > 0 {
			h.roomBuffers[room] = buf[validIdx:]
		}
	}
}

// ActiveClientCount returns the total number of connected clients.
func (h *Hub) ActiveClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// RoomClientCount returns the number of clients in a specific room.
func (h *Hub) RoomClientCount(room string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms[room])
}
