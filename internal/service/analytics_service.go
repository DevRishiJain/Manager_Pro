package service

import (
	"context"
	"sort"
	"time"

	"github.com/devrishijain/table-manager/internal/adapter/forecast"
	"github.com/devrishijain/table-manager/internal/domain/expense"
	"github.com/devrishijain/table-manager/internal/domain/inventory"
	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/order"
	"github.com/devrishijain/table-manager/internal/domain/payment"
	"github.com/devrishijain/table-manager/internal/domain/session"
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
	startOfDay := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), 0, 0, 0, 0, loc).UTC()
	endOfDay := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), 23, 59, 59, 999999999, loc).UTC()

	// Query active and historical sessions for tenant
	activeSessions, _ := s.repo.ListActiveSessions(ctx, restaurantID)

	var totalGMVMinor int64
	var totalOrders int
	var completedSessions int
	var platformFeeMinor int64
	methodBreakdown := make(map[payment.Method]int64)
	sessionSeen := make(map[uuid.UUID]bool)

	// Fetch orders placed today
	todayOrders, _ := s.repo.ListOrders(ctx, restaurantID, 500, &startOfDay, &endOfDay)
	for _, o := range todayOrders {
		if o.Status != order.StateCancelled {
			totalGMVMinor += o.Total.AmountMinorUnits
			totalOrders++
			if o.SessionID != uuid.Nil {
				sessionSeen[o.SessionID] = true
			}
		}
	}

	// For sessions with orders today, fetch their confirmed payments and completion status
	for sessID := range sessionSeen {
		payments, _ := s.repo.GetPaymentsBySessionID(ctx, sessID)
		for _, p := range payments {
			if p.Status == payment.StateConfirmed {
				methodBreakdown[p.Method] += p.Amount.AmountMinorUnits
			}
		}
		sess, err := s.repo.GetSessionByID(ctx, sessID)
		if err == nil && sess != nil {
			if sess.Status == session.StateCompleted || sess.ClosedAt != nil {
				completedSessions++
			}
		}
	}

	// Also count orders from currently active sessions opened today if not yet in todayOrders
	for _, sess := range activeSessions {
		if sess.OpenedAt.In(loc).Format("2006-01-02") == todayStr {
			if !sessionSeen[sess.ID] {
				orders, _ := s.repo.GetOrdersBySessionID(ctx, sess.ID)
				for _, o := range orders {
					if !o.Status.IsTerminal() || o.Status == "SERVED" {
						totalOrders++
						totalGMVMinor += o.Total.AmountMinorUnits
					}
				}
			}
		}
	}

	// Platform fee calculation: check fees ledger, else calculate using commission rate
	fees, _ := s.repo.ListPlatformFees(ctx, restaurantID, nowInLoc.Format("2006-01"))
	for _, f := range fees {
		fTime := f.CreatedAt.In(loc)
		if fTime.Format("2006-01-02") == todayStr {
			platformFeeMinor += f.FeeAmount.AmountMinorUnits
		}
	}
	if platformFeeMinor == 0 && totalGMVMinor > 0 {
		feeRateBps := rest.CommissionRateBps
		if feeRateBps <= 0 {
			feeRateBps = 100 // default 1.00%
		}
		platformFeeMinor = (totalGMVMinor*int64(feeRateBps) + 5000) / 10000
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
	orders, _ := s.repo.ListOrders(ctx, restaurantID, 500, nil, nil)
	var historical []money.Money
	for _, o := range orders {
		if o.Status != order.StateCancelled {
			historical = append(historical, o.Total)
		}
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
	startOfMonth := time.Date(nowInLoc.Year(), nowInLoc.Month(), 1, 0, 0, 0, 0, loc).UTC()
	endOfDay := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), 23, 59, 59, 999999999, loc).UTC()

	var totalGMVMinor int64
	var orderCount int
	var platformFeeMinor int64

	monthOrders, _ := s.repo.ListOrders(ctx, restaurantID, 500, &startOfMonth, &endOfDay)
	for _, o := range monthOrders {
		if o.Status != order.StateCancelled {
			totalGMVMinor += o.Total.AmountMinorUnits
			orderCount++
		}
	}

	fees, _ := s.repo.ListPlatformFees(ctx, restaurantID, monthPeriod)
	for _, f := range fees {
		platformFeeMinor += f.FeeAmount.AmountMinorUnits
	}
	if platformFeeMinor == 0 && totalGMVMinor > 0 {
		feeRateBps := rest.CommissionRateBps
		if feeRateBps <= 0 {
			feeRateBps = 100 // default 1.00%
		}
		platformFeeMinor = (totalGMVMinor*int64(feeRateBps) + 5000) / 10000
	}

	var aovMinor int64
	if orderCount > 0 {
		aovMinor = totalGMVMinor / int64(orderCount)
	}

	return &TodayAnalytics{
		Date:               monthPeriod,
		Timezone:           rest.Timezone,
		TotalGMV:           money.New(totalGMVMinor),
		OrderCount:         orderCount,
		AverageOrderValue:  money.New(aovMinor),
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

	// Group orders by hour
	orders, _ := s.repo.ListOrders(ctx, restaurantID, 500, nil, nil)
	for _, o := range orders {
		h := o.PlacedAt.Hour()
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
	rest, err := s.repo.GetRestaurantByID(ctx, restaurantID)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(rest.Timezone)
	if err != nil {
		loc = time.UTC
	}

	now := time.Now().In(loc)
	startOfCurrentMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).UTC()
	endOfCurrentMonth := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999999999, loc).UTC()

	priorMonthDate := now.AddDate(0, -1, 0)
	startOfPriorMonth := time.Date(priorMonthDate.Year(), priorMonthDate.Month(), 1, 0, 0, 0, 0, loc).UTC()
	endOfPriorMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).Add(-time.Nanosecond).UTC()

	currentOrders, _ := s.repo.ListOrders(ctx, restaurantID, 500, &startOfCurrentMonth, &endOfCurrentMonth)
	priorOrders, _ := s.repo.ListOrders(ctx, restaurantID, 500, &startOfPriorMonth, &endOfPriorMonth)

	var curMinor, priorMinor int64
	for _, o := range currentOrders {
		if o.Status != order.StateCancelled {
			curMinor += o.Total.AmountMinorUnits
		}
	}
	for _, o := range priorOrders {
		if o.Status != order.StateCancelled {
			priorMinor += o.Total.AmountMinorUnits
		}
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

type DateRangeInfo struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

type PnLSummary struct {
	GrossRevenue         money.Money `json:"gross_revenue"`
	COGSVariableExpenses money.Money `json:"cogs_variable_expenses"`
	FixedExpenses        money.Money `json:"fixed_expenses"`
	TotalExpenses        money.Money `json:"total_expenses"`
	NetProfit            money.Money `json:"net_profit"`
	FoodCostPct          float64     `json:"food_cost_pct"`
	NetMarginPct         float64     `json:"net_margin_pct"`
	TargetFoodCostPct    float64     `json:"target_food_cost_pct"`
}

type DailySalesTrend struct {
	Date         string `json:"date"`
	Revenue      int64  `json:"revenue"`
	OrderCount   int    `json:"order_count"`
	VariableCost int64  `json:"variable_cost"`
}

type TopDishPerformance struct {
	MenuItemName      string  `json:"menu_item_name"`
	QuantitySold      int     `json:"quantity_sold"`
	TotalRevenueMinor int64   `json:"total_revenue_minor"`
	CostMinor         int64   `json:"cost_minor"`
	GrossProfitMinor  int64   `json:"gross_profit_minor"`
	MarginPct         float64 `json:"margin_pct"`
}

type ExecutiveAnalytics struct {
	DateRange           DateRangeInfo        `json:"date_range"`
	PnL                 PnLSummary           `json:"pnl"`
	SalesTrend          []DailySalesTrend    `json:"sales_trend"`
	PaymentMethods      map[string]int64     `json:"payment_methods"`
	TopDishes           []TopDishPerformance `json:"top_dishes"`
	LowStockAlertsCount int                  `json:"low_stock_alerts_count"`
	TotalOrders         int                  `json:"total_orders"`
	AverageOrderValue   money.Money          `json:"average_order_value"`
}

func (s *AnalyticsService) GetExecutiveAnalytics(ctx context.Context, restaurantID uuid.UUID, startDate, endDate *time.Time) (*ExecutiveAnalytics, error) {
	rest, err := s.repo.GetRestaurantByID(ctx, restaurantID)
	if err != nil {
		return nil, err
	}

	loc, err := time.LoadLocation(rest.Timezone)
	if err != nil {
		loc = time.UTC
	}

	nowInLoc := time.Now().In(loc)
	var startT, endT time.Time
	if startDate != nil {
		startT = *startDate
	} else {
		// Default to start of current month
		startT = time.Date(nowInLoc.Year(), nowInLoc.Month(), 1, 0, 0, 0, 0, loc).UTC()
	}
	if endDate != nil {
		endT = *endDate
	} else {
		// Default to end of today
		endT = time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), 23, 59, 59, 999999999, loc).UTC()
	}

	// 1. Fetch Orders in range
	orders, _ := s.repo.ListOrders(ctx, restaurantID, 5000, &startT, &endT)
	var totalGrossRevenueMinor int64
	var orderCount int
	dailyMap := make(map[string]*DailySalesTrend)
	dishSoldMap := make(map[string]*TopDishPerformance)
	paymentMethods := make(map[string]int64)
	sessionSeen := make(map[uuid.UUID]bool)

	for _, o := range orders {
		if o.Status == order.StateCancelled {
			continue
		}
		totalGrossRevenueMinor += o.Total.AmountMinorUnits
		orderCount++
		if o.SessionID != uuid.Nil {
			sessionSeen[o.SessionID] = true
		}

		dayKey := o.PlacedAt.In(loc).Format("2006-01-02")
		dTrend, ok := dailyMap[dayKey]
		if !ok {
			dTrend = &DailySalesTrend{Date: dayKey}
			dailyMap[dayKey] = dTrend
		}
		dTrend.Revenue += o.Total.AmountMinorUnits
		dTrend.OrderCount++

		for _, item := range o.Items {
			name := item.ItemNameSnapshot
			if name == "" {
				name = "Special Item"
			}
			dish, dOk := dishSoldMap[name]
			if !dOk {
				dish = &TopDishPerformance{
					MenuItemName: name,
				}
				dishSoldMap[name] = dish
			}
			dish.QuantitySold += item.Quantity
			dish.TotalRevenueMinor += item.LineTotal.AmountMinorUnits
		}
	}

	// 2. Fetch Payments for sessions
	for sessID := range sessionSeen {
		payments, _ := s.repo.GetPaymentsBySessionID(ctx, sessID)
		for _, p := range payments {
			if p.Status == payment.StateConfirmed {
				paymentMethods[string(p.Method)] += p.Amount.AmountMinorUnits
			}
		}
	}

	// 3. Fetch Expenses in range
	expenses, _ := s.repo.ListExpenses(ctx, restaurantID, nil, nil, &startT, &endT)
	var cogsVariableMinor int64
	var fixedExpensesMinor int64

	for _, exp := range expenses {
		if exp.Type == expense.TypeVariable {
			cogsVariableMinor += exp.Amount.AmountMinorUnits
			dayKey := exp.ExpenseDate.Format("2006-01-02")
			dTrend, ok := dailyMap[dayKey]
			if !ok {
				dTrend = &DailySalesTrend{Date: dayKey}
				dailyMap[dayKey] = dTrend
			}
			dTrend.VariableCost += exp.Amount.AmountMinorUnits
		} else {
			fixedExpensesMinor += exp.Amount.AmountMinorUnits
		}
	}

	totalExpensesMinor := cogsVariableMinor + fixedExpensesMinor
	netProfitMinor := totalGrossRevenueMinor - totalExpensesMinor

	var foodCostPct, netMarginPct float64
	if totalGrossRevenueMinor > 0 {
		foodCostPct = float64(cogsVariableMinor) / float64(totalGrossRevenueMinor) * 100.0
		netMarginPct = float64(netProfitMinor) / float64(totalGrossRevenueMinor) * 100.0
	}

	// 4. Enrich dish margins
	dishMargins, _ := s.repo.ListDishMargins(ctx, restaurantID)
	marginLookup := make(map[string]inventory.DishMargin)
	for _, dm := range dishMargins {
		marginLookup[dm.MenuItemName] = dm
	}

	var topDishes []TopDishPerformance
	for _, dish := range dishSoldMap {
		if dm, found := marginLookup[dish.MenuItemName]; found && dm.CostPrice.AmountMinorUnits > 0 {
			dish.CostMinor = int64(float64(dm.CostPrice.AmountMinorUnits) * float64(dish.QuantitySold))
			dish.GrossProfitMinor = dish.TotalRevenueMinor - dish.CostMinor
			if dish.TotalRevenueMinor > 0 {
				dish.MarginPct = float64(dish.GrossProfitMinor) / float64(dish.TotalRevenueMinor) * 100.0
			}
		}
		topDishes = append(topDishes, *dish)
	}

	sort.Slice(topDishes, func(i, j int) bool {
		return topDishes[i].QuantitySold > topDishes[j].QuantitySold
	})
	if len(topDishes) > 10 {
		topDishes = topDishes[:10]
	}

	// 5. Daily trend sorted by date
	var salesTrend []DailySalesTrend
	for _, d := range dailyMap {
		salesTrend = append(salesTrend, *d)
	}
	sort.Slice(salesTrend, func(i, j int) bool {
		return salesTrend[i].Date < salesTrend[j].Date
	})

	// 6. Low stock alerts count
	items, _ := s.repo.ListInventoryItems(ctx, restaurantID)
	var lowStockCount int
	for _, it := range items {
		if it.IsLowStock() {
			lowStockCount++
		}
	}

	var aovMinor int64
	if orderCount > 0 {
		aovMinor = totalGrossRevenueMinor / int64(orderCount)
	}

	return &ExecutiveAnalytics{
		DateRange: DateRangeInfo{
			StartDate: startT.In(loc).Format("2006-01-02"),
			EndDate:   endT.In(loc).Format("2006-01-02"),
		},
		PnL: PnLSummary{
			GrossRevenue:         money.New(totalGrossRevenueMinor),
			COGSVariableExpenses: money.New(cogsVariableMinor),
			FixedExpenses:        money.New(fixedExpensesMinor),
			TotalExpenses:        money.New(totalExpensesMinor),
			NetProfit:            money.New(netProfitMinor),
			FoodCostPct:          foodCostPct,
			NetMarginPct:         netMarginPct,
			TargetFoodCostPct:    30.0,
		},
		SalesTrend:          salesTrend,
		PaymentMethods:      paymentMethods,
		TopDishes:           topDishes,
		LowStockAlertsCount: lowStockCount,
		TotalOrders:         orderCount,
		AverageOrderValue:   money.New(aovMinor),
	}, nil
}
