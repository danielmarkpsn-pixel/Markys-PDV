package saas

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestInventoryHTTPIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" { t.Skip("DATABASE_URL não configurada; teste de estoque PostgreSQL ignorado") }
	cfg, err := LoadConfig(func(key string) string {
		switch key { case "SAAS_MODE": return "postgres"; case "DATABASE_URL": return dsn; case "DB_CONNECT_TIMEOUT": return "10s" }
		return ""
	})
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db, err := OpenPostgres(ctx, cfg)
	if err != nil { t.Fatal(err) }
	defer db.Close()
	if err := ApplyMigrations(ctx, db); err != nil { t.Fatal(err) }

	document := fmt.Sprintf("INVENTORY-%d", time.Now().UnixNano())
	var tenantID, ownerID string
	if err := db.QueryRowContext(ctx, "INSERT INTO tenants(legal_name,display_name,document) VALUES('Inventory Test','Inventory Test',$1) RETURNING id::text", document).Scan(&tenantID); err != nil { t.Fatal(err) }
	if _, err := db.ExecContext(ctx, "INSERT INTO tenant_subscriptions(tenant_id,status,seat_limit) VALUES($1::uuid,'active',2)", tenantID); err != nil { t.Fatal(err) }
	hash, err := HashPassword("Inventory-Owner-2026!")
	if err != nil { t.Fatal(err) }
	if err := db.QueryRowContext(ctx, "INSERT INTO tenant_users(tenant_id,username,display_name,password_hash,role) VALUES($1::uuid,'owner','Owner',$2,'owner') RETURNING id::text", tenantID, hash).Scan(&ownerID); err != nil { t.Fatal(err) }
	token, _, err := CreateLoginSession(ctx, db, tenantID, ownerID, "inventory-test", "127.0.0.1", time.Now().Add(time.Hour))
	if err != nil { t.Fatal(err) }

	var standardID, lotID string
	if err := db.QueryRowContext(ctx, "INSERT INTO tenant_products(tenant_id,code,name,stock) VALUES($1::uuid,'SKU-STOCK','Produto Estoque',0) RETURNING id::text", tenantID).Scan(&standardID); err != nil { t.Fatal(err) }
	if err := db.QueryRowContext(ctx, "INSERT INTO tenant_products(tenant_id,code,name,stock,track_lot,track_expiry) VALUES($1::uuid,'SKU-LOT','Produto Lote',0,true,true) RETURNING id::text", tenantID).Scan(&lotID); err != nil { t.Fatal(err) }

	auth := NewHTTPHandler(db, true)
	mux := http.NewServeMux()
	auth.Register(mux)
	NewInventoryHandler(db, auth).Register(mux)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	entry := request(http.MethodPost, "/api/inventory/adjustments", fmt.Sprintf(`{"product_id":%q,"movement_type":"entry","quantity":10,"note":"Compra inicial"}`, standardID))
	if entry.Code != http.StatusCreated { t.Fatalf("entrada: status=%d body=%s", entry.Code, entry.Body.String()) }
	exit := request(http.MethodPost, "/api/inventory/adjustments", fmt.Sprintf(`{"product_id":%q,"movement_type":"exit","quantity":4,"note":"Saída teste"}`, standardID))
	if exit.Code != http.StatusCreated { t.Fatalf("saída: status=%d body=%s", exit.Code, exit.Body.String()) }
	overdraw := request(http.MethodPost, "/api/inventory/adjustments", fmt.Sprintf(`{"product_id":%q,"movement_type":"exit","quantity":7}`, standardID))
	if overdraw.Code != http.StatusConflict { t.Fatalf("saída acima do saldo: status=%d body=%s", overdraw.Code, overdraw.Body.String()) }
	count := request(http.MethodPost, "/api/inventory/adjustments", fmt.Sprintf(`{"product_id":%q,"movement_type":"count","quantity":3,"note":"Inventário físico"}`, standardID))
	if count.Code != http.StatusCreated { t.Fatalf("inventário: status=%d body=%s", count.Code, count.Body.String()) }

	var stock float64
	if err := db.QueryRowContext(ctx, "SELECT stock FROM tenant_products WHERE tenant_id=$1::uuid AND id=$2::uuid", tenantID, standardID).Scan(&stock); err != nil { t.Fatal(err) }
	if stock != 3 { t.Fatalf("saldo esperado 3, recebido %v", stock) }

	expiry := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	lotEntry := request(http.MethodPost, "/api/inventory/adjustments", fmt.Sprintf(`{"product_id":%q,"movement_type":"entry","quantity":5,"lot_code":"LOTE-001","expires_on":%q}`, lotID, expiry))
	if lotEntry.Code != http.StatusCreated { t.Fatalf("entrada por lote: status=%d body=%s", lotEntry.Code, lotEntry.Body.String()) }
	conflictingExpiry := time.Now().AddDate(2, 0, 0).Format("2006-01-02")
	conflictingEntry := request(http.MethodPost, "/api/inventory/adjustments", fmt.Sprintf(`{"product_id":%q,"movement_type":"entry","quantity":4,"lot_code":"LOTE-001","expires_on":%q}`, lotID, conflictingExpiry))
	if conflictingEntry.Code != http.StatusConflict { t.Fatalf("lote com validade divergente deveria ser recusado: status=%d body=%s", conflictingEntry.Code, conflictingEntry.Body.String()) }
	var stockAfterConflict float64
	if err := db.QueryRowContext(ctx, "SELECT stock FROM tenant_products WHERE tenant_id=$1::uuid AND id=$2::uuid", tenantID, lotID).Scan(&stockAfterConflict); err != nil { t.Fatal(err) }
	if stockAfterConflict != 5 { t.Fatalf("entrada conflitante não deveria alterar o estoque; esperado 5, recebido %v", stockAfterConflict) }
	lotExit := request(http.MethodPost, "/api/inventory/adjustments", fmt.Sprintf(`{"product_id":%q,"movement_type":"exit","quantity":2,"lot_code":"LOTE-001"}`, lotID))
	if lotExit.Code != http.StatusCreated { t.Fatalf("saída por lote: status=%d body=%s", lotExit.Code, lotExit.Body.String()) }
	var lotStock float64
	if err := db.QueryRowContext(ctx, "SELECT quantity FROM tenant_product_lots WHERE tenant_id=$1::uuid AND product_id=$2::uuid AND lot_code='LOTE-001'", tenantID, lotID).Scan(&lotStock); err != nil { t.Fatal(err) }
	if lotStock != 3 { t.Fatalf("saldo do lote esperado 3, recebido %v", lotStock) }

	history := request(http.MethodGet, "/api/inventory/movements?limit=20", "")
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), "LOTE-001") { t.Fatalf("histórico: status=%d body=%s", history.Code, history.Body.String()) }
}
