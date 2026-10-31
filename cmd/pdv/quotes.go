package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type QuoteItem struct {
	ProductID int64   `json:"product_id"`
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Unit      string  `json:"unit"`
	Quantity  float64 `json:"quantity"`
	UnitPrice int64   `json:"unit_price_cents"`
	Total     int64   `json:"total_cents"`
}

type Quote struct {
	ID          int64       `json:"id"`
	Number      string      `json:"number"`
	CustomerID  int64       `json:"customer_id"`
	Customer    string      `json:"customer_name"`
	Document    string      `json:"customer_document"`
	Phone       string      `json:"customer_phone"`
	Address     string      `json:"customer_address"`
	Items       []QuoteItem `json:"items"`
	Subtotal    int64       `json:"subtotal_cents"`
	Discount    int64       `json:"discount_cents"`
	Total       int64       `json:"total_cents"`
	ValidUntil  string      `json:"valid_until"`
	Notes       string      `json:"notes"`
	Status      string      `json:"status"`
	CreatedAt   string      `json:"created_at"`
}

func ensureQuotesTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS quotes (
		id INTEGER PRIMARY KEY, quote_number TEXT NOT NULL UNIQUE,
		customer_id INTEGER REFERENCES customers(id), customer_name TEXT NOT NULL,
		customer_document TEXT NOT NULL DEFAULT '', customer_phone TEXT NOT NULL DEFAULT '',
		customer_address TEXT NOT NULL DEFAULT '', items_json TEXT NOT NULL,
		subtotal_cents INTEGER NOT NULL CHECK(subtotal_cents>=0),
		discount_cents INTEGER NOT NULL DEFAULT 0 CHECK(discount_cents>=0),
		total_cents INTEGER NOT NULL CHECK(total_cents>=0),
		valid_until TEXT NOT NULL DEFAULT '', notes TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN('pending','approved','rejected','expired')),
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	); CREATE INDEX IF NOT EXISTS idx_quotes_created ON quotes(created_at DESC);`)
	return err
}

func (a *App) quotes(w http.ResponseWriter, r *http.Request) {
	if err := ensureQuotesTable(a.db); err != nil { http.Error(w, "Não foi possível preparar os orçamentos", 500); return }
	switch r.Method {
	case http.MethodGet:
		if raw := strings.TrimSpace(r.URL.Query().Get("id")); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64); if err != nil || id <= 0 { http.Error(w,"Orçamento inválido",400); return }
			q, err := readQuote(a.db, id); if errors.Is(err,sql.ErrNoRows) { http.Error(w,"Orçamento não encontrado",404); return }; if err != nil { http.Error(w,"Não foi possível consultar o orçamento",500); return }; jsonOut(w,200,q); return
		}
		rows,err:=a.db.Query(`SELECT id,quote_number,COALESCE(customer_id,0),customer_name,customer_document,customer_phone,customer_address,items_json,subtotal_cents,discount_cents,total_cents,valid_until,notes,status,created_at FROM quotes ORDER BY id DESC LIMIT 300`)
		if err!=nil { http.Error(w,"Não foi possível consultar os orçamentos",500); return }; defer rows.Close()
		out:=[]Quote{}
		for rows.Next(){ q,err:=scanQuote(rows); if err!=nil {http.Error(w,"Erro ao ler orçamentos",500);return}; out=append(out,q) }
		if err:=rows.Err();err!=nil{http.Error(w,"Erro ao ler orçamentos",500);return}; jsonOut(w,200,out)
	case http.MethodPost:
		var q Quote
		r.Body=http.MaxBytesReader(w,r.Body,1<<20); dec:=json.NewDecoder(r.Body); dec.DisallowUnknownFields()
		if err:=dec.Decode(&q);err!=nil{http.Error(w,"Dados do orçamento inválidos",400);return}
		q.Customer=strings.TrimSpace(q.Customer);q.Document=strings.TrimSpace(q.Document);q.Phone=strings.TrimSpace(q.Phone);q.Address=strings.TrimSpace(q.Address);q.Notes=strings.TrimSpace(q.Notes)
		if q.Customer==""||len(q.Customer)>160||len(q.Document)>30||len(q.Phone)>40||len(q.Address)>300||len(q.Notes)>2000||len(q.Items)==0||len(q.Items)>200 {http.Error(w,"Informe o cliente e ao menos um item válido",400);return}
		if q.ValidUntil!="" { if _,err:=time.Parse("2006-01-02",q.ValidUntil);err!=nil{http.Error(w,"Validade inválida",400);return} }
		var subtotal int64
		for i:=range q.Items { it:=&q.Items[i]; if it.Name==""||it.Quantity<=0||it.Quantity>1e6||math.IsNaN(it.Quantity)||math.IsInf(it.Quantity,0)||it.UnitPrice<0 {http.Error(w,"Confira os itens e quantidades",400);return}; v:=it.Quantity*float64(it.UnitPrice);if v>float64(math.MaxInt64){http.Error(w,"Valor do orçamento muito alto",400);return};it.Total=int64(math.Round(v));if subtotal>math.MaxInt64-it.Total{http.Error(w,"Valor do orçamento muito alto",400);return};subtotal+=it.Total }
		if q.Discount<0||q.Discount>subtotal{http.Error(w,"Desconto inválido",400);return};q.Subtotal=subtotal;q.Total=subtotal-q.Discount
		if q.CustomerID>0 {var exists int;if err:=a.db.QueryRow("SELECT COUNT(*) FROM customers WHERE id=?",q.CustomerID).Scan(&exists);err!=nil||exists==0{http.Error(w,"Cliente cadastrado não encontrado",400);return}}
		items,err:=json.Marshal(q.Items);if err!=nil{http.Error(w,"Não foi possível preparar os itens",500);return}
		number:="ORC-"+time.Now().Format("20060102-150405")
		res,err:=a.db.Exec(`INSERT INTO quotes(quote_number,customer_id,customer_name,customer_document,customer_phone,customer_address,items_json,subtotal_cents,discount_cents,total_cents,valid_until,notes) VALUES(?,NULLIF(?,0),?,?,?,?,?,?,?,?,?,?)`,number,q.CustomerID,q.Customer,q.Document,q.Phone,q.Address,string(items),q.Subtotal,q.Discount,q.Total,q.ValidUntil,q.Notes)
		if err!=nil{http.Error(w,"Não foi possível salvar o orçamento",500);return};q.ID,err=res.LastInsertId();if err!=nil{http.Error(w,"Orçamento salvo, mas não foi possível obter seu número",500);return};q.Number=number;q.Status="pending";jsonOut(w,201,q)
	case http.MethodPatch:
		id,err:=strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")),10,64);if err!=nil||id<=0{http.Error(w,"Orçamento inválido",400);return}
		var body struct{Status string `json:"status"`};if err:=json.NewDecoder(http.MaxBytesReader(w,r.Body,1<<20)).Decode(&body);err!=nil{http.Error(w,"Dados inválidos",400);return}
		if body.Status!="approved"&&body.Status!="rejected"&&body.Status!="pending"{http.Error(w,"Status inválido",400);return}
		res,err:=a.db.Exec("UPDATE quotes SET status=?,updated_at=CURRENT_TIMESTAMP WHERE id=?",body.Status,id);if err!=nil{http.Error(w,"Não foi possível atualizar o orçamento",500);return};n,_:=res.RowsAffected();if n==0{http.Error(w,"Orçamento não encontrado",404);return};jsonOut(w,200,map[string]any{"id":id,"status":body.Status})
	default: w.Header().Set("Allow","GET, POST, PATCH");http.Error(w,"Método não permitido",405)
	}
}

type quoteScanner interface { Scan(...any) error }
func scanQuote(s quoteScanner)(Quote,error){var q Quote;var raw string;err:=s.Scan(&q.ID,&q.Number,&q.CustomerID,&q.Customer,&q.Document,&q.Phone,&q.Address,&raw,&q.Subtotal,&q.Discount,&q.Total,&q.ValidUntil,&q.Notes,&q.Status,&q.CreatedAt);if err!=nil{return q,err};if err=json.Unmarshal([]byte(raw),&q.Items);err!=nil{return q,err};return q,nil}
func readQuote(db *sql.DB,id int64)(Quote,error){return scanQuote(db.QueryRow(`SELECT id,quote_number,COALESCE(customer_id,0),customer_name,customer_document,customer_phone,customer_address,items_json,subtotal_cents,discount_cents,total_cents,valid_until,notes,status,created_at FROM quotes WHERE id=?`,id))}
