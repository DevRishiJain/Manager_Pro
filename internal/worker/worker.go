package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/audit"
	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/google/uuid"
)

type OutboxDispatcher func(ctx context.Context, event storage.OutboxEvent) error

type Worker struct {
	repo          storage.Repository
	logger        *slog.Logger
	stopCh        chan struct{}
	dispatcher    OutboxDispatcher
	leaseDuration time.Duration
	maxRetries    int
	baseBackoff   time.Duration
}

func NewWorker(repo storage.Repository, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		repo:          repo,
		logger:        logger,
		stopCh:        make(chan struct{}),
		leaseDuration: 30 * time.Second,
		maxRetries:    5,
		baseBackoff:   1 * time.Second,
	}
}

func (w *Worker) SetDispatcher(d OutboxDispatcher) {
	w.dispatcher = d
}

func (w *Worker) SetLeaseDuration(d time.Duration) {
	w.leaseDuration = d
}

func (w *Worker) SetMaxRetries(r int) {
	w.maxRetries = r
}

func (w *Worker) SetBaseBackoff(b time.Duration) {
	w.baseBackoff = b
}

func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	w.logger.Info("Background worker started: running outbox and expiry sweeps")

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Background worker stopping")
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.processOutbox(ctx)
			w.sweepExpiredSessions(ctx)
		}
	}
}

func (w *Worker) Stop() {
	close(w.stopCh)
}

// ProcessOutbox fetches pending events from transactional outbox and dispatches them.
func (w *Worker) ProcessOutboxOnce(ctx context.Context) int {
	return w.processOutbox(ctx)
}

func (w *Worker) processOutbox(ctx context.Context) int {
	// 1. Claim pending events atomically with lease duration
	events, err := w.repo.ClaimPendingOutbox(ctx, 50, w.leaseDuration)
	if err != nil {
		w.logger.Error("Failed to claim pending outbox events", "error", err)
		return 0
	}

	count := 0
	for _, e := range events {
		w.logger.Info("Dispatching claimed outbox event", "type", e.EventType, "aggregate_id", e.AggregateID, "retry", e.Retries)

		var dispatchErr error
		if w.dispatcher != nil {
			dispatchErr = w.dispatcher(ctx, e)
		} else {
			// Default dispatch: simulated external bus / log
			w.logger.Info("Outbox event published to event bus", "event_id", e.ID, "type", e.EventType)
		}

		if dispatchErr == nil {
			// Acknowledged successfully
			_ = w.repo.MarkOutboxPublished(ctx, e.ID)
			count++
		} else {
			// Calculate exponential backoff (e.g. 1s, 2s, 4s, 8s, 16s...)
			shift := e.Retries
			if shift > 6 {
				shift = 6
			}
			backoff := w.baseBackoff * time.Duration(1<<uint(shift))

			w.logger.Warn("Failed to dispatch outbox event, scheduling retry or dead-letter",
				"event_id", e.ID, "error", dispatchErr, "retries", e.Retries+1, "backoff", backoff)

			_ = w.repo.MarkOutboxFailed(ctx, e.ID, dispatchErr.Error(), backoff, w.maxRetries)
		}
	}
	return count
}

// SweepExpiredSessions transitions inactive sessions to EXPIRED with 0 GMV and 0 fee.
func (w *Worker) SweepExpiredSessionsOnce(ctx context.Context) int {
	return w.sweepExpiredSessions(ctx)
}

func (w *Worker) sweepExpiredSessions(ctx context.Context) int {
	restaurants, err := w.repo.ListRestaurants(ctx)
	if err != nil {
		return 0
	}

	now := time.Now()
	expiredCount := 0

	for _, r := range restaurants {
		activeSessions, _ := w.repo.ListActiveSessions(ctx, r.ID)
		for _, s := range activeSessions {
			if now.After(s.ExpiryDeadline) {
				beforeBytes, _ := json.Marshal(s)
				s.Status = session.StateExpired
				closeReason := session.CloseReasonExpired
				actorType := session.ActorSystem
				s.CloseReason = &closeReason
				s.ClosedByActorType = &actorType
				s.ClosedAt = &now

				if err := w.repo.UpdateSession(ctx, &s); err == nil {
					afterBytes, _ := json.Marshal(s)
					_ = w.repo.AppendAuditLog(ctx, &audit.AuditLog{
						ID:           uuid.New(),
						ActorType:    audit.ActorTypeSystem,
						ActorID:      "SYSTEM_EXPIRY_WORKER",
						RestaurantID: s.RestaurantID,
						SessionID:    &s.ID,
						Action:       "SESSION_EXPIRED_INACTIVITY",
						BeforeState:  beforeBytes,
						AfterState:   afterBytes,
						CreatedAt:    now,
					})
					expiredCount++
				}
			}
		}
	}
	return expiredCount
}
