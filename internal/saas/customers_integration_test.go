package saas

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTenantCustomersHTTPIntegration(t *testing.T) {
	dsn:=os.Getenv("DATABASE_URL")
	if dsn=="" { t.Skip("DATABASE_URL não configurada; teste de clientes ignorado") }
	cfg,err:=LoadConfig(func(key string)string{switch key{case "SAAS_MODE":return "postgres";case "DATABASE_URL":return dsn;case "DB_CONNECT_TIMEOUT":return "10s"};return ""})
	if err!=nil { t.Fatal(err) }
	ctx,cancel:=context.WithTimeout(context.Background(),60*time.Second);defer cancel()
	db,err:=OpenPostgres(ctx,cfg);if err!=nil{t.Fatal(err)};defer db.Close()
	if err=ApplyMigrations(ctx,db);err!=nil{t.Fatal(err)}
	suffix:=fmt.Sprintf("%d",time.Now().UnixNano());document:="CUST"+suffix
	var tenantID,ownerID string
	if err=db.QueryRowContext(ctx,"INSERT INTO tenants(legal_name,display_name,document) VALUES('Customer Test','Customer Test',$1) RETURNING id::text",document).Scan(&tenantID);err!=nil{t.Fatal(err)}
	if _,err=db.ExecContext(ctx,"INSERT INTO tenant_subscriptions(tenant_id,status,seat_limit) VALUES($1::uuid,'active',2)",tenantID);err!=nil{t.Fatal(err)}
	hash,err:=HashPassword("Customer-Owner-2026!");if err!=nil{t.Fatal(err)}
	if err=db.QueryRowContext(ctx,"INSERT INTO tenant_users(tenant_id,username,display_name,password_hash,role) VALUES($1::uuid,'owner','Owner',$2,'owner') RETURNING id::text",tenantID,hash).Scan(&ownerID);err!=nil{t.Fatal(err)}
	token,_,err:=CreateLoginSession(ctx,db,tenantID,ownerID,"customer-test","127.0.0.1",time.Now().Add(time.Hour));if err!=nil{t.Fatal(err)}
	auth:=NewHTTPHandler(db,true);mux:=http.NewServeMux();auth.Register(mux);NewCustomerHandler(db,auth).Register(mux)
	request:=func(method,path,body string)*httptest.ResponseRecorder{req:=httptest.NewRequest(method,path,strings.NewReader(body));req.AddCookie(&http.Cookie{Name:SessionCookieName,Value:token});rec:=httptest.NewRecorder();mux.ServeHTTP(rec,req);return rec}
	doc:="123.456.789-09"
	created:=request(http.MethodPost,"/api/customers",fmt.Sprintf(`{"name":"Cliente Integração","document":%q,"phone":"92999990000","email":"cliente@example.com","address":"Rua Teste, 10"}`,doc))
	if created.Code!=http.StatusCreated{t.Fatalf("criar cliente: %d %s",created.Code,created.Body.String())}
	var result struct{ID string `json:"id"`};if err:=json.Unmarshal(created.Body.Bytes(),&result);err!=nil||result.ID==""{t.Fatalf("resposta inválida: %v %s",err,created.Body.String())}
	if got:=request(http.MethodPost,"/api/customers",fmt.Sprintf(`{"name":"Duplicado","document":%q}`,doc));got.Code!=http.StatusConflict{t.Fatalf("documento duplicado deveria ser bloqueado: %d %s",got.Code,got.Body.String())}
	list:=request(http.MethodGet,"/api/customers?q=Integração","")
	if list.Code!=http.StatusOK||!strings.Contains(list.Body.String(),result.ID){t.Fatalf("pesquisa de clientes: %d %s",list.Code,list.Body.String())}
	updated:=request(http.MethodPut,"/api/customers/"+result.ID,`{"name":"Cliente Atualizado","document":"12345678909","phone":"92988887777","email":"novo@example.com","address":"Av. Nova, 20"}`)
	if updated.Code!=http.StatusOK{t.Fatalf("atualizar cliente: %d %s",updated.Code,updated.Body.String())}
	deactivated:=request(http.MethodDelete,"/api/customers/"+result.ID,"{}")
	if deactivated.Code!=http.StatusOK{t.Fatalf("inativar cliente: %d %s",deactivated.Code,deactivated.Body.String())}
}
