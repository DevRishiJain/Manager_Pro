-- 003_schema_sync.sql: Sync SQL schema with Go entity fields
-- Addresses 3 schema gaps identified in production readiness audit.

-- 1. exit_passes: Add optimistic locking version column (matches Go ExitPass.Version)
ALTER TABLE exit_passes ADD COLUMN IF NOT EXISTS version INT NOT NULL DEFAULT 1;

-- 2. payments: Add S3 evidence metadata columns (matches Go Payment struct)
--    These store the private S3 object coordinates for payment proof photos.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS evidence_bucket VARCHAR(255);
ALTER TABLE payments ADD COLUMN IF NOT EXISTS evidence_object_key VARCHAR(512);
ALTER TABLE payments ADD COLUMN IF NOT EXISTS evidence_content_type VARCHAR(128);
ALTER TABLE payments ADD COLUMN IF NOT EXISTS evidence_size_bytes BIGINT;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS evidence_sha256 VARCHAR(128);
ALTER TABLE payments ADD COLUMN IF NOT EXISTS evidence_uploaded_at TIMESTAMPTZ;

-- 3. outbox_events: Add lease-based claim tracking and retry backoff columns
--    These support the transactional outbox worker's atomic lease, exponential backoff, and DLQ.
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS claimed_at TIMESTAMPTZ;
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS claim_lease_expires_at TIMESTAMPTZ;
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ;
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS last_error TEXT;

-- Index for efficient outbox polling: fetch unclaimed PENDING events or expired leases
CREATE INDEX IF NOT EXISTS idx_outbox_pending_claimable
ON outbox_events (status, next_retry_at)
WHERE status IN ('PENDING', 'CLAIMED', 'FAILED');
