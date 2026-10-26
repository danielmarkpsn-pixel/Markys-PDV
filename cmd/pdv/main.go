package main

import (
	"database/sql"
	"encoding/csv"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed web/*
var assets embed.FS

type App struct{ db *sql.DB; dbPath string; masterDB *sql.DB; companyID int64 }

type Product struct {
	ID int64 `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
	Unit string `json:"unit"`
	Price int64 `json:"price_cents"`
	Cost int64 `json:"cost_cents"`
	Barcode string `json:"barcode"`
	ImageURL string `json:"image_url"`
	CategoryID int64 `json:"category_id"`
	SupplierID int64 `json:"supplier_id"`
	Stock float64 `json:"stock"`
	Active bool `json:"active"`
}

type SaleItemRequest struct {
	Code string `json:"code"`
	Quantity float64 `json:"quantity"`
}
type PaymentRequest struct {
	Method string `json:"method"`
	Amount int64 `json:"amount_cents"`
}
type SaleRequest struct {
	Items []SaleItemRequest `json:"items"`
	CustomerID int64 `json:"customer_id"`
	Payments []PaymentRequest `json:"payments"`
	Discount int64 `json:"discount_cents"`
}
type saleLine struct {
	Product Product
	Quantity float64
	LineTotal int64
}

func main() {
	dbPath := env("PDV_DB", defaultDatabasePath())
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil { log.Fatal(err) }
	db, err := sql.Open("sqlite", dbPath)
	if err != nil { log.Fatal(err) }
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil { log.Fatal(err) }
	schema, err := assets.ReadFile("web/schema.sql")
	if err != nil { log.Fatal(err) }
	if _, err = db.Exec(string(schema)); err != nil { log.Fatal(err) }
	if err = migrateProductColumns(db); err != nil { log.Fatal(err) }
	if err = migrateSaleCustomerColumn(db); err != nil { log.Fatal(err) }
	if err = initCompanies(db); err != nil { log.Fatal(err) }
	webRoot, err := fs.Sub(assets, "web")
	if err != nil { log.Fatal(err) }
	a := &App{db: db, dbPath: dbPath, masterDB: db}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", a.health)
	mux.HandleFunc("/api/auth/companies", a.authCompanies)
	mux.HandleFunc("/api/auth/status", a.authStatus)
	mux.HandleFunc("/api/auth/setup", a.authSetup)
	mux.HandleFunc("/api/auth/login", a.authLogin)
	mux.HandleFunc("/api/auth/recover-admin", a.authRecoverAdmin)
	mux.HandleFunc("/api/auth/logout", a.authLogout)
	mux.HandleFunc("/api/admin/companies", a.companies)
	mux.HandleFunc("/api/admin/companies/status", a.companyStatus)
	mux.HandleFunc("/api/admin/companies/branding", a.companyBrandingUpdate)
	mux.HandleFunc("/api/admin/companies/bootstrap", a.companyBootstrap)
	mux.HandleFunc("/api/admin/equipment", a.equipment)
	mux.Handle("/", http.FileServer(http.FS(webRoot)))
	srv := &http.Server{Addr: env("PDV_ADDR", "0.0.0.0:8080"), Handler: headers(a.rootHandler(mux)), ReadHeaderTimeout: 5*time.Second}
	log.Printf("MARKYS PDV listening on %s", srv.Addr)
	openBrowserWhenReady()
	log.Fatal(srv.ListenAndServe())
}

func (a *App) customers(w http.ResponseWriter,r *http.Request) {
 type customer struct { ID int64 `json:"id"`; Name string `json:"name"`; Document string `json:"document"`; Phone string `json:"phone"`; Email string `json:"email"`; Address string `json:"address"`; CreatedAt string `json:"created_at"` }
 switch r.Method {
 case http.MethodGet:
 rows,err:=a.db.Query("SELECT id,name,document,phone,email,address,created_at FROM customers ORDER BY name COLLATE NOCASE"); if err!=nil {http.Error(w,"Não foi possível consultar clientes",500);return}; defer rows.Close()
 out:=[]customer{}; for rows.Next(){var c customer;if err:=rows.Scan(&c.ID,&c.Name,&c.Document,&c.Phone,&c.Email,&c.Address,&c.CreatedAt);err!=nil{http.Error(w,"Erro ao ler clientes",500);return};out=append(out,c)};if err:=rows.Err();err!=nil{http.Error(w,"Erro ao ler clientes",500);return};jsonOut(w,200,out)
 case http.MethodPost:
 r.Body=http.MaxBytesReader(w,r.Body,1<<20);var c customer;if err:=json.NewDecoder(r.Body).Decode(&c);err!=nil{http.Error(w,"Dados de cliente inválidos",400);return}
 c.Name=strings.TrimSpace(c.Name);c.Document=strings.TrimSpace(c.Document);c.Phone=strings.TrimSpace(c.Phone);c.Email=strings.TrimSpace(c.Email);c.Address=strings.TrimSpace(c.Address)
 if c.Name==""||len(c.Name)>160||len(c.Document)>20||len(c.Phone)>30||len(c.Email)>160||len(c.Address)>240{http.Error(w,"Confira o nome e os limites dos campos",400);return}
 res,err:=a.db.Exec("INSERT INTO customers(name,document,phone,email,address) VALUES(?,?,?,?,?)",c.Name,c.Document,c.Phone,c.Email,c.Address);if err!=nil{http.Error(w,"Cliente não salvo: CPF/CNPJ pode já estar cadastrado",409);return};c.ID,err=res.LastInsertId();if err!=nil{http.Error(w,"Não foi possível identificar o cliente salvo",500);return};a.writeAudit(requestUserID(a,r),"customer_created","customer",strconv.FormatInt(c.ID,10),"Cliente "+c.Name+" cadastrado");jsonOut(w,201,c)
 default:w.Header().Set("Allow","GET, POST");http.Error(w,"Método não permitido",405)
 }
}

func migrateSaleCustomerColumn(db *sql.DB) error {
	rows,err:=db.Query("PRAGMA table_info(sales)");if err!=nil{return err}
	found:=false
	for rows.Next(){var cid,notnull,pk int;var name,typ string;var def any;if err:=rows.Scan(&cid,&name,&typ,&notnull,&def,&pk);err!=nil{rows.Close();return err};if name=="customer_id"{found=true}}
	if err:=rows.Err();err!=nil{rows.Close();return err};rows.Close()
	if !found { if _,err:=db.Exec("ALTER TABLE sales ADD COLUMN customer_id INTEGER REFERENCES customers(id)");err!=nil{return err} }
	return nil
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet { http.Error(w, "Method not allowed", http.StatusMethodNotAllowed); return }
	jsonOut(w, http.StatusOK, map[string]any{"status":"ok", "product":"MARKYS PDV", "stage":"sales-foundation", "fiscal_ready":false})
}

func migrateProductColumns(db *sql.DB) error {
	columns := []struct{name, definition string}{
		{"cost_cents", "INTEGER NOT NULL DEFAULT 0 CHECK(cost_cents>=0)"},
		{"barcode", "TEXT NOT NULL DEFAULT ''"},
		{"category_id", "INTEGER REFERENCES categories(id)"},
		{"supplier_id", "INTEGER REFERENCES suppliers(id)"},
		{"image_url", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, col := range columns {
		rows, err := db.Query("PRAGMA table_info(products)")
		if err != nil { return err }
		found := false
		for rows.Next() { var cid int; var name, typ string; var notnull, pk int; var def any; if err := rows.Scan(&cid,&name,&typ,&notnull,&def,&pk); err != nil { rows.Close(); return err }; if name == col.name { found = true } }
		if err := rows.Err(); err != nil { rows.Close(); return err }; rows.Close()
		if !found { if _, err := db.Exec("ALTER TABLE products ADD COLUMN "+col.name+" "+col.definition); err != nil { return err } }
	}
	_, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_products_barcode ON products(barcode); CREATE INDEX IF NOT EXISTS idx_products_category ON products(category_id); CREATE INDEX IF NOT EXISTS idx_products_supplier ON products(supplier_id)")
	return err
}

func (a *App) catalog(table string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if table != "categories" && table != "suppliers" { http.Error(w,"Not found",404); return }
		if r.Method == http.MethodGet {
			rows, err := a.db.Query("SELECT id,name FROM "+table+" WHERE active=1 ORDER BY name COLLATE NOCASE")
			if err != nil { http.Error(w,"Database error",500); return }; defer rows.Close()
			type entry struct { ID int64 `json:"id"`; Name string `json:"name"` }
			out := []entry{}
			for rows.Next() { var e entry; if err := rows.Scan(&e.ID,&e.Name); err != nil { http.Error(w,"Read error",500); return }; out=append(out,e) }
			if err := rows.Err(); err != nil { http.Error(w,"Read error",500); return }; jsonOut(w,200,out); return
		}
		if r.Method != http.MethodPost { w.Header().Set("Allow","GET, POST"); http.Error(w,"Method not allowed",405); return }
		var body struct { Name string `json:"name"` }
		if err := json.NewDecoder(http.MaxBytesReader(w,r.Body,1<<20)).Decode(&body); err != nil { http.Error(w,"Invalid JSON",400); return }
		body.Name = strings.TrimSpace(body.Name); if body.Name == "" { http.Error(w,"Name is required",400); return }
		res, err := a.db.Exec("INSERT INTO "+table+"(name) VALUES(?)",body.Name)
		if err != nil { http.Error(w,"Name may already exist or could not be saved",409); return }
		id, err := res.LastInsertId(); if err != nil { http.Error(w,"Could not identify saved entry",500); return }
		a.writeAudit(requestUserID(a,r),table+"_created",table,strconv.FormatInt(id,10),"Cadastro: "+body.Name)
		jsonOut(w,201,map[string]any{"id":id,"name":body.Name})
	}
}

func (a *App) products(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := a.db.Query(`SELECT id,code,name,unit,price_cents,cost_cents,barcode,COALESCE(category_id,0),COALESCE(supplier_id,0),stock,active,image_url FROM products ORDER BY name COLLATE NOCASE`)
		if err != nil { http.Error(w, "Database error", http.StatusInternalServerError); return }
		defer rows.Close()
		out := []Product{}
		for rows.Next() {
			var p Product; var active int
			if err := rows.Scan(&p.ID,&p.Code,&p.Name,&p.Unit,&p.Price,&p.Cost,&p.Barcode,&p.CategoryID,&p.SupplierID,&p.Stock,&active,&p.ImageURL); err != nil { http.Error(w,"Read error",500); return }
			p.Active = active == 1; out = append(out,p)
		}
		if err := rows.Err(); err != nil { http.Error(w,"Read error",500); return }
		jsonOut(w,200,out)
	case http.MethodPost, http.MethodPut:
		r.Body = http.MaxBytesReader(w,r.Body,1<<20)
		var p Product
		dec := json.NewDecoder(r.Body); dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil { http.Error(w,"Invalid JSON",400); return }
		p.Code=strings.TrimSpace(p.Code); p.Name=strings.TrimSpace(p.Name); p.Unit=strings.TrimSpace(p.Unit); p.Barcode=strings.TrimSpace(p.Barcode); p.ImageURL=strings.TrimSpace(p.ImageURL)
		if p.Code=="" || p.Name=="" || p.Unit=="" || p.Price<0 || p.Cost<0 || p.Stock<0 || math.IsNaN(p.Stock) || math.IsInf(p.Stock,0) { http.Error(w,"Código, nome, unidade e valores válidos são obrigatórios",400); return }
		if r.Method==http.MethodPut {
			id,err:=strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")),10,64)
			if err!=nil||id<=0 { http.Error(w,"ID do produto inválido",400); return }
			var oldCode string;var oldStock float64
			if err:=a.db.QueryRow("SELECT code,stock FROM products WHERE id=?",id).Scan(&oldCode,&oldStock);err!=nil { http.Error(w,"Produto não encontrado",404); return }
			_,err=a.db.Exec(`UPDATE products SET code=?,name=?,unit=?,price_cents=?,cost_cents=?,barcode=?,category_id=NULLIF(?,0),supplier_id=NULLIF(?,0),stock=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`,p.Code,p.Name,p.Unit,p.Price,p.Cost,p.Barcode,p.CategoryID,p.SupplierID,p.Stock,id)
			if err!=nil { http.Error(w,"Não foi possível atualizar o produto; confira se o código já existe",409); return }
			p.ID=id;p.Active=true
			if p.Stock!=oldStock { delta:=p.Stock-oldStock; if _,err=a.db.Exec("INSERT INTO stock_movements(product_id,movement_type,quantity,reference_type,note) VALUES(?, 'adjustment', ?, 'manual', ?)",id,delta,"Ajuste ao editar cadastro do produto");err!=nil { http.Error(w,"Produto atualizado, mas não foi possível registrar o ajuste de estoque",500);return } }
			a.writeAudit(requestUserID(a,r),"product_updated","product",strconv.FormatInt(id,10),"Produto atualizado: "+p.Code+" · "+p.Name);jsonOut(w,200,p);return
		}
		res,err:=a.db.Exec(`INSERT INTO products(code,name,unit,price_cents,cost_cents,barcode,category_id,supplier_id,stock,active,image_url) VALUES(?,?,?,?,?,?,NULLIF(?,0),NULLIF(?,0),?,1,?)`,p.Code,p.Name,p.Unit,p.Price,p.Cost,p.Barcode,p.CategoryID,p.SupplierID,p.Stock,p.ImageURL)
		if err!=nil { http.Error(w,"Não foi possível salvar o produto; o código pode já existir",409); return }
		id, idErr := res.LastInsertId(); if idErr != nil { http.Error(w,"Não foi possível identificar o produto",500); return }; p.ID=id; p.Active=true; a.writeAudit(requestUserID(a,r),"product_created","product",strconv.FormatInt(id,10),"Produto cadastrado: "+p.Code+" · "+p.Name); jsonOut(w,201,p)
	default:
		w.Header().Set("Allow","GET, POST, PUT"); http.Error(w,"Method not allowed",405)
	}
}


type StockMovementRequest struct { ProductID int64 `json:"product_id"`; Type string `json:"type"`; Quantity float64 `json:"quantity"`; Note string `json:"note"` }
func (a *App) stockMovements(w http.ResponseWriter,r *http.Request){
 userID:=requestUserID(a,r)
 switch r.Method {
 case http.MethodGet:
 rows,err:=a.db.Query(`SELECT m.id,m.product_id,p.code,p.name,m.movement_type,m.quantity,m.note,m.created_at FROM stock_movements m JOIN products p ON p.id=m.product_id ORDER BY m.id DESC LIMIT 200`);if err!=nil{http.Error(w,"Database error",500);return};defer rows.Close()
 type M struct{ID int64 `json:"id"`;ProductID int64 `json:"product_id"`;Code string `json:"code"`;Name string `json:"name"`;Type string `json:"type"`;Quantity float64 `json:"quantity"`;Note string `json:"note"`;CreatedAt string `json:"created_at"`}
 out:=[]M{};for rows.Next(){var m M;if err:=rows.Scan(&m.ID,&m.ProductID,&m.Code,&m.Name,&m.Type,&m.Quantity,&m.Note,&m.CreatedAt);err!=nil{http.Error(w,"Read error",500);return};out=append(out,m)};if err:=rows.Err();err!=nil{http.Error(w,"Read error",500);return};jsonOut(w,200,out)
 case http.MethodPost:
 r.Body=http.MaxBytesReader(w,r.Body,1<<20);var q StockMovementRequest;d:=json.NewDecoder(r.Body);d.DisallowUnknownFields();if err:=d.Decode(&q);err!=nil{http.Error(w,"JSON inválido",400);return};q.Type=strings.TrimSpace(q.Type);q.Note=strings.TrimSpace(q.Note)
 if q.ProductID<=0||q.Quantity<=0||math.IsNaN(q.Quantity)||math.IsInf(q.Quantity,0)||q.Quantity>1e7||len(q.Note)>500||q.Note==""{http.Error(w,"Informe produto, quantidade válida e motivo de até 500 caracteres",400);return};if q.Type!="entry"&&q.Type!="exit"&&q.Type!="adjustment"{http.Error(w,"Tipo inválido",400);return}
 tx,err:=a.db.Begin();if err!=nil{http.Error(w,"Não foi possível iniciar movimentação",500);return};defer tx.Rollback()
 var code,name string;var stock float64;var active int
 if err=tx.QueryRow("SELECT code,name,stock,active FROM products WHERE id=?",q.ProductID).Scan(&code,&name,&stock,&active);errors.Is(err,sql.ErrNoRows){http.Error(w,"Produto não encontrado",404);return}else if err!=nil{http.Error(w,"Erro ao consultar produto",500);return};if active!=1{http.Error(w,"Produto inativo",409);return}
 delta:=q.Quantity;typ:=q.Type;if q.Type=="exit"{delta=-q.Quantity};if q.Type=="adjustment"{delta=q.Quantity-stock};if stock+delta<0{http.Error(w,"Estoque não pode ficar negativo",409);return}
 if _,err=tx.Exec("UPDATE products SET stock=stock+?,updated_at=CURRENT_TIMESTAMP WHERE id=?",delta,q.ProductID);err!=nil{http.Error(w,"Erro ao atualizar estoque",500);return}
 if _,err=tx.Exec("INSERT INTO stock_movements(product_id,movement_type,quantity,reference_type,note) VALUES(?,?,?,'manual',?)",q.ProductID,typ,delta,q.Note);err!=nil{http.Error(w,"Erro ao registrar movimentação",500);return}
 if _,err=tx.Exec("INSERT INTO audit_log(user_id,action,entity_type,entity_id,details) VALUES(?,?,?,?,?)",nullableUserID(userID),"stock_"+typ,"product",strconv.FormatInt(q.ProductID,10),fmt.Sprintf("%s: variação %.3f; estoque %.3f -> %.3f",q.Note,delta,stock,stock+delta));err!=nil{http.Error(w,"Erro na auditoria",500);return}
 if err=tx.Commit();err!=nil{http.Error(w,"Erro ao confirmar movimentação",500);return};jsonOut(w,201,map[string]any{"product_id":q.ProductID,"code":code,"name":name,"movement_type":typ,"quantity":delta,"stock":stock+delta,"note":q.Note})
 default:w.Header().Set("Allow","GET, POST");http.Error(w,"Method not allowed",405)
 }
}
func (a *App) stockReportCSV(w http.ResponseWriter,r *http.Request){if r.Method!=http.MethodGet{http.Error(w,"Method not allowed",405);return};rows,err:=a.db.Query("SELECT code,barcode,name,unit,cost_cents,price_cents,stock,active FROM products ORDER BY name COLLATE NOCASE");if err!=nil{http.Error(w,"Database error",500);return};defer rows.Close();w.Header().Set("Content-Type","text/csv; charset=utf-8");w.Header().Set("Content-Disposition",`attachment; filename="markys-relatorio-estoque.csv"`);_,_=w.Write([]byte{0xEF,0xBB,0xBF});cw:=csv.NewWriter(w);_=cw.Write([]string{"SKU","Código de barras","Produto","Unidade","Custo (centavos)","Venda (centavos)","Estoque","Ativo"});for rows.Next(){var c,b,n,u string;var cost,price int64;var stock float64;var active int;if err:=rows.Scan(&c,&b,&n,&u,&cost,&price,&stock,&active);err!=nil{http.Error(w,"Read error",500);return};_=cw.Write([]string{c,b,n,u,strconv.FormatInt(cost,10),strconv.FormatInt(price,10),strconv.FormatFloat(stock,'f',-1,64),strconv.Itoa(active)})};if err:=rows.Err();err!=nil{http.Error(w,"Read error",500);return};cw.Flush()}
func (a *App) movementReportCSV(w http.ResponseWriter,r *http.Request){if r.Method!=http.MethodGet{http.Error(w,"Method not allowed",405);return};from,to:=strings.TrimSpace(r.URL.Query().Get("from")),strings.TrimSpace(r.URL.Query().Get("to"));for _,v:=range []string{from,to}{if v!=""{if _,err:=time.Parse("2006-01-02",v);err!=nil{http.Error(w,"Data inválida",400);return}}};rows,err:=a.db.Query(`SELECT m.created_at,p.code,p.name,m.movement_type,m.quantity,m.reference_type,COALESCE(m.reference_id,0),m.note FROM stock_movements m JOIN products p ON p.id=m.product_id WHERE (?='' OR date(m.created_at)>=date(?)) AND (?='' OR date(m.created_at)<=date(?)) ORDER BY m.id DESC`,from,from,to,to);if err!=nil{http.Error(w,"Database error",500);return};defer rows.Close();w.Header().Set("Content-Type","text/csv; charset=utf-8");w.Header().Set("Content-Disposition",`attachment; filename="markys-relatorio-movimentacoes.csv"`);_,_=w.Write([]byte{0xEF,0xBB,0xBF});cw:=csv.NewWriter(w);_=cw.Write([]string{"Data","SKU","Produto","Tipo","Quantidade (variação)","Referência","ID referência","Motivo"});for rows.Next(){var dt,c,n,t,ref,note string;var qty float64;var id int64;if err:=rows.Scan(&dt,&c,&n,&t,&qty,&ref,&id,&note);err!=nil{http.Error(w,"Read error",500);return};_=cw.Write([]string{dt,c,n,t,strconv.FormatFloat(qty,'f',-1,64),ref,strconv.FormatInt(id,10),note})};if err:=rows.Err();err!=nil{http.Error(w,"Read error",500);return};cw.Flush()}

func (a *App) sales(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if rawID:=strings.TrimSpace(r.URL.Query().Get("id"));rawID!="" {
			id,err:=strconv.ParseInt(rawID,10,64);if err!=nil||id<=0{http.Error(w,"Identificador de venda inválido",400);return}
			type SaleCustomer struct { Name string `json:"name"`; Phone string `json:"phone"`; Address string `json:"address"` }
			type SaleItemDetail struct { Name string `json:"name"`; Quantity float64 `json:"quantity"`; UnitPrice int64 `json:"unit_price_cents"`; Total int64 `json:"total_cents"` }
			type SalePaymentDetail struct { Method string `json:"method"`; Amount int64 `json:"amount_cents"` }
			type SaleDetail struct { ID int64 `json:"id"`; Number string `json:"sale_number"`; Total int64 `json:"total_cents"`; Subtotal int64 `json:"subtotal_cents"`; Discount int64 `json:"discount_cents"`; Status string `json:"status"`; CreatedAt string `json:"created_at"`; Customer *SaleCustomer `json:"customer"`; Items []SaleItemDetail `json:"items"`; Payments []SalePaymentDetail `json:"payments"` }
			var s SaleDetail
			var custName,custPhone,custAddress sql.NullString
			err=a.db.QueryRow(`SELECT s.id,s.sale_number,s.total_cents,s.subtotal_cents,s.discount_cents,s.status,s.created_at,c.name,c.phone,c.address FROM sales s LEFT JOIN customers c ON c.id=s.customer_id WHERE s.id=?`,id).Scan(&s.ID,&s.Number,&s.Total,&s.Subtotal,&s.Discount,&s.Status,&s.CreatedAt,&custName,&custPhone,&custAddress)
			if errors.Is(err,sql.ErrNoRows){http.Error(w,"Venda não encontrada",404);return};if err!=nil{http.Error(w,"Não foi possível consultar a venda",500);return}
			if custName.Valid{s.Customer=&SaleCustomer{Name:custName.String,Phone:custPhone.String,Address:custAddress.String}}
			s.Items=[]SaleItemDetail{};rows,err:=a.db.Query(`SELECT description,quantity,unit_price_cents,line_total_cents FROM sale_items WHERE sale_id=? ORDER BY id`,id);if err!=nil{http.Error(w,"Não foi possível consultar os itens",500);return}
			for rows.Next(){var item SaleItemDetail;if err:=rows.Scan(&item.Name,&item.Quantity,&item.UnitPrice,&item.Total);err!=nil{rows.Close();http.Error(w,"Erro ao ler itens",500);return};s.Items=append(s.Items,item)};if err:=rows.Err();err!=nil{rows.Close();http.Error(w,"Erro ao ler itens",500);return};rows.Close()
			s.Payments=[]SalePaymentDetail{};pr,err:=a.db.Query(`SELECT method,amount_cents FROM payments WHERE sale_id=? ORDER BY id`,id);if err!=nil{http.Error(w,"Não foi possível consultar pagamentos",500);return};defer pr.Close()
			for pr.Next(){var p SalePaymentDetail;if err:=pr.Scan(&p.Method,&p.Amount);err!=nil{http.Error(w,"Erro ao ler pagamentos",500);return};s.Payments=append(s.Payments,p)};if err:=pr.Err();err!=nil{http.Error(w,"Erro ao ler pagamentos",500);return}
			jsonOut(w,200,s);return
		}
		rows,err:=a.db.Query(`SELECT id,sale_number,total_cents,status,created_at FROM sales ORDER BY id DESC LIMIT 20`)
		if err!=nil { http.Error(w,"Database error",500); return }; defer rows.Close()
		type SaleSummary struct { ID int64 `json:"id"`; Number string `json:"sale_number"`; Total int64 `json:"total_cents"`; Status string `json:"status"`; CreatedAt string `json:"created_at"` }
		out:=[]SaleSummary{}
		for rows.Next(){ var s SaleSummary; if err:=rows.Scan(&s.ID,&s.Number,&s.Total,&s.Status,&s.CreatedAt);err!=nil{http.Error(w,"Read error",500);return};out=append(out,s) }
		if err:=rows.Err();err!=nil{http.Error(w,"Read error",500);return}
		jsonOut(w,200,out)
	case http.MethodPost:
		a.createSale(w,r)
	case http.MethodDelete:
		a.cancelSale(w,r)
	default:
		w.Header().Set("Allow","GET, POST, DELETE"); http.Error(w,"Method not allowed",405)
	}
}

func (a *App) cancelSale(w http.ResponseWriter, r *http.Request) {
	userID:=requestUserID(a,r)
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
	if err != nil || id <= 0 { http.Error(w, "Identificador de venda inválido", 400); return }
	tx, err := a.db.Begin()
	if err != nil { http.Error(w, "Não foi possível iniciar o cancelamento", 500); return }
	defer tx.Rollback()
	var number, status string
	err = tx.QueryRow("SELECT sale_number, status FROM sales WHERE id=?", id).Scan(&number, &status)
	if errors.Is(err, sql.ErrNoRows) { http.Error(w, "Venda não encontrada", 404); return }
	if err != nil { http.Error(w, "Não foi possível consultar a venda", 500); return }
	if status != "completed" { http.Error(w, "Somente vendas concluídas podem ser canceladas", 409); return }
	rows, err := tx.Query("SELECT product_id, quantity FROM sale_items WHERE sale_id=? ORDER BY id", id)
	if err != nil { http.Error(w, "Não foi possível consultar os itens da venda", 500); return }
	type cancelledItem struct { productID int64; quantity float64 }
	items := []cancelledItem{}
	for rows.Next() { var item cancelledItem; if err := rows.Scan(&item.productID, &item.quantity); err != nil { rows.Close(); http.Error(w, "Erro ao ler itens da venda", 500); return }; items = append(items, item) }
	if err = rows.Err(); err != nil { rows.Close(); http.Error(w, "Erro ao ler itens da venda", 500); return }; rows.Close()
	if len(items) == 0 { http.Error(w, "Venda sem itens; cancelamento bloqueado", 409); return }
	res, err := tx.Exec("UPDATE sales SET status='cancelled' WHERE id=? AND status='completed'", id)
	if err != nil { http.Error(w, "Não foi possível cancelar a venda", 500); return }
	changed, err := res.RowsAffected()
	if err != nil || changed != 1 { http.Error(w, "A venda já foi alterada; atualize a tela", 409); return }
	for _, item := range items {
		if _, err = tx.Exec("UPDATE products SET stock=stock+?, updated_at=CURRENT_TIMESTAMP WHERE id=?", item.quantity, item.productID); err != nil { http.Error(w, "Não foi possível estornar o estoque", 500); return }
		if _, err = tx.Exec("INSERT INTO stock_movements(product_id,movement_type,quantity,reference_type,reference_id,note) VALUES(?,'sale_cancel',?,'sale',?,?)", item.productID, item.quantity, id, "Estorno da venda "+number); err != nil { http.Error(w, "Não foi possível registrar o estorno de estoque", 500); return }
	}
	if _, err = tx.Exec("INSERT INTO audit_log(user_id,action,entity_type,entity_id,details) VALUES(?,'sale_cancelled','sale',?,?)", nullableUserID(userID), strconv.FormatInt(id,10), "Venda "+number+" cancelada e estoque estornado"); err != nil { http.Error(w, "Não foi possível registrar a auditoria", 500); return }
	if err = tx.Commit(); err != nil { http.Error(w, "Não foi possível confirmar o cancelamento", 500); return }
	jsonOut(w, 200, map[string]any{"id": id, "sale_number": number, "status": "cancelled", "stock_reversed": true})
}

func (a *App) createSale(w http.ResponseWriter,r *http.Request) {
	userID:=requestUserID(a,r)
	if a.db!=nil { if err:=migrateSaleCustomerColumn(a.db);err!=nil { http.Error(w,"Não foi possível preparar o vínculo de cliente da venda",500);return } }
	r.Body=http.MaxBytesReader(w,r.Body,1<<20)
	var req SaleRequest
	dec:=json.NewDecoder(r.Body); dec.DisallowUnknownFields()
	if err:=dec.Decode(&req);err!=nil{http.Error(w,"Invalid JSON",400);return}
	if len(req.Items)==0 || len(req.Items)>500 {http.Error(w,"Add at least one item (maximum 500)",400);return}
	if len(req.Payments)==0 || len(req.Payments)>20 {http.Error(w,"At least one payment is required",400);return}
	if req.Discount<0 {http.Error(w,"Invalid discount",400);return}

	// Consolidate repeated product codes so stock checks and deductions remain consistent.
	quantities:=map[string]float64{}
	for _,item:=range req.Items {
		code:=strings.TrimSpace(item.Code)
		if code=="" || item.Quantity<=0 || math.IsNaN(item.Quantity) || math.IsInf(item.Quantity,0) || item.Quantity>1e7 {http.Error(w,"Invalid product code or quantity",400);return}
		quantities[code]+=item.Quantity
		if math.IsInf(quantities[code],0) {http.Error(w,"Invalid quantity",400);return}
	}
	methods:=map[string]bool{"cash":true,"pix":true,"debit":true,"credit":true,"voucher":true}
	var paid int64
	for _,p:=range req.Payments { if !methods[p.Method] || p.Amount<=0 || paid>math.MaxInt64-p.Amount {http.Error(w,"Invalid payment method or amount",400);return}; paid+=p.Amount }

	tx,err:=a.db.Begin()
	if err!=nil {http.Error(w,"Could not begin sale",500);return}
	defer tx.Rollback()
	lines:=make([]saleLine,0,len(quantities)); var subtotal int64
	for code,qty:=range quantities {
		var p Product; var active int
		err=tx.QueryRow(`SELECT id,code,name,unit,price_cents,cost_cents,barcode,COALESCE(category_id,0),COALESCE(supplier_id,0),stock,active FROM products WHERE code=? OR barcode=? ORDER BY CASE WHEN code=? THEN 0 ELSE 1 END LIMIT 1`,code,code,code).Scan(&p.ID,&p.Code,&p.Name,&p.Unit,&p.Price,&p.Cost,&p.Barcode,&p.CategoryID,&p.SupplierID,&p.Stock,&active)
		if errors.Is(err,sql.ErrNoRows) {http.Error(w,"Product not found: "+code,400);return}
		if err!=nil {http.Error(w,"Could not read product",500);return}
		if active!=1 {http.Error(w,"Inactive product: "+code,400);return}
		if qty>p.Stock {http.Error(w,"Insufficient stock for: "+p.Name,409);return}
		lineValue:=qty*float64(p.Price)
		if lineValue>float64(math.MaxInt64) {http.Error(w,"Sale value too large",400);return}
		lineTotal:=int64(math.Round(lineValue))
		if subtotal>math.MaxInt64-lineTotal {http.Error(w,"Sale value too large",400);return}
		subtotal+=lineTotal; lines=append(lines,saleLine{Product:p,Quantity:qty,LineTotal:lineTotal})
	}
	if req.Discount>subtotal {http.Error(w,"Discount cannot exceed subtotal",400);return}
	total:=subtotal-req.Discount
	if paid!=total {http.Error(w,fmt.Sprintf("Payment total must equal sale total (%d cents)",total),400);return}
	var cashSessionID int64
	err = tx.QueryRow(`SELECT id FROM cash_sessions WHERE status='open' ORDER BY id DESC LIMIT 1`).Scan(&cashSessionID)
	if errors.Is(err,sql.ErrNoRows) { http.Error(w,"Abra o caixa antes de finalizar vendas",409); return }
	if err != nil { http.Error(w,"Erro ao verificar caixa",500); return }
	number:="MK"+time.Now().Format("20060102150405.000000000")
	if req.CustomerID>0 { var exists int; if err:=tx.QueryRow("SELECT COUNT(*) FROM customers WHERE id=?",req.CustomerID).Scan(&exists);err!=nil||exists==0 {http.Error(w,"Cliente não encontrado",400);return} }
	res,err:=tx.Exec(`INSERT INTO sales(sale_number,cash_session_id,operator_id,status,subtotal_cents,discount_cents,total_cents,completed_at,customer_id) VALUES(?,?,?,'completed',?,?,?,CURRENT_TIMESTAMP,NULLIF(?,0))`,number,cashSessionID,nullableUserID(userID),subtotal,req.Discount,total,req.CustomerID)
	if err != nil { http.Error(w,"Could not create sale",500); return }; if res == nil { http.Error(w,"Could not create sale",500); return }; saleID,err:=res.LastInsertId();if err!=nil{http.Error(w,"Could not identify created sale",500);return}
	for _,line:=range lines {
		_,err=tx.Exec(`INSERT INTO sale_items(sale_id,product_id,description,quantity,unit_price_cents,line_total_cents) VALUES(?,?,?,?,?,?)`,saleID,line.Product.ID,line.Product.Name,line.Quantity,line.Product.Price,line.LineTotal)
		if err!=nil {http.Error(w,"Could not save sale items",500);return}
		result,err:=tx.Exec(`UPDATE products SET stock=stock-?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND active=1 AND stock>=?`,line.Quantity,line.Product.ID,line.Quantity)
		if err!=nil {http.Error(w,"Could not update stock",500);return}
		changed,_:=result.RowsAffected();if changed!=1{http.Error(w,"Stock changed during sale; retry",409);return}
		_,err=tx.Exec(`INSERT INTO stock_movements(product_id,movement_type,quantity,reference_type,reference_id,note) VALUES(?,'sale',?,'sale',?,?)`,line.Product.ID,-line.Quantity,saleID,"Venda "+number)
		if err!=nil {http.Error(w,"Could not record stock movement",500);return}
	}
	for _,p:=range req.Payments {
		_,err=tx.Exec(`INSERT INTO payments(sale_id,method,amount_cents) VALUES(?,?,?)`,saleID,p.Method,p.Amount)
		if err!=nil {http.Error(w,"Could not save payment",500);return}
	}
	if err=tx.Commit();err!=nil {http.Error(w,"Sale could not be committed",500);return}
	a.writeAudit(userID,"sale_completed","sale",strconv.FormatInt(saleID,10),fmt.Sprintf("Venda %s concluída; total %d centavos",number,total))
	jsonOut(w,201,map[string]any{"id":saleID,"sale_number":number,"status":"completed","subtotal_cents":subtotal,"discount_cents":req.Discount,"total_cents":total})
}

func defaultDatabasePath() string {
	const relative = "data/markys-pdv.db"
	if runtime.GOOS != "windows" {
		return relative
	}
	exe, err := os.Executable()
	if err != nil || !strings.Contains(strings.ToLower(filepath.Base(exe)), "markys-pdv") {
		return relative
	}
	return filepath.Join(filepath.Dir(exe), "data", "markys-pdv.db")
}

func jsonOut(w http.ResponseWriter,code int,v any){w.Header().Set("Content-Type","application/json; charset=utf-8");w.WriteHeader(code);_ = json.NewEncoder(w).Encode(v)}
func env(k,d string)string{v:=strings.TrimSpace(os.Getenv(k));if v==""{return d};return v}
func headers(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("X-Frame-Options","DENY");w.Header().Set("Referrer-Policy","no-referrer");w.Header().Set("Cache-Control","no-store, no-cache, must-revalidate");w.Header().Set("Pragma","no-cache");next.ServeHTTP(w,r)})}


// openBrowserWhenReady opens the local interface after the HTTP server starts.
// It is intentionally Windows-only so headless/Linux deployments are unaffected.
// openWindowsAppBrowser starts Edge or Chrome in app mode with silent printing enabled.
// This keeps checkout on one screen and sends print jobs to the Windows default printer.
func openWindowsAppBrowser(url string) bool {
	if runtime.GOOS != "windows" { return false }
	local := os.Getenv("LOCALAPPDATA")
	programFiles := os.Getenv("PROGRAMFILES")
	programFilesX86 := os.Getenv("PROGRAMFILES(X86)")
	candidates := []string{
		filepath.Join(programFiles, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(programFilesX86, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(local, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(programFiles, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(programFilesX86, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"),
	}
	for _, browser := range candidates {
		if browser == "" { continue }
		if _, err := os.Stat(browser); err != nil { continue }
		cmd := exec.Command(browser, "--kiosk-printing", "--app="+url)
		if err := cmd.Start(); err != nil {
			log.Printf("Não foi possível iniciar o navegador em modo de impressão silenciosa (%s): %v", browser, err)
			continue
		}
		log.Printf("Interface aberta em modo aplicativo com impressão silenciosa: %s", browser)
		return true
	}
	return false
}

func openBrowserWhenReady() {
	if runtime.GOOS != "windows" {
		return
	}
	go func() {
		const url = "http://localhost:8080"
		client := &http.Client{Timeout: 500 * time.Millisecond}
		for attempt := 0; attempt < 40; attempt++ {
			resp, err := client.Get(url + "/api/health")
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					if !openWindowsAppBrowser(url) {
						if err := exec.Command("cmd", "/c", "start", "", url).Start(); err != nil {
							log.Printf("Não foi possível abrir o navegador automaticamente: %v", err)
						} else {
							log.Printf("Navegador padrão aberto. Para impressão silenciosa, inicie o Edge/Chrome com --kiosk-printing.")
						}
					}
					return
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
		log.Printf("O servidor iniciou, mas não foi possível confirmar a interface em %s", url)
	}()
}
