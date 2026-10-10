package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/devrishijain/table-manager/internal/config"
	"github.com/jackc/pgx/v5"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	logger.Info("Starting PostgreSQL migration runner", "database_url", cfg.Database.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, cfg.Database.URL)
	if err != nil {
		logger.Error("Failed to connect to PostgreSQL database", "error", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	migrationFiles := []string{
		"internal/storage/postgres/migrations/001_initial_schema.sql",
		"internal/storage/postgres/migrations/002_rls_policies.sql",
		"internal/storage/postgres/migrations/003_schema_sync.sql",
		"internal/storage/postgres/migrations/004_staff_employee_id.sql",
		"internal/storage/postgres/migrations/005_performance_indexes.sql",
		"internal/storage/postgres/migrations/006_dining_sessions_vehicle_number.sql",
		"internal/storage/postgres/migrations/007_expenses_inventory_recipes.sql",
		"internal/storage/postgres/migrations/008_expense_inventory_order_correlation.sql",
		"internal/storage/postgres/migrations/009_db_optimization_and_cleanup.sql",
		"internal/storage/postgres/migrations/010_db_optimization_matrix.sql",
		"internal/storage/postgres/migrations/011_password_resets.sql",
		"internal/storage/postgres/migrations/012_restaurant_subscriptions.sql",
		"internal/storage/postgres/migrations/013_franchise_variants_waiter_otp.sql",
		"internal/storage/postgres/migrations/014_perf_indexes_batch.sql",
	}

	for _, file := range migrationFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			logger.Error("Failed to read migration file", "file", file, "error", err)
			os.Exit(1)
		}

		logger.Info("Applying migration", "file", file, "size_bytes", len(content))
		if _, err := conn.Exec(ctx, string(content)); err != nil {
			logger.Error("Migration failed", "file", file, "error", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Successfully applied %s\n", file)
	}

	logger.Info("All database migrations applied successfully")
}
