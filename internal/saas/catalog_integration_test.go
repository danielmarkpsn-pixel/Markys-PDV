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

func TestTenantCatalogHTTPIntegration(t *testing.T) {
	dsn:=os.Getenv("DATABASE_URL");if dsn==""{t.Skip("DATABASE_URL não configurada; teste de catálogo ignorado")}
	cfg,err:=LoadConfig(func(key string)string{if key=="SAAS_MODE"{return "postgres"};if key=="DATABASE_URL"{return dsn};if key=="DB_CONNECT_TIMEOUT"{return "10s"};return ""});if err!=nil{t.Fatal(err)}
	ctx,cancel:=context.WithTimeout(context.Background(),45*time.Second);defer cancel()
	db,err:=OpenPostgres(ctx,cfg);if err!=nil{t.Fatal(err)};defer db.Close()
	if err=ApplyMigrations(ctx,db);err!=nil{t.Fatal(err)}
	suffix:=fmt.Sprintf("%d",time.Now().UnixNano());document:="CAT"+suffix
	var tenantID,ownerID string
	if err=db.QueryRowContext(ctx,"INSERT INTO tenants(legal_name,display_name,document) VALUES('Catalog Test','Catalog Test',$1) RETURNING id::text",document).Scan(&tenantID);err!=nil{t.Fatal(err)}
	if _,err=db.ExecContext(ctx,"INSERT INTO tenant_subscriptions(tenant_id,status,seat_limit) VALUES($1::uuid,'active',3)",tenantID);err!=nil{t.Fatal(err)}
	hash,err:=HashPassword("Catalog-Owner-2026!");if err!=nil{t.Fatal(err)}
	if err=db.QueryRowContext(ctx,"INSERT INTO tenant_users(tenant_id,username,display_name,password_hash,role) VALUES($1::uuid,'owner','Owner',$2,'owner') RETURNING id::text",tenantID,hash).Scan(&ownerID);err!=nil{t.Fatal(err)}
	token,_,err:=CreateLoginSession(ctx,db,tenantID,ownerID,"catalog-test","127.0.0.1",time.Now().Add(time.Hour));if err!=nil{t.Fatal(err)}
	auth:=NewHTTPHandler(db,true);mux:=http.NewServeMux();auth.Register(mux);NewCatalogHandler(db,auth).Register(mux)
	request:=func(method,path,body string)*httptest.ResponseRecorder{req:=httptest.NewRequest(method,path,strings.NewReader(body));req.AddCookie(&http.Cookie{Name:SessionCookieName,Value:token});rec:=httptest.NewRecorder();mux.ServeHTTP(rec,req);return rec}
	category:=request(http.MethodPost,"/api/catalog/categories",`{"name":"Bebidas"}`);if category.Code!=http.StatusCreated{t.Fatalf("criar categoria: %d %s",category.Code,category.Body.String())}
	var categoryResult struct{ID string `json:"id"`};if err:=json.Unmarshal(category.Body.Bytes(),&categoryResult);err!=nil{t.Fatal(err)}
	supplier:=request(http.MethodPost,"/api/catalog/suppliers",`{"name":"Fornecedor Teste","document":"123","phone":"9999","email":"teste@example.com"}`);if supplier.Code!=http.StatusCreated{t.Fatalf("criar fornecedor: %d %s",supplier.Code,supplier.Body.String())}
	var supplierResult struct{ID string `json:"id"`};if err:=json.Unmarshal(supplier.Body.Bytes(),&supplierResult);err!=nil{t.Fatal(err)}
	productBody:=fmt.Sprintf(`{"code":"SKU-%s","name":"Água Mineral","unit":"UN","price_cents":500,"cost_cents":250,"category_id":%q,"supplier_id":%q}`,suffix,categoryResult.ID,supplierResult.ID)
	product:=request(http.MethodPost,"/api/catalog/products",productBody);if product.Code!=http.StatusCreated{t.Fatalf("criar produto: %d %s",product.Code,product.Body.String())}
	listCategories:=request(http.MethodGet,"/api/catalog/categories","");if listCategories.Code!=http.StatusOK||!strings.Contains(listCategories.Body.String(),"Bebidas"){t.Fatalf("listar categorias: %d %s",listCategories.Code,listCategories.Body.String())}
	listProducts:=request(http.MethodGet,"/api/catalog/products?q=Água","");if listProducts.Code!=http.StatusOK||!strings.Contains(listProducts.Body.String(),"Água Mineral"){t.Fatalf("listar produtos: %d %s",listProducts.Code,listProducts.Body.String())}
}
