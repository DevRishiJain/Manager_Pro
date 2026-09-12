-- 001_initial_schema.sql: Core schema for Restaurant Dining OS Backend

CREATE TABLE IF NOT EXISTS restaurants (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    gstin VARCHAR(64) NOT NULL,
    commission_rate_bps BIGINT NOT NULL DEFAULT 100, -- 100 = 1.00%
    settlement_bank_details TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'ACTIVE',
    timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Kolkata',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tables (
    id UUID PRIMARY KEY,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    table_number VARCHAR(32) NOT NULL,
    table_token VARCHAR(128) NOT NULL UNIQUE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (restaurant_id, table_number)
);

CREATE TABLE IF NOT EXISTS staff_users (
    id UUID PRIMARY KEY,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    phone VARCHAR(32) NOT NULL,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(32) NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS guard_users (
    id UUID PRIMARY KEY,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    phone VARCHAR(32) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS menu_categories (
    id UUID PRIMARY KEY,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    display_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS menu_items (
    id UUID PRIMARY KEY,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    category_id UUID NOT NULL REFERENCES menu_categories(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    price_minor BIGINT NOT NULL,
    currency VARCHAR(16) NOT NULL DEFAULT 'INR',
    is_available BOOLEAN NOT NULL DEFAULT TRUE,
    hsn_sac_code VARCHAR(32) NOT NULL DEFAULT '996331',
    cgst_rate_bps BIGINT NOT NULL DEFAULT 250, -- 2.5%
    sgst_rate_bps BIGINT NOT NULL DEFAULT 250, -- 2.5%
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS dining_sessions (
    id UUID PRIMARY KEY,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE RESTRICT,
    table_id UUID NOT NULL REFERENCES tables(id) ON DELETE RESTRICT,
    status VARCHAR(32) NOT NULL DEFAULT 'OPEN',
    opened_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ,
    verified_at TIMESTAMPTZ,
    verified_by_staff_id UUID REFERENCES staff_users(id),
    running_total_minor BIGINT NOT NULL DEFAULT 0,
    final_total_minor BIGINT NOT NULL DEFAULT 0,
    platform_fee_minor BIGINT NOT NULL DEFAULT 0,
    currency VARCHAR(16) NOT NULL DEFAULT 'INR',
    session_token VARCHAR(255) NOT NULL UNIQUE,
    device_fingerprint VARCHAR(255) NOT NULL DEFAULT '',
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expiry_deadline TIMESTAMPTZ NOT NULL,
    close_reason VARCHAR(32),
    closed_by_actor_type VARCHAR(32),
    closed_by_actor_id VARCHAR(64),
    version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Constraint: A table may have at most one non-terminal session open at a time
CREATE UNIQUE INDEX IF NOT EXISTS idx_unique_active_session_per_table 
ON dining_sessions (table_id) 
WHERE status IN ('OPEN', 'OPEN_VERIFIED', 'AWAITING_PAYMENT');

CREATE TABLE IF NOT EXISTS session_participants (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES dining_sessions(id) ON DELETE CASCADE,
    device_token VARCHAR(255) NOT NULL,
    display_name VARCHAR(128) NOT NULL DEFAULT '',
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS orders (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES dining_sessions(id) ON DELETE CASCADE,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE RESTRICT,
    sequence_number INT NOT NULL DEFAULT 1,
    status VARCHAR(32) NOT NULL,
    placed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_at TIMESTAMPTZ,
    accepted_by_staff_id UUID REFERENCES staff_users(id),
    subtotal_minor BIGINT NOT NULL DEFAULT 0,
    tax_total_minor BIGINT NOT NULL DEFAULT 0,
    total_minor BIGINT NOT NULL DEFAULT 0,
    currency VARCHAR(16) NOT NULL DEFAULT 'INR',
    cancelled_at TIMESTAMPTZ,
    cancellation_stage VARCHAR(32),
    cancellation_fee_applicable BOOLEAN NOT NULL DEFAULT FALSE,
    version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS order_items (
    id UUID PRIMARY KEY,
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    menu_item_id UUID NOT NULL REFERENCES menu_items(id),
    variant_id UUID,
    item_name_snapshot VARCHAR(255) NOT NULL,
    quantity INT NOT NULL DEFAULT 1,
    unit_price_minor BIGINT NOT NULL,
    line_total_minor BIGINT NOT NULL,
    currency VARCHAR(16) NOT NULL DEFAULT 'INR',
    hsn_sac_code_snapshot VARCHAR(32) NOT NULL DEFAULT '996331',
    cgst_rate_bps_snapshot BIGINT NOT NULL,
    sgst_rate_bps_snapshot BIGINT NOT NULL,
    cgst_amount_minor BIGINT NOT NULL,
    sgst_amount_minor BIGINT NOT NULL,
    special_instructions TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payments (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES dining_sessions(id) ON DELETE RESTRICT,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE RESTRICT,
    method VARCHAR(32) NOT NULL,
    external_platform_name VARCHAR(64),
    amount_minor BIGINT NOT NULL,
    currency VARCHAR(16) NOT NULL DEFAULT 'INR',
    status VARCHAR(32) NOT NULL,
    gateway_reference_id VARCHAR(255),
    evidence_transaction_id VARCHAR(255),
    evidence_photo_url TEXT,
    confirmed_by_staff_id UUID REFERENCES staff_users(id),
    confirmed_at TIMESTAMPTZ,
    version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS refunds (
    id UUID PRIMARY KEY,
    payment_id UUID NOT NULL REFERENCES payments(id) ON DELETE RESTRICT,
    session_id UUID NOT NULL REFERENCES dining_sessions(id) ON DELETE RESTRICT,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE RESTRICT,
    amount_minor BIGINT NOT NULL,
    currency VARCHAR(16) NOT NULL DEFAULT 'INR',
    reason TEXT NOT NULL,
    initiated_by UUID NOT NULL REFERENCES staff_users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS adjustments (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES dining_sessions(id) ON DELETE RESTRICT,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE RESTRICT,
    type VARCHAR(32) NOT NULL,
    amount_minor BIGINT NOT NULL,
    currency VARCHAR(16) NOT NULL DEFAULT 'INR',
    notes TEXT NOT NULL,
    created_by UUID NOT NULL REFERENCES staff_users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS exit_passes (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES dining_sessions(id) ON DELETE CASCADE,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE RESTRICT,
    otp_hash VARCHAR(128) NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    used_by_guard_id UUID REFERENCES guard_users(id),
    status VARCHAR(32) NOT NULL,
    failed_attempts INT NOT NULL DEFAULT 0,
    locked_until TIMESTAMPTZ,
    requires_override BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS platform_fee_ledger (
    id UUID PRIMARY KEY,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE RESTRICT,
    session_id UUID NOT NULL REFERENCES dining_sessions(id) ON DELETE RESTRICT,
    gmv_amount_minor BIGINT NOT NULL,
    fee_amount_minor BIGINT NOT NULL,
    currency VARCHAR(16) NOT NULL DEFAULT 'INR',
    fee_rate_applied BIGINT NOT NULL,
    billing_period VARCHAR(32) NOT NULL,
    settlement_status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS refund_adjustments (
    id UUID PRIMARY KEY,
    platform_fee_ledger_id UUID NOT NULL REFERENCES platform_fee_ledger(id) ON DELETE RESTRICT,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE RESTRICT,
    session_id UUID NOT NULL REFERENCES dining_sessions(id) ON DELETE RESTRICT,
    refund_id UUID NOT NULL REFERENCES refunds(id) ON DELETE RESTRICT,
    original_fee_minor BIGINT NOT NULL,
    adjusted_fee_reduction_minor BIGINT NOT NULL,
    currency VARCHAR(16) NOT NULL DEFAULT 'INR',
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS restaurant_settlements (
    id UUID PRIMARY KEY,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE RESTRICT,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    gross_sales_minor BIGINT NOT NULL,
    platform_fees_owed_minor BIGINT NOT NULL,
    refund_adjustments_minor BIGINT NOT NULL,
    net_payable_platform_minor BIGINT NOT NULL,
    currency VARCHAR(16) NOT NULL DEFAULT 'INR',
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    invoiced_at TIMESTAMPTZ,
    settled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS restaurant_settings (
    restaurant_id UUID PRIMARY KEY REFERENCES restaurants(id) ON DELETE CASCADE,
    exit_verification_mode VARCHAR(32) NOT NULL DEFAULT 'NO_EXIT_VERIFICATION',
    shared_session_policy VARCHAR(32) NOT NULL DEFAULT 'SHARED_TABLE_SESSION',
    high_value_threshold_minor BIGINT NOT NULL DEFAULT 500000,
    rapid_order_jump_factor INT NOT NULL DEFAULT 3,
    external_evidence_required BOOLEAN NOT NULL DEFAULT TRUE,
    pos_evidence_required BOOLEAN NOT NULL DEFAULT FALSE,
    first_order_otp_ttl_minutes INT NOT NULL DEFAULT 15,
    exit_pass_otp_ttl_minutes INT NOT NULL DEFAULT 120,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS restaurant_onboarding (
    restaurant_id UUID PRIMARY KEY REFERENCES restaurants(id) ON DELETE CASCADE,
    current_step INT NOT NULL DEFAULT 1,
    steps_completed JSONB NOT NULL DEFAULT '[]',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    assigned_platform_contact VARCHAR(255),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id UUID PRIMARY KEY,
    actor_type VARCHAR(32) NOT NULL,
    actor_id VARCHAR(64) NOT NULL,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE RESTRICT,
    session_id UUID REFERENCES dining_sessions(id) ON DELETE SET NULL,
    action VARCHAR(64) NOT NULL,
    before_state JSONB,
    after_state JSONB,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Immutability enforcement trigger on audit_logs
CREATE OR REPLACE FUNCTION forbid_audit_log_modification()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'Audit log entries are strictly immutable. UPDATE and DELETE are prohibited.';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_log_immutability ON audit_logs;
CREATE TRIGGER audit_log_immutability
BEFORE UPDATE OR DELETE ON audit_logs
FOR EACH ROW EXECUTE FUNCTION forbid_audit_log_modification();

CREATE TABLE IF NOT EXISTS staff_actions (
    id UUID PRIMARY KEY,
    audit_log_id UUID NOT NULL REFERENCES audit_logs(id) ON DELETE RESTRICT,
    staff_id UUID NOT NULL REFERENCES staff_users(id) ON DELETE RESTRICT,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE RESTRICT,
    session_id UUID REFERENCES dining_sessions(id) ON DELETE SET NULL,
    action_type VARCHAR(64) NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS outbox_events (
    id UUID PRIMARY KEY,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    event_type VARCHAR(64) NOT NULL,
    aggregate_id VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    retries INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS webhook_events (
    id UUID PRIMARY KEY,
    gateway VARCHAR(32) NOT NULL,
    event_id VARCHAR(255) NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(gateway, event_id)
);
