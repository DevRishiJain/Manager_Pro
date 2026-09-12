package api

import (
	"net/http"

	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/api/middleware"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

func NewRouter(handler *handlers.APIHandler, repo storage.Repository, jwtSecret []byte) http.Handler {
	r := chi.NewRouter()

	// Global Middlewares
	r.Use(middleware.CorrelationMiddleware)
	r.Use(middleware.SecurityHeadersMiddleware)
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)

	idempotencyMgr := middleware.NewIdempotencyManager()
	r.Use(idempotencyMgr.Middleware())

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
		// Public routes
		api.Post("/session/start", handler.StartSession)
		api.Post("/webhooks/razorpay", handler.RazorpayWebhook)
		api.Post("/public/menu/ai-catalog", handler.AICatalogMenu)
		api.Post("/public/menu/ai-query", handler.AIQueryMenu)

		// Customer session routes (Opaque session token auth)
		api.Group(func(cr chi.Router) {
			cr.Use(middleware.CustomerAuth(repo))
			cr.Post("/session/{id}/orders", handler.PlaceOrder)
			cr.Get("/session/{id}", handler.GetSessionDetails)
			cr.Post("/session/{id}/pay", handler.CustomerPay)
			cr.Get("/session/{id}/exit-pass", handler.GetExitPass)
		})

		// Staff routes (Staff JWT auth)
		api.Group(func(sr chi.Router) {
			sr.Use(middleware.StaffAuth(jwtSecret))
			sr.Post("/staff/sessions/{id}/verify-first-order", handler.VerifyFirstOrder)
			sr.Post("/staff/orders/{id}/accept", handler.AcceptOrder)
			sr.Post("/staff/payments/{id}/confirm", handler.StaffConfirmPayment)
			sr.Post("/staff/payments/confirm", handler.StaffConfirmPayment)
			sr.Post("/staff/sessions/{id}/force-close", handler.ForceCloseSession)
			sr.Get("/staff/dashboard/tables", handler.GetTableDashboard)
		})

		// Kitchen (KDS) routes
		api.Group(func(kr chi.Router) {
			kr.Use(middleware.StaffAuth(jwtSecret))
			kr.Use(middleware.RequireRole("KITCHEN", "MANAGER", "RESTAURANT_ADMIN", "RESTAURANT_OWNER"))
			kr.Get("/kitchen/orders/queue", handler.GetKitchenQueue)
			kr.Post("/kitchen/orders/{id}/status", handler.UpdateKitchenStatus)
		})

		// Guard routes (Guard-scoped JWT auth)
		api.Group(func(gr chi.Router) {
			gr.Use(middleware.GuardAuth(jwtSecret))
			gr.Post("/guard/verify-exit", handler.GuardVerifyExit)
		})

		// Restaurant Admin / Operations / Analytics / Ledger routes
		api.Group(func(tr chi.Router) {
			tr.Use(middleware.StaffAuth(jwtSecret))
			tr.Use(middleware.RequireRole("RESTAURANT_ADMIN", "RESTAURANT_OWNER", "MANAGER"))
			
			// Dashboard Overview & Performance (§8A)
			tr.Get("/restaurant/dashboard/overview", handler.GetDashboardOverview)
			tr.Get("/restaurant/analytics/today", handler.GetTodayAnalytics)
			tr.Get("/restaurant/analytics/month-to-date", handler.GetMonthToDateAnalytics)
			tr.Get("/restaurant/analytics/compare", handler.GetPeriodComparison)
			tr.Get("/restaurant/analytics/peak-hours", handler.GetPeakHours)
			tr.Get("/restaurant/analytics/forecast", handler.GetSalesForecast)
			tr.Get("/restaurant/analytics/table-performance", handler.GetTablePerformance)
			tr.Get("/restaurant/analytics/menu-performance", handler.GetMenuPerformance)
			
			// Ledger & Settlements (§6, §6A)
			tr.Get("/restaurant/ledger", handler.GetLedgerPayable)
			tr.Get("/restaurant/settlements", handler.GetSettlements)

			// Menu Management
			tr.Get("/restaurant/menu/categories", handler.ListCategories)
			tr.Post("/restaurant/menu/categories", handler.CreateCategory)
			tr.Get("/restaurant/menu/items", handler.ListMenuItems)
			tr.Post("/restaurant/menu/items", handler.CreateMenuItem)
			tr.Post("/restaurant/menu/ai-catalog", handler.AICatalogMenu)
			tr.Post("/restaurant/menu/ai-query", handler.AIQueryMenu)

			// Staff Management
			tr.Get("/restaurant/staff", handler.ListStaff)

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
			pr.Get("/admin/restaurants", handler.ListAllRestaurants)
			pr.Get("/admin/restaurants/{id}", handler.GetRestaurantDetails)
			pr.Get("/admin/restaurants/{id}/onboarding", handler.GetRestaurantOnboardingAdmin)
			pr.Get("/admin/analytics/platform", handler.GetPlatformAnalytics)
			pr.Get("/admin/fraud-review", handler.GetFraudReviewQueue)
			pr.Post("/admin/restaurants/{id}/commission-rate", handler.OverrideCommissionRate)
			pr.Post("/admin/restaurants/{id}/suspend", handler.SuspendRestaurant)
			pr.Post("/admin/restaurants/{id}/reactivate", handler.ReactivateRestaurant)
			pr.Get("/admin/restaurants/{id}/tables/qr-export", handler.ExportTableQRs)
		})
	})

	return r
}
