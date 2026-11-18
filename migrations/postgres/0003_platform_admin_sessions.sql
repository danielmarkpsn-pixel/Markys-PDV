-- MARKYS PDV Online — persistent platform administrator sessions.
BEGIN;
CREATE TABLE IF NOT EXISTS platform_admin_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id uuid NOT NULL REFERENCES platform_admins(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    user_agent text NOT NULL DEFAULT '',
    ip_address inet
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_platform_admin_one_active_session
    ON platform_admin_sessions(admin_id) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_platform_admin_sessions_valid
    ON platform_admin_sessions(admin_id, expires_at) WHERE revoked_at IS NULL;
COMMIT;
