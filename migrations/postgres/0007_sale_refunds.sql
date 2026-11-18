-- MARKYS PDV Online — auditable full-sale refunds and exact stock restoration.
BEGIN;
ALTER TABLE public.tenant_stock_movements ADD COLUMN IF NOT EXISTS sale_id uuid;
DO $body$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='tenant_stock_movements_sale_fk') THEN
  ALTER TABLE public.tenant_stock_movements ADD CONSTRAINT tenant_stock_movements_sale_fk
   FOREIGN KEY (tenant_id,sale_id) REFERENCES public.tenant_sales(tenant_id,id);
 END IF;
END
$body$;
CREATE INDEX IF NOT EXISTS idx_tenant_stock_movements_sale ON public.tenant_stock_movements(tenant_id,sale_id,created_at) WHERE sale_id IS NOT NULL;
CREATE TABLE IF NOT EXISTS public.tenant_sale_refunds (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid NOT NULL REFERENCES public.tenants(id) ON DELETE RESTRICT,
 sale_id uuid NOT NULL, refunded_by uuid NOT NULL, reason text NOT NULL CHECK(length(trim(reason)) BETWEEN 5 AND 500),
 total_cents bigint NOT NULL CHECK(total_cents>0), created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id), UNIQUE(tenant_id,sale_id),
 FOREIGN KEY(tenant_id,sale_id) REFERENCES public.tenant_sales(tenant_id,id),
 FOREIGN KEY(tenant_id,refunded_by) REFERENCES public.tenant_users(tenant_id,id)
);
CREATE TABLE IF NOT EXISTS public.tenant_sale_refund_payments (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid NOT NULL, refund_id uuid NOT NULL,
 original_payment_id uuid NOT NULL, method text NOT NULL CHECK(method IN('cash','pix','debit','credit','voucher','other')),
 amount_cents bigint NOT NULL CHECK(amount_cents>0), created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id), UNIQUE(tenant_id,original_payment_id),
 FOREIGN KEY(tenant_id,refund_id) REFERENCES public.tenant_sale_refunds(tenant_id,id),
 FOREIGN KEY(tenant_id,original_payment_id) REFERENCES public.tenant_payments(tenant_id,id)
);
CREATE INDEX IF NOT EXISTS idx_tenant_sale_refunds_sale ON public.tenant_sale_refunds(tenant_id,sale_id,created_at DESC);
ALTER TABLE public.tenant_sale_refunds ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.tenant_sale_refunds FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_scope ON public.tenant_sale_refunds;
CREATE POLICY tenant_scope ON public.tenant_sale_refunds USING(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
ALTER TABLE public.tenant_sale_refund_payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.tenant_sale_refund_payments FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_scope ON public.tenant_sale_refund_payments;
CREATE POLICY tenant_scope ON public.tenant_sale_refund_payments USING(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
COMMIT;
