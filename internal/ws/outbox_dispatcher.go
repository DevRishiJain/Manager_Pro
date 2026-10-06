package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/google/uuid"
)

type OutboxDispatcher struct {
	hub           *Hub
	repo          storage.Repository
	logger        *slog.Logger
	notifyCh      chan struct{}
	stopCh        chan struct{}
	wg            sync.WaitGroup
	pollInterval  time.Duration
	leaseDuration time.Duration
	batchSize     int
}

func NewOutboxDispatcher(hub *Hub, repo storage.Repository, logger *slog.Logger) *OutboxDispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &OutboxDispatcher{
		hub:           hub,
		repo:          repo,
		logger:        logger,
		notifyCh:      make(chan struct{}, 100),
		stopCh:        make(chan struct{}),
		pollInterval:  100 * time.Millisecond,
		leaseDuration: 30 * time.Second,
		batchSize:     50,
	}
}

// Trigger signals the dispatcher to immediately sweep pending events without waiting for poll interval.
func (d *OutboxDispatcher) Trigger() {
	select {
	case d.notifyCh <- struct{}{}:
	default:
	}
}

// Start runs the outbox dispatch worker loop until context is canceled or Stop() is called.
func (d *OutboxDispatcher) Start(ctx context.Context) {
	d.wg.Add(1)
	defer d.wg.Done()

	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case <-d.notifyCh:
			d.DispatchPending(ctx)
		case <-ticker.C:
			d.DispatchPending(ctx)
		}
	}
}

// Stop stops the background dispatcher loop.
func (d *OutboxDispatcher) Stop() {
	select {
	case <-d.stopCh:
	default:
		close(d.stopCh)
	}
	d.wg.Wait()
}

// DispatchPending claims pending outbox events from storage and broadcasts them via the Hub.
func (d *OutboxDispatcher) DispatchPending(ctx context.Context) int {
	events, err := d.repo.ClaimPendingOutbox(ctx, d.batchSize, d.leaseDuration)
	if err != nil {
		d.logger.Error("OutboxDispatcher: failed to claim pending outbox events", "error", err)
		return 0
	}

	dispatched := 0
	for _, e := range events {
		room := e.Room
		if room == "" {
			if e.RestaurantID != uuid.Nil {
				room = fmt.Sprintf("restaurant:%s:floor", e.RestaurantID.String())
			} else {
				room = "global"
			}
		}

		seq := d.hub.seqCounter.Add(1)
		env := &Envelope{
			V:    1,
			ID:   e.ID.String(),
			Seq:  seq,
			T:    e.EventType,
			Room: room,
			TS:   time.Now().UnixMilli(),
			D:    e.Payload,
		}

		if err := d.hub.BroadcastEnvelope(env); err != nil {
			d.logger.Warn("OutboxDispatcher: failed to broadcast envelope", "event_id", e.ID, "error", err)
			_ = d.repo.MarkOutboxFailed(ctx, e.ID, err.Error(), 1*time.Second, 5)
			continue
		}

		if err := d.repo.MarkOutboxPublished(ctx, e.ID); err != nil {
			d.logger.Error("OutboxDispatcher: failed to mark outbox published", "event_id", e.ID, "error", err)
		} else {
			dispatched++
		}
	}

	return dispatched
}

// PublishEvent creates an OutboxEvent, records it in the repository, and optionally triggers immediate dispatch.
func PublishEvent(
	ctx context.Context,
	repo storage.Repository,
	dispatcher *OutboxDispatcher,
	restaurantID uuid.UUID,
	eventType string,
	room string,
	aggregateID string,
	payload interface{},
) (*storage.OutboxEvent, error) {
	dataBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal outbox payload: %w", err)
	}

	event := &storage.OutboxEvent{
		ID:           uuid.New(),
		RestaurantID: restaurantID,
		EventType:    eventType,
		Room:         room,
		AggregateID:  aggregateID,
		Payload:      dataBytes,
		Status:       storage.OutboxStatusPending,
		CreatedAt:    time.Now(),
	}

	if err := repo.StoreOutboxEvent(ctx, event); err != nil {
		return nil, fmt.Errorf("failed to store outbox event: %w", err)
	}

	if dispatcher != nil && dispatcher.hub != nil {
		seq := dispatcher.hub.seqCounter.Add(1)
		env := &Envelope{
			V:    1,
			ID:   event.ID.String(),
			Seq:  seq,
			T:    event.EventType,
			Room: room,
			TS:   time.Now().UnixMilli(),
			D:    dataBytes,
		}
		if err := dispatcher.hub.BroadcastEnvelope(env); err == nil {
			event.Status = storage.OutboxStatusPublished
			now := time.Now()
			event.PublishedAt = &now
			_ = repo.MarkOutboxPublished(ctx, event.ID)
		}
	} else if dispatcher != nil {
		dispatcher.Trigger()
	}

	return event, nil
}

// PublishEventToRooms stores and broadcasts an event to multiple target rooms.
func PublishEventToRooms(
	ctx context.Context,
	repo storage.Repository,
	dispatcher *OutboxDispatcher,
	restaurantID uuid.UUID,
	eventType string,
	rooms []string,
	aggregateID string,
	payload interface{},
) ([]*storage.OutboxEvent, error) {
	dataBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal outbox payload: %w", err)
	}

	var events []*storage.OutboxEvent
	for _, room := range rooms {
		event := &storage.OutboxEvent{
			ID:           uuid.New(),
			RestaurantID: restaurantID,
			EventType:    eventType,
			Room:         room,
			AggregateID:  aggregateID,
			Payload:      dataBytes,
			Status:       storage.OutboxStatusPending,
			CreatedAt:    time.Now(),
		}
		if err := repo.StoreOutboxEvent(ctx, event); err != nil {
			return events, err
		}
		events = append(events, event)
	}

	if len(events) == 0 {
		return events, nil
	}

	if dispatcher != nil && dispatcher.hub != nil {
		seq := dispatcher.hub.seqCounter.Add(1)
		env := &Envelope{
			V:    1,
			ID:   events[0].ID.String(),
			Seq:  seq,
			T:    eventType,
			Room: rooms[0],
			TS:   time.Now().UnixMilli(),
			D:    dataBytes,
		}
		if err := dispatcher.hub.BroadcastToRooms(rooms, env); err == nil {
			now := time.Now()
			for _, evt := range events {
				evt.Status = storage.OutboxStatusPublished
				evt.PublishedAt = &now
				_ = repo.MarkOutboxPublished(ctx, evt.ID)
			}
		}
	} else if dispatcher != nil {
		dispatcher.Trigger()
	}

	return events, nil
}

