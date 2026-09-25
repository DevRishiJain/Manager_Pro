-- 005_performance_indexes.sql: High-performance Zero-Join architecture & partial indexes for extreme scale

-- 1. Denormalize immutable display metadata on orders for 0-join sub-millisecond lookups
ALTER TABLE orders ADD COLUMN IF NOT EXISTS table_number VARCHAR(64) DEFAULT '';
ALTER TABLE orders ADD COLUMN IF NOT EXISTS items_summary JSONB DEFAULT '[]'::jsonb;

-- 2. Backfill table_number & items_summary on existing orders if empty
UPDATE orders o
SET table_number = COALESCE(t.table_number, 'Table')
FROM dining_sessions s
JOIN tables t ON s.table_id = t.id
WHERE o.session_id = s.id AND (o.table_number IS NULL OR o.table_number = '');

UPDATE orders o
SET items_summary = sub.summary
FROM (
	SELECT 
		order_id,
		jsonb_agg(
			jsonb_build_object(
				'id', id,
				'menu_item_id', menu_item_id,
				'item_name_snapshot', item_name_snapshot,
				'quantity', quantity,
				'unit_price_minor', unit_price_minor,
				'line_total_minor', line_total_minor,
				'special_instructions', COALESCE(special_instructions, '')
			) ORDER BY created_at ASC
		) AS summary
	FROM order_items
	GROUP BY order_id
) sub
WHERE o.id = sub.order_id AND (o.items_summary IS NULL OR o.items_summary = '[]'::jsonb);

-- 3. Partial index for Pending Orders queue (Waiters real-time queue - 0 Joins required)
CREATE INDEX IF NOT EXISTS idx_orders_waiter_pending_optimized
ON orders (restaurant_id, placed_at ASC)
WHERE status IN ('PLACED_UNVERIFIED', 'PLACED_VERIFIED');

-- 4. Partial index for Active Kitchen Queue (KDS preparation queue - 0 Joins required)
CREATE INDEX IF NOT EXISTS idx_orders_kds_active_optimized
ON orders (restaurant_id, sequence_number ASC)
WHERE status IN ('ACCEPTED', 'PREPARING');

-- 5. Composite index for fast session order history lookup
CREATE INDEX IF NOT EXISTS idx_orders_session_seq
ON orders (session_id, sequence_number ASC);

-- 6. Foreign key index on order_items for fast relational writes & accounting
CREATE INDEX IF NOT EXISTS idx_order_items_order_fk
ON order_items (order_id);

-- 7. Index for active table-session resolution
CREATE INDEX IF NOT EXISTS idx_sessions_table_active_opt
ON dining_sessions (table_id, status)
WHERE status NOT IN ('COMPLETED', 'WALKOUT', 'EXPIRED', 'FORCE_CLOSED');
