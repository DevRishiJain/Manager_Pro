-- 014_perf_indexes_batch.sql: Indexes for batch query paths and admin cross-tenant scans

-- Platform fee ledger: cross-tenant admin overview scans by restaurant
CREATE INDEX IF NOT EXISTS idx_platform_fee_ledger_rest_created
ON platform_fee_ledger (restaurant_id, created_at DESC);

-- Audit logs: tenant-scoped listing with pagination
CREATE INDEX IF NOT EXISTS idx_audit_logs_rest_created
ON audit_logs (restaurant_id, created_at DESC);

-- Staff actions: tenant-scoped listing with pagination
CREATE INDEX IF NOT EXISTS idx_staff_actions_rest_created
ON staff_actions (restaurant_id, created_at DESC);

-- Staff employee_id case-insensitive lookup (avoids full scan on UPPER())
CREATE INDEX IF NOT EXISTS idx_staff_users_employee_id_upper
ON staff_users (UPPER(employee_id));

-- Expenses: tenant + date pagination
CREATE INDEX IF NOT EXISTS idx_restaurant_expenses_rest_date
ON restaurant_expenses (restaurant_id, expense_date DESC, created_at DESC);

-- Payments: batch lookup by session IDs (admin/analytics batch paths)
CREATE INDEX IF NOT EXISTS idx_payments_session_status
ON payments (session_id, status);

-- Orders: cross-tenant recent activity feed (admin)
CREATE INDEX IF NOT EXISTS idx_orders_placed_desc
ON orders (placed_at DESC);

-- Dining sessions: batch lookup by IDs
CREATE INDEX IF NOT EXISTS idx_dining_sessions_id_status
ON dining_sessions (id, status);
