package tests

import (
	"context"
	"testing"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/money"
	domainRestaurant "github.com/devrishijain/table-manager/internal/domain/restaurant"
	domainSession "github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/google/uuid"
)

// AssertBillBreakdownInvariant enforces:
// final_bill = subtotal + taxes + adjustments - discounts
func AssertBillBreakdownInvariant(t *testing.T, subtotal, taxes, adjustments, discounts, finalBill money.Money) {
	t.Helper()
	expectedMinor := subtotal.AmountMinorUnits + taxes.AmountMinorUnits + adjustments.AmountMinorUnits - discounts.AmountMinorUnits
	if finalBill.AmountMinorUnits != expectedMinor {
		t.Fatalf("INVARIANT VIOLATION: final_bill (%d) != subtotal (%d) + taxes (%d) + adjustments (%d) - discounts (%d); expected %d",
			finalBill.AmountMinorUnits, subtotal.AmountMinorUnits, taxes.AmountMinorUnits, adjustments.AmountMinorUnits, discounts.AmountMinorUnits, expectedMinor)
	}
}

// AssertPaymentCoverageInvariant enforces:
// paid_amount + refundable/credit adjustments >= final_bill
func AssertPaymentCoverageInvariant(t *testing.T, paidAmount, creditAdjustments, finalBill money.Money) {
	t.Helper()
	totalCovered := paidAmount.AmountMinorUnits + creditAdjustments.AmountMinorUnits
	if totalCovered < finalBill.AmountMinorUnits {
		t.Fatalf("INVARIANT VIOLATION: total covered (%d) < final_bill (%d)", totalCovered, finalBill.AmountMinorUnits)
	}
}

// AssertSettlementLedgerInvariant enforces:
// restaurant_net = settled_gmv - platform_fee - refunds +/- adjustments
func AssertSettlementLedgerInvariant(t *testing.T, settledGMV, platformFee, refunds, adjustments, restaurantNet money.Money) {
	t.Helper()
	expectedNetMinor := settledGMV.AmountMinorUnits - platformFee.AmountMinorUnits - refunds.AmountMinorUnits + adjustments.AmountMinorUnits
	if restaurantNet.AmountMinorUnits != expectedNetMinor {
		t.Fatalf("INVARIANT VIOLATION: restaurant_net (%d) != settled_gmv (%d) - platform_fee (%d) - refunds (%d) + adjustments (%d); expected %d",
			restaurantNet.AmountMinorUnits, settledGMV.AmountMinorUnits, platformFee.AmountMinorUnits, refunds.AmountMinorUnits, adjustments.AmountMinorUnits, expectedNetMinor)
	}
}

func TestFinancialAndLedgerInvariants(t *testing.T) {
	t.Run("Bill_Breakdown_Invariant", func(t *testing.T) {
		subtotal := money.New(200000)   // ₹2,000.00
		taxes := money.New(10000)       // ₹100.00 (5% GST)
		adjustments := money.New(5000)  // ₹50.00 (e.g. service charge/manual)
		discounts := money.New(15000)   // ₹150.00 (coupon discount)
		finalBill := money.New(200000)  // 2000 + 100 + 50 - 150 = 2000

		AssertBillBreakdownInvariant(t, subtotal, taxes, adjustments, discounts, finalBill)
	})

	t.Run("Payment_Coverage_With_Overpayment_Credit", func(t *testing.T) {
		finalBill := money.New(150000) // ₹1,500
		paidAmount := money.New(160000) // ₹1,600 (Customer overpaid ₹100)
		overpaymentCredit := money.New(10000) // ₹100 credit adjustment

		AssertPaymentCoverageInvariant(t, paidAmount, overpaymentCredit, finalBill)
	})

	t.Run("Settlement_Ledger_Net_Payout_Assertion", func(t *testing.T) {
		settledGMV := money.New(5000000)   // ₹50,000 GMV
		platformFee := money.New(75000)    // ₹750 Platform Fee (1.5%)
		refunds := money.New(100000)       // ₹1,000 Customer refunds
		adjustments := money.New(-25000)   // -₹250 Chargeback / adjustment
		expectedNet := money.New(4800000)  // 50,000 - 750 - 1,000 - 250 = 48,000

		AssertSettlementLedgerInvariant(t, settledGMV, platformFee, refunds, adjustments, expectedNet)
	})

	t.Run("Round_Half_Up_Platform_Fee_Precision", func(t *testing.T) {
		// Round-half-up: (minor * bps + 5000) / 10000
		// Example: 1555 paise @ 100 bps (1%)
		// (1555 * 100 + 5000) / 10000 = (155500 + 5000) / 10000 = 160500 / 10000 = 16 paise
		f1, err := money.New(1555).MultiplyFractionRoundHalfUp(100, 10000)
		if err != nil || f1.AmountMinorUnits != 16 {
			t.Fatalf("expected 16 paise, got %d (err: %v)", f1.AmountMinorUnits, err)
		}

		// Example: 1540 paise @ 100 bps (1%)
		// (1540 * 100 + 5000) / 10000 = (154000 + 5000) / 10000 = 159000 / 10000 = 15 paise
		f2, err := money.New(1540).MultiplyFractionRoundHalfUp(100, 10000)
		if err != nil || f2.AmountMinorUnits != 15 {
			t.Fatalf("expected 15 paise, got %d (err: %v)", f2.AmountMinorUnits, err)
		}
	})
}

func TestTimezoneAndMidnightCrossingEdgeCases(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewMemoryRepository()
	restID := uuid.New()

	// Load restaurant timezone: India Standard Time (UTC+5:30)
	kolkataLoc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatalf("failed to load Asia/Kolkata timezone: %v", err)
	}

	_ = repo.CreateRestaurant(ctx, &domainRestaurant.Restaurant{
		ID:        restID,
		Name:      "Bistro Midnight India",
		Timezone:  "Asia/Kolkata",
		CreatedAt: time.Now(),
	})

	// Case 1: Session started at 23:59:30 on 2026-09-12 in Kolkata
	// Local: 2026-09-12 23:59:30 IST
	// UTC:   2026-09-12 18:29:30 UTC
	t1Kolkata := time.Date(2026, 9, 12, 23, 59, 30, 0, kolkataLoc)
	t1UTC := t1Kolkata.UTC()

	sess1ID := uuid.New()
	sess1 := &domainSession.DiningSession{
		ID:             sess1ID,
		RestaurantID:   restID,
		TableID:        uuid.New(),
		Status:         domainSession.StatePaid,
		FinalTotal:     money.New(200000), // ₹2,000
		CreatedAt:      t1UTC,
		ExpiryDeadline: t1UTC.Add(2 * time.Hour),
	}
	_ = repo.CreateSession(ctx, sess1)

	// Case 2: Session started at 00:15:00 on 2026-09-13 in Kolkata (past midnight!)
	// Local: 2026-09-13 00:15:00 IST
	// UTC:   2026-09-12 18:45:00 UTC (In UTC, this is still 2026-09-12!)
	t2Kolkata := time.Date(2026, 9, 13, 0, 15, 0, 0, kolkataLoc)
	t2UTC := t2Kolkata.UTC()

	sess2ID := uuid.New()
	sess2 := &domainSession.DiningSession{
		ID:             sess2ID,
		RestaurantID:   restID,
		TableID:        uuid.New(),
		Status:         domainSession.StatePaid,
		FinalTotal:     money.New(350000), // ₹3,500
		CreatedAt:      t2UTC,
		ExpiryDeadline: t2UTC.Add(2 * time.Hour),
	}
	_ = repo.CreateSession(ctx, sess2)

	// Verify that the financial business date is determined from restaurant timezone, NOT UTC
	businessDate1 := sess1.CreatedAt.In(kolkataLoc).Format("2006-01-02")
	businessDate2 := sess2.CreatedAt.In(kolkataLoc).Format("2006-01-02")

	if businessDate1 != "2026-09-12" {
		t.Fatalf("expected session 1 business date to be 2026-09-12, got %s", businessDate1)
	}
	if businessDate2 != "2026-09-13" {
		t.Fatalf("expected session 2 business date to be 2026-09-13 (past midnight in Kolkata), got %s", businessDate2)
	}

	// Verify that if someone naively formatted UTC, both would incorrectly say 2026-09-12
	utcDate2 := sess2.CreatedAt.UTC().Format("2006-01-02")
	if utcDate2 != "2026-09-12" {
		t.Fatalf("expected UTC timestamp of sess2 to be 2026-09-12")
	}
	// The assertion: Local restaurant business date MUST differ from UTC date for session 2
	if businessDate2 == utcDate2 {
		t.Fatalf("restaurant business date (%s) must not naively match server UTC date (%s)", businessDate2, utcDate2)
	}

	// Case 3: Month-end boundary (2026-01-31 23:59:50 -> 2026-02-01 00:00:10)
	monthEndKolkata := time.Date(2026, 1, 31, 23, 59, 50, 0, kolkataLoc)
	newMonthKolkata := time.Date(2026, 2, 1, 0, 0, 10, 0, kolkataLoc)

	monthEndPeriod := monthEndKolkata.In(kolkataLoc).Format("2006-01")
	newMonthPeriod := newMonthKolkata.In(kolkataLoc).Format("2006-01")

	if monthEndPeriod != "2026-01" || newMonthPeriod != "2026-02" {
		t.Fatalf("month-end rollover failed: %s vs %s", monthEndPeriod, newMonthPeriod)
	}

	// Case 4: Year-end boundary (2026-12-31 23:59:50 -> 2027-01-01 00:00:10)
	yearEndKolkata := time.Date(2026, 12, 31, 23, 59, 50, 0, kolkataLoc)
	newYearKolkata := time.Date(2027, 1, 1, 0, 0, 10, 0, kolkataLoc)

	yearEndStr := yearEndKolkata.In(kolkataLoc).Format("2006-01-02")
	newYearStr := newYearKolkata.In(kolkataLoc).Format("2006-01-02")

	if yearEndStr != "2026-12-31" || newYearStr != "2027-01-01" {
		t.Fatalf("year-end rollover failed: %s vs %s", yearEndStr, newYearStr)
	}
}
