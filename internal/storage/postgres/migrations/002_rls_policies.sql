-- 002_rls_policies.sql: Defense-in-depth Row-Level Security (RLS) policies for multi-tenancy

DO $$
DECLARE
    tbl text;
    tables text[] := ARRAY[
        'tables',
        'staff_users',
        'guard_users',
        'menu_categories',
        'menu_items',
        'dining_sessions',
        'orders',
        'payments',
        'refunds',
        'adjustments',
        'exit_passes',
        'platform_fee_ledger',
        'refund_adjustments',
        'restaurant_settlements',
        'restaurant_settings',
        'restaurant_onboarding',
        'audit_logs',
        'staff_actions',
        'outbox_events'
    ];
BEGIN
    FOREACH tbl IN ARRAY tables
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY;', tbl);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY;', tbl);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation_policy ON %I;', tbl);
        EXECUTE format(
            'CREATE POLICY tenant_isolation_policy ON %I
             FOR ALL
             USING (
                 CASE 
                     WHEN current_setting(''app.is_platform_admin'', true) = ''true'' THEN true
                     WHEN NULLIF(current_setting(''app.current_restaurant_id'', true), '''') IS NOT NULL THEN
                         restaurant_id = current_setting(''app.current_restaurant_id'', true)::uuid
                     ELSE false
                 END
             )
             WITH CHECK (
                 CASE 
                     WHEN current_setting(''app.is_platform_admin'', true) = ''true'' THEN true
                     WHEN NULLIF(current_setting(''app.current_restaurant_id'', true), '''') IS NOT NULL THEN
                         restaurant_id = current_setting(''app.current_restaurant_id'', true)::uuid
                     ELSE false
                 END
             );',
            tbl
        );
    END LOOP;
END;
$$;
