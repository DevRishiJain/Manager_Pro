package forecast

import (
	"context"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/google/uuid"
)

type Projection struct {
	Date          time.Time   `json:"date"`
	DayOfWeek     string      `json:"day_of_week"`
	MinProjected  money.Money `json:"min_projected"`
	ExpectedGMV   money.Money `json:"expected_gmv"`
	MaxProjected  money.Money `json:"max_projected"`
}

type ForecastProvider interface {
	ProjectSales(ctx context.Context, restaurantID uuid.UUID, horizonDays int, historicalDailyGMV []money.Money) ([]Projection, error)
}

// WeightedMovingAverageForecast implements an explainable, lightweight moving-average forecast.
type WeightedMovingAverageForecast struct{}

func NewWeightedMovingAverageForecast() *WeightedMovingAverageForecast {
	return &WeightedMovingAverageForecast{}
}

func (f *WeightedMovingAverageForecast) ProjectSales(ctx context.Context, restaurantID uuid.UUID, horizonDays int, historicalDailyGMV []money.Money) ([]Projection, error) {
	if horizonDays <= 0 {
		horizonDays = 7
	}

	// Calculate baseline average from historical daily GMV
	var totalMinor int64
	count := int64(len(historicalDailyGMV))
	if count == 0 {
		// Fallback default baseline if no history yet
		totalMinor = 5000000 // 50,000 INR
		count = 1
	} else {
		for _, m := range historicalDailyGMV {
			totalMinor += m.AmountMinorUnits
		}
	}
	avgDailyMinor := totalMinor / count

	projections := make([]Projection, horizonDays)
	now := time.Now()

	for i := 0; i < horizonDays; i++ {
		targetDate := now.AddDate(0, 0, i+1)
		dow := targetDate.Weekday()

		// Weekend multiplier (Friday, Saturday, Sunday have higher volume)
		var factorNumerator, factorDenominator int64 = 100, 100
		if dow == time.Saturday || dow == time.Sunday {
			factorNumerator = 135 // +35% on weekends
		} else if dow == time.Friday {
			factorNumerator = 120 // +20% on Friday
		} else if dow == time.Monday {
			factorNumerator = 85  // -15% on Monday
		}

		expectedMinor := (avgDailyMinor * factorNumerator) / factorDenominator
		minMinor := (expectedMinor * 85) / 100
		maxMinor := (expectedMinor * 115) / 100

		projections[i] = Projection{
			Date:         targetDate,
			DayOfWeek:    dow.String(),
			MinProjected: money.New(minMinor),
			ExpectedGMV:  money.New(expectedMinor),
			MaxProjected: money.New(maxMinor),
		}
	}

	return projections, nil
}
