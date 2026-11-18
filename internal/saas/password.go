package saas

import (
 "context"
 "crypto/hmac"
 "crypto/rand"
 "crypto/sha256"
 "database/sql"
 "encoding/base64"
 "errors"
 "fmt"
 "strconv"
 "strings"
)

var ErrInvalidCredentials = errors.New("usuário ou senha inválidos")

type AuthenticatedUser struct {
 TenantID string
 UserID string
 Username string
 DisplayName string
 Role string
}

// HashPassword produces the PBKDF2-SHA256 format already used by the desktop
// edition, allowing password hashes to be migrated without plaintext passwords.
func HashPassword(password string) (string, error) {
 if len(password) < 10 || len(password) > 256 { return "", fmt.Errorf("a senha deve ter entre 10 e 256 caracteres") }
 salt := make([]byte, 16)
 if _, err := rand.Read(salt); err != nil { return "", err }
 const iterations = 210000
 derived := pbkdf2SHA256([]byte(password), salt, iterations, 32)
 return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(derived)), nil
}

func verifyPasswordHash(password, encoded string) bool {
 parts := strings.Split(encoded, "$")
 if len(parts) != 4 || parts[0] != "pbkdf2-sha256" { return false }
 iterations, err := strconv.Atoi(parts[1])
 if err != nil || iterations < 100000 || iterations > 1000000 { return false }
 salt, err := base64.RawStdEncoding.DecodeString(parts[2])
 if err != nil || len(salt) < 16 { return false }
 expected, err := base64.RawStdEncoding.DecodeString(parts[3])
 if err != nil || len(expected) != 32 { return false }
 actual := pbkdf2SHA256([]byte(password), salt, iterations, len(expected))
 return hmac.Equal(actual, expected)
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLen int) []byte {
 out := make([]byte, 0, keyLen)
 for block := uint32(1); len(out) < keyLen; block++ {
  mac := hmac.New(sha256.New, password)
  _, _ = mac.Write(salt)
  _, _ = mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
  u := mac.Sum(nil)
  t := append([]byte(nil), u...)
  for i := 1; i < iterations; i++ {
   mac = hmac.New(sha256.New, password)
   _, _ = mac.Write(u)
   u = mac.Sum(nil)
   for j := range t { t[j] ^= u[j] }
  }
  out = append(out, t...)
 }
 return out[:keyLen]
}

// AuthenticateUser validates credentials inside the authenticated tenant scope.
// Tenant ID must be resolved by trusted server-side company lookup, not request claims.
func AuthenticateUser(ctx context.Context, db *sql.DB, tenantID, username, password string) (AuthenticatedUser, error) {
 username = strings.TrimSpace(username)
 if tenantID == "" || username == "" || password == "" { return AuthenticatedUser{}, ErrInvalidCredentials }
 var user AuthenticatedUser
 var passwordHash string
 err := WithTenantTx(ctx, db, tenantID, nil, func(tx *sql.Tx) error {
  err := tx.QueryRowContext(ctx, "SELECT id::text, username, display_name, role, password_hash FROM tenant_users WHERE tenant_id = $1::uuid AND lower(username) = lower($2) AND active = true", tenantID, username).Scan(&user.UserID, &user.Username, &user.DisplayName, &user.Role, &passwordHash)
  if err != nil { return ErrInvalidCredentials }
  return nil
 })
 if err != nil || !verifyPasswordHash(password, passwordHash) { return AuthenticatedUser{}, ErrInvalidCredentials }
 user.TenantID = tenantID
 return user, nil
}
