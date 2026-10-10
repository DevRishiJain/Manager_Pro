package api

import (
	"net/http"
	"time"

	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/api/middleware"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/devrishijain/table-manager/internal/ws"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

func NewRouter(handler *handlers.APIHandler, repo storage.Repository, jwtSecret []byte, wsServerOpt ...*ws.Server) http.Handler {
	var wsServer *ws.Server
	if len(wsServerOpt) > 0 && wsServerOpt[0] != nil {
		wsServer = wsServerOpt[0]
	} else {
		wsTM := handler.GetWSTicketManager()
		if wsTM == nil {
			wsTM = ws.NewTicketManager(30 * time.Second)
			handler.SetWSTicketManager(wsTM)
		}
		hub := ws.NewHub()
		wsServer = ws.NewServer(hub, wsTM, []string{"*"})
	}

	r := chi.NewRouter()

	// Global Middlewares
	r.Use(middleware.CORSMiddleware)
	r.Use(middleware.RequestMetricsMiddleware(middleware.GlobalMetrics))
	r.Use(middleware.CorrelationMiddleware)
	r.Use(middleware.SecurityHeadersMiddleware)
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Compress(5))
	rateLimiter := middleware.NewRateLimiter(120, 1*time.Minute)
	r.Use(rateLimiter.Middleware)

	r.Use(middleware.ETagMiddleware)
	idempotencyMgr := middleware.NewIdempotencyManager()
	r.Use(idempotencyMgr.Middleware())
	r.Use(middleware.SubscriptionGateMiddleware(repo, jwtSecret))

	// Metrics endpoint
	r.Get("/metrics", middleware.MetricsHandler(middleware.GlobalMetrics))

	// Real-Time WebSocket endpoint (§Phase 2)
	r.Get("/ws/v1", wsServer.ServeHTTP)

	// Health Check & Root Handlers
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"table-manager-dining-os","version":"v2.0.0"}`))
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"service":"Table Manager Dining OS API","docs":"/api/v1","status":"healthy"}`))
	})

	r.Route("/api/v1", func(api chi.Router) {
		// WebSocket Ticket Authentication (§Phase 2.1)
		api.Post("/ws/ticket", handler.GetWSTicket)

		// Public routes
		api.Post("/session/start", handler.StartSession)
		api.Get("/public/restaurant/check-handle", handler.CheckHandleAvailability)
		api.Get("/public/restaurant/{identifier}", handler.LookupRestaurantPublic)
		api.Get("/public/franchise/invite/{code}", handler.GetFranchiseInviteInfo)
		api.Post("/staff/login", handler.StaffLogin)
		api.Post("/auth/staff/login", handler.StaffLogin)
		api.Post("/auth/forgot-password", handler.ForgotPassword)
		api.Post("/auth/reset-password", handler.ResetPassword)
		api.Post("/staff/forgot-password", handler.ForgotPassword)
		api.Post("/staff/reset-password", handler.ResetPassword)
		api.Post("/public/onboard", handler.OnboardRestaurant)
		api.Post("/auth/restaurant/signup", handler.OnboardRestaurant)
		api.Post("/webhooks/razorpay", handler.RazorpayWebhook)
		api.Post("/public/menu/ai-catalog", handler.AICatalogMenu)
		api.Post("/public/menu/ai-query", handler.AIQueryMenu)

		// Customer session routes (Opaque session token auth)
		api.Group(func(cr chi.Router) {
			cr.Use(middleware.CustomerAuth(repo))
			cr.Post("/session/{id}/orders", handler.PlaceOrder)
			cr.Post("/session/{id}/orders/{orderId}/cancel", handler.CancelOrder)
			cr.Get("/session/{id}", handler.GetSessionDetails)
			cr.Post("/session/{id}/pay", handler.CustomerPay)
			cr.Get("/session/{id}/exit-pass", handler.GetExitPass)
			cr.Post("/session/{id}/assistance", handler.RequestAssistance)
			cr.Post("/session/{id}/assistance/dismiss", handler.DismissAssistance)
		})

		// Staff routes (Staff JWT auth)
		api.Group(func(sr chi.Router) {
			sr.Use(middleware.StaffAuth(jwtSecret))
			sr.Post("/staff/sessions/start", handler.StaffStartSession)
			sr.Post("/staff/sessions/{id}/verify-first-order", handler.VerifyFirstOrder)
			sr.Post("/staff/sessions/{id}/orders", handler.PlaceOrder)
			sr.Post("/staff/sessions/{id}/verify-exit", handler.StaffVerifyExit)
			sr.Get("/staff/orders/pending", handler.GetPendingOrders)
			sr.Post("/staff/orders/{id}/accept", handler.AcceptOrder)
			sr.Post("/staff/orders/{id}/cancel", handler.CancelOrder)
			sr.Post("/staff/payments/{id}/confirm", handler.StaffConfirmPayment)
			sr.Post("/staff/payments/confirm", handler.StaffConfirmPayment)
			sr.Post("/staff/payments/{id}/void", handler.StaffVoidPayment)
			sr.Post("/staff/sessions/{id}/force-close", handler.ForceCloseSession)
			sr.Post("/staff/sessions/{id}/assistance/dismiss", handler.DismissAssistance)
			sr.Get("/staff/dashboard/tables", handler.GetTableDashboard)
			sr.Get("/restaurant/subscription", handler.GetSubscription)
			sr.Post("/restaurant/subscription/renew", handler.RenewSubscription)
			sr.Post("/staff/sessions/{id}/assign-waiter", handler.AssignWaiter)
			sr.Post("/staff/billing/quick", handler.StaffQuickBilling)
		})

		// Franchise Governance routes
		api.Group(func(fr chi.Router) {
			fr.Use(middleware.StaffAuth(jwtSecret))
			fr.Use(middleware.RequireRole("FRANCHISE_OWNER", "SUPER_ADMIN"))
			fr.Get("/franchise/outlets", handler.GetFranchiseOutlets)
			fr.Get("/franchise/summary", handler.GetFranchiseSummary)
			fr.Post("/franchise/invite-code", handler.GenerateFranchiseInviteCode)
			fr.Post("/franchise/outlets/create", handler.CreateFranchiseOutlet)
		})

		// Store Owner Link Franchise route
		api.Group(func(lr chi.Router) {
			lr.Use(middleware.StaffAuth(jwtSecret))
			lr.Use(middleware.RequireRole("RESTAURANT_OWNER", "RESTAURANT_ADMIN", "FRANCHISE_OWNER", "SUPER_ADMIN"))
			lr.Post("/restaurant/link-franchise", handler.LinkRestaurantToFranchise)
		})

		// Kitchen (KDS) routes (§Phase 5.2 Hardening)
		api.Group(func(kr chi.Router) {
			kr.Use(middleware.StaffAuth(jwtSecret))
			kr.Use(middleware.RequireRole("KITCHEN", "WAITER", "MANAGER", "RESTAURANT_ADMIN", "RESTAURANT_OWNER", "FRANCHISE_OWNER", "SUPER_ADMIN"))
			kr.Get("/kitchen/orders/queue", handler.GetKitchenQueue)
			kr.Post("/kitchen/orders/{id}/status", handler.UpdateKitchenStatus)
		})

		// Menu Read routes (Accessible by all staff roles & public with restaurant_id)
		api.Group(func(mr chi.Router) {
			mr.Use(middleware.StaffAuthOptional(jwtSecret))
			mr.Get("/restaurant/menu/categories", handler.ListCategories)
			mr.Get("/restaurant/menu/items", handler.ListMenuItems)
		})

		// Guard routes (Guard-scoped JWT auth)
		api.Group(func(gr chi.Router) {
			gr.Use(middleware.GuardAuth(jwtSecret))
			gr.Post("/guard/verify-exit", handler.GuardVerifyExit)
		})

		// Restaurant Admin / Operations / Analytics / Ledger routes
		api.Group(func(tr chi.Router) {
			tr.Use(middleware.StaffAuth(jwtSecret))
			tr.Use(middleware.RequireRole("RESTAURANT_ADMIN", "RESTAURANT_OWNER", "FRANCHISE_OWNER", "SUPER_ADMIN", "MANAGER"))

			// Dashboard Overview & Performance (§8A)
			tr.Get("/restaurant/dashboard/overview", handler.GetDashboardOverview)
			tr.Get("/restaurant/analytics/dashboard", handler.GetExecutiveDashboardAnalytics)
			tr.Get("/restaurant/analytics/today", handler.GetTodayAnalytics)
			tr.Get("/restaurant/analytics/month-to-date", handler.GetMonthToDateAnalytics)
			tr.Get("/restaurant/analytics/compare", handler.GetPeriodComparison)
			tr.Get("/restaurant/analytics/peak-hours", handler.GetPeakHours)
			tr.Get("/restaurant/analytics/forecast", handler.GetSalesForecast)
			tr.Get("/restaurant/analytics/table-performance", handler.GetTablePerformance)
			tr.Get("/restaurant/analytics/menu-performance", handler.GetMenuPerformance)

			// Expenses Management
			tr.Get("/restaurant/expenses", handler.ListExpenses)
			tr.Post("/restaurant/expenses", handler.CreateExpense)
			tr.Get("/restaurant/expenses/{id}/items", handler.GetExpenseLineItems)
			tr.Delete("/restaurant/expenses/{id}", handler.DeleteExpense)

			// Inventory & Stock Management
			tr.Get("/restaurant/inventory", handler.ListInventory)
			tr.Post("/restaurant/inventory", handler.CreateInventoryItem)
			tr.Put("/restaurant/inventory/{id}", handler.UpdateInventoryItem)
			tr.Delete("/restaurant/inventory/{id}", handler.DeleteInventoryItem)
			tr.Post("/restaurant/inventory/{id}/stock", handler.LogStockMovement)
			tr.Get("/restaurant/inventory/logs", handler.ListInventoryLogs)

			// Recipe Costing & Dish Margins
			tr.Get("/restaurant/recipes/{menu_item_id}", handler.GetRecipe)
			tr.Post("/restaurant/recipes/{menu_item_id}", handler.SaveRecipe)
			tr.Get("/restaurant/recipes/margins", handler.ListDishMargins)

			// Ledger & Settlements (§6, §6A)
			tr.Get("/restaurant/ledger", handler.GetLedgerPayable)
			tr.Get("/restaurant/settlements", handler.GetSettlements)
			tr.Get("/restaurant/orders", handler.GetRestaurantOrders)

			// Menu Management (Mutations require ADMIN/OWNER/MANAGER)
			tr.Post("/restaurant/menu/categories", handler.CreateCategory)
			tr.Post("/restaurant/menu/items", handler.CreateMenuItem)
			tr.Put("/restaurant/menu/items/{id}/variants", handler.UpdateMenuItemVariants)
			tr.Post("/restaurant/menu/ai-catalog", handler.AICatalogMenu)
			tr.Post("/restaurant/menu/ai-query", handler.AIQueryMenu)

			// Tables Management
			tr.Get("/restaurant/tables", handler.ListTables)
			tr.Post("/restaurant/tables", handler.CreateTable)
			tr.Post("/restaurant/tables/generate-token", handler.GenerateTableToken)
			tr.Put("/restaurant/tables/{id}", handler.UpdateTable)

			// Staff Management
			tr.Get("/restaurant/staff", handler.ListStaff)
			tr.Post("/restaurant/staff", handler.CreateStaff)
			tr.Put("/restaurant/staff/{id}/password", handler.UpdateStaffPassword)

			// Settings & Onboarding
			tr.Get("/restaurant/settings", handler.GetSettings)
			tr.Put("/restaurant/settings", handler.UpdateSettings)
			tr.Get("/restaurant/onboarding", handler.GetOnboarding)
			tr.Get("/restaurant/onboarding/progress", handler.GetOnboardingProgress)
			tr.Post("/restaurant/onboarding/clone-menu", handler.CloneMenu)
			tr.Post("/restaurant/onboarding/go-live", handler.GoLive)

			// File Upload & Private Evidence
			tr.Post("/restaurant/upload-proof", handler.UploadPaymentProof)
			tr.Get("/restaurant/payments/{id}/evidence-url", handler.GetPaymentEvidenceURL)
		})

		// Platform Super Admin routes (Strict Platform JWT auth) (§8B)
		api.Group(func(pr chi.Router) {
			pr.Use(middleware.PlatformAdminAuth(jwtSecret))
			pr.Get("/admin/overview", handler.GetAdminOverview)
			pr.Get("/admin/franchises", handler.GetAdminFranchises)
			pr.Get("/admin/activity/feed", handler.GetActivityFeed)
			pr.Get("/admin/restaurants", handler.ListAllRestaurants)
			pr.Get("/admin/restaurants/{id}/activity", handler.GetRestaurantActivity)
			pr.Post("/admin/restaurants/{id}/subscription-otp", handler.AdminGenerateSubscriptionOTP)
			pr.Get("/admin/restaurants/{id}/subscription-otps", handler.AdminListSubscriptionOTPs)
			pr.Get("/admin/restaurants/{id}", handler.GetRestaurantDetails)
			pr.Get("/admin/restaurants/{id}/onboarding", handler.GetRestaurantOnboardingAdmin)
			pr.Get("/admin/analytics/platform", handler.GetPlatformAnalytics)
			pr.Get("/admin/fraud-review", handler.GetFraudReviewQueue)
			pr.Post("/admin/restaurants/{id}/commission-rate", handler.OverrideCommissionRate)
			pr.Post("/admin/restaurants/{id}/suspend", handler.SuspendRestaurant)
			pr.Post("/admin/restaurants/{id}/reactivate", handler.ReactivateRestaurant)
			pr.Post("/admin/restaurants/{id}/extend-subscription", handler.AdminExtendSubscription)
			pr.Get("/admin/restaurants/{id}/tables/qr-export", handler.ExportTableQRs)
		})
	})

	return r
}
