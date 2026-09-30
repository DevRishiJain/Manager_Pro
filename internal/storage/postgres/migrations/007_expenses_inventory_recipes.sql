-- Migration 007: Restaurant Expenses, Inventory Tracking, and Recipe Costing

-- 1. Restaurant Expenses (Variable supplies + Fixed overhead)
CREATE TABLE IF NOT EXISTS restaurant_expenses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    type VARCHAR(32) NOT NULL, -- 'VARIABLE' or 'FIXED'
    category VARCHAR(64) NOT NULL, -- 'VEGETABLES', 'MEAT_POULTRY', 'DAIRY', 'GROCERY_SPICES', 'PACKAGING', 'GAS_UTILITY', 'SALARY', 'RENT', 'ELECTRICITY', 'MAINTENANCE', 'OTHER'
    title VARCHAR(255) NOT NULL,
    amount_minor BIGINT NOT NULL,
    currency VARCHAR(8) NOT NULL DEFAULT 'INR',
    paid_via VARCHAR(32) NOT NULL DEFAULT 'CASH', -- 'CASH', 'UPI', 'BANK_TRANSFER', 'CHEQUE', 'CREDIT'
    vendor_name VARCHAR(255) DEFAULT '',
    expense_date DATE NOT NULL,
    notes TEXT DEFAULT '',
    created_by_staff_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_expenses_tenant_date ON restaurant_expenses(restaurant_id, expense_date);
CREATE INDEX IF NOT EXISTS idx_expenses_category ON restaurant_expenses(restaurant_id, category);

-- 2. Inventory Items (Raw materials / stock on hand)
CREATE TABLE IF NOT EXISTS inventory_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    category VARCHAR(64) NOT NULL, -- 'Meat', 'Dairy', 'Vegetables', 'Pantry', 'Beverages', 'Packaging'
    unit VARCHAR(32) NOT NULL, -- 'kg', 'g', 'l', 'ml', 'pcs', 'packs'
    current_stock NUMERIC(12, 3) NOT NULL DEFAULT 0.000,
    min_threshold NUMERIC(12, 3) NOT NULL DEFAULT 0.000,
    unit_cost_minor BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_inventory_tenant ON inventory_items(restaurant_id);

-- 3. Inventory Stock Movement & Wastage Logs
CREATE TABLE IF NOT EXISTS inventory_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    inventory_item_id UUID NOT NULL REFERENCES inventory_items(id) ON DELETE CASCADE,
    change_type VARCHAR(32) NOT NULL, -- 'STOCK_IN', 'WASTAGE', 'ORDER_CONSUMPTION', 'ADJUSTMENT'
    quantity NUMERIC(12, 3) NOT NULL, -- positive for in, negative for out
    unit_cost_minor BIGINT NOT NULL DEFAULT 0,
    total_cost_minor BIGINT NOT NULL DEFAULT 0,
    reference VARCHAR(255) DEFAULT '', -- e.g. "Invoice #419", "Spoilage Walk-in Fridge"
    logged_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_inventory_logs ON inventory_logs(restaurant_id, inventory_item_id, logged_at);

-- 4. Recipe Ingredients (Bill of Materials per Menu Item)
CREATE TABLE IF NOT EXISTS recipe_ingredients (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    menu_item_id UUID NOT NULL REFERENCES menu_items(id) ON DELETE CASCADE,
    inventory_item_id UUID NOT NULL REFERENCES inventory_items(id) ON DELETE CASCADE,
    quantity_required NUMERIC(12, 3) NOT NULL, -- e.g. 0.250 (kg) per dish
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(menu_item_id, inventory_item_id)
);

CREATE INDEX IF NOT EXISTS idx_recipe_menu_item ON recipe_ingredients(menu_item_id);
