-- Verify tenant row isolation with a non-superuser, non-BYPASSRLS role.
-- Run after 0001_saas_access_foundation.sql as the database owner.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'markys_app_test') THEN
        CREATE ROLE markys_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_users, tenant_sessions, tenant_audit_events TO markys_app_test;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO markys_app_test;

BEGIN;
DO $$
DECLARE
    tenant_a uuid := gen_random_uuid();
    tenant_b uuid := gen_random_uuid();
    user_a uuid;
    user_b uuid;
    visible_count integer;
BEGIN
    INSERT INTO tenants(id, legal_name, display_name, document)
    VALUES (tenant_a, 'RLS A', 'RLS A', 'RLS-A-' || replace(tenant_a::text, '-', '')),
           (tenant_b, 'RLS B', 'RLS B', 'RLS-B-' || replace(tenant_b::text, '-', ''));
    INSERT INTO tenant_subscriptions(tenant_id, status, seat_limit)
    VALUES (tenant_a, 'active', 2), (tenant_b, 'active', 2);
    INSERT INTO tenant_users(tenant_id, username, display_name, password_hash, role)
    VALUES (tenant_a, 'rls-a', 'RLS A', 'hash', 'cashier') RETURNING id INTO user_a;
    INSERT INTO tenant_users(tenant_id, username, display_name, password_hash, role)
    VALUES (tenant_b, 'rls-b', 'RLS B', 'hash', 'cashier') RETURNING id INTO user_b;

    EXECUTE 'SET LOCAL ROLE markys_app_test';
    PERFORM set_config('app.tenant_id', tenant_a::text, true);
    SELECT count(*) INTO visible_count FROM tenant_users;
    IF visible_count <> 1 THEN
        RAISE EXCEPTION 'Tenant A must see only its own user; visible rows: %', visible_count;
    END IF;

    BEGIN
        INSERT INTO tenant_users(tenant_id, username, display_name, password_hash, role)
        VALUES (tenant_b, 'cross-write', 'Cross write', 'hash', 'cashier');
        RAISE EXCEPTION 'Cross-tenant insert unexpectedly succeeded';
    EXCEPTION WHEN insufficient_privilege THEN
        NULL;
    END;
END
$$;
ROLLBACK;
