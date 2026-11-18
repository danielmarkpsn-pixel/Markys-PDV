package saas

import (
	"context"
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
}
