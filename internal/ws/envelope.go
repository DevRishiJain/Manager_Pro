package ws

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Envelope is the canonical realtime message format across TableOS.
type Envelope struct {
	V    int             `json:"v"`    // Protocol version: 1
	ID   string          `json:"id"`   // Unique Event UUID
	Seq  int64           `json:"seq"`  // Monotonic sequence number
	T    string          `json:"t"`    // Event Type (e.g., "ORDER_CREATED", "BILL_REQUESTED")
	Room string          `json:"room"` // Target room
	TS   int64           `json:"ts"`   // Unix timestamp in milliseconds
	D    json.RawMessage `json:"d"`    // Payload
}

// NewEnvelope creates a new version 1 event envelope.
func NewEnvelope(eventType, room string, payload interface{}, seq int64) (*Envelope, error) {
	dataBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return &Envelope{
		V:    1,
		ID:   uuid.New().String(),
		Seq:  seq,
		T:    eventType,
		Room: room,
		TS:   time.Now().UnixMilli(),
		D:    dataBytes,
	}, nil
}
