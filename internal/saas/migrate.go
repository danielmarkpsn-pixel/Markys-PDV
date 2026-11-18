package saas

import (
	"context"
	"database/sql"
	"fmt"

	postgresmigrations "example.com/markys-pdv/migrations/postgres"
)

const migrationLockKey int64 = 73190420261118

// ApplyMigrations applies the embedded PostgreSQL migrations in order.
// Call only during an explicit maintenance/deployment step, never on every request.
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	if db == nil { return fmt.Errorf("banco PostgreSQL não informado") }
	conn, err := db.Conn(ctx)
	if err != nil { return fmt.Errorf("obter conexão exclusiva para migração: %w", err) }
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return fmt.Errorf("bloquear execução de migrações: %w", err)
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockKey)

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil { return fmt.Errorf("preparar controle de migrações: %w", err) }

	entries, err := postgresmigrations.FS.ReadDir(".")
	if err != nil { return fmt.Errorf("listar migrações embutidas: %w", err) }
	for _, entry := range entries {
		if entry.IsDir() || len(entry.Name()) < 5 || entry.Name()[0:4] != "0001" { continue }
		var exists bool
		err := conn.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)", entry.Name()).Scan(&exists)
		if err != nil { return fmt.Errorf("consultar migração %s: %w", entry.Name(), err) }
		if exists { continue }
		sqlBytes, err := postgresmigrations.FS.ReadFile(entry.Name())
		if err != nil { return fmt.Errorf("ler migração %s: %w", entry.Name(), err) }
		if _, err := conn.ExecContext(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("aplicar migração %s: %w", entry.Name(), err)
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES ($1) ON CONFLICT DO NOTHING", entry.Name()); err != nil {
			return fmt.Errorf("registrar migração %s: %w", entry.Name(), err)
		}
	}
	return nil
}
