package money

import (
	"testing"
)

func TestMoneyArithmetic(t *testing.T) {
	m1 := New(10050) // 100.50 INR
	m2 := New(4950)  // 49.50 INR

	sum, err := m1.Add(m2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sum.AmountMinorUnits != 15000 {
		t.Errorf("expected 15000, got %d", sum.AmountMinorUnits)
	}

	diff, err := m1.Sub(m2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff.AmountMinorUnits != 5100 {
		t.Errorf("expected 5100, got %d", diff.AmountMinorUnits)
	}

	mult := m2.MultiplyInt(3)
	if mult.AmountMinorUnits != 14850 {
		t.Errorf("expected 14850, got %d", mult.AmountMinorUnits)
	}
}

func TestRoundHalfUp(t *testing.T) {
	// Test 1% commission calculation on 1555 paise (15.55 INR)
	// 1555 * 100 / 10000 = 15.55 -> rounds to 16 paise
	m := New(1555)
	fee, err := m.MultiplyFractionRoundHalfUp(100, 10000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fee.AmountMinorUnits != 16 {
		t.Errorf("expected 16 paise, got %d", fee.AmountMinorUnits)
	}

	// 1540 * 100 / 10000 = 15.40 -> rounds to 15 paise
	m2 := New(1540)
	fee2, err := m2.MultiplyFractionRoundHalfUp(100, 10000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fee2.AmountMinorUnits != 15 {
		t.Errorf("expected 15 paise, got %d", fee2.AmountMinorUnits)
	}

	// Exact half-way: 1550 * 100 / 10000 = 15.5 -> rounds to 16 paise
	m3 := New(1550)
	fee3, err := m3.MultiplyFractionRoundHalfUp(100, 10000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fee3.AmountMinorUnits != 16 {
		t.Errorf("expected 16 paise (half-up), got %d", fee3.AmountMinorUnits)
	}
}

func TestCurrencyMismatch(t *testing.T) {
	inr := NewWithCurrency(1000, "INR")
	usd := NewWithCurrency(1000, "USD")

	_, err := inr.Add(usd)
	if err != ErrCurrencyMismatch {
		t.Errorf("expected ErrCurrencyMismatch, got %v", err)
	}
}
