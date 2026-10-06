package ws

import (
	"context"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 35 * time.Second
	pingPeriod     = 25 * time.Second
	sendBufferSize = 128
)

type Client struct {
	ID        string
	Hub       *Hub
	Conn      *websocket.Conn
	Send      chan []byte
	Claims    *TicketClaims
	RemoteIP  string
	Rooms     map[string]bool
	closeOnce syncOnce
}

type syncOnce struct {
	done bool
}

func (s *syncOnce) Do(f func()) {
	if !s.done {
		s.done = true
		f()
	}
}

func NewClient(hub *Hub, conn *websocket.Conn, claims *TicketClaims, remoteIP string) *Client {
	c := &Client{
		ID:       uuid.New().String(),
		Hub:      hub,
		Conn:     conn,
		Send:     make(chan []byte, sendBufferSize),
		Claims:   claims,
		RemoteIP: remoteIP,
		Rooms:    make(map[string]bool),
	}
	for _, room := range claims.Rooms {
		c.Rooms[room] = true
	}
	return c
}

func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.Send)
		_ = c.Conn.Close(websocket.StatusNormalClosure, "closing connection")
	})
}

// WritePump writes messages from the Send channel to the WebSocket connection.
func (c *Client) WritePump(ctx context.Context) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Hub.Unregister(c)
		c.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.Send:
			if !ok {
				// Hub closed the channel
				_ = c.Conn.Close(websocket.StatusGoingAway, "hub closed channel")
				return
			}

			writeCtx, cancel := context.WithTimeout(ctx, writeWait)
			err := c.Conn.Write(writeCtx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ticker.C:
			// Ping heartbeat
			pingCtx, cancel := context.WithTimeout(ctx, writeWait)
			err := c.Conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// ReadPump reads incoming messages from the WebSocket connection to detect disconnects.
func (c *Client) ReadPump(ctx context.Context) {
	defer func() {
		c.Hub.Unregister(c)
		c.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			_, _, err := c.Conn.Read(ctx)
			if err != nil {
				return
			}
		}
	}
}
