# Expense, Inventory, and Order Correlation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish an automated closed loop between restaurant expenses, kitchen raw material stock movements, wastage financial losses, and order recipe depletion with minimal user input.

**Architecture:** Extend PostgreSQL with migration `008_expense_inventory_order_correlation.sql` (`expense_line_items`, foreign keys). Enhance Go backend domain entities and application services (`ExpenseService`, `InventoryService`, `OrderService`) to automatically synchronize stock on purchase, post wastage loss to expenses, and deplete recipe ingredients upon order acceptance. On Next.js frontend, add multi-item wholesale invoice entry and live wastage loss calculation.

**Tech Stack:** Go (chi, pgxpool), PostgreSQL, Next.js 14, React, Redux Toolkit Query, Tailwind CSS.

**Spec:** [docs/superpowers/specs/2026-09-30-expense-inventory-order-correlation-design.md](file:///c:/Users/Admin/WorkDir/Manager_Pro/docs/superpowers/specs/2026-09-30-expense-inventory-order-correlation-design.md)

## Global Constraints
- Currency stored as `BIGINT` minor units (paise; 100 paise = ₹1.00).
- Quantities stored as `NUMERIC(12, 3)` (e.g. 10.500 kg).
- Negative inventory balances are permitted when orders cook before a delivery invoice is logged, with clear status indicators.
- Zero placeholder data or mock templates; live API binding.

---

### Task 1: Database Migration for Expense Line Items & Cross-Entity Keys

**Files:**
- Create: `internal/storage/postgres/migrations/008_expense_inventory_order_correlation.sql`
- Modify: `cmd/migrate/main.go`

- [x] **Step 1: Create migration 008**
  Define `expense_line_items` table, alter `restaurant_expenses` to add `is_stock_purchase` and `inventory_log_id`, alter `inventory_logs` to add `expense_id` and `order_id`.

- [x] **Step 2: Register in migrate runner**
  Add `008_expense_inventory_order_correlation.sql` to `cmd/migrate/main.go`.

- [x] **Step 3: Run migration**
  Execute `go run cmd/migrate/main.go` and verify tables and columns are created.

---

### Task 2: Backend Domain & Repository Layer Updates

**Files:**
- Modify: `internal/domain/expense/entity.go`
- Modify: `internal/domain/inventory/entity.go`
- Modify: `internal/storage/repository.go`
- Modify: `internal/storage/postgres/repository.go`
- Modify: `internal/storage/memory/memory.go`

- [x] **Step 1: Update Domain Entities**
  Add `ExpenseLineItem` struct in `expense/entity.go`.
  Add `LineItems []ExpenseLineItem` and `IsStockPurchase bool` to `Expense`.
  Add `ExpenseID *uuid.UUID` and `OrderID *uuid.UUID` to `InventoryLog`.

- [x] **Step 2: Update Repository Interfaces & Implementations**
  Add `CreateExpenseWithItems(ctx, expense, lineItems)` and `ListExpenseLineItems(ctx, expenseID)`.
  Update `CreateInventoryLog` to persist `expense_id` and `order_id`.
  Implement in `postgres/repository.go` and `memory/memory.go`.

- [x] **Step 3: Verify compilation**
  Run `go build ./...` and `go test ./internal/storage/...`.

---

### Task 3: Service Orchestration & Automated Workflows

**Files:**
- Modify: `internal/service/expense_service.go`
- Modify: `internal/service/inventory_service.go`
- Modify: `internal/service/order_service.go`
- Modify: `cmd/server/main.go`

- [x] **Step 1: Auto Stock-In on Expense Creation**
  In `ExpenseService.CreateExpense`: If `is_stock_purchase` is true, for each line item with an `inventory_item_id`, call `InventoryService.LogStockMovement` with `STOCK_IN` to automatically bump stock and unit cost.

- [x] **Step 2: Auto Wastage Expense on Spoilage Log**
  In `InventoryService.LogStockMovement`: When `change_type == WASTAGE`, compute loss amount and automatically call `ExpenseService.CreateExpense` with category `FOOD_WASTAGE` and payment method `INVENTORY_WRITE_OFF`.

- [x] **Step 3: Auto Recipe Depletion on Order Acceptance**
  In `OrderService.UpdateOrderStatus`: When moving to `ACCEPTED` or `PREPARING`, fetch recipe ingredients for all items in the order and call `CreateInventoryLog` with `ORDER_CONSUMPTION` (negative quantity).

- [x] **Step 4: Verify with unit tests**
  Run `go test ./internal/service/...`.

---

### Task 4: API Handlers & Routing

**Files:**
- Modify: `internal/api/handlers/expense_handlers.go`
- Modify: `internal/api/handlers/inventory_handlers.go`

- [x] **Step 1: Support Line Items in Expense Handler**
  Update `CreateExpense` handler request DTO to parse `is_stock_purchase` and `line_items`. Return created line items in response.

- [x] **Step 2: Verify Endpoints with Curl**
  Test creating an expense with stock item and verify stock increments. Test logging wastage and verify expense is created.

---

### Task 5: Frontend Multi-Item Wholesale Invoice & Wastage Loss UI

**Files:**
- Modify: `TABLE_OS-UI-/src/types/domain.ts`
- Modify: `TABLE_OS-UI-/src/types/api.ts`
- Modify: `TABLE_OS-UI-/src/store/api/restaurantApi.ts`
- Modify: `TABLE_OS-UI-/src/app/(tenant)/restaurant/expenses/page.tsx`
- Modify: `TABLE_OS-UI-/src/app/(tenant)/restaurant/inventory/page.tsx`

- [x] **Step 1: Update Frontend Types & RTK Query**
  Add `ExpenseLineItem` to types and update `CreateExpenseRequest`.

- [x] **Step 2: Multi-Item Invoice Modal in Expenses**
  Add a "Wholesale / Supplier Bill" tab in "Log Expense" modal with line items grid (Raw Material selector, auto-unit, unit price, quantity, line total).

- [x] **Step 3: Live Wastage Loss in Inventory Modal**
  In "Log Movement" modal, when Wastage is selected, show live calculated financial loss in ₹ based on unit cost.

- [x] **Step 4: Typecheck Frontend**
  Run `npx tsc --noEmit` in `TABLE_OS-UI-`.

---

### Task 6: End-to-End Verification

- [x] **Step 1: Purchase 10 kg Paneer via Expense**
  Log expense "Bought 10 kg Paneer for ₹3,500" $\rightarrow$ Verify Paneer stock increases by 10 kg and unit cost becomes ₹350/kg.

- [x] **Step 2: Log 2 kg Milk Wastage**
  Log 2 kg Milk wastage $\rightarrow$ Verify Milk stock decreases by 2 kg and a ₹130 `FOOD_WASTAGE` expense appears in the ledger.

- [x] **Step 3: Accept Order with Recipe**
  Accept an order containing Butter Chicken $\rightarrow$ Verify Chicken and Butter stocks are automatically depleted.
