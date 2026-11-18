package saas

import (
 "context"
 "crypto/rand"
 "crypto/sha256"
 "database/sql"
 "encoding/base64"
 "errors"
 "fmt"
 "strings"
 "time"
)

var ErrInvalidSession = errors.New("sessão inválida, expirada ou substituída")

type SessionIdentity struct {
 TenantID string
 UserID string
 Role string
}

// CreateLoginSession revokes the previous active session and creates a new one.
// The raw token is returned once; only its SHA-256 hash is persisted.
func CreateLoginSession(ctx context.Context, db *sql.DB, tenantID, userID, userAgent, ipAddress string, expiresAt time.Time) (string, string, error) {
 if !expiresAt.After(time.Now()) { return "", "", fmt.Errorf("validade da sessão deve estar no futuro") }
 random := make([]byte, 32)
 if _, err := rand.Read(random); err != nil { return "", "", fmt.Errorf("gerar token de sessão: %w", err) }
 token := tenantID + "." + base64.RawURLEncoding.EncodeToString(random)
 hash := sha256.Sum256([]byte(token))
 var sessionID string
 err := WithTenantTx(ctx, db, tenantID, nil, func(tx *sql.Tx) error {
  var allowed bool
  err := tx.QueryRowContext(ctx, "SELECT (u.active AND t.status = 'active' AND s.status IN ('trial','active')) FROM tenant_users u JOIN tenants t ON t.id = u.tenant_id JOIN tenant_subscriptions s ON s.tenant_id = u.tenant_id WHERE u.tenant_id = $1::uuid AND u.id = $2::uuid", tenantID, userID).Scan(&allowed)
  if err != nil || !allowed { return ErrInvalidSession }
  if _, err := tx.ExecContext(ctx, "UPDATE tenant_sessions SET revoked_at = now() WHERE tenant_id = $1::uuid AND user_id = $2::uuid AND revoked_at IS NULL", tenantID, userID); err != nil { return fmt.Errorf("encerrar sessão anterior: %w", err) }
  var ip any
  if strings.TrimSpace(ipAddress) != "" { ip = ipAddress }
  return tx.QueryRowContext(ctx, "INSERT INTO tenant_sessions(tenant_id, user_id, token_hash, expires_at, user_agent, ip_address) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6) RETURNING id::text", tenantID, userID, hash[:], expiresAt, userAgent, ip).Scan(&sessionID)
 })
 if err != nil { return "", "", err }
 return token, sessionID, nil
}

// ValidateLoginSession resolves the tenant from the token prefix and verifies
// the stored token hash inside that tenant's RLS-scoped transaction.
func ValidateLoginSession(ctx context.Context, db *sql.DB, token string) (SessionIdentity, error) {
 parts := strings.SplitN(token, ".", 2)
 if len(parts) != 2 || len(parts[0]) != 36 || len(parts[1]) < 32 { return SessionIdentity{}, ErrInvalidSession }
 tenantID := parts[0]
 hash := sha256.Sum256([]byte(token))
 var identity SessionIdentity
 err := WithTenantTx(ctx, db, tenantID, nil, func(tx *sql.Tx) error {
  return tx.QueryRowContext(ctx, "SELECT s.tenant_id::text, s.user_id::text, u.role FROM tenant_sessions s JOIN tenant_users u ON u.id = s.user_id AND u.tenant_id = s.tenant_id JOIN tenants t ON t.id = s.tenant_id JOIN tenant_subscriptions sub ON sub.tenant_id = s.tenant_id WHERE s.tenant_id = $1::uuid AND s.token_hash = $2 AND s.revoked_at IS NULL AND s.expires_at > now() AND u.active = true AND t.status = 'active' AND sub.status IN ('trial','active')", tenantID, hash[:]).Scan(&identity.TenantID, &identity.UserID, &identity.Role)
 })
 if err != nil { return SessionIdentity{}, ErrInvalidSession }
 return identity, nil
}

// RevokeLoginSession invalidates the supplied token for the authenticated tenant.
func RevokeLoginSession(ctx context.Context, db *sql.DB, tenantID, token string) error {
 hash := sha256.Sum256([]byte(token))
 return WithTenantTx(ctx, db, tenantID, nil, func(tx *sql.Tx) error {
  _, err := tx.ExecContext(ctx, "UPDATE tenant_sessions SET revoked_at = now() WHERE tenant_id = $1::uuid AND token_hash = $2 AND revoked_at IS NULL", tenantID, hash[:])
  return err
 })
}
