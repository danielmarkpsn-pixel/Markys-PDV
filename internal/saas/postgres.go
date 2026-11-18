package saas

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// OpenPostgres opens and verifies the SaaS PostgreSQL connection.
// It never falls back to SQLite: callers must explicitly select their runtime.
func OpenPostgres(ctx context.Context, cfg Config) (*sql.DB, error) {
	if cfg.Mode != "postgres" || cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("configuração PostgreSQL SaaS incompleta")
	}
	pgxCfg, err := pgx.ParseConfig(cfg.DatabaseURL)
	if err != nil { return nil, fmt.Errorf("interpretar DATABASE_URL: %w", err) }
	// Migrations contain multiple SQL statements and PL/pgSQL blocks.
	// Simple protocol is required for executing these scripts as a whole.
	pgxCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db := stdlib.OpenDB(*pgxCfg)
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("validar conexão PostgreSQL: %w", err)
	}
	return db, nil
}

// WithTenantTx binds the authenticated tenant to the transaction using a
// transaction-local setting consumed by PostgreSQL RLS policies.
// tenantID must come from the server-side authenticated session, never request input.
func WithTenantTx(ctx context.Context, db *sql.DB, tenantID string, opts *sql.TxOptions, fn func(*sql.Tx) error) error {
	if tenantID == "" { return fmt.Errorf("tenant autenticado não informado") }
	if db == nil || fn == nil { return fmt.Errorf("banco e função da transação são obrigatórios") }
	tx, err := db.BeginTx(ctx, opts)
	if err != nil { return fmt.Errorf("iniciar transação do tenant: %w", err) }
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		return fmt.Errorf("definir contexto RLS do tenant: %w", err)
	}
	if err := fn(tx); err != nil { return err }
	if err := tx.Commit(); err != nil { return fmt.Errorf("confirmar transação do tenant: %w", err) }
	return nil
}
