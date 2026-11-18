-- MARKYS PDV Online — tendered cash and change reconciliation.
BEGIN;
ALTER TABLE public.tenant_sales
  ADD COLUMN IF NOT EXISTS change_cents bigint NOT NULL DEFAULT 0 CHECK (change_cents >= 0);
ALTER TABLE public.tenant_payments
  ADD COLUMN IF NOT EXISTS received_cents bigint NOT NULL DEFAULT 0 CHECK (received_cents >= amount_cents);
UPDATE public.tenant_payments SET received_cents=amount_cents WHERE received_cents=0 AND amount_cents>0;
COMMIT;
