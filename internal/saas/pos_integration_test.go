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

	var lotProductID,lotEarlyID,lotLateID string
	if err:=db.QueryRowContext(ctx,"INSERT INTO tenant_products(tenant_id,code,name,price_cents,stock,track_lot,track_expiry) VALUES($1::uuid,'POS-LOT','Produto por lote',1000,6,true,true) RETURNING id::text",tenantID).Scan(&lotProductID);err!=nil{t.Fatal(err)}
	early:=time.Now().AddDate(0,0,30).Format("2006-01-02")
	late:=time.Now().AddDate(0,0,60).Format("2006-01-02")
	if err:=db.QueryRowContext(ctx,"INSERT INTO tenant_product_lots(tenant_id,product_id,lot_code,expires_on,quantity) VALUES($1::uuid,$2::uuid,'LOTE-FEFO-1',$3,3) RETURNING id::text",tenantID,lotProductID,early).Scan(&lotEarlyID);err!=nil{t.Fatal(err)}
	if err:=db.QueryRowContext(ctx,"INSERT INTO tenant_product_lots(tenant_id,product_id,lot_code,expires_on,quantity) VALUES($1::uuid,$2::uuid,'LOTE-FEFO-2',$3,3) RETURNING id::text",tenantID,lotProductID,late).Scan(&lotLateID);err!=nil{t.Fatal(err)}
	lotSaleBody:=fmt.Sprintf(`{"cash_session_id":%q,"items":[{"product_id":%q,"quantity":4}],"payments":[{"method":"cash","amount_cents":4000}]}`,openResult.ID,lotProductID)
	lotSale:=request(http.MethodPost,"/api/pos/sales",lotSaleBody)
	if lotSale.Code!=http.StatusCreated{t.Fatalf("venda por lote FEFO: status=%d body=%s",lotSale.Code,lotSale.Body.String())}
	var earlyQty,lateQty float64
	if err:=db.QueryRowContext(ctx,"SELECT quantity FROM tenant_product_lots WHERE tenant_id=$1::uuid AND id=$2::uuid",tenantID,lotEarlyID).Scan(&earlyQty);err!=nil{t.Fatal(err)}
	if err:=db.QueryRowContext(ctx,"SELECT quantity FROM tenant_product_lots WHERE tenant_id=$1::uuid AND id=$2::uuid",tenantID,lotLateID).Scan(&lateQty);err!=nil{t.Fatal(err)}
	if earlyQty!=0||lateQty!=2{t.Fatalf("baixa FEFO esperava lote próximo=0 e lote seguinte=2; recebeu %v e %v",earlyQty,lateQty)}
	var stock float64
	if err:=db.QueryRowContext(ctx,"SELECT stock FROM tenant_products WHERE tenant_id=$1::uuid AND id=$2::uuid",tenantID,productID).Scan(&stock);err!=nil{t.Fatal(err)}
	if stock!=8{t.Fatalf("estoque esperado 8 após venda, recebido %v",stock)}
	splitBody:=fmt.Sprintf(`{"cash_session_id":%q,"items":[{"product_id":%q,"quantity":1}],"payments":[{"method":"cash","amount_cents":1000,"received_cents":1500},{"method":"pix","amount_cents":1500,"received_cents":1500}]}`,openResult.ID,productID)
	splitSale:=request(http.MethodPost,"/api/pos/sales",splitBody)
	if splitSale.Code!=http.StatusCreated||!strings.Contains(splitSale.Body.String(),`"change_cents":500`){t.Fatalf("venda com pagamento dividido e troco: status=%d body=%s",splitSale.Code,splitSale.Body.String())}
	var splitResult struct{ID string `json:"id"`}
	if err:=json.Unmarshal(splitSale.Body.Bytes(),&splitResult);err!=nil||splitResult.ID==""{t.Fatalf("id da venda dividida inválido: %v %s",err,splitSale.Body.String())}
	refund:=request(http.MethodPost,"/api/pos/sales/"+splitResult.ID+"/refund",`{"reason":"Cliente desistiu da compra"}`)
	if refund.Code!=http.StatusOK||!strings.Contains(refund.Body.String(),`"status":"refunded"`){t.Fatalf("estorno integral: status=%d body=%s",refund.Code,refund.Body.String())}
	duplicateRefund:=request(http.MethodPost,"/api/pos/sales/"+splitResult.ID+"/refund",`{"reason":"Tentativa duplicada de estorno"}`)
	if duplicateRefund.Code!=http.StatusConflict{t.Fatalf("estorno duplicado deveria ser bloqueado: status=%d body=%s",duplicateRefund.Code,duplicateRefund.Body.String())}
	if err:=db.QueryRowContext(ctx,"SELECT stock FROM tenant_products WHERE tenant_id=$1::uuid AND id=$2::uuid",tenantID,productID).Scan(&stock);err!=nil{t.Fatal(err)}
	if stock!=8{t.Fatalf("estoque deveria retornar a 8 após estorno, recebido %v",stock)}
	var lotSaleResult struct{ID string `json:"id"`}
	if err:=json.Unmarshal(lotSale.Body.Bytes(),&lotSaleResult);err!=nil||lotSaleResult.ID==""{t.Fatalf("id da venda por lote inválido: %v %s",err,lotSale.Body.String())}
	lotRefund:=request(http.MethodPost,"/api/pos/sales/"+lotSaleResult.ID+"/refund",`{"reason":"Devolução integral do produto"}`)
	if lotRefund.Code!=http.StatusOK{t.Fatalf("estorno da venda por lote: status=%d body=%s",lotRefund.Code,lotRefund.Body.String())}
	if err:=db.QueryRowContext(ctx,"SELECT quantity FROM tenant_product_lots WHERE tenant_id=$1::uuid AND id=$2::uuid",tenantID,lotEarlyID).Scan(&earlyQty);err!=nil{t.Fatal(err)}
	if err:=db.QueryRowContext(ctx,"SELECT quantity FROM tenant_product_lots WHERE tenant_id=$1::uuid AND id=$2::uuid",tenantID,lotLateID).Scan(&lateQty);err!=nil{t.Fatal(err)}
	if earlyQty!=3||lateQty!=3{t.Fatalf("estorno deveria recompor exatamente os lotes FEFO em 3 e 3; recebeu %v e %v",earlyQty,lateQty)}
	closed:=request(http.MethodPost,"/api/pos/cash/close",fmt.Sprintf(`{"session_id":%q,"closing_amount_cents":5900}`,openResult.ID))
	if closed.Code!=http.StatusOK||!strings.Contains(closed.Body.String(),`"expected_cash_cents":6000`)||!strings.Contains(closed.Body.String(),`"difference_cents":-100`){t.Fatalf("fechamento após estornos: status=%d body=%s",closed.Code,closed.Body.String())}
	list:=request(http.MethodGet,"/api/pos/sales?limit=20","")
	if list.Code!=http.StatusOK||!strings.Contains(list.Body.String(),"sale_number"){t.Fatalf("listagem de vendas: status=%d body=%s",list.Code,list.Body.String())}
}
