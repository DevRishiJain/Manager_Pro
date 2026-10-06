-- 011_password_resets.sql: Single-use password reset tokens with 15-minute expiration
CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id UUID PRIMARY KEY,
    staff_id UUID NOT NULL REFERENCES staff_users(id) ON DELETE CASCADE,
    token_hash VARCHAR(255) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_password_reset_staff ON password_reset_tokens (staff_id);
CREATE INDEX IF NOT EXISTS idx_password_reset_token ON password_reset_tokens (token_hash);
