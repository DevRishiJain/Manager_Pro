package money

import (
	"errors"
	"fmt"
)

var (
	ErrCurrencyMismatch = errors.New("cannot operate on different currencies")
	ErrNegativeAmount   = errors.New("monetary amount cannot be negative in this context")
	ErrDivisionByZero   = errors.New("division by zero in fractional calculation")
)

// DefaultCurrency is INR (paise minor units) as per the system default.
const DefaultCurrency = "INR"

// Money represents an immutable financial amount in minor units (e.g. paise for INR, cents for USD).
// Floats are strictly prohibited for financial accounting.
type Money struct {
	AmountMinorUnits int64  `json:"amount_minor_units"`
	Currency         string `json:"currency"`
}

// New creates a new Money instance with default currency (INR).
func New(minorUnits int64) Money {
	return Money{
		AmountMinorUnits: minorUnits,
		Currency:         DefaultCurrency,
	}
}

// NewWithCurrency creates a Money instance with a specified currency.
func NewWithCurrency(minorUnits int64, currency string) Money {
	if currency == "" {
		currency = DefaultCurrency
	}
	return Money{
		AmountMinorUnits: minorUnits,
		Currency:         currency,
	}
}

// Zero returns a zero-value Money in the given or default currency.
func Zero(currency ...string) Money {
	c := DefaultCurrency
	if len(currency) > 0 && currency[0] != "" {
		c = currency[0]
	}
	return Money{AmountMinorUnits: 0, Currency: c}
}

// Add returns m + other. Fails on currency mismatch.
func (m Money) Add(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, ErrCurrencyMismatch
	}
	return Money{
		AmountMinorUnits: m.AmountMinorUnits + other.AmountMinorUnits,
		Currency:         m.Currency,
	}, nil
}

// MustAdd returns m + other, panicking on currency mismatch.
func (m Money) MustAdd(other Money) Money {
	res, err := m.Add(other)
	if err != nil {
		panic(err)
	}
	return res
}

// Sub returns m - other. Fails on currency mismatch.
func (m Money) Sub(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, ErrCurrencyMismatch
	}
	return Money{
		AmountMinorUnits: m.AmountMinorUnits - other.AmountMinorUnits,
		Currency:         m.Currency,
	}, nil
}

// MustSub returns m - other, panicking on currency mismatch.
func (m Money) MustSub(other Money) Money {
	res, err := m.Sub(other)
	if err != nil {
		panic(err)
	}
	return res
}

// MultiplyInt multiplies money by an integer factor (e.g. quantity).
func (m Money) MultiplyInt(qty int64) Money {
	return Money{
		AmountMinorUnits: m.AmountMinorUnits * qty,
		Currency:         m.Currency,
	}
}

// MultiplyFractionRoundHalfUp multiplies m by (numerator / denominator) using deterministic
// Round-Half-Up arithmetic:
//
// For positive products: (val * num + denom/2) / denom
//
// This is used for platform commission rate calculation (e.g. 100 bps / 10000)
// and GST breakdown (e.g. 250 bps / 10000).
func (m Money) MultiplyFractionRoundHalfUp(numerator, denominator int64) (Money, error) {
	if denominator == 0 {
		return Money{}, ErrDivisionByZero
	}
	if m.AmountMinorUnits == 0 || numerator == 0 {
		return Money{AmountMinorUnits: 0, Currency: m.Currency}, nil
	}

	prod := m.AmountMinorUnits * numerator
	var rounded int64
	if prod >= 0 {
		rounded = (prod + (denominator / 2)) / denominator
	} else {
		rounded = (prod - (denominator / 2)) / denominator
	}

	return Money{
		AmountMinorUnits: rounded,
		Currency:         m.Currency,
	}, nil
}

// Equal returns true if currency and amount match.
func (m Money) Equal(other Money) bool {
	return m.Currency == other.Currency && m.AmountMinorUnits == other.AmountMinorUnits
}

// GreaterThan returns true if m > other. Panics on currency mismatch.
func (m Money) GreaterThan(other Money) bool {
	if m.Currency != other.Currency {
		panic(ErrCurrencyMismatch)
	}
	return m.AmountMinorUnits > other.AmountMinorUnits
}

// GreaterThanOrEqual returns true if m >= other. Panics on currency mismatch.
func (m Money) GreaterThanOrEqual(other Money) bool {
	if m.Currency != other.Currency {
		panic(ErrCurrencyMismatch)
	}
	return m.AmountMinorUnits >= other.AmountMinorUnits
}

// LessThan returns true if m < other. Panics on currency mismatch.
func (m Money) LessThan(other Money) bool {
	if m.Currency != other.Currency {
		panic(ErrCurrencyMismatch)
	}
	return m.AmountMinorUnits < other.AmountMinorUnits
}

// IsZero returns true if amount is 0.
func (m Money) IsZero() bool {
	return m.AmountMinorUnits == 0
}

// IsPositive returns true if amount > 0.
func (m Money) IsPositive() bool {
	return m.AmountMinorUnits > 0
}

// IsNegative returns true if amount < 0.
func (m Money) IsNegative() bool {
	return m.AmountMinorUnits < 0
}

// String returns formatted currency representation (e.g. "INR 100.50").
func (m Money) String() string {
	sign := ""
	amt := m.AmountMinorUnits
	if amt < 0 {
		sign = "-"
		amt = -amt
	}
	major := amt / 100
	minor := amt % 100
	return fmt.Sprintf("%s%s %d.%02d", sign, m.Currency, major, minor)
}
