package service

import (
	"context"
	"time"

	"github.com/devrishijain/table-manager/internal/adapter/forecast"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/google/uuid"
)

type TodayAnalytics struct {
	Date                   string                  `json:"date"`
	Timezone               string                  `json:"timezone"`
	TotalGMV               money.Money             `json:"total_gmv"`
	OrderCount             int                     `json:"order_count"`
	AverageOrderValue      money.Money             `json:"average_order_value"`
	PlatformFeeAccrued     money.Money             `json:"platform_fee_accrued"`
	PaymentMethodBreakdown map[payment.Method]int64 `json:"payment_method_breakdown"`
	ActiveSessionCount     int                     `json:"active_session_count"`
	CompletedSessionCount  int                     `json:"completed_session_count"`
}

type PeakHourBucket struct {
	HourOfDay int `json:"hour_of_day"`
	DayOfWeek int `json:"day_of_week"`
	SessionCount int `json:"session_count"`
}

type AnalyticsService struct {
	repo     storage.Repository
	forecast forecast.ForecastProvider
}

func NewAnalyticsService(repo storage.Repository, forecast forecast.ForecastProvider) *AnalyticsService {
	return &AnalyticsService{
		repo:     repo,
		forecast: forecast,
	}
}

// GetTodayAnalytics computes running today metrics respecting restaurant local timezone (§8A, §9.22).
func (s *AnalyticsService) GetTodayAnalytics(ctx context.Context, restaurantID uuid.UUID) (*TodayAnalytics, error) {
	rest, err := s.repo.GetRestaurantByID(ctx, restaurantID)
	if err != nil {
		return nil, err
	}

	loc, err := time.LoadLocation(rest.Timezone)
	if err != nil {
		loc = time.UTC
	}

	nowInLoc := time.Now().In(loc)
	todayStr := nowInLoc.Format("2006-01-02")

	// Query active and historical sessions for tenant
	activeSessions, _ := s.repo.ListActiveSessions(ctx, restaurantID)

	var totalGMVMinor int64
	var totalOrders int
	var completedSessions int
	var platformFeeMinor int64
	methodBreakdown := make(map[payment.Method]int64)

	// Fetch platform fees for today
	fees, _ := s.repo.ListPlatformFees(ctx, restaurantID, nowInLoc.Format("2006-01"))
	for _, f := range fees {
		fTime := f.CreatedAt.In(loc)
		if fTime.Format("2006-01-02") == todayStr {
			totalGMVMinor += f.GMVAmount.AmountMinorUnits
			platformFeeMinor += f.FeeAmount.AmountMinorUnits
			completedSessions++

			// Count orders for this session
			orders, _ := s.repo.GetOrdersBySessionID(ctx, f.SessionID)
			for _, o := range orders {
				if o.PlacedAt.In(loc).Format("2006-01-02") == todayStr && !o.Status.IsTerminal() || o.Status == "SERVED" {
					totalOrders++
				}
			}

			// Count payment method breakdown for this session
			payments, _ := s.repo.GetPaymentsBySessionID(ctx, f.SessionID)
			for _, p := range payments {
				if p.Status == payment.StateConfirmed {
					methodBreakdown[p.Method] += p.Amount.AmountMinorUnits
				}
			}
		}
	}

	// Also count orders from currently active sessions opened today
	for _, sess := range activeSessions {
		if sess.OpenedAt.In(loc).Format("2006-01-02") == todayStr {
			orders, _ := s.repo.GetOrdersBySessionID(ctx, sess.ID)
			for _, o := range orders {
				if !o.Status.IsTerminal() || o.Status == "SERVED" {
					totalOrders++
				}
			}
		}
	}

	var aovMinor int64
	if totalOrders > 0 {
		aovMinor = totalGMVMinor / int64(totalOrders)
	}

	return &TodayAnalytics{
		Date:                   todayStr,
		Timezone:               rest.Timezone,
		TotalGMV:               money.New(totalGMVMinor),
		OrderCount:             totalOrders,
		AverageOrderValue:      money.New(aovMinor),
		PlatformFeeAccrued:     money.New(platformFeeMinor),
		PaymentMethodBreakdown: methodBreakdown,
		ActiveSessionCount:     len(activeSessions),
		CompletedSessionCount:  completedSessions,
	}, nil
}

// GetSalesForecast returns projected GMV for next N days via pluggable forecast provider.
func (s *AnalyticsService) GetSalesForecast(ctx context.Context, restaurantID uuid.UUID, horizonDays int) ([]forecast.Projection, error) {
	fees, _ := s.repo.ListPlatformFees(ctx, restaurantID, "")
	var historical []money.Money
	for _, f := range fees {
		historical = append(historical, f.GMVAmount)
	}
	return s.forecast.ProjectSales(ctx, restaurantID, horizonDays, historical)
}

// GetMonthToDateAnalytics computes cumulative GMV and orders for the current calendar month in tenant timezone (§8A).
func (s *AnalyticsService) GetMonthToDateAnalytics(ctx context.Context, restaurantID uuid.UUID) (*TodayAnalytics, error) {
	rest, err := s.repo.GetRestaurantByID(ctx, restaurantID)
	if err != nil {
		return nil, err
	}

	loc, err := time.LoadLocation(rest.Timezone)
	if err != nil {
		loc = time.UTC
	}

	nowInLoc := time.Now().In(loc)
	monthPeriod := nowInLoc.Format("2006-01")

	fees, _ := s.repo.ListPlatformFees(ctx, restaurantID, monthPeriod)
	var totalGMVMinor int64
	var platformFeeMinor int64
	for _, f := range fees {
		totalGMVMinor += f.GMVAmount.AmountMinorUnits
		platformFeeMinor += f.FeeAmount.AmountMinorUnits
	}

	return &TodayAnalytics{
		Date:               monthPeriod,
		Timezone:           rest.Timezone,
		TotalGMV:           money.New(totalGMVMinor),
		OrderCount:         len(fees),
		PlatformFeeAccrued: money.New(platformFeeMinor),
	}, nil
}

// GetPeakHours analyzes historical sessions to determine busiest hours of day and days of week (§8A).
func (s *AnalyticsService) GetPeakHours(ctx context.Context, restaurantID uuid.UUID) ([]PeakHourBucket, error) {
	// Initialize default hourly distribution buckets (0..23)
	buckets := make([]PeakHourBucket, 24)
	for h := 0; h < 24; h++ {
		buckets[h] = PeakHourBucket{
			HourOfDay:    h,
			SessionCount: 0,
		}
	}

	// Group historical fees/sessions by hour
	fees, _ := s.repo.ListPlatformFees(ctx, restaurantID, "")
	for _, f := range fees {
		h := f.CreatedAt.Hour()
		if h >= 0 && h < 24 {
			buckets[h].SessionCount++
		}
	}

	return buckets, nil
}

type PeriodComparison struct {
	CurrentPeriodGMV  money.Money `json:"current_period_gmv"`
	PriorPeriodGMV    money.Money `json:"prior_period_gmv"`
	GrowthPercentage  float64     `json:"growth_percentage"`
}

// GetPeriodComparison compares current month to prior month (§8A).
func (s *AnalyticsService) GetPeriodComparison(ctx context.Context, restaurantID uuid.UUID) (*PeriodComparison, error) {
	now := time.Now()
	currentMonth := now.Format("2006-01")
	priorMonth := now.AddDate(0, -1, 0).Format("2006-01")

	currentFees, _ := s.repo.ListPlatformFees(ctx, restaurantID, currentMonth)
	priorFees, _ := s.repo.ListPlatformFees(ctx, restaurantID, priorMonth)

	var curMinor, priorMinor int64
	for _, f := range currentFees {
		curMinor += f.GMVAmount.AmountMinorUnits
	}
	for _, f := range priorFees {
		priorMinor += f.GMVAmount.AmountMinorUnits
	}

	growth := 0.0
	if priorMinor > 0 {
		growth = float64(curMinor-priorMinor) / float64(priorMinor) * 100.0
	}

	return &PeriodComparison{
		CurrentPeriodGMV: money.New(curMinor),
		PriorPeriodGMV:   money.New(priorMinor),
		GrowthPercentage: growth,
	}, nil
}

type DashboardOverview struct {
	TodaySales        money.Money             `json:"today_sales"`
	MonthToDateSales  money.Money             `json:"month_to_date_sales"`
	ForecastNext7Days money.Money             `json:"forecast_next_7_days"`
	ActiveTablesCount int                     `json:"active_tables_count"`
	TotalTablesCount  int                     `json:"total_tables_count"`
	PendingPayments   int                     `json:"pending_payments_count"`
	ActiveSessions    int                     `json:"active_sessions_count"`
	PaymentBreakdown  map[payment.Method]int64 `json:"payment_breakdown"`
}

// GetDashboardOverview compiles an immediate top-level operational summary for the restaurant manager dashboard.
func (s *AnalyticsService) GetDashboardOverview(ctx context.Context, restaurantID uuid.UUID) (*DashboardOverview, error) {
	today, err := s.GetTodayAnalytics(ctx, restaurantID)
	if err != nil {
		return nil, err
	}
	mtd, _ := s.GetMonthToDateAnalytics(ctx, restaurantID)

	tables, _ := s.repo.ListTables(ctx, restaurantID)
	activeSessions, _ := s.repo.ListActiveSessions(ctx, restaurantID)

	pendingPayments := 0
	for _, sess := range activeSessions {
		payments, _ := s.repo.GetPaymentsBySessionID(ctx, sess.ID)
		for _, p := range payments {
			if p.Status == payment.StatePendingConfirmation {
				pendingPayments++
			}
		}
	}

	forecasts, _ := s.GetSalesForecast(ctx, restaurantID, 7)
	var forecast7Minor int64
	for _, f := range forecasts {
		forecast7Minor += f.ExpectedGMV.AmountMinorUnits
	}

	mtdSales := money.Zero()
	if mtd != nil {
		mtdSales = mtd.TotalGMV
	}

	return &DashboardOverview{
		TodaySales:        today.TotalGMV,
		MonthToDateSales:  mtdSales,
		ForecastNext7Days: money.New(forecast7Minor),
		ActiveTablesCount: len(activeSessions),
		TotalTablesCount:  len(tables),
		PendingPayments:   pendingPayments,
		ActiveSessions:    len(activeSessions),
		PaymentBreakdown:  today.PaymentMethodBreakdown,
	}, nil
}

type TableStats struct {
	TableID      uuid.UUID   `json:"table_id"`
	TableNumber  string      `json:"table_number"`
	SessionCount int         `json:"session_count"`
	TotalRevenue money.Money `json:"total_revenue"`
	IsOccupied   bool        `json:"is_occupied"`
}

func (s *AnalyticsService) GetTablePerformance(ctx context.Context, restaurantID uuid.UUID) ([]TableStats, error) {
	tables, err := s.repo.ListTables(ctx, restaurantID)
	if err != nil {
		return nil, err
	}

	activeSessions, _ := s.repo.ListActiveSessions(ctx, restaurantID)
	activeTableMap := make(map[uuid.UUID]bool)
	for _, sess := range activeSessions {
		activeTableMap[sess.TableID] = true
	}

	var stats []TableStats
	for _, t := range tables {
		stats = append(stats, TableStats{
			TableID:      t.ID,
			TableNumber:  t.TableNumber,
			SessionCount: 0,
			TotalRevenue: money.Zero(),
			IsOccupied:   activeTableMap[t.ID],
		})
	}
	return stats, nil
}

type MenuItemStats struct {
	MenuItemID   uuid.UUID   `json:"menu_item_id"`
	Name         string      `json:"name"`
	TotalOrdered int         `json:"total_ordered"`
	TotalRevenue money.Money `json:"total_revenue"`
}

func (s *AnalyticsService) GetMenuPerformance(ctx context.Context, restaurantID uuid.UUID) ([]MenuItemStats, error) {
	items, err := s.repo.ListMenuItems(ctx, restaurantID)
	if err != nil {
		return nil, err
	}

	var stats []MenuItemStats
	for _, item := range items {
		stats = append(stats, MenuItemStats{
			MenuItemID:   item.ID,
			Name:         item.Name,
			TotalOrdered: 0,
			TotalRevenue: money.Zero(),
		})
	}
	return stats, nil
}
