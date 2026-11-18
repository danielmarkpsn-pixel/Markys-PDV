-- MARKYS PDV Online — PostgreSQL SaaS foundation
-- This migration defines the first production-oriented tenant/access model.
-- It is intentionally NOT wired to the current SQLite runtime yet.

BEGIN;

CREATE TABLE IF NOT EXISTS tenants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_name text NOT NULL,
    display_name text NOT NULL DEFAULT '',
    document text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'suspended', 'cancelled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS saas_plans (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tenant_subscriptions (
    tenant_id uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE RESTRICT,
    plan_id uuid REFERENCES saas_plans(id) ON DELETE RESTRICT,
    status text NOT NULL DEFAULT 'trial'
        CHECK (status IN ('trial', 'active', 'past_due', 'suspended', 'cancelled')),
    seat_limit integer NOT NULL CHECK (seat_limit >= 1),
    current_period_start timestamptz,
    current_period_end timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tenant_users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    username text NOT NULL,
    display_name text NOT NULL,
    email text NOT NULL DEFAULT '',
    password_hash text NOT NULL,
    role text NOT NULL DEFAULT 'cashier'
        CHECK (role IN ('owner', 'admin', 'manager', 'cashier', 'stockkeeper')),
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_users_username
    ON tenant_users (tenant_id, lower(username));

CREATE INDEX IF NOT EXISTS idx_tenant_users_active
    ON tenant_users (tenant_id, active);

-- One non-revoked session per user. Login must revoke the previous session
-- and insert the new one in the same database transaction.
CREATE TABLE IF NOT EXISTS tenant_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    user_id uuid NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    replaced_by_session_id uuid REFERENCES tenant_sessions(id) ON DELETE SET NULL,
    user_agent text NOT NULL DEFAULT '',
    ip_address inet,
    FOREIGN KEY (tenant_id, user_id)
        REFERENCES tenant_users (tenant_id, id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_sessions_one_active_per_user
    ON tenant_sessions (tenant_id, user_id)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_tenant_sessions_valid
    ON tenant_sessions (tenant_id, user_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS tenant_audit_events (
    id bigserial PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    actor_user_id uuid,
    event_type text NOT NULL,
    entity_type text NOT NULL DEFAULT '',
    entity_id text NOT NULL DEFAULT '',
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_tenant_audit_events_tenant_created
    ON tenant_audit_events (tenant_id, created_at DESC);

CREATE TABLE IF NOT EXISTS platform_admins (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username text NOT NULL UNIQUE,
    display_name text NOT NULL,
    password_hash text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Seat enforcement is performed in the database, not only in the UI.
-- Locking the subscription row serializes concurrent user creation attempts.
CREATE OR REPLACE FUNCTION enforce_tenant_user_seat_limit()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_seat_limit integer;
    v_subscription_status text;
    v_active_users bigint;
    v_becoming_active boolean;
BEGIN
    v_becoming_active := NEW.active AND
        (TG_OP = 'INSERT' OR OLD.active IS DISTINCT FROM NEW.active);

    IF NOT v_becoming_active THEN
        RETURN NEW;
    END IF;

    SELECT seat_limit, status
      INTO v_seat_limit, v_subscription_status
      FROM public.tenant_subscriptions
     WHERE tenant_id = NEW.tenant_id
     FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'A empresa não possui licença configurada'
            USING ERRCODE = '23514';
    END IF;

    IF v_subscription_status NOT IN ('trial', 'active') THEN
        RAISE EXCEPTION 'A assinatura não autoriza novos usuários'
            USING ERRCODE = '23514';
    END IF;

    SELECT count(*)
      INTO v_active_users
      FROM public.tenant_users
     WHERE tenant_id = NEW.tenant_id
       AND active = true
       AND (TG_OP = 'INSERT' OR id <> NEW.id);

    IF v_active_users >= v_seat_limit THEN
        RAISE EXCEPTION 'Limite de logins contratados atingido (% de %)',
            v_active_users, v_seat_limit
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_enforce_tenant_user_seat_limit ON tenant_users;
CREATE TRIGGER trg_enforce_tenant_user_seat_limit
BEFORE INSERT OR UPDATE OF active ON tenant_users
FOR EACH ROW
EXECUTE FUNCTION enforce_tenant_user_seat_limit();

-- The application must set this transaction-local value immediately after
-- beginning each tenant-scoped transaction:
-- SELECT set_config('app.tenant_id', '<authenticated-tenant-uuid>', true);
ALTER TABLE tenant_users ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_users FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_users_isolation ON tenant_users;
CREATE POLICY tenant_users_isolation ON tenant_users
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE tenant_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_sessions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_sessions_isolation ON tenant_sessions;
CREATE POLICY tenant_sessions_isolation ON tenant_sessions
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE tenant_audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_audit_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_audit_events_isolation ON tenant_audit_events;
CREATE POLICY tenant_audit_events_isolation ON tenant_audit_events
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

COMMIT;
