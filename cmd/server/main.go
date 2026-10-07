package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/devrishijain/table-manager/internal/adapter/forecast"
	"github.com/devrishijain/table-manager/internal/adapter/storage"
	"github.com/devrishijain/table-manager/internal/api"
	"github.com/devrishijain/table-manager/internal/api/handlers"
	"github.com/devrishijain/table-manager/internal/config"
	"github.com/devrishijain/table-manager/internal/service"
	domainstorage "github.com/devrishijain/table-manager/internal/storage"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/internal/storage/postgres"
	"github.com/devrishijain/table-manager/internal/worker"
	"github.com/devrishijain/table-manager/internal/ws"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Load centralized configuration from environment / .env
	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	logger.Info("Starting Restaurant Dining OS Backend",
		"version", "v2.0.0",
		"env", cfg.App.Env,
		"port", cfg.App.Port,
		"razorpay_key_id", cfg.Razorpay.KeyID,
	)

	// Storage initialization
	var repo domainstorage.Repository
	if cfg.Database.URL != "" {
		poolCtx, poolCancel := context.WithTimeout(context.Background(), 20*time.Second)
		pgxCfg, err := pgxpool.ParseConfig(cfg.Database.URL)
		if err != nil {
			logger.Error("Invalid PostgreSQL database URL configuration", "error", err)
			os.Exit(1)
		}

		pgxCfg.MaxConns = int32(cfg.Database.MaxOpenConns)
		if pgxCfg.MaxConns < 25 {
			pgxCfg.MaxConns = 50
		}
		pgxCfg.MinConns = 10
		pgxCfg.MaxConnIdleTime = 5 * time.Minute
		pgxCfg.MaxConnLifetime = 30 * time.Minute
		pgxCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, "SET app.is_platform_admin = 'true'")
			return err
		}

		pool, poolErr := pgxpool.NewWithConfig(poolCtx, pgxCfg)
		var pingErr error
		if poolErr == nil {
			pingErr = pool.Ping(poolCtx)
		}
		poolCancel()

		if poolErr == nil && pingErr == nil {
			repo = postgres.NewPostgresRepository(pool)
			logger.Info("PostgreSQL database repository connected & active", "db_url", cfg.Database.URL)
		} else {
			connErr := poolErr
			if connErr == nil {
				connErr = pingErr
			}
			if !cfg.Database.AllowMemoryFallback {
				logger.Error("FATAL: PostgreSQL connection failed and memory fallback is disabled (ALLOW_MEMORY_FALLBACK=false). Set ALLOW_MEMORY_FALLBACK=true if in-memory fallback is desired.", "error", connErr)
				os.Exit(1)
			}
			logger.Warn("PostgreSQL connection failed, using memory fallback (ALLOW_MEMORY_FALLBACK=true)", "error", connErr)
			repo = memory.NewMemoryRepository()
		}
	} else {
		if !cfg.Database.AllowMemoryFallback {
			logger.Error("FATAL: DATABASE_URL is not configured and memory fallback is disabled. Set DATABASE_URL or set ALLOW_MEMORY_FALLBACK=true.")
			os.Exit(1)
		}
		logger.Warn("DATABASE_URL not set, using memory repository (ALLOW_MEMORY_FALLBACK=true)")
		repo = memory.NewMemoryRepository()
	}

	var objectStore storage.ObjectStore
	if cfg.Storage.Type == "minio" || cfg.Storage.Type == "s3" {
		objectStore = storage.NewS3ObjectStore(storage.S3Config{
			Region:          cfg.Storage.Region,
			BucketName:      cfg.Storage.Bucket,
			AccessKeyID:     cfg.Storage.AccessKey,
			SecretAccessKey: cfg.Storage.SecretKey,
			Endpoint:        cfg.Storage.Endpoint,
		})
		logger.Info("Object storage initialized", "type", cfg.Storage.Type, "endpoint", cfg.Storage.Endpoint, "bucket", cfg.Storage.Bucket)
	} else {
		objectStore = storage.NewMemoryObjectStore()
		logger.Info("Object storage initialized", "type", "memory")
	}

	forecastProvider := forecast.NewWeightedMovingAverageForecast()

	// Wrap repository with query tracker for metrics instrumentation
	repo = domainstorage.NewTrackedRepository(repo)

	// Bootstrap platform super admin (idempotent; only when configured)
	if cfg.Auth.SuperAdminEmail != "" && cfg.Auth.SuperAdminPassword != "" {
		if err := service.EnsureSuperAdmin(context.Background(), repo, cfg.Auth.SuperAdminEmail, cfg.Auth.SuperAdminPassword); err != nil {
			logger.Error("Failed to ensure super admin", "error", err)
		} else {
			logger.Info("Platform super admin ensured", "email", cfg.Auth.SuperAdminEmail)
		}
	}

	// Service layers
	sessionSvc := service.NewSessionService(repo)
	orderSvc := service.NewOrderService(repo)
	exitSvc := service.NewExitService(repo)
	ledgerSvc := service.NewLedgerService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, cfg.Razorpay.WebhookSecret)
	analyticsSvc := service.NewAnalyticsService(repo, forecastProvider)
	onboardingSvc := service.NewOnboardingService(repo)

	// AI Menu Cataloging Service
	var aiCatalogSvc *service.AICatalogService
	if cfg.AI.GeminiAPIKey != "" {
		aiCatalogSvc = service.NewAICatalogService(repo, objectStore, cfg.AI.GeminiAPIKey, cfg.AI.LLMModel)
		logger.Info("Gemini AI Catalog Service initialized", "model", cfg.AI.LLMModel)
	}

	// Background worker
	bgWorker := worker.NewWorker(repo, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bgWorker.Start(ctx)

	staffSvc := service.NewStaffService(repo, []byte(cfg.Auth.JWTSecret))

	// Handlers & Router
	apiHandler := handlers.NewAPIHandler(
		sessionSvc,
		orderSvc,
		paymentSvc,
		exitSvc,
		ledgerSvc,
		analyticsSvc,
		onboardingSvc,
		objectStore,
		repo,
		cfg.Razorpay.WebhookSecret,
	)
	apiHandler.SetStaffService(staffSvc)
	apiHandler.SetJWTSecret([]byte(cfg.Auth.JWTSecret))
	expSvc := service.NewExpenseService(repo)
	invSvc := service.NewInventoryService(repo)
	apiHandler.SetExpenseService(expSvc)
	apiHandler.SetInventoryService(invSvc)
	if aiCatalogSvc != nil {
		apiHandler.SetAICatalogService(aiCatalogSvc)
	}

	// Real-Time WebSocket Hub, Ticket Manager & Outbox Dispatcher (§Phase 2)
	wsHub := ws.NewHub()
	wsHub.StartCleanup(ctx)
	wsTM := ws.NewTicketManager(30 * time.Second)
	wsServer := ws.NewServer(wsHub, wsTM, []string{"*"})
	apiHandler.SetWSTicketManager(wsTM)

	outboxDispatcher := ws.NewOutboxDispatcher(wsHub, repo, logger)
	go outboxDispatcher.Start(ctx)
	defer outboxDispatcher.Stop()

	orderSvc.SetOutboxDispatcher(outboxDispatcher)
	sessionSvc.SetOutboxDispatcher(outboxDispatcher)
	paymentSvc.SetOutboxDispatcher(outboxDispatcher)
	exitSvc.SetOutboxDispatcher(outboxDispatcher)

	router := api.NewRouter(apiHandler, repo, []byte(cfg.Auth.JWTSecret), wsServer)

	server := &http.Server{
		Addr:         ":" + cfg.App.Port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("HTTP server listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server gracefully...")
	bgWorker.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
	}
	logger.Info("Server stopped cleanly")
}
