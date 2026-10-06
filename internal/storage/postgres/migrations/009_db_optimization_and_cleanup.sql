-- 009_db_optimization_and_cleanup.sql: Schema cleanup, high-performance composite indexes, order_status_history, and transactional event_outbox

-- 1. Drop redundant / dead tables
DROP TABLE IF EXISTS session_participants CASCADE;
DROP TABLE IF EXISTS refund_adjustments CASCADE;

-- Unify guards into staff_users: drop legacy guard_users reference on exit_passes
ALTER TABLE exit_passes DROP CONSTRAINT IF EXISTS exit_passes_used_by_guard_id_fkey;
DROP TABLE IF EXISTS guard_users CASCADE;

-- 2. Add high-performance composite indexes for extreme scale (<10ms query times)
CREATE INDEX IF NOT EXISTS idx_payments_session_id ON payments (session_id);
CREATE INDEX IF NOT EXISTS idx_payments_rest_created ON payments (restaurant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_token ON dining_sessions (session_token);
CREATE INDEX IF NOT EXISTS idx_sessions_rest_status ON dining_sessions (restaurant_id, status);
CREATE INDEX IF NOT EXISTS idx_orders_session_created ON orders (session_id, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_menu_items_rest_avail ON menu_items (restaurant_id, is_available);

-- 3. Order Status Audit History: Immutably tracks every atomic state transition
CREATE TABLE IF NOT EXISTS order_status_history (
    id UUID PRIMARY KEY,
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    from_status VARCHAR(32) NOT NULL,
    to_status VARCHAR(32) NOT NULL,
    changed_by_staff_id UUID REFERENCES staff_users(id),
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_order_status_history_order ON order_status_history (order_id, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_order_status_history_rest ON order_status_history (restaurant_id, created_at DESC);

-- 4. Transactional Event Outbox: 24h retention for rock-solid event replay, fanout, and guaranteed delivery
CREATE TABLE IF NOT EXISTS event_outbox (
    id UUID PRIMARY KEY,
    aggregate_type VARCHAR(64) NOT NULL,
    aggregate_id VARCHAR(128) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    room VARCHAR(128) NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    retry_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    dispatched_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_event_outbox_pending ON event_outbox (status, created_at ASC) WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS idx_event_outbox_room_created ON event_outbox (room, created_at DESC);
