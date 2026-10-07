-- 013: Franchise model, menu item variants, waiter session ownership, subscription OTPs

CREATE TABLE IF NOT EXISTS franchises (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    owner_staff_id UUID NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE restaurants ADD COLUMN IF NOT EXISTS franchise_id UUID NULL REFERENCES franchises(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_restaurants_franchise ON restaurants (franchise_id);

CREATE TABLE IF NOT EXISTS franchise_invite_codes (
    code VARCHAR(32) PRIMARY KEY,
    franchise_id UUID NOT NULL REFERENCES franchises(id),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ NULL,
    used_by_restaurant_id UUID NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS subscription_otps (
    id UUID PRIMARY KEY,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    otp_hash VARCHAR(128) NOT NULL,
    days INT NOT NULL,
    plan VARCHAR(50) NOT NULL DEFAULT 'PRO',
    status VARCHAR(20) NOT NULL DEFAULT 'ISSUED',
    attempts INT NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ NULL,
    created_by_staff_id UUID NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_subscription_otps_restaurant_status ON subscription_otps (restaurant_id, status);

CREATE TABLE IF NOT EXISTS menu_item_variants (
    id UUID PRIMARY KEY,
    menu_item_id UUID NOT NULL REFERENCES menu_items(id) ON DELETE CASCADE,
    name VARCHAR(64) NOT NULL,
    price_minor BIGINT NOT NULL,
    is_available BOOLEAN NOT NULL DEFAULT TRUE,
    display_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_menu_item_variants_item ON menu_item_variants (menu_item_id);

ALTER TABLE dining_sessions ADD COLUMN IF NOT EXISTS assigned_waiter_id UUID NULL;
ALTER TABLE dining_sessions ADD COLUMN IF NOT EXISTS assigned_waiter_name VARCHAR(255) NOT NULL DEFAULT '';
