-- Integration checks for 0001_saas_access_foundation.sql.
-- Run with psql as the non-superuser application/migration owner:
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/postgres/tests/0001_saas_access_foundation_test.sql
-- All test data is rolled back.

BEGIN;

DO $$
DECLARE
    v_tenant uuid := gen_random_uuid();
    v_tenant_b uuid := gen_random_uuid();
    v_user uuid;
    v_user_b uuid;
    v_session uuid;
    v_count integer;
BEGIN
    INSERT INTO tenants(id, legal_name, display_name, document)
    VALUES (v_tenant, 'Empresa Teste A', 'Teste A', 'TESTE-A-' || replace(v_tenant::text, '-', ''));

    INSERT INTO tenants(id, legal_name, display_name, document)
    VALUES (v_tenant_b, 'Empresa Teste B', 'Teste B', 'TESTE-B-' || replace(v_tenant_b::text, '-', ''));

    INSERT INTO tenant_subscriptions(tenant_id, status, seat_limit)
    VALUES (v_tenant, 'active', 5), (v_tenant_b, 'active', 2);

    -- The contracted five active accounts must be accepted.
    FOR i IN 1..5 LOOP
        INSERT INTO tenant_users(tenant_id, username, display_name, password_hash, role)
        VALUES (v_tenant, 'user' || i, 'User ' || i, 'test-hash', 'cashier');
    END LOOP;

    SELECT count(*) INTO v_count FROM tenant_users WHERE tenant_id = v_tenant AND active;
    IF v_count <> 5 THEN
        RAISE EXCEPTION 'Expected five active seats, got %', v_count;
    END IF;

    -- A sixth active account must fail at database level.
    BEGIN
        INSERT INTO tenant_users(tenant_id, username, display_name, password_hash, role)
        VALUES (v_tenant, 'user6', 'User 6', 'test-hash', 'cashier');
        RAISE EXCEPTION 'Sixth account unexpectedly passed seat limit';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;

    -- A deactivated user frees one seat, and can be reactivated when a seat is free.
    UPDATE tenant_users SET active = false
     WHERE tenant_id = v_tenant AND username = 'user5';

    INSERT INTO tenant_users(tenant_id, username, display_name, password_hash, role)
    VALUES (v_tenant, 'user6', 'User 6', 'test-hash', 'cashier');

    UPDATE tenant_users SET active = true
     WHERE tenant_id = v_tenant AND username = 'user5';
    -- Reactivation is now over capacity and must fail.
    RAISE EXCEPTION 'Reactivation unexpectedly passed seat limit';
EXCEPTION WHEN check_violation THEN
    -- Expected only for the reactivation assertion above.
    NULL;
END;
$$;

-- Session uniqueness: one unrevoked session per tenant/user.
DO $$
DECLARE
    v_tenant uuid := gen_random_uuid();
    v_user uuid;
BEGIN
    INSERT INTO tenants(id, legal_name, display_name, document)
    VALUES (v_tenant, 'Empresa Sessao', 'Sessao', 'SESSION-' || replace(v_tenant::text, '-', ''));
    INSERT INTO tenant_subscriptions(tenant_id, status, seat_limit)
    VALUES (v_tenant, 'active', 1);
    INSERT INTO tenant_users(tenant_id, username, display_name, password_hash, role)
    VALUES (v_tenant, 'only-user', 'Only User', 'test-hash', 'owner')
    RETURNING id INTO v_user;

    INSERT INTO tenant_sessions(tenant_id, user_id, token_hash, expires_at)
    VALUES (v_tenant, v_user, decode(repeat('a', 64), 'hex'), now() + interval '1 hour');

    BEGIN
        INSERT INTO tenant_sessions(tenant_id, user_id, token_hash, expires_at)
        VALUES (v_tenant, v_user, decode(repeat('b', 64), 'hex'), now() + interval '1 hour');
        RAISE EXCEPTION 'Second active session unexpectedly passed';
    EXCEPTION WHEN unique_violation THEN
        NULL;
    END;

    -- Revocation allows the same user to sign in on a replacement device.
    UPDATE tenant_sessions SET revoked_at = now()
     WHERE tenant_id = v_tenant AND user_id = v_user AND revoked_at IS NULL;

    INSERT INTO tenant_sessions(tenant_id, user_id, token_hash, expires_at)
    VALUES (v_tenant, v_user, decode(repeat('b', 64), 'hex'), now() + interval '1 hour');
END;
$$;

ROLLBACK;
