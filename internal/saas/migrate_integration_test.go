package saas

import (
	"context"
 "errors"
 "fmt"
	"os"
	"testing"
	"time"
)

func TestApplyMigrationsAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" { t.Skip("DATABASE_URL não configurada; teste de integração ignorado") }
	cfg, err := LoadConfig(func(key string) string {
		if key == "SAAS_MODE" { return "postgres" }
		if key == "DATABASE_URL" { return dsn }
		if key == "DB_CONNECT_TIMEOUT" { return "10s" }
		return ""
	})
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := OpenPostgres(ctx, cfg)
	if err != nil { t.Fatal(err) }
	defer db.Close()

	if err := ApplyMigrations(ctx, db); err != nil { t.Fatalf("primeira aplicação: %v", err) }
	if err := ApplyMigrations(ctx, db); err != nil { t.Fatalf("segunda aplicação idempotente: %v", err) }
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version = '0001_saas_access_foundation.sql'").Scan(&count); err != nil { t.Fatal(err) }
	if count != 1 { t.Fatalf("esperava exatamente uma migração registrada; obtido %d", count) }
 var tenantID, userID string
 doc := fmt.Sprintf("SESSION-TEST-%d", time.Now().UnixNano())
 if err := db.QueryRowContext(ctx, "INSERT INTO tenants(legal_name,display_name,document) VALUES ('Sessões Teste','Sessões Teste',$1) RETURNING id::text", doc).Scan(&tenantID); err != nil { t.Fatal(err) }
 if _, err := db.ExecContext(ctx, "INSERT INTO tenant_subscriptions(tenant_id,status,seat_limit) VALUES ($1::uuid,'active',1)", tenantID); err != nil { t.Fatal(err) }
 if err := db.QueryRowContext(ctx, "INSERT INTO tenant_users(tenant_id,username,display_name,password_hash,role) VALUES ($1::uuid,'session-test','Session Test','test-hash','owner') RETURNING id::text", tenantID).Scan(&userID); err != nil { t.Fatal(err) }

 firstToken, _, err := CreateLoginSession(ctx, db, tenantID, userID, "test-agent-1", "127.0.0.1", time.Now().Add(time.Hour))
 if err != nil { t.Fatalf("criar primeira sessão: %v", err) }
 if _, err := ValidateLoginSession(ctx, db, firstToken); err != nil { t.Fatalf("validar primeira sessão: %v", err) }
 secondToken, _, err := CreateLoginSession(ctx, db, tenantID, userID, "test-agent-2", "127.0.0.1", time.Now().Add(time.Hour))
 if err != nil { t.Fatalf("substituir sessão no segundo dispositivo: %v", err) }
 if _, err := ValidateLoginSession(ctx, db, firstToken); !errors.Is(err, ErrInvalidSession) { t.Fatalf("sessão anterior deveria ser invalidada, erro: %v", err) }
 identity, err := ValidateLoginSession(ctx, db, secondToken)
 if err != nil { t.Fatalf("validar sessão substituta: %v", err) }
 if identity.TenantID != tenantID || identity.UserID != userID || identity.Role != "owner" { t.Fatalf("identidade inesperada: %+v", identity) }

}
