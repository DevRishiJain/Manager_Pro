package tests

import (
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/google/uuid"
)

func TestSubscriptionDomain(t *testing.T) {
	now := time.Now().UTC()
	restActive := &restaurant.Restaurant{
		ID:                 uuid.New(),
		Name:               "Active Cafe",
		SubscriptionPlan:   "PRO",
		SubscriptionStatus: "ACTIVE",
		SubscriptionEndAt:  now.Add(10 * 24 * time.Hour),
	}

	if !restActive.IsSubscriptionActive() {
		t.Fatalf("expected active restaurant to return IsSubscriptionActive == true")
	}

	days := restActive.DaysRemaining()
	if days < 9 || days > 10 {
		t.Fatalf("expected ~10 days remaining, got %d", days)
	}

	restExpired := &restaurant.Restaurant{
		ID:                 uuid.New(),
		Name:               "Expired Diner",
		SubscriptionPlan:   "PRO",
		SubscriptionStatus: "EXPIRED",
		SubscriptionEndAt:  now.Add(-2 * 24 * time.Hour),
	}

	if restExpired.IsSubscriptionActive() {
		t.Fatalf("expected expired restaurant to return IsSubscriptionActive == false")
	}

	if restExpired.DaysRemaining() != 0 {
		t.Fatalf("expected 0 days remaining for expired restaurant, got %d", restExpired.DaysRemaining())
	}
}
