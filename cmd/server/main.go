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
	var repo domainstorage.Repository = memory.NewMemoryRepository()
	if cfg.Database.URL != "" {
		poolCtx, poolCancel := context.WithTimeout(context.Background(), 5*time.Second)
		pgxCfg, err := pgxpool.ParseConfig(cfg.Database.URL)
		if err == nil {
			pgxCfg.MaxConns = int32(cfg.Database.MaxOpenConns)
			pgxCfg.MinConns = int32(cfg.Database.MaxIdleConns)
			pgxCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
				_, err := conn.Exec(ctx, "SET app.is_platform_admin = 'true'")
				return err
			}
			pool, err := pgxpool.NewWithConfig(poolCtx, pgxCfg)
			if err == nil && pool.Ping(poolCtx) == nil {
				repo = postgres.NewPostgresRepository(pool)
				logger.Info("PostgreSQL database repository connected & active", "db_url", cfg.Database.URL)
			} else {
				logger.Warn("PostgreSQL connection failed, using memory fallback", "error", err)
			}
		}
		poolCancel()
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
	if aiCatalogSvc != nil {
		apiHandler.SetAICatalogService(aiCatalogSvc)
	}

	router := api.NewRouter(apiHandler, repo, []byte(cfg.Auth.JWTSecret))

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
