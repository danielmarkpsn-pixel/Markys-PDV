package saas

import (
 "context"
 "fmt"
 "errors"
 "net/http"
 "net/http/httptest"
 "os"
 "strings"
 "testing"
 "time"
)

func TestTenantAdminHTTPIntegration(t *testing.T) {
 dsn:=os.Getenv("DATABASE_URL");if dsn==""{t.Skip("DATABASE_URL não configurada; teste HTTP PostgreSQL ignorado")}
 cfg,err:=LoadConfig(func(key string)string{if key=="SAAS_MODE"{return "postgres"};if key=="DATABASE_URL"{return dsn};if key=="DB_CONNECT_TIMEOUT"{return "10s"};return ""})
 if err!=nil{t.Fatal(err)}
 ctx,cancel:=context.WithTimeout(context.Background(),45*time.Second);defer cancel()
 db,err:=OpenPostgres(ctx,cfg);if err!=nil{t.Fatal(err)};defer db.Close()
 if err:=ApplyMigrations(ctx,db);err!=nil{t.Fatal(err)}
 document:=fmt.Sprintf("ADMIN-HTTP-%d",time.Now().UnixNano())
 var tenantID,ownerID string
 if err:=db.QueryRowContext(ctx,"INSERT INTO tenants(legal_name,display_name,document) VALUES('Admin HTTP Test','Admin HTTP Test',$1) RETURNING id::text",document).Scan(&tenantID);err!=nil{t.Fatal(err)}
 if _,err:=db.ExecContext(ctx,"INSERT INTO tenant_subscriptions(tenant_id,status,seat_limit) VALUES($1::uuid,'active',2)",tenantID);err!=nil{t.Fatal(err)}
 ownerHash,err:=HashPassword("Owner-Password-2026!");if err!=nil{t.Fatal(err)}
 if err:=db.QueryRowContext(ctx,"INSERT INTO tenant_users(tenant_id,username,display_name,password_hash,role) VALUES($1::uuid,'owner','Owner',$2,'owner') RETURNING id::text",tenantID,ownerHash).Scan(&ownerID);err!=nil{t.Fatal(err)}
 token,_,err:=CreateLoginSession(ctx,db,tenantID,ownerID,"integration-test","127.0.0.1",time.Now().Add(time.Hour));if err!=nil{t.Fatal(err)}
 auth:=NewHTTPHandler(db,true);mux:=http.NewServeMux();auth.Register(mux);NewTenantAdminHandler(db,auth).Register(mux)
 request:=func(method,path,body string)*httptest.ResponseRecorder{
  req:=httptest.NewRequest(method,path,strings.NewReader(body));req.AddCookie(&http.Cookie{Name:SessionCookieName,Value:token})
  rec:=httptest.NewRecorder();mux.ServeHTTP(rec,req);return rec
 }
 created:=request(http.MethodPost,"/api/admin/users",`{"username":"cashier1","display_name":"Operador 1","password":"Cashier-Password-2026!","role":"cashier"}`)
 if created.Code!=http.StatusCreated{t.Fatalf("criar usuário: status=%d body=%s",created.Code,created.Body.String())}
 var cashierID string
 if err:=db.QueryRowContext(ctx,"SELECT id::text FROM tenant_users WHERE tenant_id=$1::uuid AND username='cashier1'",tenantID).Scan(&cashierID);err!=nil{t.Fatal(err)}
 cashierToken,_,err:=CreateLoginSession(ctx,db,tenantID,cashierID,"cashier-device","127.0.0.2",time.Now().Add(time.Hour))
 if err!=nil{t.Fatal(err)}
 duplicateSeat:=request(http.MethodPost,"/api/admin/users",`{"username":"cashier2","display_name":"Operador 2","password":"Cashier-Password-2026!","role":"cashier"}`)
 if duplicateSeat.Code!=http.StatusConflict{t.Fatalf("limite de assentos: status=%d body=%s",duplicateSeat.Code,duplicateSeat.Body.String())}
 deactivated:=request(http.MethodPut,"/api/admin/users/"+cashierID,`{"active":false}`)
 if deactivated.Code!=http.StatusOK{t.Fatalf("desativar usuário: status=%d body=%s",deactivated.Code,deactivated.Body.String())}
 if _,err:=ValidateLoginSession(ctx,db,cashierToken);!errors.Is(err,ErrInvalidSession){t.Fatalf("sessão deve ser revogada ao desativar usuário, erro recebido: %v",err)}
 reactivated:=request(http.MethodPut,"/api/admin/users/"+cashierID,`{"active":true}`)
 if reactivated.Code!=http.StatusOK{t.Fatalf("reativar usuário: status=%d body=%s",reactivated.Code,reactivated.Body.String())}
 if _,err:=ValidateLoginSession(ctx,db,cashierToken);!errors.Is(err,ErrInvalidSession){t.Fatalf("sessão antiga não pode voltar após reativação, erro recebido: %v",err)}
 cashierToken,_,err=CreateLoginSession(ctx,db,tenantID,cashierID,"cashier-device-2","127.0.0.2",time.Now().Add(time.Hour))
 if err!=nil{t.Fatal(err)}
 passwordReset:=request(http.MethodPut,"/api/admin/users/"+cashierID,`{"password":"Cashier-New-Password-2026!"}`)
 if passwordReset.Code!=http.StatusOK{t.Fatalf("redefinir senha: status=%d body=%s",passwordReset.Code,passwordReset.Body.String())}
 if _,err:=ValidateLoginSession(ctx,db,cashierToken);!errors.Is(err,ErrInvalidSession){t.Fatalf("sessão deve ser revogada ao redefinir senha, erro recebido: %v",err)}
 list:=request(http.MethodGet,"/api/admin/users","")
 if list.Code!=http.StatusOK||!strings.Contains(list.Body.String(),"cashier1"){t.Fatalf("listar usuários: status=%d body=%s",list.Code,list.Body.String())}
 license:=request(http.MethodGet,"/api/admin/license","")
 if license.Code!=http.StatusOK||!strings.Contains(license.Body.String(),`"contracted_logins":2`)||!strings.Contains(license.Body.String(),`"active_logins":2`){t.Fatalf("consultar licença: status=%d body=%s",license.Code,license.Body.String())}
}
