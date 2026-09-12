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
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/internal/worker"
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
	repo := memory.NewMemoryRepository()
	objectStore := storage.NewMemoryObjectStore()
	forecastProvider := forecast.NewWeightedMovingAverageForecast()

	// Service layers
	sessionSvc := service.NewSessionService(repo)
	orderSvc := service.NewOrderService(repo)
	exitSvc := service.NewExitService(repo)
	ledgerSvc := service.NewLedgerService(repo)
	paymentSvc := service.NewPaymentService(repo, ledgerSvc, exitSvc, cfg.Razorpay.WebhookSecret)
	analyticsSvc := service.NewAnalyticsService(repo, forecastProvider)
	onboardingSvc := service.NewOnboardingService(repo)

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
