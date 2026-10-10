package middleware

import (
	"context"
	"sync"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/google/uuid"
)

type subCacheEntry struct {
	rest      *restaurant.Restaurant
	expiresAt time.Time
}

const subCacheTTL = 30 * time.Second

var (
	subCache   = make(map[uuid.UUID]subCacheEntry)
	subCacheMu sync.RWMutex
)

// cachedGetSubscription returns the subscription state from an in-process TTL cache,
// falling back to the repository on miss or expiry. This eliminates 1-2 DB round-trips
// from every authenticated request passing through SubscriptionGateMiddleware.
func cachedGetSubscription(repo interface {
	GetSubscription(ctx context.Context, restaurantID uuid.UUID) (*restaurant.Restaurant, error)
}, ctx context.Context, restID uuid.UUID) (*restaurant.Restaurant, error) {
	subCacheMu.RLock()
	if entry, ok := subCache[restID]; ok && time.Now().Before(entry.expiresAt) {
		subCacheMu.RUnlock()
		return entry.rest, nil
	}
	subCacheMu.RUnlock()

	rest, err := repo.GetSubscription(ctx, restID)
	if err != nil || rest == nil {
		return rest, err
	}

	subCacheMu.Lock()
	subCache[restID] = subCacheEntry{rest: rest, expiresAt: time.Now().Add(subCacheTTL)}
	subCacheMu.Unlock()
	return rest, nil
}

// InvalidateSubscriptionCache clears a cached entry (call on renewal/suspend/reactivate).
func InvalidateSubscriptionCache(restID uuid.UUID) {
	subCacheMu.Lock()
	delete(subCache, restID)
	subCacheMu.Unlock()
}
