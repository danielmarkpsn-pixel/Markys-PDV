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

func TestPOSCashAndSaleHTTPIntegration(t *testing.T) {
	dsn:=os.Getenv("DATABASE_URL")
	if dsn==""{t.Skip("DATABASE_URL não configurada; teste POS PostgreSQL ignorado")}
	cfg,err:=LoadConfig(func(key string)string{switch key{case "SAAS_MODE":return "postgres";case "DATABASE_URL":return dsn;case "DB_CONNECT_TIMEOUT":return "10s"};return ""})
	if err!=nil{t.Fatal(err)}
	ctx,cancel:=context.WithTimeout(context.Background(),60*time.Second);defer cancel()
	db,err:=OpenPostgres(ctx,cfg);if err!=nil{t.Fatal(err)};defer db.Close()
	if err:=ApplyMigrations(ctx,db);err!=nil{t.Fatal(err)}
	document:=fmt.Sprintf("POS-HTTP-%d",time.Now().UnixNano())
	var tenantID,ownerID,productID string
	if err:=db.QueryRowContext(ctx,"INSERT INTO tenants(legal_name,display_name,document) VALUES('POS Test','POS Test',$1) RETURNING id::text",document).Scan(&tenantID);err!=nil{t.Fatal(err)}
	if _,err:=db.ExecContext(ctx,"INSERT INTO tenant_subscriptions(tenant_id,status,seat_limit) VALUES($1::uuid,'active',2)",tenantID);err!=nil{t.Fatal(err)}
	hash,err:=HashPassword("POS-Owner-Password-2026!");if err!=nil{t.Fatal(err)}
	if err:=db.QueryRowContext(ctx,"INSERT INTO tenant_users(tenant_id,username,display_name,password_hash,role) VALUES($1::uuid,'owner','Owner',$2,'owner') RETURNING id::text",tenantID,hash).Scan(&ownerID);err!=nil{t.Fatal(err)}
	token,_,err:=CreateLoginSession(ctx,db,tenantID,ownerID,"pos-test","127.0.0.1",time.Now().Add(time.Hour));if err!=nil{t.Fatal(err)}
	if err:=db.QueryRowContext(ctx,"INSERT INTO tenant_products(tenant_id,code,name,price_cents,stock) VALUES($1::uuid,'POS-SKU','Produto POS',2500,10) RETURNING id::text",tenantID).Scan(&productID);err!=nil{t.Fatal(err)}
	auth:=NewHTTPHandler(db,true);mux:=http.NewServeMux();auth.Register(mux);NewPOSHandler(db,auth).Register(mux)
	request:=func(method,path,body string)*httptest.ResponseRecorder{req:=httptest.NewRequest(method,path,strings.NewReader(body));req.AddCookie(&http.Cookie{Name:SessionCookieName,Value:token});rec:=httptest.NewRecorder();mux.ServeHTTP(rec,req);return rec}
	opened:=request(http.MethodPost,"/api/pos/cash/open",`{"terminal_code":"PDV-01","opening_amount_cents":1000}`)
	if opened.Code!=http.StatusCreated{t.Fatalf("abrir caixa: status=%d body=%s",opened.Code,opened.Body.String())}
	var openResult struct{ID string `json:"id"`}
	if err:=json.Unmarshal(opened.Body.Bytes(),&openResult);err!=nil||openResult.ID==""{t.Fatalf("resposta de abertura inválida: %v %s",err,opened.Body.String())}
	duplicate:=request(http.MethodPost,"/api/pos/cash/open",`{"terminal_code":"PDV-01","opening_amount_cents":0}`)
	if duplicate.Code!=http.StatusConflict{t.Fatalf("segundo caixa aberto no mesmo terminal deveria ser recusado: %d %s",duplicate.Code,duplicate.Body.String())}
	saleBody:=fmt.Sprintf(`{"cash_session_id":%q,"items":[{"product_id":%q,"quantity":2}],"payments":[{"method":"cash","amount_cents":5000}]}`,openResult.ID,productID)
	sale:=request(http.MethodPost,"/api/pos/sales",saleBody)
	if sale.Code!=http.StatusCreated||!strings.Contains(sale.Body.String(),`"status":"completed"`){t.Fatalf("venda: status=%d body=%s",sale.Code,sale.Body.String())}
	tooMuch:=fmt.Sprintf(`{"cash_session_id":%q,"items":[{"product_id":%q,"quantity":9}],"payments":[{"method":"cash","amount_cents":22500}]}`,openResult.ID,productID)
	rejected:=request(http.MethodPost,"/api/pos/sales",tooMuch)
	if rejected.Code!=http.StatusConflict{t.Fatalf("venda acima do estoque deveria ser recusada: %d %s",rejected.Code,rejected.Body.String())}
	var stock float64
	if err:=db.QueryRowContext(ctx,"SELECT stock FROM tenant_products WHERE tenant_id=$1::uuid AND id=$2::uuid",tenantID,productID).Scan(&stock);err!=nil{t.Fatal(err)}
	if stock!=8{t.Fatalf("estoque esperado 8 após venda, recebido %v",stock)}
	closed:=request(http.MethodPost,"/api/pos/cash/close",fmt.Sprintf(`{"session_id":%q,"closing_amount_cents":5900}`,openResult.ID))
	if closed.Code!=http.StatusOK||!strings.Contains(closed.Body.String(),`"expected_cash_cents":6000`)||!strings.Contains(closed.Body.String(),`"difference_cents":-100`){t.Fatalf("fechamento: status=%d body=%s",closed.Code,closed.Body.String())}
	list:=request(http.MethodGet,"/api/pos/sales?limit=20","")
	if list.Code!=http.StatusOK||!strings.Contains(list.Body.String(),"POS-SKU")==true{t.Fatalf("listagem de vendas: status=%d body=%s",list.Code,list.Body.String())}
}
