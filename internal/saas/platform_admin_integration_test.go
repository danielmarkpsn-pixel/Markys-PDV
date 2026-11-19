package saas

import (
	"context"
	"fmt"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
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
	// Exercise platform endpoints through a non-owner, non-BYPASSRLS runtime role.
	roleName:="markys_platform_test_"+suffix
	rolePassword:="Markys-Platform-Test-Only-2026!"
	if _,err=db.ExecContext(ctx,fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOBYPASSRLS",roleName,rolePassword));err!=nil{t.Fatal(err)}
	t.Cleanup(func(){_,_=db.ExecContext(context.Background(),"DROP OWNED BY "+roleName);_,_=db.ExecContext(context.Background(),"DROP ROLE "+roleName)})
	grants:=[]string{
		"GRANT USAGE ON SCHEMA public TO "+roleName,
		"GRANT SELECT,INSERT,UPDATE,DELETE ON tenants,tenant_subscriptions,tenant_users,tenant_sessions,tenant_audit_events,saas_plans,platform_admins,platform_admin_sessions TO "+roleName,
		"GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO "+roleName,
	}
	for _,statement:=range grants{if _,err=db.ExecContext(ctx,statement);err!=nil{t.Fatal(err)}}
	pgxConfig,err:=pgx.ParseConfig(dsn);if err!=nil{t.Fatal(err)}
	pgxConfig.User=roleName;pgxConfig.Password=rolePassword
	appDB:=stdlib.OpenDB(*pgxConfig);appDB.SetMaxOpenConns(4);defer appDB.Close()
	if err=appDB.PingContext(ctx);err!=nil{t.Fatal(err)}
	username:="platform-test-"+suffix
	hash,err:=HashPassword("Platform-Password-2026!");if err!=nil{t.Fatal(err)}
	var adminID string
	if err=db.QueryRowContext(ctx,"INSERT INTO platform_admins(username,display_name,password_hash) VALUES($1,'Platform Test',$2) RETURNING id::text",username,hash).Scan(&adminID);err!=nil{t.Fatal(err)}
	h:=NewPlatformAdminHandler(appDB,true,"bootstrap-secret-for-tests")
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
	if company.Code!=http.StatusCreated{t.Fatalf("criar empresa com runtime RLS: status=%d body=%s",company.Code,company.Body.String())}
	var companyResult struct{TenantID string `json:"tenant_id"`;OwnerUserID string `json:"owner_user_id"`}
	if err:=json.Unmarshal(company.Body.Bytes(),&companyResult);err!=nil||companyResult.TenantID==""||companyResult.OwnerUserID==""{t.Fatalf("resposta de empresa inválida: %v %s",err,company.Body.String())}
	list:=request(http.MethodGet,"/api/platform/tenants","")
	if list.Code!=http.StatusOK||!strings.Contains(list.Body.String(),"Empresa Teste")||!strings.Contains(list.Body.String(),`"active_users":1`){t.Fatalf("listar empresas e contar vagas com RLS: status=%d body=%s",list.Code,list.Body.String())}
	// A second active account must be counted even though the runtime role is subject to tenant_users RLS.
	cashierHash,err:=HashPassword("Cashier-Platform-Test-2026!");if err!=nil{t.Fatal(err)}
	if _,err=db.ExecContext(ctx,"INSERT INTO tenant_users(tenant_id,username,display_name,password_hash,role) VALUES($1::uuid,'cashier','Cashier',$2,'cashier')",companyResult.TenantID,cashierHash);err!=nil{t.Fatal(err)}
	tooSmall:=request(http.MethodPut,"/api/platform/tenants/"+companyResult.TenantID,`{"seat_limit":1}`)
	if tooSmall.Code!=http.StatusConflict{t.Fatalf("redução abaixo dos logins ativos deveria ser bloqueada: status=%d body=%s",tooSmall.Code,tooSmall.Body.String())}
	ownerToken,_,err:=CreateLoginSession(ctx,appDB,companyResult.TenantID,companyResult.OwnerUserID,"platform-owner-test","127.0.0.3",time.Now().Add(time.Hour))
	if err!=nil{t.Fatal(err)}
	suspend:=request(http.MethodPut,"/api/platform/tenants/"+companyResult.TenantID,`{"status":"suspended"}`)
	if suspend.Code!=http.StatusOK{t.Fatalf("suspender empresa: status=%d body=%s",suspend.Code,suspend.Body.String())}
	var revoked bool
	if err=db.QueryRowContext(ctx,"SELECT revoked_at IS NOT NULL FROM tenant_sessions WHERE tenant_id=$1::uuid AND user_id=$2::uuid",companyResult.TenantID,companyResult.OwnerUserID).Scan(&revoked);err!=nil{t.Fatal(err)}
	if !revoked{t.Fatal("suspensão deveria revogar permanentemente a sessão da empresa")}
	reactivate:=request(http.MethodPut,"/api/platform/tenants/"+companyResult.TenantID,`{"status":"active"}`)
	if reactivate.Code!=http.StatusOK{t.Fatalf("reativar empresa: status=%d body=%s",reactivate.Code,reactivate.Body.String())}
	if _,err:=ValidateLoginSession(ctx,appDB,ownerToken);!errors.Is(err,ErrInvalidSession){t.Fatalf("sessão anterior não pode voltar após reativação da empresa: %v",err)}
	var auditCount int
	if err=db.QueryRowContext(ctx,"SELECT count(*) FROM tenant_audit_events WHERE tenant_id=$1::uuid AND event_type IN ('platform.tenant.created','platform.tenant.updated')",companyResult.TenantID).Scan(&auditCount);err!=nil{t.Fatal(err)}
	if auditCount!=3{t.Fatalf("esperava auditoria de criação, suspensão e reativação; recebeu %d",auditCount)}
}
