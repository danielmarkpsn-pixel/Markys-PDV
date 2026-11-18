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

func TestPlatformAdminHTTPIntegration(t *testing.T) {
	dsn:=os.Getenv("DATABASE_URL"); if dsn=="" { t.Skip("DATABASE_URL não configurada; teste de plataforma ignorado") }
	cfg,err:=LoadConfig(func(key string)string{if key=="SAAS_MODE"{return "postgres"};if key=="DATABASE_URL"{return dsn};if key=="DB_CONNECT_TIMEOUT"{return "10s"};return ""})
	if err!=nil {t.Fatal(err)}
	ctx,cancel:=context.WithTimeout(context.Background(),45*time.Second); defer cancel()
	db,err:=OpenPostgres(ctx,cfg);if err!=nil{t.Fatal(err)};defer db.Close()
	if err=ApplyMigrations(ctx,db);err!=nil{t.Fatal(err)}
	suffix:=fmt.Sprintf("%d",time.Now().UnixNano())
	username:="platform-test-"+suffix
	hash,err:=HashPassword("Platform-Password-2026!");if err!=nil{t.Fatal(err)}
	var adminID string
	if err=db.QueryRowContext(ctx,"INSERT INTO platform_admins(username,display_name,password_hash) VALUES($1,'Platform Test',$2) RETURNING id::text",username,hash).Scan(&adminID);err!=nil{t.Fatal(err)}
	h:=NewPlatformAdminHandler(db,true,"bootstrap-secret-for-tests")
	mux:=http.NewServeMux();h.Register(mux)
	loginReq:=httptest.NewRequest(http.MethodPost,"/api/platform/login",strings.NewReader(fmt.Sprintf(`{"username":%q,"password":"Platform-Password-2026!"}`,username)))
	loginRec:=httptest.NewRecorder();mux.ServeHTTP(loginRec,loginReq)
	if loginRec.Code!=http.StatusOK{t.Fatalf("login plataforma: status=%d body=%s",loginRec.Code,loginRec.Body.String())}
	cookies:=loginRec.Result().Cookies();var session *http.Cookie
	for _,c:=range cookies{if c.Name==platformSessionCookie{session=c}}
	if session==nil{t.Fatal("cookie de sessão da plataforma não foi emitido")}
	request:=func(method,path,body string)*httptest.ResponseRecorder{
		req:=httptest.NewRequest(method,path,strings.NewReader(body));req.AddCookie(session)
		rec:=httptest.NewRecorder();mux.ServeHTTP(rec,req);return rec
	}
	planCode:="test-"+suffix[len(suffix)-8:]
	plan:=request(http.MethodPost,"/api/platform/plans",fmt.Sprintf(`{"code":%q,"name":"Plano de Teste"}`,planCode))
	if plan.Code!=http.StatusCreated{t.Fatalf("criar plano: status=%d body=%s",plan.Code,plan.Body.String())}
	document:="9"+suffix[len(suffix)-10:]
	company:=request(http.MethodPost,"/api/platform/tenants",fmt.Sprintf(`{"legal_name":"Empresa Teste","display_name":"Empresa Teste","document":%q,"owner_username":"owner-%s","owner_name":"Proprietário Teste","owner_password":"Owner-Password-2026!","plan_code":%q,"seat_limit":3}`,document,suffix,planCode))
	if company.Code!=http.StatusCreated{t.Fatalf("criar empresa: status=%d body=%s",company.Code,company.Body.String())}
	list:=request(http.MethodGet,"/api/platform/tenants","")
	if list.Code!=http.StatusOK||!strings.Contains(list.Body.String(),"Empresa Teste"){t.Fatalf("listar empresas: status=%d body=%s",list.Code,list.Body.String())}
}
