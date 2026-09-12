package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/devrishijain/table-manager/internal/config"
	"github.com/devrishijain/table-manager/internal/storage/memory"
	"github.com/devrishijain/table-manager/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load configuration in worker", "error", err)
		os.Exit(1)
	}

	logger.Info("Starting Restaurant Dining OS Background Worker standalone process",
		"env", cfg.App.Env,
		"outbox_poll_interval", cfg.Worker.OutboxPollInterval,
		"inactivity_sweep_interval", cfg.Worker.InactivitySweepInterval,
	)

	repo := memory.NewMemoryRepository()
	w := worker.NewWorker(repo, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.Start(ctx)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Stopping background worker...")
	w.Stop()
}
