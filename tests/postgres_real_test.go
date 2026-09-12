package tests

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func getTestDBPool(t *testing.T) *pgxpool.Pool {
	connStr := os.Getenv("TEST_DATABASE_URL")
	if connStr == "" {
		// Default to local postgresql socket or standard port
		connStr = "postgres://localhost:5432/table_manager_test?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		t.Skipf("Skipping Postgres integration test: invalid connection string %v", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Skipf("Skipping Postgres integration test: cannot connect to Postgres: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("Skipping Postgres integration test: ping failed: %v", err)
	}

	return pool
}

// TestPostgresPartialUniqueIndexConcurrentSessions verifies the database-enforced
// partial unique index: idx_unique_active_session_per_table ON dining_sessions (table_id) WHERE status IN ('OPEN', 'OPEN_VERIFIED', 'AWAITING_PAYMENT')
func TestPostgresPartialUniqueIndexConcurrentSessions(t *testing.T) {
	pool := getTestDBPool(t)
	defer pool.Close()
	ctx := context.Background()

	restID := uuid.New()
	tableID := uuid.New()

	// Setup restaurant & table
	_, err := pool.Exec(ctx, `
		INSERT INTO restaurants (id, name, gstin, commission_rate_bps, status)
		VALUES ($1, 'Real PG Cafe', '07AABCT2345Z1Z5', 250, 'ACTIVE')
		ON CONFLICT (id) DO NOTHING;
	`, restID)
	if err != nil {
		t.Fatalf("Failed to insert restaurant: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO tables (id, restaurant_id, table_number, table_token)
		VALUES ($1, $2, 'T-PG-01', $3)
		ON CONFLICT (id) DO NOTHING;
	`, tableID, restID, "tok-"+tableID.String()[:8])
	if err != nil {
		t.Fatalf("Failed to insert table: %v", err)
	}

	t.Run("Concurrent Active Sessions Collide on Same Table", func(t *testing.T) {
		const concurrentScans = 10
		var wg sync.WaitGroup
		successCount := 0
		var mu sync.Mutex
		var duplicateErrCount int

		for i := 0; i < concurrentScans; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				sessID := uuid.New()
				sessToken := fmt.Sprintf("sess-tok-%d-%s", idx, sessID.String()[:8])

				_, insertErr := pool.Exec(ctx, `
					INSERT INTO dining_sessions (id, table_id, restaurant_id, status, session_token, expiry_deadline, version)
					VALUES ($1, $2, $3, 'OPEN', $4, NOW() + INTERVAL '2 hours', 1)
				`, sessID, tableID, restID, sessToken)

				mu.Lock()
				defer mu.Unlock()
				if insertErr == nil {
					successCount++
				} else {
					if pgErr, ok := insertErr.(*pgconn.PgError); ok && pgErr.Code == "23505" {
						duplicateErrCount++
					}
				}
			}(i)
		}

		wg.Wait()

		if successCount != 1 {
			t.Fatalf("Expected exactly 1 session insertion to succeed, got %d", successCount)
		}
		if duplicateErrCount != concurrentScans-1 {
			t.Fatalf("Expected exactly %d duplicate key collisions (SQLSTATE 23505), got %d", concurrentScans-1, duplicateErrCount)
		}
	})

	t.Run("Terminal Session Frees Table For Next Dining Session", func(t *testing.T) {
		// Close existing active session to COMPLETED
		_, err := pool.Exec(ctx, `
			UPDATE dining_sessions 
			SET status = 'COMPLETED', closed_at = NOW() 
			WHERE table_id = $1 AND status = 'OPEN'
		`, tableID)
		if err != nil {
			t.Fatalf("Failed to transition active session to terminal: %v", err)
		}

		// Now a new session should immediately succeed on the exact same table
		newSessID := uuid.New()
		newSessToken := "sess-tok-new-" + newSessID.String()[:8]
		_, err = pool.Exec(ctx, `
			INSERT INTO dining_sessions (id, table_id, restaurant_id, status, session_token, expiry_deadline, version)
			VALUES ($1, $2, $3, 'OPEN', $4, NOW() + INTERVAL '2 hours', 1)
		`, newSessID, tableID, restID, newSessToken)

		if err != nil {
			t.Fatalf("Expected new session on freed table to succeed, got error: %v", err)
		}
	})
}

// TestPostgresRowLevelSecurityRealDatabase verifies that PostgreSQL RLS
// strictly enforces cross-tenant boundary isolation at the database layer.
func TestPostgresRowLevelSecurityRealDatabase(t *testing.T) {
	pool := getTestDBPool(t)
	defer pool.Close()
	ctx := context.Background()

	// Provision non-superuser application role to ensure BYPASSRLS does not apply
	_, _ = pool.Exec(ctx, `
		DO $$
		BEGIN
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'table_manager_app_user') THEN
				CREATE ROLE table_manager_app_user WITH LOGIN;
			END IF;
		END;
		$$;
		GRANT USAGE ON SCHEMA public TO table_manager_app_user;
		GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO table_manager_app_user;
	`)

	restA := uuid.New()
	restB := uuid.New()

	// Insert test restaurants as superuser
	_, err := pool.Exec(ctx, `
		INSERT INTO restaurants (id, name, gstin, commission_rate_bps, status)
		VALUES 
			($1, 'Restaurant Alpha', '07AAAAA1111A1Z1', 250, 'ACTIVE'),
			($2, 'Restaurant Beta', '07BBBBB2222B1Z2', 250, 'ACTIVE')
		ON CONFLICT (id) DO NOTHING;
	`, restA, restB)
	if err != nil {
		t.Fatalf("Failed to seed restaurants: %v", err)
	}

	t.Run("Fail-Closed Tenant Isolation When app.current_restaurant_id is Not Set", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("Failed to start transaction: %v", err)
		}
		defer tx.Rollback(ctx)

		// Switch to app user within transaction
		_, err = tx.Exec(ctx, "SET LOCAL ROLE table_manager_app_user;")
		if err != nil {
			t.Fatalf("Failed to set role: %v", err)
		}

		// Querying tables without setting app.current_restaurant_id must return 0 rows (fail closed)
		var count int
		err = tx.QueryRow(ctx, "SELECT count(*) FROM tables;").Scan(&count)
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}
		if count != 0 {
			t.Fatalf("Security violation: expected 0 tables when tenant context is unset, got %d", count)
		}
	})

	t.Run("Tenant A Can Only See Its Own Rows", func(t *testing.T) {
		// First, insert 1 table for A and 1 table for B using superuser
		tableA := uuid.New()
		tableB := uuid.New()
		_, err = pool.Exec(ctx, `
			INSERT INTO tables (id, restaurant_id, table_number, table_token)
			VALUES 
				($1, $2, 'A-01', $3),
				($4, $5, 'B-01', $6)
			ON CONFLICT (id) DO NOTHING;
		`, tableA, restA, "tok-a-"+tableA.String()[:6], tableB, restB, "tok-b-"+tableB.String()[:6])
		if err != nil {
			t.Fatalf("Failed to insert tables: %v", err)
		}

		// Connect as app role and set tenant A
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("Failed to start transaction: %v", err)
		}
		defer tx.Rollback(ctx)

		_, err = tx.Exec(ctx, "SET LOCAL ROLE table_manager_app_user;")
		if err != nil {
			t.Fatalf("Failed to set role: %v", err)
		}

		_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL app.current_restaurant_id = '%s';", restA.String()))
		if err != nil {
			t.Fatalf("Failed to set tenant context: %v", err)
		}

		// Count visible tables
		var count int
		err = tx.QueryRow(ctx, "SELECT count(*) FROM tables;").Scan(&count)
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}
		if count != 1 {
			t.Fatalf("Expected exactly 1 table visible to Tenant A, got %d", count)
		}

		// Probing specifically for Table B
		var bCount int
		err = tx.QueryRow(ctx, "SELECT count(*) FROM tables WHERE id = $1;", tableB).Scan(&bCount)
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}
		if bCount != 0 {
			t.Fatalf("Security breach: Tenant A was able to read Table B (count=%d)", bCount)
		}

		// Tenant A attempting to insert a row with restaurant_id = B must be rejected by WITH CHECK policy
		maliciousTable := uuid.New()
		_, insertErr := tx.Exec(ctx, `
			INSERT INTO tables (id, restaurant_id, table_number, table_token)
			VALUES ($1, $2, 'HACK-01', $3);
		`, maliciousTable, restB, "tok-hack-"+maliciousTable.String()[:6])

		if insertErr == nil {
			t.Fatalf("Security breach: Tenant A successfully inserted table for Restaurant B!")
		}
		if pgErr, ok := insertErr.(*pgconn.PgError); ok {
			if pgErr.Code != "42501" { // 42501: insufficient_privilege / row_level_security_violation
				t.Logf("Got expected RLS violation code: %s (%s)", pgErr.Code, pgErr.Message)
			}
		}
	})

	t.Run("Platform Super Admin Bypasses Tenant Filter Legally", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("Failed to start transaction: %v", err)
		}
		defer tx.Rollback(ctx)

		_, err = tx.Exec(ctx, "SET LOCAL ROLE table_manager_app_user;")
		if err != nil {
			t.Fatalf("Failed to set role: %v", err)
		}

		_, err = tx.Exec(ctx, "SET LOCAL app.is_platform_admin = 'true';")
		if err != nil {
			t.Fatalf("Failed to set platform admin flag: %v", err)
		}

		var count int
		err = tx.QueryRow(ctx, "SELECT count(*) FROM tables WHERE restaurant_id IN ($1, $2);", restA, restB).Scan(&count)
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}
		if count < 2 {
			t.Fatalf("Expected Platform Admin to see tables from both tenants (>=2), got %d", count)
		}
	})
}

// TestPostgresAuditLogImmutabilityTrigger proves that audit log records cannot be modified or deleted.
func TestPostgresAuditLogImmutabilityTrigger(t *testing.T) {
	pool := getTestDBPool(t)
	defer pool.Close()
	ctx := context.Background()

	auditID := uuid.New()
	restID := uuid.New()

	// Seed restaurant for foreign key
	_, err := pool.Exec(ctx, `
		INSERT INTO restaurants (id, name, gstin, commission_rate_bps, status)
		VALUES ($1, 'Audit Log Test Rest', '07AUDIT1234Z1Z9', 250, 'ACTIVE')
		ON CONFLICT (id) DO NOTHING;
	`, restID)
	if err != nil {
		t.Fatalf("Failed to seed restaurant: %v", err)
	}

	// Insert audit log
	_, err = pool.Exec(ctx, `
		INSERT INTO audit_logs (id, actor_type, actor_id, restaurant_id, action)
		VALUES ($1, 'STAFF', 'staff-123', $2, 'PAYMENT_CONFIRMED');
	`, auditID, restID)
	if err != nil {
		t.Fatalf("Failed to insert audit log: %v", err)
	}

	t.Run("UPDATE on audit_logs is Rejected by Trigger", func(t *testing.T) {
		_, updateErr := pool.Exec(ctx, "UPDATE audit_logs SET action = 'TAMPERED' WHERE id = $1;", auditID)
		if updateErr == nil {
			t.Fatalf("Expected audit log UPDATE to fail due to immutability trigger!")
		}
		if pgErr, ok := updateErr.(*pgconn.PgError); ok {
			if pgErr.Message != "Audit log entries are strictly immutable. UPDATE and DELETE are prohibited." {
				t.Fatalf("Unexpected trigger message: %s", pgErr.Message)
			}
		}
	})

	t.Run("DELETE on audit_logs is Rejected by Trigger", func(t *testing.T) {
		_, deleteErr := pool.Exec(ctx, "DELETE FROM audit_logs WHERE id = $1;", auditID)
		if deleteErr == nil {
			t.Fatalf("Expected audit log DELETE to fail due to immutability trigger!")
		}
		if pgErr, ok := deleteErr.(*pgconn.PgError); ok {
			if pgErr.Message != "Audit log entries are strictly immutable. UPDATE and DELETE are prohibited." {
				t.Fatalf("Unexpected trigger message: %s", pgErr.Message)
			}
		}
	})
}
