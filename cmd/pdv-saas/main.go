package main

import (
	"context"
	"errors"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"example.com/markys-pdv/internal/saas"
)

//go:embed web/*
var webAssets embed.FS

func main() {
	cfg, err := saas.LoadConfig(os.Getenv)
	if err != nil { log.Fatal(err) }
	if cfg.Mode != "postgres" { log.Fatal("O serviço SaaS exige SAAS_MODE=postgres; SQLite não é usado neste executável") }

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.Migrate {
		migrationURL:=strings.TrimSpace(os.Getenv("SAAS_MIGRATION_DATABASE_URL"))
		if migrationURL=="" { log.Fatal("SAAS_MIGRATE=true exige SAAS_MIGRATION_DATABASE_URL com credencial exclusiva de migração; DATABASE_URL deve permanecer restrita ao runtime") }
		migrationCfg:=cfg
		migrationCfg.DatabaseURL=migrationURL
		migrationDB,err:=saas.OpenPostgres(ctx,migrationCfg)
		if err!=nil { log.Fatal(err) }
		if err:=saas.ApplyMigrations(ctx,migrationDB);err!=nil { _=migrationDB.Close();log.Fatal(err) }
		if err:=migrationDB.Close();err!=nil { log.Fatal(err) }
	}

	db, err := saas.OpenPostgres(ctx, cfg)
	if err != nil { log.Fatal(err) }
	defer db.Close()
	if err:=saas.ValidateRuntimeRole(ctx,db);err!=nil { log.Fatal(err) }

	mux := http.NewServeMux()
	auth := saas.NewHTTPHandler(db, strings.EqualFold(os.Getenv("SAAS_COOKIE_SECURE"), "true"))
	auth.Register(mux)
	saas.NewTenantAdminHandler(db, auth).Register(mux)
	saas.NewPlatformAdminHandler(db, strings.EqualFold(os.Getenv("SAAS_COOKIE_SECURE"), "true"), os.Getenv("SAAS_PLATFORM_BOOTSTRAP_TOKEN")).Register(mux)
	saas.NewCustomerHandler(db, auth).Register(mux)
	saas.NewCatalogHandler(db, auth).Register(mux)
	saas.NewInventoryHandler(db, auth).Register(mux)
	saas.NewPOSHandler(db, auth).Register(mux)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet { http.Error(w, "Método não permitido", http.StatusMethodNotAllowed); return }
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"status":"ok","service":"markys-pdv-saas-auth","database":"postgres"}`))
	})
	staticFiles, err := fs.Sub(webAssets, "web")
	if err != nil { log.Fatal(err) }
	mux.Handle("/", http.FileServer(http.FS(staticFiles)))

	addr := strings.TrimSpace(os.Getenv("SAAS_ADDR"))
	if addr == "" { addr = ":8080" }
	server := &http.Server{Addr: addr, Handler: securityHeaders(saas.NewLoginRateLimiter(30, 5*time.Minute).Middleware(mux)), ReadHeaderTimeout: 5*time.Second, IdleTimeout: 60*time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("MARKYS PDV SaaS auth API listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) { log.Fatal(err) }
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
