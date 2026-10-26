package main

import (
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"time"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestInitialAdminSetupAndLogin(t *testing.T) {
 app:=newCancellationTestApp(t)
 setup:=httptest.NewRecorder()
 app.authSetup(setup,httptest.NewRequest(http.MethodPost,"/api/auth/setup",strings.NewReader(`{"username":"admin","display_name":"Administrador","password":"senha-forte-123"}`)))
 if setup.Code!=http.StatusCreated {t.Fatalf("setup status %d: %s",setup.Code,setup.Body.String())}
 if setup.Header().Get("Set-Cookie")=="" {t.Fatal("setup should create a session cookie")}
 var stored string
 if err:=app.db.QueryRow("SELECT password_hash FROM users WHERE username='admin'").Scan(&stored);err!=nil{t.Fatal(err)}
 if stored=="senha-forte-123"||!strings.HasPrefix(stored,"pbkdf2-sha256$"){t.Fatal("password must be stored as a salted PBKDF2 hash")}
 again:=httptest.NewRecorder()
 app.authSetup(again,httptest.NewRequest(http.MethodPost,"/api/auth/setup",strings.NewReader(`{"username":"other","display_name":"Other","password":"senha-forte-123"}`)))
 if again.Code!=http.StatusConflict {t.Fatalf("second setup should be blocked, got %d",again.Code)}
 login:=httptest.NewRecorder()
 app.authLogin(login,httptest.NewRequest(http.MethodPost,"/api/auth/login",strings.NewReader(`{"username":"admin","password":"senha-forte-123"}`)))
 if login.Code!=http.StatusOK {t.Fatalf("login status %d: %s",login.Code,login.Body.String())}
 bad:=httptest.NewRecorder()
 app.authLogin(bad,httptest.NewRequest(http.MethodPost,"/api/auth/login",strings.NewReader(`{"username":"admin","password":"wrong-password"}`)))
 if bad.Code!=http.StatusUnauthorized {t.Fatalf("wrong password status %d",bad.Code)}
}

func TestAuthMiddlewareProtectsAPIAndChecksRoles(t *testing.T) {
 app:=newCancellationTestApp(t)
 mux:=http.NewServeMux()
 mux.HandleFunc("/api/products",func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusOK)})
 mux.HandleFunc("/api/sales",func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusOK)})
 mux.HandleFunc("/api/cash",func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusOK)})
 protected:=app.authMiddleware(mux)
 denied:=httptest.NewRecorder()
 protected.ServeHTTP(denied,httptest.NewRequest(http.MethodGet,"/api/products",nil))
 if denied.Code!=http.StatusUnauthorized {t.Fatalf("unauthenticated API status %d",denied.Code)}
 if _,err:=app.db.Exec("INSERT INTO users(username,password_hash,display_name,role,active) VALUES('cashier','unused','Cashier','cashier',1)");err!=nil{t.Fatal(err)}
 raw:=make([]byte,32);for i:=range raw{raw[i]=byte(i+1)}
 token:=base64.RawURLEncoding.EncodeToString(raw)
 var id int64;if err:=app.db.QueryRow("SELECT id FROM users WHERE username='cashier'").Scan(&id);err!=nil{t.Fatal(err)}
 sessionStore.Lock();sessionStore.items[token]=sessionInfo{userID:id,expires:time.Now().Add(time.Hour)};sessionStore.Unlock()
 req:=httptest.NewRequest(http.MethodGet,"/api/products",nil);req.AddCookie(&http.Cookie{Name:"markys_session",Value:token})
 allowed:=httptest.NewRecorder();protected.ServeHTTP(allowed,req)
 if allowed.Code!=http.StatusOK {t.Fatalf("cashier should be allowed to read products, got %d",allowed.Code)}
 deniedWrite:=httptest.NewRecorder();writeReq:=httptest.NewRequest(http.MethodPost,"/api/products",strings.NewReader("{}"));writeReq.AddCookie(&http.Cookie{Name:"markys_session",Value:token});protected.ServeHTTP(deniedWrite,writeReq)
 if deniedWrite.Code!=http.StatusForbidden {t.Fatalf("cashier product write should be denied, got %d",deniedWrite.Code)}
 for _,tc:=range []struct{method,path string;want int}{{http.MethodGet,"/api/sales",http.StatusForbidden},{http.MethodDelete,"/api/sales",http.StatusForbidden},{http.MethodPost,"/api/sales",http.StatusOK},{http.MethodGet,"/api/cash",http.StatusOK},{http.MethodPost,"/api/cash",http.StatusOK},{http.MethodGet,"/api/stock-movements",http.StatusForbidden}} {
  req:=httptest.NewRequest(tc.method,tc.path,nil);req.AddCookie(&http.Cookie{Name:"markys_session",Value:token});rec:=httptest.NewRecorder();protected.ServeHTTP(rec,req)
  if rec.Code!=tc.want {t.Errorf("cashier %s %s: expected %d, got %d",tc.method,tc.path,tc.want,rec.Code)}
 }
 sessionStore.Lock();delete(sessionStore.items,token);sessionStore.Unlock()
}

func TestAdminAuditAndConsistentDatabaseBackup(t *testing.T) {
 dbPath:=filepath.Join(t.TempDir(),"markys-pdv.db")
 db,err:=sql.Open("sqlite",dbPath);if err!=nil{t.Fatal(err)};db.SetMaxOpenConns(1)
 schema,err:=assets.ReadFile("web/schema.sql");if err!=nil{t.Fatal(err)}
 if _,err=db.Exec(string(schema));err!=nil{t.Fatal(err)}
 if _,err=db.Exec("INSERT INTO users(username,password_hash,display_name,role,active) VALUES('admin','unused','Administrador','admin',1)");err!=nil{t.Fatal(err)}
 if _,err=db.Exec("INSERT INTO audit_log(user_id,action,entity_type,entity_id,details) VALUES(1,'test_event','system','1','Evento de teste')");err!=nil{t.Fatal(err)}
 app:=&App{db:db,dbPath:dbPath};t.Cleanup(func(){db.Close()})
 token:=base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
 sessionStore.Lock();sessionStore.items[token]=sessionInfo{userID:1,expires:time.Now().Add(time.Hour)};sessionStore.Unlock()
 t.Cleanup(func(){sessionStore.Lock();delete(sessionStore.items,token);sessionStore.Unlock()})
 auditReq:=httptest.NewRequest(http.MethodGet,"/api/admin/audit",nil);auditReq.AddCookie(&http.Cookie{Name:"markys_session",Value:token})
 auditRec:=httptest.NewRecorder();app.auditEntries(auditRec,auditReq)
 if auditRec.Code!=http.StatusOK||!strings.Contains(auditRec.Body.String(),"test_event"){t.Fatalf("audit status %d: %s",auditRec.Code,auditRec.Body.String())}
 backupReq:=httptest.NewRequest(http.MethodGet,"/api/admin/backup",nil);backupReq.AddCookie(&http.Cookie{Name:"markys_session",Value:token})
 backupRec:=httptest.NewRecorder();app.databaseBackup(backupRec,backupReq)
 if backupRec.Code!=http.StatusOK{t.Fatalf("backup status %d: %s",backupRec.Code,backupRec.Body.String())}
 if !strings.HasPrefix(backupRec.Body.String(),"SQLite format 3"){t.Fatalf("download is not a SQLite database: %q",backupRec.Body.String()[:min(30,backupRec.Body.Len())])}
}

func TestHeaders(t *testing.T) {
	h := headers(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing security header")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestCreateSaleRejectsEmptyCart(t *testing.T) {
	app := &App{}
	req := httptest.NewRequest(http.MethodPost, "/api/sales", strings.NewReader(`{"items":[],"payments":[{"method":"cash","amount_cents":100}]}`))
	rec := httptest.NewRecorder()

	app.createSale(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestCreateSaleRejectsInvalidPaymentMethod(t *testing.T) {
	app := &App{}
	req := httptest.NewRequest(http.MethodPost, "/api/sales", strings.NewReader(`{"items":[{"code":"SKU-1","quantity":1}],"payments":[{"method":"crypto","amount_cents":100}]}`))
	rec := httptest.NewRecorder()

	app.createSale(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}


func newCancellationTestApp(t *testing.T) *App {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil { t.Fatal(err) }
	db.SetMaxOpenConns(1)
	schema, err := assets.ReadFile("web/schema.sql")
	if err != nil { t.Fatal(err) }
	if _, err := db.Exec(string(schema)); err != nil { t.Fatal(err) }
	if _, err := db.Exec("INSERT INTO products(code,name,unit,price_cents,stock,active) VALUES('SKU-1','Produto teste','UN',1000,4,1)"); err != nil { t.Fatal(err) }
	if _, err := db.Exec("INSERT INTO cash_sessions(terminal_code,opening_amount_cents,status) VALUES('PDV-01',0,'open')"); err != nil { t.Fatal(err) }
	if _, err := db.Exec("INSERT INTO sales(sale_number,cash_session_id,status,subtotal_cents,total_cents) VALUES('MK-TEST',1,'completed',2000,2000)"); err != nil { t.Fatal(err) }
	if _, err := db.Exec("INSERT INTO sale_items(sale_id,product_id,description,quantity,unit_price_cents,line_total_cents) VALUES(1,1,'Produto teste',2,1000,2000)"); err != nil { t.Fatal(err) }
	t.Cleanup(func(){ db.Close() })
	return &App{db:db}
}

func TestCancelSaleRestoresStockAndRecordsStatus(t *testing.T) {
	app := newCancellationTestApp(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/sales?id=1", nil)
	rec := httptest.NewRecorder()
	app.sales(rec, req)
	if rec.Code != http.StatusOK { t.Fatalf("expected %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String()) }
	var stock float64
	var status string
	if err := app.db.QueryRow("SELECT stock FROM products WHERE id=1").Scan(&stock); err != nil { t.Fatal(err) }
	if err := app.db.QueryRow("SELECT status FROM sales WHERE id=1").Scan(&status); err != nil { t.Fatal(err) }
	if stock != 6 { t.Fatalf("expected stock 6 after reversal, got %v", stock) }
	if status != "cancelled" { t.Fatalf("expected cancelled status, got %q", status) }
	var movements int
	if err := app.db.QueryRow("SELECT COUNT(*) FROM stock_movements WHERE movement_type='sale_cancel' AND reference_id=1").Scan(&movements); err != nil { t.Fatal(err) }
	if movements != 1 { t.Fatalf("expected one reversal movement, got %d", movements) }
}

func TestCancelSaleCannotReverseStockTwice(t *testing.T) {
	app := newCancellationTestApp(t)
	for i:=0; i<2; i++ {
		req := httptest.NewRequest(http.MethodDelete, "/api/sales?id=1", nil)
		rec := httptest.NewRecorder()
		app.sales(rec, req)
		if i==0 && rec.Code != http.StatusOK { t.Fatalf("first cancel: expected %d, got %d", http.StatusOK, rec.Code) }
		if i==1 && rec.Code != http.StatusConflict { t.Fatalf("second cancel: expected %d, got %d", http.StatusConflict, rec.Code) }
	}
	var stock float64
	if err := app.db.QueryRow("SELECT stock FROM products WHERE id=1").Scan(&stock); err != nil { t.Fatal(err) }
	if stock != 6 { t.Fatalf("stock should be reversed only once; got %v", stock) }
}


func TestProductRegistrationStoresCostBarcodeAndCategory(t *testing.T) {
	app := newCancellationTestApp(t)
	catReq := httptest.NewRequest(http.MethodPost, "/api/categories", strings.NewReader(`{"name":"Bebidas"}`))
	catRec := httptest.NewRecorder()
	app.catalog("categories")(catRec, catReq)
	if catRec.Code != http.StatusCreated { t.Fatalf("category status %d: %s", catRec.Code, catRec.Body.String()) }
	productJSON := `{"code":"SKU-2","name":"Suco","unit":"UN","price_cents":1500,"cost_cents":800,"barcode":"7891234567890","category_id":1,"supplier_id":0,"stock":10}`
	productReq := httptest.NewRequest(http.MethodPost, "/api/products", strings.NewReader(productJSON))
	productRec := httptest.NewRecorder()
	app.products(productRec, productReq)
	if productRec.Code != http.StatusCreated { t.Fatalf("product status %d: %s", productRec.Code, productRec.Body.String()) }
	var code, barcode string
	var cost, price int64
	var categoryID int64
	if err := app.db.QueryRow("SELECT code,barcode,cost_cents,price_cents,category_id FROM products WHERE code='SKU-2'").Scan(&code,&barcode,&cost,&price,&categoryID); err != nil { t.Fatal(err) }
	if code != "SKU-2" || barcode != "7891234567890" || cost != 800 || price != 1500 || categoryID != 1 {
		t.Fatalf("unexpected saved product: code=%q barcode=%q cost=%d price=%d category=%d",code,barcode,cost,price,categoryID)
	}
}

func TestSaleCanFindProductByBarcode(t *testing.T) {
	app := newCancellationTestApp(t)
	if _, err := app.db.Exec("INSERT INTO products(code,name,unit,price_cents,cost_cents,barcode,stock,active) VALUES('SKU-2','Suco','UN',1500,800,'7891234567890',10,1)"); err != nil { t.Fatal(err) }
	body := `{"items":[{"code":"7891234567890","quantity":1}],"payments":[{"method":"cash","amount_cents":1500}],"discount_cents":0}`
	req := httptest.NewRequest(http.MethodPost, "/api/sales", strings.NewReader(body))
	rec := httptest.NewRecorder()
	app.sales(rec, req)
	if rec.Code != http.StatusCreated { t.Fatalf("sale status %d: %s", rec.Code, rec.Body.String()) }
	var stock float64
	if err := app.db.QueryRow("SELECT stock FROM products WHERE code='SKU-2'").Scan(&stock); err != nil { t.Fatal(err) }
	if stock != 9 { t.Fatalf("expected stock 9 after barcode sale, got %v", stock) }
}


func TestManualStockEntryUpdatesStockAndAudit(t *testing.T) {
 app:=newCancellationTestApp(t)
 req:=httptest.NewRequest(http.MethodPost,"/api/stock-movements",strings.NewReader(`{"product_id":1,"type":"entry","quantity":3,"note":"Compra NF 123"}`))
 rec:=httptest.NewRecorder();app.stockMovements(rec,req)
 if rec.Code!=http.StatusCreated {t.Fatalf("status %d: %s",rec.Code,rec.Body.String())}
 var stock float64;var movement string;var qty float64;var audit int
 if err:=app.db.QueryRow("SELECT stock FROM products WHERE id=1").Scan(&stock);err!=nil{t.Fatal(err)}
 if err:=app.db.QueryRow("SELECT movement_type,quantity FROM stock_movements WHERE reference_type='manual' ORDER BY id DESC LIMIT 1").Scan(&movement,&qty);err!=nil{t.Fatal(err)}
 if err:=app.db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='stock_entry'").Scan(&audit);err!=nil{t.Fatal(err)}
 if stock!=7||movement!="entry"||qty!=3||audit!=1 {t.Fatalf("stock=%v movement=%s qty=%v audit=%d",stock,movement,qty,audit)}
}

func TestManualStockExitCannotMakeStockNegative(t *testing.T) {
 app:=newCancellationTestApp(t)
 req:=httptest.NewRequest(http.MethodPost,"/api/stock-movements",strings.NewReader(`{"product_id":1,"type":"exit","quantity":99,"note":"Perda"}`))
 rec:=httptest.NewRecorder();app.stockMovements(rec,req)
 if rec.Code!=http.StatusConflict {t.Fatalf("expected conflict, got %d: %s",rec.Code,rec.Body.String())}
 var stock float64;if err:=app.db.QueryRow("SELECT stock FROM products WHERE id=1").Scan(&stock);err!=nil{t.Fatal(err)}
 if stock!=4 {t.Fatalf("stock should remain 4, got %v",stock)}
}

func TestStockReportCSV(t *testing.T) {
 app:=newCancellationTestApp(t)
 rec:=httptest.NewRecorder();app.stockReportCSV(rec,httptest.NewRequest(http.MethodGet,"/api/reports/stock.csv",nil))
 if rec.Code!=http.StatusOK {t.Fatalf("status %d",rec.Code)}
 if !strings.Contains(rec.Body.String(),"SKU-1")||!strings.Contains(rec.Body.String(),"Produto teste"){t.Fatalf("report missing product: %s",rec.Body.String())}
}

func TestTenantSessionReadsOnlyItsCompanyDatabase(t *testing.T) {
 dir:=t.TempDir()
 masterPath:=filepath.Join(dir,"master.db")
 tenantPath:=filepath.Join(dir,"tenant.db")
 master,err:=sql.Open("sqlite",masterPath);if err!=nil{t.Fatal(err)};master.SetMaxOpenConns(1)
 tenant,err:=sql.Open("sqlite",tenantPath);if err!=nil{t.Fatal(err)};tenant.SetMaxOpenConns(1)
 schema,err:=assets.ReadFile("web/schema.sql");if err!=nil{t.Fatal(err)}
 if _,err=master.Exec(string(schema));err!=nil{t.Fatal(err)}
 if err=initCompanies(master);err!=nil{t.Fatal(err)}
 if _,err=tenant.Exec(string(schema));err!=nil{t.Fatal(err)}
 if _,err=tenant.Exec("INSERT INTO users(username,password_hash,display_name,role,active) VALUES('tenantadmin','unused','Administrador da filial','admin',1)");err!=nil{t.Fatal(err)}
 if _,err=tenant.Exec("INSERT INTO products(code,name,unit,price_cents,stock,active) VALUES('TENANT-SKU','Produto da filial','UN',200,5,1)");err!=nil{t.Fatal(err)}
 if _,err=master.Exec("INSERT INTO products(code,name,unit,price_cents,stock,active) VALUES('MASTER-SKU','Produto principal','UN',100,2,1)");err!=nil{t.Fatal(err)}
 res,err:=master.Exec("INSERT INTO companies(legal_name,trade_name,document,status,database_mode,database_path,theme_color) VALUES('Filial Teste','Filial','12345678901','active','create',?,'#123456')",tenantPath);if err!=nil{t.Fatal(err)}
 companyID,err:=res.LastInsertId();if err!=nil{t.Fatal(err)}
 app:=&App{db:master,dbPath:masterPath,masterDB:master}
 token:=base64.RawURLEncoding.EncodeToString([]byte("tenant-session-token-for-test-1234"))
 sessionStore.Lock();sessionStore.items[token]=sessionInfo{userID:1,companyID:companyID,expires:time.Now().Add(time.Hour)};sessionStore.Unlock()
 t.Cleanup(func(){sessionStore.Lock();delete(sessionStore.items,token);sessionStore.Unlock();tenantDBCache.Lock();cached:=tenantDBCache.items[tenantPath];delete(tenantDBCache.items,tenantPath);tenantDBCache.Unlock();if cached!=nil{_ = cached.Close()};tenant.Close();master.Close()})
 mux:=http.NewServeMux();mux.Handle("/",http.NotFoundHandler())
 req:=httptest.NewRequest(http.MethodGet,"/api/products",nil);req.AddCookie(&http.Cookie{Name:"markys_session",Value:token})
 rec:=httptest.NewRecorder();app.rootHandler(mux).ServeHTTP(rec,req)
 if rec.Code!=http.StatusOK{t.Fatalf("tenant product request status %d: %s",rec.Code,rec.Body.String())}
 if !strings.Contains(rec.Body.String(),"TENANT-SKU")||strings.Contains(rec.Body.String(),"MASTER-SKU"){t.Fatalf("tenant received wrong database contents: %s",rec.Body.String())}
}


func TestParseStoneCatalogJSONLD(t *testing.T) {
 html := []byte(`<!doctype html><html><head><script type="application/ld+json">{"@context":"https://schema.org","@type":"Product","name":"Café 500 g","image":"https://cdn.example.test/cafe.jpg","gtin13":"7891234567890","category":"Mercearia","offers":{"@type":"Offer","price":"19.90","priceCurrency":"BRL"}}</script></head></html>`)
 items := parseStoneCatalog(html)
 if len(items) != 1 { t.Fatalf("expected one item, got %d", len(items)) }
 got := items[0]
 if got.Name != "Café 500 g" || got.Price != 19.90 || got.Barcode != "7891234567890" || got.Category != "Mercearia" || got.ImageURL != "https://cdn.example.test/cafe.jpg" {
  t.Fatalf("unexpected parsed item: %#v", got)
 }
}

func TestValidateStoneCatalogURL(t *testing.T) {
 if _, err := validateStoneCatalogURL("https://catalogo.stone.com.br/abc"); err != nil { t.Fatalf("valid Stone URL rejected: %v", err) }
 for _, raw := range []string{"http://catalogo.stone.com.br/abc", "https://example.com/catalogo", "https://catalogo.stone.com.br.example.com/abc", "file:///etc/passwd"} {
  if _, err := validateStoneCatalogURL(raw); err == nil { t.Fatalf("unsafe URL accepted: %s", raw) }
 }
}


func TestParseXMLCatalogTemplate(t *testing.T) {
 raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
 <produtos><produto><codigo>ABC-01</codigo><nome>Café torrado</nome><unidade>PC</unidade><preco_venda>19.90</preco_venda><preco_custo>12,50</preco_custo><codigo_barras>7891234567890</codigo_barras><categoria>Mercearia</categoria><ncm>09012100</ncm></produto></produtos>`)
 items, err := parseXMLCatalog(raw)
 if err != nil { t.Fatal(err) }
 if len(items)!=1 { t.Fatalf("expected 1 item, got %d",len(items)) }
 p:=items[0]
 if p.Code!="ABC-01" || p.Name!="Café torrado" || p.Unit!="PC" || p.Price!=19.90 || p.Cost!=12.50 || p.Barcode!="7891234567890" || p.Category!="Mercearia" || p.NCM!="09012100" {
  t.Fatalf("unexpected XML item: %#v",p)
 }
}

func TestParseXMLCatalogNFeProduct(t *testing.T) {
 raw:=[]byte(`<nfeProc><NFe><infNFe><det><prod><cProd>100</cProd><xProd>Refrigerante</xProd><qCom>2</qCom><vProd>15.00</vProd><uCom>UN</uCom><cEAN>7891234567890</cEAN></prod></det></infNFe></NFe></nfeProc>`)
 items,err:=parseXMLCatalog(raw)
 if err!=nil { t.Fatal(err) }
 if len(items)!=1 || items[0].Name!="Refrigerante" || items[0].Price!=7.5 || items[0].Code!="100" { t.Fatalf("unexpected NFe parse: %#v",items) }
}


func TestAdminCanChangeUserRoleAndCannotDemoteSelf(t *testing.T) {
	app:=newCancellationTestApp(t)
	if _,err:=app.db.Exec("INSERT INTO users(username,password_hash,display_name,role,active) VALUES('admin','unused','Admin','admin',1),('operator','unused','Operador','cashier',1)");err!=nil { t.Fatal(err) }
	token:=base64.RawURLEncoding.EncodeToString([]byte("role-change-session-token-123456789"))
	sessionStore.Lock();sessionStore.items[token]=sessionInfo{userID:1,companyID:0,expires:time.Now().Add(time.Hour)};sessionStore.Unlock()
	t.Cleanup(func(){sessionStore.Lock();delete(sessionStore.items,token);sessionStore.Unlock()})
	req:=httptest.NewRequest(http.MethodPatch,"/api/users/role?id=2",strings.NewReader(`{"role":"stockkeeper"}`))
	req.AddCookie(&http.Cookie{Name:"markys_session",Value:token})
	rec:=httptest.NewRecorder();app.userRole(rec,req)
	if rec.Code!=http.StatusOK { t.Fatalf("role change status %d: %s",rec.Code,rec.Body.String()) }
	var role string
	if err:=app.db.QueryRow("SELECT role FROM users WHERE id=2").Scan(&role);err!=nil { t.Fatal(err) }
	if role!="stockkeeper" { t.Fatalf("expected stockkeeper role, got %q",role) }
	selfReq:=httptest.NewRequest(http.MethodPatch,"/api/users/role?id=1",strings.NewReader(`{"role":"cashier"}`))
	selfReq.AddCookie(&http.Cookie{Name:"markys_session",Value:token})
	selfRec:=httptest.NewRecorder();app.userRole(selfRec,selfReq)
	if selfRec.Code!=http.StatusConflict { t.Fatalf("self demotion should be rejected, got %d",selfRec.Code) }
	var admins int
	if err:=app.db.QueryRow("SELECT COUNT(*) FROM users WHERE role='admin' AND active=1").Scan(&admins);err!=nil { t.Fatal(err) }
	if admins!=1 { t.Fatalf("expected one active admin after rejected self-demotion, got %d",admins) }
}

func TestAdminPasswordResetHashesPasswordAndRevokesOtherSessions(t *testing.T) {
	app:=newCancellationTestApp(t)
	if _,err:=app.db.Exec("INSERT INTO users(username,password_hash,display_name,role,active) VALUES('admin','unused','Admin','admin',1),('operator','old-hash','Operador','cashier',1)");err!=nil { t.Fatal(err) }
	token:=base64.RawURLEncoding.EncodeToString([]byte("password-reset-current-session-1234"))
	other:=base64.RawURLEncoding.EncodeToString([]byte("password-reset-other-session-12345"))
	sessionStore.Lock()
	sessionStore.items[token]=sessionInfo{userID:1,companyID:0,expires:time.Now().Add(time.Hour)}
	sessionStore.items[other]=sessionInfo{userID:2,companyID:0,expires:time.Now().Add(time.Hour)}
	sessionStore.Unlock()
	t.Cleanup(func(){sessionStore.Lock();delete(sessionStore.items,token);delete(sessionStore.items,other);sessionStore.Unlock()})
	req:=httptest.NewRequest(http.MethodPatch,"/api/users/password?id=2",strings.NewReader(`{"password":"nova-senha-segura-123"}`))
	req.AddCookie(&http.Cookie{Name:"markys_session",Value:token})
	rec:=httptest.NewRecorder();app.userPasswordReset(rec,req)
	if rec.Code!=http.StatusOK { t.Fatalf("password reset status %d: %s",rec.Code,rec.Body.String()) }
	var hash string
	if err:=app.db.QueryRow("SELECT password_hash FROM users WHERE id=2").Scan(&hash);err!=nil { t.Fatal(err) }
	if !verifyPassword("nova-senha-segura-123",hash) { t.Fatal("new password was not saved as a valid hash") }
	sessionStore.Lock();_,stillValid:=sessionStore.items[other];_,currentStillValid:=sessionStore.items[token];sessionStore.Unlock()
	if stillValid { t.Fatal("other user sessions should be revoked after password reset") }
	if !currentStillValid { t.Fatal("administrator's current session should remain valid") }
}

func TestCompanyStorageRootIsAnchoredToMasterDatabase(t *testing.T) {
 base:=filepath.Join(t.TempDir(),"install","data","markys-pdv.db")
 app:=&App{dbPath:base}
 got,err:=app.companyStorageRoot()
 if err!=nil { t.Fatal(err) }
 want:=filepath.Join(filepath.Dir(base),"companies")
 if got!=want { t.Fatalf("company storage root = %q, want %q",got,want) }
}

