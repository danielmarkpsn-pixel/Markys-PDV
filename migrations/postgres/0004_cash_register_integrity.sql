-- MARKYS PDV Online — cash terminal integrity and closing reconciliation.
BEGIN;

ALTER TABLE public.tenant_cash_sessions
    ADD COLUMN IF NOT EXISTS expected_cash_cents bigint,
    ADD COLUMN IF NOT EXISTS cash_difference_cents bigint;

CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_cash_sessions_one_open_per_terminal
    ON public.tenant_cash_sessions(tenant_id, terminal_code)
    WHERE status = 'open';

CREATE INDEX IF NOT EXISTS idx_tenant_sales_cash_session
    ON public.tenant_sales(tenant_id, cash_session_id, created_at DESC);

COMMIT;
