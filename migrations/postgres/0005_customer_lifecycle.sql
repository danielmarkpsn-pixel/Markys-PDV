-- MARKYS PDV Online — customer lifecycle and search support.
BEGIN;
ALTER TABLE tenant_customers
  ADD COLUMN IF NOT EXISTS active boolean NOT NULL DEFAULT true,
  ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS idx_tenant_customers_active_name
  ON tenant_customers(tenant_id, active, lower(name));
COMMIT;
