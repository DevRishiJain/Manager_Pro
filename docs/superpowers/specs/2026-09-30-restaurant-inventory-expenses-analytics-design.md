# Restaurant Expenses, Inventory, Recipe Costing & Consolidated Analytics Design

## 1. Executive Summary & Vision

TableOS is transitioning from a point-of-sale dining session logger into a complete **Restaurant Operating & Profit Engine**. 

Restaurant managers and owners frequently operate with revenue visibility but total opacity into profitability. By pairing real-time customer ordering with **Daily & Fixed Expense Tracking**, **Raw Material Inventory**, and **Dish Recipe Costing**, TableOS delivers the ultimate restaurant metric: **Real-Time Net Profit (P&L)**, **Food Cost %**, and **Prime Cost %**.

Simultaneously, the previous fragmented 7-subpage analytics suite (`/today`, `/month-to-date`, `/compare`, `/forecast`, `/peak-hours`, `/table-performance`, `/menu-performance`) is retired and consolidated into a **single, responsive Executive Analytics Dashboard** (`/restaurant/analytics`).

---

## 2. Architecture & Subsystems

```
                               ┌──────────────────────────────────────────────┐
                               │               TABLE_OS-UI-                   │
                               │                                              │
                               │  /restaurant/expenses  /restaurant/inventory │
                               │            /restaurant/analytics (Unified)   │
                               └──────────────────────┬───────────────────────┘
                                                      │ REST API
                                                      ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          Manager_Pro (Go Backend)                           │
│                                                                             │
│  ┌────────────────────┐  ┌────────────────────┐  ┌───────────────────────┐  │
│  │   Expense Service  │  │  Inventory Service │  │  Consolidated Service │  │
│  │  (Fixed & Variable)│  │  (Stock & Recipes) │  │ (P&L, Sales, Ops KPIs)│  │
│  └─────────┬──────────┘  └─────────┬──────────┘  └───────────┬───────────┘  │
│            │                       │                         │              │
│            └───────────────────────┼─────────────────────────┘              │
│                                    ▼                                        │
│                           PostgreSQL Database                               │
│  ┌──────────────────────┐┌──────────────────────┐┌────────────────────────┐ │
│  │ restaurant_expenses  ││ inventory_items      ││ recipe_ingredients     │ │
│  │ (salary, veg, rent)  ││ inventory_logs (S-in)││ (BOM / item costing)   │ │
│  └──────────────────────┘└──────────────────────┘└────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 3. Data Models & Database Schema

### 3.1. Subsystem 1: Expenses (`restaurant_expenses`)
Stores operational spending across daily variable supplies (vegetables, meat, gas) and recurring fixed overheads (rent, staff wages, utilities).

```sql
CREATE TABLE IF NOT EXISTS restaurant_expenses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    type VARCHAR(32) NOT NULL, -- 'VARIABLE' (Daily) or 'FIXED' (Monthly/Periodic)
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
```

### 3.2. Subsystem 2: Inventory & Stock Tracking (`inventory_items` & `inventory_logs`)
Maintains stock on hand for key raw materials and audit trails for additions, spoilage/wastage, and adjustments.

```sql
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

CREATE INDEX IF NOT EXISTS idx_inventory_tenant ON inventory_items(restaurant_id);
CREATE INDEX IF NOT EXISTS idx_inventory_logs ON inventory_logs(restaurant_id, inventory_item_id, logged_at);
```

### 3.3. Subsystem 3: Recipe Ingredients / Bill of Materials (`recipe_ingredients`)
Maps raw inventory ingredients to sellable menu items to dynamically calculate production cost and gross profit margin per dish.

```sql
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
```

---

## 4. API Interface Specification

### 4.1. Expense Endpoints
* `POST /api/v1/restaurant/expenses`: Log an expense (fixed or variable).
* `GET /api/v1/restaurant/expenses?start_date=...&end_date=...&type=...&category=...`: List expenses with date filtering.
* `DELETE /api/v1/restaurant/expenses/{id}`: Delete an erroneous expense entry.

### 4.2. Inventory Endpoints
* `GET /api/v1/restaurant/inventory`: List all raw materials with current stock, alerts, and valuation.
* `POST /api/v1/restaurant/inventory`: Create a raw material entry.
* `POST /api/v1/restaurant/inventory/{id}/stock`: Record a stock event (`STOCK_IN`, `WASTAGE`, `ADJUSTMENT`).
* `DELETE /api/v1/restaurant/inventory/{id}`: Remove raw material.

### 4.3. Recipe & Costing Endpoints
* `GET /api/v1/restaurant/recipes/{menu_item_id}`: Get ingredient breakdown for a menu dish.
* `POST /api/v1/restaurant/recipes/{menu_item_id}`: Assign ingredients and quantities to a menu item.
* `GET /api/v1/restaurant/recipes/margins`: Returns all dishes with their selling price, ingredient cost, gross profit, and margin %.

### 4.4. Unified Analytics & P&L Endpoint
* `GET /api/v1/restaurant/analytics/dashboard?start_date=...&end_date=...`
  Returns consolidated analytics:
  ```json
  {
    "date_range": { "start_date": "2026-09-01", "end_date": "2026-09-30" },
    "pnl": {
      "gross_revenue": { "amount_minor_units": 2487450, "currency": "INR" },
      "cogs_variable_expenses": { "amount_minor_units": 795000, "currency": "INR" },
      "fixed_expenses": { "amount_minor_units": 650000, "currency": "INR" },
      "total_expenses": { "amount_minor_units": 1445000, "currency": "INR" },
      "net_profit": { "amount_minor_units": 1042450, "currency": "INR" },
      "food_cost_percentage": 31.96,
      "net_margin_percentage": 41.91
    },
    "sales": {
      "order_count": 30,
      "average_order_value": { "amount_minor_units": 82915, "currency": "INR" },
      "payment_breakdown": { "CASH": 2487450, "UPI": 0, "CARD": 0 }
    },
    "top_dishes": [
      { "name": "Chicken Tikka", "quantity": 12, "revenue_minor": 456000, "estimated_margin_percent": 68.5 }
    ],
    "hourly_distribution": [
      { "hour": 18, "order_count": 14, "revenue_minor": 1205000 }
    ],
    "low_stock_alerts_count": 2
  }
  ```

---

## 5. Frontend Consolidation Plan (`TABLE_OS-UI-`)

1. **Retire Old Analytics Routes**:
   - Delete directories: `src/app/(tenant)/restaurant/analytics/{today, month-to-date, compare, forecast, peak-hours, table-performance, menu-performance}`.
   - Clean up sidebar navigation in `src/app/(tenant)/restaurant/layout.tsx`.
2. **Implement Unified Executive Dashboard (`src/app/(tenant)/restaurant/analytics/page.tsx`)**:
   - Top banner: Real-time P&L summary (Revenue, COGS Food Cost, Fixed Overhead, Net Take-Home Profit).
   - Middle section: Sales Trends & Payment Breakdown Donut.
   - Bottom section: Top Menu Performers & Rush Hours Heatmap.
3. **New Expense Management Page (`src/app/(tenant)/restaurant/expenses/page.tsx`)**:
   - Quick "Record Expense" modal with pre-set categories (Vegetables, Meat, Gas, Salaries, Rent).
   - Ledger table of recent entries with delete/filter controls.
4. **New Inventory & Stock Page (`src/app/(tenant)/restaurant/inventory/page.tsx`)**:
   - Itemized stock list with low-stock badges.
   - "Stock In / Wastage Log" action modal.
   - Recipe cost viewer per dish.

---

## 6. Implementation Rollout

* **Phase 1: Expenses & P&L Analytics**
  - Database migration for `restaurant_expenses`.
  - Backend Expense API and P&L aggregation.
  - Frontend Expense Ledger and Unified `/restaurant/analytics` Dashboard.
* **Phase 2: Inventory Stock Tracking**
  - Database migration for `inventory_items` and `inventory_logs`.
  - Backend Inventory API.
  - Frontend `/restaurant/inventory` dashboard with stock in/out and low-stock alerts.
* **Phase 3: Recipe Costing & Dish Margin Intelligence**
  - Database migration for `recipe_ingredients`.
  - Backend Recipe Costing engine.
  - Menu engineering dashboard showing gross profit % per dish.
