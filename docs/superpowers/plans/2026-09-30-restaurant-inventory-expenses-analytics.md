# Restaurant Expenses, Inventory, Recipe Costing & Consolidated Analytics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a complete restaurant financial and inventory operating system including Expense Tracking (variable daily + fixed monthly), Raw Material Inventory with low-stock alerts, Dish Recipe Costing, and a Consolidated Executive Analytics & P&L Dashboard.

**Architecture:** Extend the Go backend with PostgreSQL tables (`restaurant_expenses`, `inventory_items`, `inventory_logs`, `recipe_ingredients`), corresponding domain entities, services, and REST handlers. On Next.js frontend, retire the 7 redundant analytics sub-pages and deliver a single unified `/restaurant/analytics` dashboard alongside dedicated `/restaurant/expenses` and `/restaurant/inventory` management interfaces.

**Tech Stack:** Go (chi router, pgxpool, standard library), PostgreSQL, Next.js 14, React, Redux Toolkit Query (RTK Query), Tailwind CSS, Lucide icons.

**Spec:** [docs/superpowers/specs/2026-09-30-restaurant-inventory-expenses-analytics-design.md](file:///c:/Users/Admin/WorkDir/Manager_Pro/docs/superpowers/specs/2026-09-30-restaurant-inventory-expenses-analytics-design.md)

## Global Constraints
- Currency stored as `BIGINT` in minor units (paise for INR; 100 paise = ₹1.00).
- Multi-tenant isolation enforced via `restaurant_id` foreign keys and indexed queries.
- Clean standard library Go code without unnecessary dependencies.
- Zero placeholder data or mock templates in UI; actual live API data binding.

---

### Task 1: Database Migration for Expenses, Inventory, and Recipes

**Files:**
- Create: `internal/storage/postgres/migrations/007_expenses_inventory_recipes.sql`
- Modify: `cmd/migrate/main.go`

- [x] **Step 1: Write migration SQL file**
Create `internal/storage/postgres/migrations/007_expenses_inventory_recipes.sql` defining:
  - `restaurant_expenses` table with indexes on `(restaurant_id, expense_date)` and `(restaurant_id, category)`.
  - `inventory_items` table with threshold and stock fields.
  - `inventory_logs` table for tracking stock movements and wastage.
  - `recipe_ingredients` table mapping `menu_items` to `inventory_items`.

- [x] **Step 2: Register migration in runner**
Add `"internal/storage/postgres/migrations/007_expenses_inventory_recipes.sql"` to `migrationFiles` in `cmd/migrate/main.go`.

- [x] **Step 3: Execute migration**
Run `go run cmd/migrate/main.go` and verify all 4 tables are created in PostgreSQL.

---

### Task 2: Backend Domain Entities and Repository Layer

**Files:**
- Create: `internal/domain/expense/entity.go`
- Create: `internal/domain/inventory/entity.go`
- Modify: `internal/storage/repository.go`
- Modify: `internal/storage/postgres/repository.go`
- Modify: `internal/storage/memory/memory.go`

- [x] **Step 1: Define Expense and Inventory Domain Entities**
Create `internal/domain/expense/entity.go` and `internal/domain/inventory/entity.go` with domain types:
  - `Expense`, `ExpenseType` (`VARIABLE`, `FIXED`), `ExpenseCategory`, `PaymentMethod`.
  - `InventoryItem`, `InventoryLog`, `ChangeType` (`STOCK_IN`, `WASTAGE`, `ADJUSTMENT`), `RecipeIngredient`.

- [x] **Step 2: Add Repository Interface Methods**
Update `internal/storage/repository.go`:
  - `CreateExpense`, `ListExpenses`, `DeleteExpense`.
  - `CreateInventoryItem`, `GetInventoryItemByID`, `ListInventoryItems`, `UpdateInventoryItem`, `DeleteInventoryItem`.
  - `CreateInventoryLog`, `ListInventoryLogs`.
  - `SaveRecipeIngredients`, `GetRecipeIngredientsByMenuItemID`, `ListDishMargins`.

- [x] **Step 3: Implement PostgreSQL and Memory Storage**
Implement the SQL queries in `internal/storage/postgres/repository.go` and in-memory mock equivalents in `internal/storage/memory/memory.go`.

- [x] **Step 4: Verify Compilation**
Run `go build ./...` to verify all interfaces and implementations compile cleanly.

---

### Task 3: Backend Services, Analytics P&L Aggregator, and REST API Handlers

**Files:**
- Create: `internal/service/expense_service.go`
- Create: `internal/service/inventory_service.go`
- Modify: `internal/service/analytics_service.go`
- Create: `internal/api/handlers/expense_handlers.go`
- Create: `internal/api/handlers/inventory_handlers.go`
- Modify: `internal/api/handlers/handlers.go`
- Modify: `internal/api/router.go`

- [x] **Step 1: Implement Expense and Inventory Services**
Create `internal/service/expense_service.go` and `internal/service/inventory_service.go` with business logic (stock balance adjustment on logs, dish margin computation).

- [x] **Step 2: Implement Executive P&L Aggregator in AnalyticsService**
Add `GetExecutiveAnalytics(ctx context.Context, restaurantID uuid.UUID, startDate, endDate *time.Time)` to `internal/service/analytics_service.go`:
  - Aggregates **Gross Revenue** from orders.
  - Aggregates **COGS (Variable Food Expenses)** and computes **Food Cost %** (`COGS / Revenue * 100`).
  - Aggregates **Fixed Operating Expenses** (Rent, Salaries, Utilities).
  - Computes **Net Profit** (`Revenue - Total Expenses`) and **Net Margin %**.
  - Fetches top dish sales, payment method breakdown, and low stock items count.

- [x] **Step 3: Implement Handlers and Router Endpoints**
Create `expense_handlers.go` and `inventory_handlers.go`, and wire into `internal/api/router.go`:
  - `/api/v1/restaurant/expenses` (GET, POST, DELETE)
  - `/api/v1/restaurant/inventory` (GET, POST, PUT, DELETE)
  - `/api/v1/restaurant/inventory/{id}/stock` (POST)
  - `/api/v1/restaurant/recipes/{menu_item_id}` (GET, POST)
  - `/api/v1/restaurant/recipes/margins` (GET)
  - `/api/v1/restaurant/analytics/dashboard` (GET)

- [x] **Step 4: Verify with Unit Tests & Curl**
Compile and test with `go test ./internal/...` and test running server.

---

### Task 4: Frontend Redux API Store Integration

**Files:**
- Modify: `TABLE_OS-UI-/src/store/api/restaurantApi.ts`
- Modify: `TABLE_OS-UI-/src/types/domain.ts`
- Modify: `TABLE_OS-UI-/src/types/api.ts`

- [x] **Step 1: Define Frontend Domain Types**
Add `Expense`, `InventoryItem`, `InventoryLog`, `RecipeIngredient`, `DishMargin`, `ExecutiveAnalytics` interfaces in `src/types/domain.ts` and `src/types/api.ts`.

- [x] **Step 2: Add RTK Query Endpoints**
In `src/store/api/restaurantApi.ts`:
  - `getExpenses`, `createExpense`, `deleteExpense` (provides/invalidates tag `Expense`).
  - `getInventory`, `createInventoryItem`, `logInventoryStock`, `deleteInventoryItem` (provides/invalidates tag `Inventory`).
  - `getRecipe`, `saveRecipe`, `getDishMargins` (provides/invalidates tag `Recipe`).
  - `getExecutiveDashboardAnalytics` (provides tag `Analytics`).

---

### Task 5: Consolidate Analytics Dashboard & Clean Up Deprecated Routes

**Files:**
- Delete: `TABLE_OS-UI-/src/app/(tenant)/restaurant/analytics/{today,month-to-date,compare,forecast,peak-hours,table-performance,menu-performance}`
- Create: `TABLE_OS-UI-/src/app/(tenant)/restaurant/analytics/page.tsx`
- Modify: `TABLE_OS-UI-/src/app/(tenant)/restaurant/layout.tsx`

- [x] **Step 1: Delete Deprecated Sub-routes**
Remove the 7 individual directories under `src/app/(tenant)/restaurant/analytics/`.

- [x] **Step 2: Build Unified Executive Analytics Page**
In `src/app/(tenant)/restaurant/analytics/page.tsx`:
  - Date Range Filter bar (Today, Yesterday, Last 7 Days, This Month, Custom).
  - **P&L Banner**: Gross Revenue, COGS (Food Cost % with target gauge), Operating Overhead, Net Profit, and Net Margin %.
  - **Sales Trends & Payment Modes**: Visual bar/donut breakdown of Cash vs. UPI vs. Cards.
  - **Dish Margin & Velocity**: Best sellers + estimated margin %.
  - **Operational Speed & Rush Hours**: Table turn duration and hourly distribution.

- [x] **Step 3: Update Sidebar Navigation**
In `src/app/(tenant)/restaurant/layout.tsx`, replace the 7 sub-links with single clean links:
  - `Analytics & P&L` -> `/restaurant/analytics`
  - `Expenses & Bills` -> `/restaurant/expenses`
  - `Stock & Inventory` -> `/restaurant/inventory`

---

### Task 6: Frontend Expense Management & Inventory Screens

**Files:**
- Create: `TABLE_OS-UI-/src/app/(tenant)/restaurant/expenses/page.tsx`
- Create: `TABLE_OS-UI-/src/app/(tenant)/restaurant/inventory/page.tsx`

- [x] **Step 1: Build Expense Management Screen**
Create `src/app/(tenant)/restaurant/expenses/page.tsx`:
  - Quick "Add Expense" Modal: Type (`VARIABLE` / `FIXED`), Category (`VEGETABLES`, `MEAT_POULTRY`, `DAIRY`, `GROCERY_SPICES`, `PACKAGING`, `GAS_UTILITY`, `SALARY`, `RENT`, etc.), Title, Amount (₹), Paid Via, Vendor, Notes.
  - Summary KPI cards: Today's Expenses, Month's Variable Supplies, Month's Fixed Overhead.
  - Expense Ledger Table with date filters and delete action.

- [x] **Step 2: Build Inventory & Stock Screen**
Create `src/app/(tenant)/restaurant/inventory/page.tsx`:
  - Stock table with low-stock badges, unit cost, and total valuation.
  - "New Raw Material" modal.
  - "Log Stock Movement" modal (`Stock In`, `Wastage / Spoilage`, `Manual Adjustment`).
  - Dish Recipe Margin tab: Inspect recipe ingredient cost vs. menu selling price.

---

### Task 7: End-to-End Verification & Demonstration Data

**Files:**
- Seed: Sample real-world expenses and inventory items to demonstrate live P&L.

- [x] **Step 1: Seed Demonstration Data**
Add sample records for "The Spice Route" (vegetables, chicken, cooking oil, staff salaries, rent).

- [x] **Step 2: Verify End-to-End**
Verify the `/restaurant/analytics` dashboard displays real revenue, real food cost %, real operating overhead, and computed net profit.
Verify `/restaurant/expenses` and `/restaurant/inventory` interact seamlessly.
