-- 004_staff_employee_id.sql: Add employee_id to staff_users and optimize pending order retrieval

-- 1. Add employee_id column to staff_users table
ALTER TABLE staff_users ADD COLUMN IF NOT EXISTS employee_id VARCHAR(64);

-- 2. Backfill existing staff_users with an employee_id if null
UPDATE staff_users 
SET employee_id = 'EMP-' || UPPER(SUBSTRING(role::text, 1, 3)) || '-' || UPPER(SUBSTRING(id::text, 1, 4))
WHERE employee_id IS NULL OR employee_id = '';

-- 3. Unique index for employee_id per restaurant
CREATE UNIQUE INDEX IF NOT EXISTS idx_staff_restaurant_employee_id 
ON staff_users (restaurant_id, employee_id) 
WHERE employee_id IS NOT NULL;

-- 4. Index for quick retrieval of pending orders needing waiter acceptance
CREATE INDEX IF NOT EXISTS idx_orders_restaurant_pending
ON orders (restaurant_id, status, placed_at ASC)
WHERE status IN ('PLACED_UNVERIFIED', 'PLACED_VERIFIED');
