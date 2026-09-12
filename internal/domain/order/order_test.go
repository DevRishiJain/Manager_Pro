package order

import (
	"testing"

	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/google/uuid"
)

func TestOrderTransitions(t *testing.T) {
	t.Run("Valid Transitions", func(t *testing.T) {
		validMoves := [][2]State{
			{StatePlacedUnverified, StatePlacedVerified},
			{StatePlacedUnverified, StateAccepted},
			{StatePlacedUnverified, StateCancelled},
			{StatePlacedVerified, StateAccepted},
			{StatePlacedVerified, StateRejected},
			{StatePlacedVerified, StateCancelled},
			{StateAccepted, StatePreparing},
			{StateAccepted, StateCancelled},
			{StatePreparing, StateReady},
			{StatePreparing, StateCancelled},
			{StateReady, StateServed},
		}
		for _, m := range validMoves {
			if err := ValidateTransition(m[0], m[1]); err != nil {
				t.Errorf("expected transition %s -> %s to be valid: %v", m[0], m[1], err)
			}
		}
	})

	t.Run("Prohibited Transitions", func(t *testing.T) {
		invalidMoves := [][2]State{
			{StateServed, StateCancelled},
			{StateRejected, StateAccepted},
			{StateCancelled, StatePreparing},
			{StateReady, StatePreparing},
			{StatePlacedUnverified, StateReady},
		}
		for _, m := range invalidMoves {
			if err := ValidateTransition(m[0], m[1]); err == nil {
				t.Errorf("expected transition %s -> %s to be rejected", m[0], m[1])
			}
		}
	})

	t.Run("Terminal States", func(t *testing.T) {
		if !StateServed.IsTerminal() {
			t.Error("expected SERVED to be terminal")
		}
		if !StateRejected.IsTerminal() {
			t.Error("expected REJECTED to be terminal")
		}
		if !StateCancelled.IsTerminal() {
			t.Error("expected CANCELLED to be terminal")
		}
		if StatePreparing.IsTerminal() {
			t.Error("expected PREPARING to be non-terminal")
		}
	})
}

func TestOrderItemPriceSnapshot(t *testing.T) {
	item := OrderItem{
		ID:                  uuid.New(),
		OrderID:             uuid.New(),
		MenuItemID:          uuid.New(),
		ItemNameSnapshot:    "Paneer Butter Masala",
		Quantity:            2,
		UnitPriceSnapshot:   money.New(25000), // 250.00 INR
		LineTotal:           money.New(50000), // 500.00 INR
		HSNSACCodeSnapshot:  "996331",
		CGSTRateBpsSnapshot: 250,
		SGSTRateBpsSnapshot: 250,
		CGSTAmount:          money.New(1250), // 12.50 INR
		SGSTAmount:          money.New(1250), // 12.50 INR
	}

	if item.HSNSACCodeSnapshot != "996331" {
		t.Errorf("expected HSN/SAC code 996331, got %s", item.HSNSACCodeSnapshot)
	}
	if item.LineTotal.AmountMinorUnits != 50000 {
		t.Errorf("expected line total 50000, got %d", item.LineTotal.AmountMinorUnits)
	}
}
