-- MARKYS PDV Online — tendered cash and change reconciliation.
BEGIN;
ALTER TABLE public.tenant_sales
  ADD COLUMN IF NOT EXISTS change_cents bigint NOT NULL DEFAULT 0 CHECK (change_cents >= 0);
ALTER TABLE public.tenant_payments
  ADD COLUMN IF NOT EXISTS received_cents bigint;
UPDATE public.tenant_payments SET received_cents=amount_cents WHERE received_cents IS NULL;
ALTER TABLE public.tenant_payments
  ALTER COLUMN received_cents SET DEFAULT 0,
  ALTER COLUMN received_cents SET NOT NULL;
DO $body$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'tenant_payments_received_gte_applied'
      AND conrelid = 'public.tenant_payments'::regclass
  ) THEN
    ALTER TABLE public.tenant_payments
      ADD CONSTRAINT tenant_payments_received_gte_applied
      CHECK (received_cents >= amount_cents);
  END IF;
END
$body$;
COMMIT;
