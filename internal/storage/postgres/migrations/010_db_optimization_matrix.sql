-- 010_db_optimization_matrix.sql: High-performance composite indexes for daily metrics, orders, and inventory
CREATE INDEX IF NOT EXISTS idx_dining_sessions_rest_opened ON dining_sessions (restaurant_id, opened_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_rest_placed ON orders (restaurant_id, placed_at DESC);
CREATE INDEX IF NOT EXISTS idx_inventory_items_rest ON inventory_items (restaurant_id, current_stock ASC);
