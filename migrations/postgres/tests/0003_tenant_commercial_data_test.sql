-- Verify business records are tenant-isolated under the non-owner runtime role.
-- Run after migrations 0001 and 0002 as the database owner.
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_products TO markys_app_test;

BEGIN;
DO $$
DECLARE
    tenant_a uuid := gen_random_uuid();
    tenant_b uuid := gen_random_uuid();
BEGIN
    INSERT INTO tenants(id, legal_name, display_name, document)
    VALUES (tenant_a, 'Commercial A', 'Commercial A', 'COMM-A-' || replace(tenant_a::text, '-', '')),
           (tenant_b, 'Commercial B', 'Commercial B', 'COMM-B-' || replace(tenant_b::text, '-', ''));
    PERFORM set_config('app.tenant_id', tenant_a::text, true);
    INSERT INTO tenant_products(tenant_id, code, name, price_cents)
    VALUES (tenant_a, 'SKU-A', 'Product A', 1000);
    PERFORM set_config('app.tenant_id', tenant_b::text, true);
    INSERT INTO tenant_products(tenant_id, code, name, price_cents)
    VALUES (tenant_b, 'SKU-B', 'Product B', 2000);
    PERFORM set_config('test.commercial_tenant_a', tenant_a::text, true);
    PERFORM set_config('test.commercial_tenant_b', tenant_b::text, true);
END
$$;

SET LOCAL ROLE markys_app_test;
SELECT set_config('app.tenant_id', current_setting('test.commercial_tenant_a'), true);

DO $$
DECLARE
    visible_count integer;
    visible_name text;
BEGIN
    SELECT count(*) INTO visible_count FROM tenant_products;
    IF visible_count <> 1 THEN
        RAISE EXCEPTION 'Tenant A must see exactly one product; visible rows: %', visible_count;
    END IF;
    SELECT name INTO visible_name FROM tenant_products WHERE code = 'SKU-A';
    IF visible_name <> 'Product A' THEN
        RAISE EXCEPTION 'Tenant A product was not visible';
    END IF;
    IF EXISTS (SELECT 1 FROM tenant_products WHERE code = 'SKU-B') THEN
        RAISE EXCEPTION 'Tenant A can read Tenant B product';
    END IF;

    BEGIN
        INSERT INTO tenant_products(tenant_id, code, name, price_cents)
        VALUES (current_setting('test.commercial_tenant_b')::uuid, 'CROSS-SKU', 'Cross Tenant', 3000);
        RAISE EXCEPTION 'Cross-tenant product insert unexpectedly succeeded';
    EXCEPTION WHEN insufficient_privilege THEN
        NULL;
    END;
END
$$;
ROLLBACK;
