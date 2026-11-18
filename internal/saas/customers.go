package saas

import (
	"database/sql"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type CustomerHandler struct { DB *sql.DB; Auth *HTTPHandler }

func NewCustomerHandler(db *sql.DB, auth *HTTPHandler) *CustomerHandler {
	return &CustomerHandler{DB: db, Auth: auth}
}

func (h *CustomerHandler) Register(mux *http.ServeMux) {
	mux.Handle("/api/customers", h.Auth.RequireSession(http.HandlerFunc(h.collection)))
	mux.Handle("/api/customers/", h.Auth.RequireSession(http.HandlerFunc(h.item)))
}

func customerManager(w http.ResponseWriter, r *http.Request) (SessionIdentity, bool) {
	id, ok := IdentityFromContext(r.Context())
	if !ok { writeJSON(w, 401, map[string]string{"error":"Sessão inválida"}); return SessionIdentity{}, false }
	if id.Role != "owner" && id.Role != "admin" && id.Role != "manager" {
		writeJSON(w, 403, map[string]string{"error":"Seu perfil não pode administrar clientes"})
		return SessionIdentity{}, false
	}
	return id, true
}

type customerRecord struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Document string `json:"document"`
	Phone string `json:"phone"`
	Email string `json:"email"`
	Address string `json:"address"`
	Active bool `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

var customerNonDigits = regexp.MustCompile(`\D`)

func normalizeCustomerDocument(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" { return "", nil }
	value = customerNonDigits.ReplaceAllString(value, "")
	if len(value) != 11 && len(value) != 14 { return "", errInvalidUserInput }
	return value, nil
}

type customerInput struct {
	Name string `json:"name"`
	Document string `json:"document"`
	Phone string `json:"phone"`
	Email string `json:"email"`
	Address string `json:"address"`
}

func normalizeCustomer(in *customerInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Phone = strings.TrimSpace(in.Phone)
	in.Email = strings.TrimSpace(in.Email)
	in.Address = strings.TrimSpace(in.Address)
	doc, err := normalizeCustomerDocument(in.Document)
	if err != nil { return err }
	in.Document = doc
	if in.Name == "" || len(in.Name) > 180 || len(in.Document) > 14 ||
		len(in.Phone) > 40 || len(in.Email) > 180 || len(in.Address) > 500 {
		return errInvalidUserInput
	}
	if strings.ContainsAny(in.Email, "\r\n") { return errInvalidUserInput }
	if in.Email != "" && (!strings.Contains(in.Email, "@") || strings.HasPrefix(in.Email, "@") || strings.HasSuffix(in.Email, "@")) {
		return errInvalidUserInput
	}
	return nil
}

func (h *CustomerHandler) collection(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFromContext(r.Context())
	if !ok { writeJSON(w, 401, map[string]string{"error":"Sessão inválida"}); return }
	switch r.Method {
	case http.MethodGet:
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(q) > 100 { writeJSON(w, 400, map[string]string{"error":"Pesquisa muito longa"}); return }
		pattern := "%" + q + "%"
		items := []customerRecord{}
		err := WithTenantTx(r.Context(), h.DB, id.TenantID, nil, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(r.Context(), `SELECT id::text,name,document,phone,email,address,active,created_at,updated_at
				FROM tenant_customers WHERE tenant_id=$1::uuid AND ($2='' OR name ILIKE $3 OR document ILIKE $3 OR phone ILIKE $3)
				ORDER BY active DESC,lower(name) LIMIT 300`, id.TenantID, q, pattern)
			if err != nil { return err }
			defer rows.Close()
			for rows.Next() {
				var x customerRecord
				if err := rows.Scan(&x.ID,&x.Name,&x.Document,&x.Phone,&x.Email,&x.Address,&x.Active,&x.CreatedAt,&x.UpdatedAt); err != nil { return err }
				items = append(items,x)
			}
			return rows.Err()
		})
		if err != nil { writeJSON(w,500,map[string]string{"error":"Não foi possível listar clientes"}); return }
		writeJSON(w,200,map[string]any{"customers":items,"limit":300})
	case http.MethodPost:
		id, ok = customerManager(w,r); if !ok { return }
		var in customerInput
		if !decodeAuthJSON(w,r,&in) { return }
		if err := normalizeCustomer(&in); err != nil { writeJSON(w,400,map[string]string{"error":"Confira nome, documento, telefone e e-mail do cliente"}); return }
		var newID string
		err := WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx) error {
			return tx.QueryRowContext(r.Context(),`INSERT INTO tenant_customers(tenant_id,name,document,phone,email,address)
				VALUES($1::uuid,$2,$3,$4,$5,$6) RETURNING id::text`,id.TenantID,in.Name,in.Document,in.Phone,in.Email,in.Address).Scan(&newID)
		})
		if err != nil { writeJSON(w,409,map[string]string{"error":"Documento já cadastrado nesta empresa ou dados inválidos"}); return }
		writeJSON(w,201,map[string]string{"id":newID})
	default:
		w.Header().Set("Allow","GET, POST")
		writeJSON(w,405,map[string]string{"error":"Método não permitido"})
	}
}

func (h *CustomerHandler) item(w http.ResponseWriter, r *http.Request) {
	id, ok := customerManager(w,r); if !ok { return }
	itemID := strings.Trim(strings.TrimPrefix(r.URL.Path,"/api/customers/"),"/")
	if itemID == "" || strings.Contains(itemID,"/") { writeJSON(w,404,map[string]string{"error":"Cliente não encontrado"}); return }
	switch r.Method {
	case http.MethodPut:
		var in customerInput
		if !decodeAuthJSON(w,r,&in) { return }
		if err := normalizeCustomer(&in); err != nil { writeJSON(w,400,map[string]string{"error":"Confira os dados do cliente"}); return }
		var result sql.Result
		err := WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx) error {
			var e error
			result,e = tx.ExecContext(r.Context(),`UPDATE tenant_customers SET name=$3,document=$4,phone=$5,email=$6,address=$7,updated_at=now()
				WHERE tenant_id=$1::uuid AND id=$2::uuid AND active=true`,id.TenantID,itemID,in.Name,in.Document,in.Phone,in.Email,in.Address)
			return e
		})
		if err != nil { writeJSON(w,409,map[string]string{"error":"Não foi possível atualizar; verifique se o documento já está em uso"}); return }
		n,_ := result.RowsAffected(); if n == 0 { writeJSON(w,404,map[string]string{"error":"Cliente ativo não encontrado"}); return }
		writeJSON(w,200,map[string]bool{"ok":true})
	case http.MethodDelete:
		var result sql.Result
		err := WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx) error {
			var e error
			result,e = tx.ExecContext(r.Context(),`UPDATE tenant_customers SET active=false,updated_at=now()
				WHERE tenant_id=$1::uuid AND id=$2::uuid AND active=true`,id.TenantID,itemID)
			return e
		})
		if err != nil { writeJSON(w,500,map[string]string{"error":"Não foi possível inativar cliente"}); return }
		n,_ := result.RowsAffected(); if n == 0 { writeJSON(w,404,map[string]string{"error":"Cliente ativo não encontrado"}); return }
		writeJSON(w,200,map[string]bool{"ok":true})
	default:
		w.Header().Set("Allow","PUT, DELETE")
		writeJSON(w,405,map[string]string{"error":"Método não permitido"})
	}
}
