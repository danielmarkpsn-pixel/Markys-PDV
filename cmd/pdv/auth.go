package main

import (
	"database/sql"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type sessionInfo struct { userID int64; companyID int64; expires time.Time }
var sessionStore = struct { sync.Mutex; items map[string]sessionInfo }{items: map[string]sessionInfo{}}

type authUser struct {
	ID int64 `json:"id"`
	Username string `json:"username"`
	DisplayName string `json:"display_name"`
	Role string `json:"role"`
	Active bool `json:"active"`
	CreatedAt string `json:"created_at"`
	CompanyID int64 `json:"company_id"`
	GlobalAdmin bool `json:"global_admin"`
}

func passwordHash(password string) (string, error) {
	if len(password) < 10 || len(password) > 256 { return "", errors.New("A senha deve ter entre 10 e 256 caracteres") }
	salt:=make([]byte,16); if _,err:=rand.Read(salt);err!=nil{return "",err}
	const iterations=210000
	u:=pbkdf2SHA256([]byte(password),salt,iterations,32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s",iterations,base64.RawStdEncoding.EncodeToString(salt),base64.RawStdEncoding.EncodeToString(u)),nil
}
func pbkdf2SHA256(password,salt []byte,iterations,keyLen int) []byte {
	out:=make([]byte,0,keyLen)
	for block:=uint32(1);len(out)<keyLen;block++ {
		mac:=hmac.New(sha256.New,password); _,_=mac.Write(salt); _,_=mac.Write([]byte{byte(block>>24),byte(block>>16),byte(block>>8),byte(block)})
		u:=mac.Sum(nil); t:=append([]byte(nil),u...)
		for i:=1;i<iterations;i++ { mac=hmac.New(sha256.New,password); _,_=mac.Write(u); u=mac.Sum(nil); for j:=range t {t[j]^=u[j]} }
		out=append(out,t...)
	}
	return out[:keyLen]
}
func verifyPassword(password,encoded string) bool {
	parts:=strings.Split(encoded,"$"); if len(parts)!=4||parts[0]!="pbkdf2-sha256"{return false}
	iterations,err:=strconv.Atoi(parts[1]); if err!=nil||iterations<100000||iterations>1000000{return false}
	salt,err:=base64.RawStdEncoding.DecodeString(parts[2]);if err!=nil||len(salt)<16{return false}
	expected,err:=base64.RawStdEncoding.DecodeString(parts[3]);if err!=nil||len(expected)!=32{return false}
	actual:=pbkdf2SHA256([]byte(password),salt,iterations,len(expected))
	return hmac.Equal(actual,expected)
}
func (a *App) authStatus(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodGet {http.Error(w,"Method not allowed",405);return}
	rawCompanyID:=r.URL.Query().Get("company_id");id,err:=parseCompanyID(rawCompanyID);if err!=nil{http.Error(w,err.Error(),400);return};if rawCompanyID==""{if cookie,e:=r.Cookie("markys_session");e==nil{sessionStore.Lock();session,ok:=sessionStore.items[cookie.Value];sessionStore.Unlock();if ok{id=session.companyID}}}
	app,err:=a.appForCompany(id);if err!=nil{http.Error(w,err.Error(),403);return}
	var count int
	if err:=app.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count);err!=nil{http.Error(w,"Database error",500);return}
	current:=app.currentUser(r)
	jsonOut(w,200,map[string]any{"setup_required":count==0,"authenticated":current!=nil,"user":current,"company_id":id})
}
func (a *App) authSetup(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodPost {http.Error(w,"Method not allowed",405);return}
	var req struct{CompanyID int64 `json:"company_id"`;Username string `json:"username"`;DisplayName string `json:"display_name"`;Password string `json:"password"`}
	if !decodeJSON(w,r,&req){return}
	if req.CompanyID>0{masterUser:=a.currentUser(r);if masterUser==nil||masterUser.Role!="admin"{http.Error(w,"Somente o administrador geral pode configurar o primeiro acesso desta empresa",403);return}}
	app,err:=a.appForCompany(req.CompanyID);if err!=nil{http.Error(w,err.Error(),403);return}
	var count int;if err:=app.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count);err!=nil{http.Error(w,"Database error",500);return};if count!=0{http.Error(w,"A configuração inicial desta empresa já foi concluída",409);return}
	req.Username=strings.TrimSpace(req.Username);req.DisplayName=strings.TrimSpace(req.DisplayName)
	if len(req.Username)<3||len(req.Username)>64||req.DisplayName==""||len(req.DisplayName)>120{http.Error(w,"Informe usuário (3 a 64 caracteres) e nome",400);return}
	hash,err:=passwordHash(req.Password);if err!=nil{http.Error(w,err.Error(),400);return}
	res,err:=app.db.Exec("INSERT INTO users(username,password_hash,display_name,role,active) VALUES(?,?,?,'admin',1)",req.Username,hash,req.DisplayName)
	if err!=nil{http.Error(w,"Não foi possível criar administrador; o usuário pode já existir",409);return}
	id,_:=res.LastInsertId();app.writeAudit(id,"initial_admin_created","user",strconv.FormatInt(id,10),"Administrador inicial configurado")
	app.createSession(w,id,req.CompanyID)
	jsonOut(w,201,map[string]any{"ok":true,"user":authUser{ID:id,Username:req.Username,DisplayName:req.DisplayName,Role:"admin",Active:true,CompanyID:req.CompanyID,GlobalAdmin:req.CompanyID==0}})
}
// authRecoverAdmin is an offline recovery path restricted to requests made on this computer.
func (a *App) authRecoverAdmin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { w.Header().Set("Allow", "POST"); http.Error(w, "Método não permitido", 405); return }
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil { host = strings.Trim(strings.TrimSpace(r.RemoteAddr), "[]") }
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() { http.Error(w, "A recuperação administrativa só pode ser feita neste computador.", http.StatusForbidden); return }
	var req struct {
		CompanyID int64 `json:"company_id"`
		Username string `json:"username"`
		DisplayName string `json:"display_name"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) { return }
	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if len(req.Username) < 3 || len(req.Username) > 64 { http.Error(w, "Informe o usuário administrador (3 a 64 caracteres).", 400); return }
	app, err := a.appForCompany(req.CompanyID)
	if err != nil { http.Error(w, err.Error(), 403); return }
	hash, err := passwordHash(req.Password)
	if err != nil { http.Error(w, err.Error(), 400); return }
	var id int64
	err = app.db.QueryRow("SELECT id FROM users WHERE username=? COLLATE NOCASE AND role='admin'", req.Username).Scan(&id)
	if err == nil {
		if _, err = app.db.Exec("UPDATE users SET password_hash=?,active=1 WHERE id=? AND role='admin'", hash, id); err != nil { http.Error(w, "Não foi possível redefinir a senha do administrador.", 500); return }
	} else if err == sql.ErrNoRows {
		var admins int
		if e := app.db.QueryRow("SELECT COUNT(*) FROM users WHERE role='admin'").Scan(&admins); e != nil { http.Error(w, "Não foi possível verificar os administradores.", 500); return }
		if admins > 0 { http.Error(w, "Já existe administrador. Informe o nome de usuário correto; nenhum usuário foi alterado.", 409); return }
		if req.DisplayName == "" || len(req.DisplayName) > 120 { http.Error(w, "Informe o nome completo para criar o primeiro administrador.", 400); return }
		res, e := app.db.Exec("INSERT INTO users(username,password_hash,display_name,role,active) VALUES(?,?,?,'admin',1)", req.Username, hash, req.DisplayName)
		if e != nil { http.Error(w, "Não foi possível criar o administrador; confira se o usuário já existe.", 409); return }
		id, err = res.LastInsertId()
		if err != nil { http.Error(w, "Não foi possível confirmar a criação do administrador.", 500); return }
	} else { http.Error(w, "Não foi possível localizar a conta administrativa.", 500); return }
	app.writeAudit(id, "admin_password_recovered", "user", strconv.FormatInt(id, 10), "Acesso administrativo recuperado localmente")
	app.createSession(w, id, req.CompanyID)
	jsonOut(w, 200, map[string]any{"ok": true, "message": "Acesso administrativo recuperado."})
}

func (a *App) authLogin(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodPost {http.Error(w,"Method not allowed",405);return}
	var req struct{CompanyID int64 `json:"company_id"`;Username string `json:"username"`;Password string `json:"password"`}
	if !decodeJSON(w,r,&req){return}
	app,err:=a.appForCompany(req.CompanyID);if err!=nil{http.Error(w,err.Error(),403);return}
	var u authUser;var hash string;var active int
	err=app.db.QueryRow("SELECT id,username,display_name,role,active,created_at,password_hash FROM users WHERE username=? COLLATE NOCASE",strings.TrimSpace(req.Username)).Scan(&u.ID,&u.Username,&u.DisplayName,&u.Role,&active,&u.CreatedAt,&hash)
	if err!=nil||active!=1||!verifyPassword(req.Password,hash){http.Error(w,"Usuário ou senha inválidos, ou usuário inativo",401);return}
	u.Active=true;u.CompanyID=req.CompanyID;u.GlobalAdmin=(u.Role=="admin"&&req.CompanyID==0);app.createSession(w,u.ID,req.CompanyID);app.writeAudit(u.ID,"login","user",strconv.FormatInt(u.ID,10),"Login realizado")
	jsonOut(w,200,map[string]any{"ok":true,"user":u})
}
func (a *App) authManagerVerify(w http.ResponseWriter,r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w,"Method not allowed",405); return }
	var req struct { Username string `json:"username"`; Password string `json:"password"` }
	if !decodeJSON(w,r,&req) { return }
	var id int64; var hash,role string; var active int
	err:=a.db.QueryRow("SELECT id,password_hash,role,active FROM users WHERE username=? COLLATE NOCASE",strings.TrimSpace(req.Username)).Scan(&id,&hash,&role,&active)
	if err!=nil || active!=1 || (role!="manager" && role!="admin") || !verifyPassword(req.Password,hash) { http.Error(w,"Credenciais de gerente inválidas",401); return }
	a.writeAudit(requestUserID(a,r),"manager_item_cancel_authorized","sale_item","", "Cancelamento de item autorizado por gerente")
	jsonOut(w,200,map[string]any{"ok":true})
}
func (a *App) authLogout(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodPost {http.Error(w,"Method not allowed",405);return}
	if c,err:=r.Cookie("markys_session");err==nil{sessionStore.Lock();delete(sessionStore.items,c.Value);sessionStore.Unlock()}
	http.SetCookie(w,&http.Cookie{Name:"markys_session",Value:"",Path:"/",HttpOnly:true,SameSite:http.SameSiteStrictMode,MaxAge:-1})
	jsonOut(w,200,map[string]any{"ok":true})
}
func (a *App) createSession(w http.ResponseWriter,userID int64,companyID int64) {
	raw:=make([]byte,32);if _,err:=rand.Read(raw);err!=nil{return};token:=base64.RawURLEncoding.EncodeToString(raw);expires:=time.Now().Add(12*time.Hour)
	sessionStore.Lock();sessionStore.items[token]=sessionInfo{userID:userID,companyID:companyID,expires:expires};sessionStore.Unlock()
	http.SetCookie(w,&http.Cookie{Name:"markys_session",Value:token,Path:"/",HttpOnly:true,SameSite:http.SameSiteStrictMode,Expires:expires,MaxAge:43200})
}
func (a *App) currentUser(r *http.Request) *authUser {
	c,err:=r.Cookie("markys_session");if err!=nil{return nil}
	sessionStore.Lock();s,ok:=sessionStore.items[c.Value];if ok&&time.Now().After(s.expires){delete(sessionStore.items,c.Value);ok=false};sessionStore.Unlock();if !ok||s.companyID!=a.companyID{return nil}
	var u authUser;var active int
	if err:=a.db.QueryRow("SELECT id,username,display_name,role,active,created_at FROM users WHERE id=?",s.userID).Scan(&u.ID,&u.Username,&u.DisplayName,&u.Role,&active,&u.CreatedAt);err!=nil||active!=1{return nil}
	u.Active=true;u.CompanyID=s.companyID;u.GlobalAdmin=(u.Role=="admin"&&s.companyID==0);return &u
}
func (a *App) users(w http.ResponseWriter,r *http.Request) {
	u:=a.currentUser(r);if u==nil||u.Role!="admin"{http.Error(w,"Acesso negado",403);return}
	switch r.Method {
	case http.MethodGet:
		rows,err:=a.db.Query("SELECT id,username,display_name,role,active,created_at FROM users ORDER BY id");if err!=nil{http.Error(w,"Database error",500);return};defer rows.Close()
		out:=[]authUser{};for rows.Next(){var v authUser;var active int;if err:=rows.Scan(&v.ID,&v.Username,&v.DisplayName,&v.Role,&active,&v.CreatedAt);err!=nil{http.Error(w,"Read error",500);return};v.Active=active==1;out=append(out,v)};if err:=rows.Err();err!=nil{http.Error(w,"Read error",500);return};jsonOut(w,200,out)
	case http.MethodPost:
		var req struct{Username string `json:"username"`;DisplayName string `json:"display_name"`;Role string `json:"role"`;Password string `json:"password"`}
		if !decodeJSON(w,r,&req){return};req.Username=strings.TrimSpace(req.Username);req.DisplayName=strings.TrimSpace(req.DisplayName)
		if len(req.Username)<3||len(req.Username)>64||req.DisplayName==""||len(req.DisplayName)>120{http.Error(w,"Informe usuário e nome válidos",400);return}
		if req.Role!="admin"&&req.Role!="manager"&&req.Role!="cashier"&&req.Role!="stockkeeper"{http.Error(w,"Perfil inválido",400);return}
		hash,err:=passwordHash(req.Password);if err!=nil{http.Error(w,err.Error(),400);return}
		res,err:=a.db.Exec("INSERT INTO users(username,password_hash,display_name,role,active) VALUES(?,?,?,?,1)",req.Username,hash,req.DisplayName,req.Role);if err!=nil{http.Error(w,"Não foi possível criar usuário; nome pode já existir",409);return}
		id,_:=res.LastInsertId();a.writeAudit(u.ID,"user_created","user",strconv.FormatInt(id,10),"Criado usuário "+req.Username+" com perfil "+req.Role);jsonOut(w,201,map[string]any{"id":id,"username":req.Username,"display_name":req.DisplayName,"role":req.Role,"active":true})
	default:w.Header().Set("Allow","GET, POST");http.Error(w,"Method not allowed",405)
	}
}
func (a *App) userStatus(w http.ResponseWriter,r *http.Request) {
	u:=a.currentUser(r);if u==nil||u.Role!="admin"{http.Error(w,"Acesso negado",403);return};if r.Method!=http.MethodPatch{w.Header().Set("Allow","PATCH");http.Error(w,"Method not allowed",405);return}
	id,err:=strconv.ParseInt(r.URL.Query().Get("id"),10,64);if err!=nil||id<=0{http.Error(w,"ID inválido",400);return}
	var req struct{Active bool `json:"active"`};if !decodeJSON(w,r,&req){return}
	if id==u.ID&&!req.Active{http.Error(w,"Não é possível desativar a própria conta",409);return}
	res,err:=a.db.Exec("UPDATE users SET active=? WHERE id=?",boolInt(req.Active),id);if err!=nil{http.Error(w,"Database error",500);return};n,_:=res.RowsAffected();if n==0{http.Error(w,"Usuário não encontrado",404);return}
	a.writeAudit(u.ID,"user_status_changed","user",strconv.FormatInt(id,10),fmt.Sprintf("active=%t",req.Active));jsonOut(w,200,map[string]any{"ok":true,"id":id,"active":req.Active})
}
func (a *App) userRole(w http.ResponseWriter,r *http.Request) {
	u:=a.currentUser(r)
	if u==nil||u.Role!="admin" { http.Error(w,"Acesso negado",403);return }
	if r.Method!=http.MethodPatch { w.Header().Set("Allow","PATCH");http.Error(w,"Método não permitido",405);return }
	id,err:=strconv.ParseInt(r.URL.Query().Get("id"),10,64)
	if err!=nil||id<=0 { http.Error(w,"ID inválido",400);return }
	var req struct { Role string `json:"role"` }
	if !decodeJSON(w,r,&req) { return }
	if req.Role!="admin"&&req.Role!="manager"&&req.Role!="cashier"&&req.Role!="stockkeeper" { http.Error(w,"Perfil inválido",400);return }
	if id==u.ID&&req.Role!="admin" { http.Error(w,"Não é permitido remover o próprio perfil de administrador",409);return }
	var oldRole string
	var active int
	if err:=a.db.QueryRow("SELECT role,active FROM users WHERE id=?",id).Scan(&oldRole,&active);err!=nil { http.Error(w,"Usuário não encontrado",404);return }
	if active==1&&oldRole=="admin"&&req.Role!="admin" {
		var admins int
		if err:=a.db.QueryRow("SELECT COUNT(*) FROM users WHERE role='admin' AND active=1").Scan(&admins);err!=nil { http.Error(w,"Não foi possível validar administradores ativos",500);return }
		if admins<=1 { http.Error(w,"A empresa precisa manter pelo menos um administrador ativo",409);return }
	}
	res,err:=a.db.Exec("UPDATE users SET role=? WHERE id=?",req.Role,id)
	if err!=nil { http.Error(w,"Não foi possível alterar o perfil",500);return }
	n,_:=res.RowsAffected();if n==0 { http.Error(w,"Usuário não encontrado",404);return }
	a.writeAudit(u.ID,"user_role_changed","user",strconv.FormatInt(id,10),fmt.Sprintf("Perfil alterado de %s para %s",oldRole,req.Role))
	jsonOut(w,200,map[string]any{"ok":true,"id":id,"role":req.Role})
}

func (a *App) userPasswordReset(w http.ResponseWriter,r *http.Request) {
	u:=a.currentUser(r)
	if u==nil||u.Role!="admin" { http.Error(w,"Acesso negado",403);return }
	if r.Method!=http.MethodPatch { w.Header().Set("Allow","PATCH");http.Error(w,"Método não permitido",405);return }
	id,err:=strconv.ParseInt(r.URL.Query().Get("id"),10,64)
	if err!=nil||id<=0 { http.Error(w,"ID inválido",400);return }
	var req struct { Password string `json:"password"` }
	if !decodeJSON(w,r,&req) { return }
	hash,err:=passwordHash(req.Password);if err!=nil { http.Error(w,err.Error(),400);return }
	var username string
	if err:=a.db.QueryRow("SELECT username FROM users WHERE id=?",id).Scan(&username);err!=nil { http.Error(w,"Usuário não encontrado",404);return }
	if _,err:=a.db.Exec("UPDATE users SET password_hash=? WHERE id=?",hash,id);err!=nil { http.Error(w,"Não foi possível redefinir a senha",500);return }
	exceptToken:=""
	if cookie,err:=r.Cookie("markys_session");err==nil { exceptToken=cookie.Value }
	sessionStore.Lock()
	for token,session:=range sessionStore.items { if session.userID==id&&token!=exceptToken { delete(sessionStore.items,token) } }
	sessionStore.Unlock()
	a.writeAudit(u.ID,"user_password_reset","user",strconv.FormatInt(id,10),"Senha redefinida por administrador para "+username+"; demais sessões encerradas")
	jsonOut(w,200,map[string]any{"ok":true,"id":id})
}

func requestUserID(a *App,r *http.Request) int64 { if u:=a.currentUser(r);u!=nil{return u.ID};return 0 }
func nullableUserID(id int64) any { if id<=0{return nil};return id }
func (a *App) writeAudit(userID int64,action,entity,id,details string) { _,_=a.db.Exec("INSERT INTO audit_log(user_id,action,entity_type,entity_id,details) VALUES(?,?,?,?,?)",nullableUserID(userID),action,entity,id,details) }
func boolInt(v bool)int{if v{return 1};return 0}
func decodeJSON(w http.ResponseWriter,r *http.Request,v any)bool{r.Body=http.MaxBytesReader(w,r.Body,1<<20);d:=json.NewDecoder(r.Body);d.DisallowUnknownFields();if err:=d.Decode(v);err!=nil{http.Error(w,"JSON inválido",400);return false};if err:=d.Decode(&struct{}{});err!=io.EOF{http.Error(w,"JSON inválido",400);return false};return true}

func (a *App) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		p:=r.URL.Path
		if !strings.HasPrefix(p,"/api/")||p=="/api/health"||p=="/api/auth/status"||p=="/api/auth/companies"||p=="/api/auth/setup"||p=="/api/auth/login"||p=="/api/auth/recover-admin"||p=="/api/auth/logout"||p=="/api/auth/manager-verify"{next.ServeHTTP(w,r);return}
		u:=a.currentUser(r);if u==nil{http.Error(w,"Autenticação necessária",http.StatusUnauthorized);return}
		if p=="/api/company/branding"&&r.Method==http.MethodGet{next.ServeHTTP(w,r);return}
		if strings.HasPrefix(p,"/api/admin/")||p=="/api/users"||strings.HasPrefix(p,"/api/users/"){if u.Role!="admin"{http.Error(w,"Acesso negado",403);return};next.ServeHTTP(w,r);return}
		switch u.Role {
		case "admin","manager": next.ServeHTTP(w,r)
		case "cashier":
			if p=="/api/cash"&&(r.Method==http.MethodGet||r.Method==http.MethodPost)||p=="/api/sales"&&r.Method==http.MethodPost||p=="/api/products"&&r.Method==http.MethodGet||p=="/api/categories"&&r.Method==http.MethodGet||p=="/api/suppliers"&&r.Method==http.MethodGet||p=="/api/customers"&&r.Method==http.MethodGet||p=="/api/quotes" {next.ServeHTTP(w,r);return};http.Error(w,"Seu perfil não permite esta operação",403)
		case "stockkeeper":
			if p=="/api/products"||p=="/api/categories"||p=="/api/suppliers"||p=="/api/stock-movements"||p=="/api/reports/stock.csv"||p=="/api/reports/movements.csv" {next.ServeHTTP(w,r);return};http.Error(w,"Seu perfil não permite esta operação",403)
		default:http.Error(w,"Perfil sem permissões",403)
		}
	})
}
