-- MARKYS PDV Online — inventory integrity constraints and lookup indexes.
BEGIN;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'tenant_products_expiry_requires_lot'
          AND conrelid = 'public.tenant_products'::regclass
    ) THEN
        ALTER TABLE public.tenant_products
            ADD CONSTRAINT tenant_products_expiry_requires_lot
            CHECK (track_expiry = false OR track_lot = true);
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_tenant_stock_movements_recent
    ON public.tenant_stock_movements(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tenant_product_lots_product_expiry
    ON public.tenant_product_lots(tenant_id, product_id, expires_on);

COMMIT;
