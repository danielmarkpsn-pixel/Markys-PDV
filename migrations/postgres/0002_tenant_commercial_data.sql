-- MARKYS PDV Online — first tenant-scoped commercial data model.
-- All business records are owned by one tenant and protected by forced RLS.

BEGIN;

CREATE TABLE IF NOT EXISTS tenant_store_settings (
    tenant_id uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE RESTRICT,
    legal_name text NOT NULL DEFAULT '',
    trade_name text NOT NULL DEFAULT '',
    document text NOT NULL DEFAULT '',
    state_code char(2) NOT NULL DEFAULT '',
    tax_regime text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tenant_categories (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    name text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_categories_name ON tenant_categories(tenant_id, lower(name));

CREATE TABLE IF NOT EXISTS tenant_suppliers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    name text NOT NULL,
    document text NOT NULL DEFAULT '',
    phone text NOT NULL DEFAULT '',
    email text NOT NULL DEFAULT '',
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_tenant_suppliers_name ON tenant_suppliers(tenant_id, lower(name));

CREATE TABLE IF NOT EXISTS tenant_products (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    code text NOT NULL,
    name text NOT NULL,
    unit text NOT NULL DEFAULT 'UN',
    price_cents bigint NOT NULL DEFAULT 0 CHECK (price_cents >= 0),
    cost_cents bigint NOT NULL DEFAULT 0 CHECK (cost_cents >= 0),
    barcode text NOT NULL DEFAULT '',
    category_id uuid,
    supplier_id uuid,
    stock numeric(14,3) NOT NULL DEFAULT 0 CHECK (stock >= 0),
    ncm text NOT NULL DEFAULT '',
    cest text NOT NULL DEFAULT '',
    cfop text NOT NULL DEFAULT '',
    tax_origin text NOT NULL DEFAULT '',
    tax_profile text NOT NULL DEFAULT '',
    track_lot boolean NOT NULL DEFAULT false,
    track_expiry boolean NOT NULL DEFAULT false,
    weighed_item boolean NOT NULL DEFAULT false,
    active boolean NOT NULL DEFAULT true,
    image_url text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, code),
    FOREIGN KEY (tenant_id, category_id) REFERENCES tenant_categories(tenant_id, id),
    FOREIGN KEY (tenant_id, supplier_id) REFERENCES tenant_suppliers(tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_tenant_products_name ON tenant_products(tenant_id, lower(name));
CREATE INDEX IF NOT EXISTS idx_tenant_products_barcode ON tenant_products(tenant_id, barcode);
CREATE INDEX IF NOT EXISTS idx_tenant_products_category ON tenant_products(tenant_id, category_id);

CREATE TABLE IF NOT EXISTS tenant_product_lots (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    product_id uuid NOT NULL,
    lot_code text NOT NULL,
    expires_on date,
    quantity numeric(14,3) NOT NULL DEFAULT 0 CHECK (quantity >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, product_id, lot_code),
    FOREIGN KEY (tenant_id, product_id) REFERENCES tenant_products(tenant_id, id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_tenant_product_lots_expiry ON tenant_product_lots(tenant_id, expires_on);

CREATE TABLE IF NOT EXISTS tenant_customers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    name text NOT NULL,
    document text NOT NULL DEFAULT '',
    phone text NOT NULL DEFAULT '',
    email text NOT NULL DEFAULT '',
    address text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_customers_document
    ON tenant_customers(tenant_id, document) WHERE document <> '';
CREATE INDEX IF NOT EXISTS idx_tenant_customers_name ON tenant_customers(tenant_id, lower(name));

CREATE TABLE IF NOT EXISTS tenant_cash_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    terminal_code text NOT NULL,
    opened_by uuid,
    opened_at timestamptz NOT NULL DEFAULT now(),
    opening_amount_cents bigint NOT NULL DEFAULT 0 CHECK (opening_amount_cents >= 0),
    closed_by uuid,
    closed_at timestamptz,
    closing_amount_cents bigint,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, opened_by) REFERENCES tenant_users(tenant_id, id),
    FOREIGN KEY (tenant_id, closed_by) REFERENCES tenant_users(tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_tenant_cash_sessions_status ON tenant_cash_sessions(tenant_id, status, opened_at DESC);

CREATE TABLE IF NOT EXISTS tenant_sales (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    sale_number text NOT NULL,
    cash_session_id uuid,
    customer_id uuid,
    operator_id uuid,
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','completed','cancelled','refunded')),
    subtotal_cents bigint NOT NULL DEFAULT 0 CHECK (subtotal_cents >= 0),
    discount_cents bigint NOT NULL DEFAULT 0 CHECK (discount_cents >= 0),
    total_cents bigint NOT NULL DEFAULT 0 CHECK (total_cents >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, sale_number),
    FOREIGN KEY (tenant_id, cash_session_id) REFERENCES tenant_cash_sessions(tenant_id, id),
    FOREIGN KEY (tenant_id, customer_id) REFERENCES tenant_customers(tenant_id, id),
    FOREIGN KEY (tenant_id, operator_id) REFERENCES tenant_users(tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_tenant_sales_created ON tenant_sales(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tenant_sales_status ON tenant_sales(tenant_id, status);

CREATE TABLE IF NOT EXISTS tenant_sale_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    sale_id uuid NOT NULL,
    product_id uuid NOT NULL,
    description text NOT NULL,
    quantity numeric(14,3) NOT NULL CHECK (quantity > 0),
    unit_price_cents bigint NOT NULL CHECK (unit_price_cents >= 0),
    discount_cents bigint NOT NULL DEFAULT 0 CHECK (discount_cents >= 0),
    line_total_cents bigint NOT NULL CHECK (line_total_cents >= 0),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, sale_id) REFERENCES tenant_sales(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, product_id) REFERENCES tenant_products(tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_tenant_sale_items_sale ON tenant_sale_items(tenant_id, sale_id);

CREATE TABLE IF NOT EXISTS tenant_payments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    sale_id uuid NOT NULL,
    method text NOT NULL CHECK (method IN ('cash','pix','debit','credit','voucher','other')),
    amount_cents bigint NOT NULL CHECK (amount_cents >= 0),
    external_reference text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, sale_id) REFERENCES tenant_sales(tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_tenant_payments_sale ON tenant_payments(tenant_id, sale_id);

CREATE TABLE IF NOT EXISTS tenant_stock_movements (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    product_id uuid NOT NULL,
    movement_type text NOT NULL,
    quantity numeric(14,3) NOT NULL,
    reference_type text NOT NULL DEFAULT '',
    reference_id uuid,
    user_id uuid,
    note text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, product_id) REFERENCES tenant_products(tenant_id, id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES tenant_users(tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_tenant_stock_movements_product ON tenant_stock_movements(tenant_id, product_id, created_at DESC);

CREATE TABLE IF NOT EXISTS tenant_equipment (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    kind text NOT NULL CHECK (kind IN ('scale','barcode_scanner','card_terminal')),
    name text NOT NULL,
    model text NOT NULL DEFAULT '',
    connection_type text NOT NULL CHECK (connection_type IN ('usb','serial','network','manual')),
    connection_details text NOT NULL DEFAULT '',
    protocol text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','configured','inactive')),
    notes text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_tenant_equipment_kind ON tenant_equipment(tenant_id, kind, status);

CREATE TABLE IF NOT EXISTS tenant_fiscal_documents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    sale_id uuid,
    document_model text NOT NULL DEFAULT '',
    environment text NOT NULL DEFAULT 'test',
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','authorized','rejected','cancelled','contingency','error')),
    access_key text NOT NULL DEFAULT '',
    protocol text NOT NULL DEFAULT '',
    xml_path text NOT NULL DEFAULT '',
    response_code text NOT NULL DEFAULT '',
    response_message text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    authorized_at timestamptz,
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, sale_id) REFERENCES tenant_sales(tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_tenant_fiscal_documents_status ON tenant_fiscal_documents(tenant_id, status);

-- Every operational table is forced through the authenticated tenant context.
DO $$
DECLARE
    table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'tenant_store_settings','tenant_categories','tenant_suppliers','tenant_products',
        'tenant_product_lots','tenant_customers','tenant_cash_sessions','tenant_sales',
        'tenant_sale_items','tenant_payments','tenant_stock_movements','tenant_equipment',
        'tenant_fiscal_documents'
    ]
    LOOP
        EXECUTE format('ALTER TABLE public.%I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('ALTER TABLE public.%I FORCE ROW LEVEL SECURITY', table_name);
        EXECUTE format('DROP POLICY IF EXISTS tenant_scope ON public.%I', table_name);
        EXECUTE format(
            'CREATE POLICY tenant_scope ON public.%I USING (tenant_id = NULLIF(current_setting(''app.tenant_id'', true), '''')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting(''app.tenant_id'', true), '''')::uuid)',
            table_name
        );
    END LOOP;
END
$$;

COMMIT;
