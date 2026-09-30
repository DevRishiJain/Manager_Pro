-- Migration 008: Expense Line Items & Inventory-Order Correlation

-- 1. Multi-Item Expense Invoices (Line items per wholesale/supplier bill)
CREATE TABLE IF NOT EXISTS expense_line_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    expense_id UUID NOT NULL REFERENCES restaurant_expenses(id) ON DELETE CASCADE,
    inventory_item_id UUID REFERENCES inventory_items(id) ON DELETE SET NULL,
    item_name VARCHAR(255) NOT NULL,
    quantity NUMERIC(12, 3) NOT NULL DEFAULT 1.000,
    unit VARCHAR(32) NOT NULL DEFAULT 'pcs',
    unit_price_minor BIGINT NOT NULL DEFAULT 0,
    total_price_minor BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_expense_items_expense_id ON expense_line_items(expense_id);
CREATE INDEX IF NOT EXISTS idx_expense_items_inventory_id ON expense_line_items(inventory_item_id);

-- 2. Alter restaurant_expenses to link with inventory
ALTER TABLE restaurant_expenses 
ADD COLUMN IF NOT EXISTS is_stock_purchase BOOLEAN NOT NULL DEFAULT FALSE,
ADD COLUMN IF NOT EXISTS inventory_log_id UUID REFERENCES inventory_logs(id) ON DELETE SET NULL;

-- 3. Alter inventory_logs to link with expenses and orders
ALTER TABLE inventory_logs 
ADD COLUMN IF NOT EXISTS expense_id UUID REFERENCES restaurant_expenses(id) ON DELETE SET NULL,
ADD COLUMN IF NOT EXISTS order_id UUID REFERENCES orders(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_inventory_logs_expense ON inventory_logs(expense_id);
CREATE INDEX IF NOT EXISTS idx_inventory_logs_order ON inventory_logs(order_id);
