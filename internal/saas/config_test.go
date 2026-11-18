package saas

import (
	"strings"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string { return func(k string) string { return values[k] } }

func TestLoadConfigDefaultsToSQLiteWithoutChangingDesktop(t *testing.T) {
	cfg, err := LoadConfig(env(nil))
	if err != nil { t.Fatal(err) }
	if cfg.Mode != "sqlite" { t.Fatalf("mode = %q", cfg.Mode) }
}

func TestPostgresModeRequiresValidDatabaseURL(t *testing.T) {
	_, err := LoadConfig(env(map[string]string{"SAAS_MODE":"postgres"}))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") { t.Fatalf("expected DATABASE_URL error, got %v", err) }
	_, err = LoadConfig(env(map[string]string{"SAAS_MODE":"postgres", "DATABASE_URL":"https://example.com"}))
	if err == nil { t.Fatal("expected invalid PostgreSQL URL error") }
}

func TestLoadPostgresConfig(t *testing.T) {
	cfg, err := LoadConfig(env(map[string]string{
		"SAAS_MODE":"postgres", "DATABASE_URL":"postgres://user:pass@localhost:5432/pdv",
		"DB_MAX_OPEN_CONNS":"12", "DB_MAX_IDLE_CONNS":"4",
		"DB_CONNECT_TIMEOUT":"8s", "SAAS_MIGRATE":"true",
	}))
	if err != nil { t.Fatal(err) }
	if cfg.MaxOpenConns != 12 || cfg.MaxIdleConns != 4 || cfg.ConnectTimeout != 8*time.Second || !cfg.Migrate {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestRejectsInvalidPoolValues(t *testing.T) {
	_, err := LoadConfig(env(map[string]string{"DB_MAX_OPEN_CONNS":"0"}))
	if err == nil { t.Fatal("expected pool validation error") }
}
