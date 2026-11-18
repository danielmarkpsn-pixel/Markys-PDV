package saas

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains the isolated PostgreSQL SaaS runtime settings.
// It deliberately does not configure or replace the desktop SQLite runtime.
type Config struct {
	Mode           string
	DatabaseURL    string
	MaxOpenConns   int
	MaxIdleConns   int
	ConnMaxLifetime time.Duration
	ConnectTimeout time.Duration
	Migrate        bool
}

func LoadConfig(getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	mode := strings.ToLower(strings.TrimSpace(getenv("SAAS_MODE")))
	if mode == "" {
		mode = "sqlite"
	}
	if mode != "sqlite" && mode != "postgres" {
		return Config{}, fmt.Errorf("SAAS_MODE inválido: %q (use sqlite ou postgres)", mode)
	}

	cfg := Config{
		Mode: mode, MaxOpenConns: 10, MaxIdleConns: 5,
		ConnMaxLifetime: 30 * time.Minute, ConnectTimeout: 5 * time.Second,
	}
	if mode == "postgres" {
		cfg.DatabaseURL = strings.TrimSpace(getenv("DATABASE_URL"))
		if cfg.DatabaseURL == "" {
			return Config{}, fmt.Errorf("DATABASE_URL é obrigatório quando SAAS_MODE=postgres")
		}
		u, err := url.Parse(cfg.DatabaseURL)
		if err != nil || u.Scheme != "postgres" && u.Scheme != "postgresql" || u.Host == "" {
			return Config{}, fmt.Errorf("DATABASE_URL deve ser uma URL PostgreSQL válida")
		}
	}
	var err error
	if cfg.MaxOpenConns, err = intEnv(getenv, "DB_MAX_OPEN_CONNS", cfg.MaxOpenConns, 1, 200); err != nil { return Config{}, err }
	if cfg.MaxIdleConns, err = intEnv(getenv, "DB_MAX_IDLE_CONNS", cfg.MaxIdleConns, 0, cfg.MaxOpenConns); err != nil { return Config{}, err }
	if cfg.ConnectTimeout, err = durationEnv(getenv, "DB_CONNECT_TIMEOUT", cfg.ConnectTimeout); err != nil { return Config{}, err }
	if cfg.ConnMaxLifetime, err = durationEnv(getenv, "DB_CONN_MAX_LIFETIME", cfg.ConnMaxLifetime); err != nil { return Config{}, err }
	cfg.Migrate = strings.EqualFold(strings.TrimSpace(getenv("SAAS_MIGRATE")), "true")
	return cfg, nil
}

func intEnv(getenv func(string) string, key string, fallback, min, max int) (int, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" { return fallback, nil }
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max { return 0, fmt.Errorf("%s deve ser um inteiro entre %d e %d", key, min, max) }
	return n, nil
}

func durationEnv(getenv func(string) string, key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" { return fallback, nil }
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 { return 0, fmt.Errorf("%s deve ser uma duração positiva (ex.: 5s ou 30m)", key) }
	return d, nil
}
