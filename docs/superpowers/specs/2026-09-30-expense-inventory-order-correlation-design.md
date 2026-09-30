# Automated Expense, Inventory, and Order Correlation Design Specification

## Overview & Goals
Eliminate duplicate data entry for restaurant operations by connecting Financial Accounting (Expenses), Kitchen Raw Materials (Inventory), and Point of Sale (Orders) in a closed loop:
1. **Purchases $\rightarrow$ Stock In**: Logging raw material purchases (single or multi-item invoices) instantly increments inventory on hand and recalculates unit purchase cost.
2. **Wastage $\rightarrow$ Spoilage Loss Expense**: Reporting spoiled or wasted ingredients calculates monetary loss and automatically books a `FOOD_WASTAGE` / `INVENTORY_WRITE_OFF` expense into the P&L ledger.
3. **Order Preparation $\rightarrow$ Recipe Ingredient Depletion**: When kitchen staff accepts or starts preparing an order, raw material ingredients are automatically deducted from stock based on the dish's Bill of Materials (BOM). If stock drops below zero, negative balance is permitted with an alert so kitchen cooking is never blocked.

---

## 1. Database Schema (`008_expense_inventory_order_correlation.sql`)

### 1.1 Multi-Item Invoices (`expense_line_items`)
```sql
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
```

### 1.2 Enhancements to `restaurant_expenses`
- `is_stock_purchase BOOLEAN NOT NULL DEFAULT FALSE`
- `inventory_log_id UUID REFERENCES inventory_logs(id) ON DELETE SET NULL`
- `category` supports `'FOOD_WASTAGE'`
- `paid_via` supports `'INVENTORY_WRITE_OFF'`

### 1.3 Enhancements to `inventory_logs`
- `expense_id UUID REFERENCES restaurant_expenses(id) ON DELETE SET NULL`
- `order_id UUID REFERENCES orders(id) ON DELETE SET NULL`
- `change_type` supports `'ORDER_CONSUMPTION'`

---

## 2. Domain & Application Service Architecture

### 2.1 Expense $\rightarrow$ Inventory Stock In
- When `ExpenseService.CreateExpense(ctx, req)` is invoked:
  - If `is_stock_purchase == true` and `line_items` are provided:
    - Insert expense and line items.
    - For each line item mapped to `inventory_item_id`:
      - Call `InventoryService.LogStockMovement`:
        - `change_type`: `STOCK_IN`
        - `quantity`: `+line_item.quantity`
        - `unit_cost_minor`: `line_item.unit_price_minor`
        - `total_cost_minor`: `line_item.total_price_minor`
        - `expense_id`: newly created expense ID
        - `reference`: `"Invoice: " + expense.title`
      - Update `inventory_items`:
        - `current_stock = current_stock + line_item.quantity`
        - `unit_cost_minor = line_item.unit_price_minor`

### 2.2 Inventory Spoilage $\rightarrow$ Spoilage Loss Expense
- When `InventoryService.LogStockMovement` is called with `change_type == WASTAGE`:
  - Compute total monetary loss: `total_loss_minor = quantity * item.unit_cost_minor`.
  - Insert `inventory_logs` row.
  - Automatically invoke `ExpenseService.CreateExpense`:
    - `type`: `VARIABLE`
    - `category`: `FOOD_WASTAGE`
    - `title`: `"Spoilage / Wastage: " + item.Name`
    - `amount_minor`: `total_loss_minor`
    - `paid_via`: `INVENTORY_WRITE_OFF`
    - `notes`: `"Auto-generated from inventory wastage log: " + reference`
    - `inventory_log_id`: newly created log ID

### 2.3 Order Preparation $\rightarrow$ Recipe Ingredient Depletion
- In `OrderService.UpdateOrderStatus(ctx, orderID, newStatus)`:
  - When transitioning to `ACCEPTED` or `PREPARING`:
    - Fetch all items in the order (`order_items`).
    - For each menu item, lookup `recipe_ingredients`.
    - For each required ingredient:
      - Compute total needed: `qty_deduct = recipe.quantity_required * order_item.quantity`.
      - Call `InventoryRepository.CreateInventoryLog`:
        - `change_type`: `ORDER_CONSUMPTION`
        - `quantity`: `-qty_deduct`
        - `order_id`: `orderID`
        - `reference`: `"Order #" + orderID.String()[:8]`
      - Update `inventory_items`:
        - `current_stock = current_stock - qty_deduct`

---

## 3. Frontend UX & "Minimum Input" Workflows

### 3.1 Multi-Item Invoice Entry in `/restaurant/expenses`
- **Single vs Multi-Item Toggle**:
  - Manager can enter a single expense OR click "Wholesale / Supplier Bill".
  - In Wholesale Bill mode:
    - Header: Vendor (e.g. "Azadpur Mandi Supplier"), Bill Date, Payment Method.
    - Items Grid: Type or pick Raw Material $\rightarrow$ Auto-fills unit and last purchase price. Enter quantity $\rightarrow$ line total and grand total calculate live.
    - One click saves the entire bill, records the expense, and increments all stock balances instantly.

### 3.2 Spoilage Logging in `/restaurant/inventory`
- When selecting "Wastage / Spoilage", the modal immediately shows:
  - Current unit cost: e.g. `₹65.00 / L`.
  - Live calculated loss: e.g. entering `2.5 L` shows `₹162.50 financial loss will be booked to P&L`.

### 3.3 Visual Stock Health & Negative Alerts
- Inventory dashboard displays negative stock warning badge if cooking depleted unrecorded morning shipments (`"Negative Stock (-2.5 kg) — Log pending delivery"`).
