package saas

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const platformSessionCookie = "markys_platform_session"

type platformAdminContextKey struct{}

func platformAdminIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(platformAdminContextKey{}).(string)
	return id
}

type PlatformAdminHandler struct {
	DB *sql.DB
	CookieSecure bool
	BootstrapToken string
	SessionTTL time.Duration
}

func NewPlatformAdminHandler(db *sql.DB, secure bool, bootstrapToken string) *PlatformAdminHandler {
	return &PlatformAdminHandler{DB: db, CookieSecure: secure, BootstrapToken: strings.TrimSpace(bootstrapToken), SessionTTL: 8*time.Hour}
}

func (h *PlatformAdminHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/platform/bootstrap", h.bootstrap)
	mux.HandleFunc("/api/platform/login", h.login)
	mux.HandleFunc("/api/platform/logout", h.logout)
	mux.Handle("/api/platform/status", h.requireSession(http.HandlerFunc(h.status)))
	mux.Handle("/api/platform/plans", h.requireSession(http.HandlerFunc(h.plans)))
	mux.Handle("/api/platform/tenants", h.requireSession(http.HandlerFunc(h.tenants)))
	mux.Handle("/api/platform/tenants/", h.requireSession(http.HandlerFunc(h.tenantByID)))
}

func (h *PlatformAdminHandler) bootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { w.Header().Set("Allow", http.MethodPost); writeJSON(w,405,map[string]string{"error":"Método não permitido"}); return }
	if h.BootstrapToken == "" { writeJSON(w,503,map[string]string{"error":"Provisionamento inicial desabilitado: configure SAAS_PLATFORM_BOOTSTRAP_TOKEN"}); return }
	provided := strings.TrimSpace(r.Header.Get("X-Platform-Bootstrap-Token"))
	if len(provided) != len(h.BootstrapToken) || subtle.ConstantTimeCompare([]byte(provided),[]byte(h.BootstrapToken)) != 1 {
		writeJSON(w,401,map[string]string{"error":"Token de provisionamento inválido"}); return
	}
	var input struct { Username string `json:"username"`; DisplayName string `json:"display_name"`; Password string `json:"password"` }
	if !decodeAuthJSON(w,r,&input) { return }
	input.Username=strings.TrimSpace(input.Username); input.DisplayName=strings.TrimSpace(input.DisplayName)
	if input.Username=="" || input.DisplayName=="" { writeJSON(w,400,map[string]string{"error":"Informe usuário e nome do administrador"}); return }
	hash,err:=HashPassword(input.Password); if err!=nil { writeJSON(w,400,map[string]string{"error":err.Error()}); return }
	tx,err:=h.DB.BeginTx(r.Context(),nil); if err!=nil { writeJSON(w,500,map[string]string{"error":"Não foi possível iniciar o provisionamento"}); return }; defer tx.Rollback()
	if _,err=tx.ExecContext(r.Context(),"SELECT pg_advisory_xact_lock(73190420261119)"); err!=nil { writeJSON(w,500,map[string]string{"error":"Não foi possível proteger o provisionamento"}); return }
	var count int
	if err=tx.QueryRowContext(r.Context(),"SELECT count(*) FROM platform_admins").Scan(&count); err!=nil { writeJSON(w,500,map[string]string{"error":"Não foi possível verificar o administrador inicial"}); return }
	if count>0 { writeJSON(w,409,map[string]string{"error":"O administrador inicial já foi configurado"}); return }
	_,err=tx.ExecContext(r.Context(),"INSERT INTO platform_admins(username,display_name,password_hash) VALUES($1,$2,$3)",input.Username,input.DisplayName,hash)
	if err!=nil { writeJSON(w,409,map[string]string{"error":"Não foi possível criar o administrador; verifique se o usuário já existe"}); return }
	if err=tx.Commit(); err!=nil { writeJSON(w,500,map[string]string{"error":"Não foi possível concluir o provisionamento"}); return }
	writeJSON(w,201,map[string]bool{"ok":true})
}

func (h *PlatformAdminHandler) login(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodPost { w.Header().Set("Allow",http.MethodPost); writeJSON(w,405,map[string]string{"error":"Método não permitido"}); return }
	var input struct { Username string `json:"username"`; Password string `json:"password"` }
	if !decodeAuthJSON(w,r,&input) { return }
	var id,displayName,encoded string
	err:=h.DB.QueryRowContext(r.Context(),"SELECT id::text,display_name,password_hash FROM platform_admins WHERE lower(username)=lower($1) AND active=true",strings.TrimSpace(input.Username)).Scan(&id,&displayName,&encoded)
	if err!=nil || !verifyPasswordHash(input.Password,encoded) { writeJSON(w,401,map[string]string{"error":"Usuário ou senha inválidos"}); return }
	random:=make([]byte,32); if _,err=rand.Read(random); err!=nil { writeJSON(w,500,map[string]string{"error":"Não foi possível iniciar sessão"}); return }
	token:=base64.RawURLEncoding.EncodeToString(random); digest:=sha256.Sum256([]byte(token))
	ttl:=h.SessionTTL; if ttl<=0 { ttl=8*time.Hour }; expires:=time.Now().Add(ttl)
	tx,err:=h.DB.BeginTx(r.Context(),nil); if err!=nil { writeJSON(w,500,map[string]string{"error":"Não foi possível iniciar sessão"}); return }; defer tx.Rollback()
	if _,err=tx.ExecContext(r.Context(),"UPDATE platform_admin_sessions SET revoked_at=now() WHERE admin_id=$1::uuid AND revoked_at IS NULL",id); err!=nil { writeJSON(w,500,map[string]string{"error":"Não foi possível substituir sessão anterior"}); return }
	ip:=platformRemoteIP(r)
	_,err=tx.ExecContext(r.Context(),"INSERT INTO platform_admin_sessions(admin_id,token_hash,expires_at,user_agent,ip_address) VALUES($1::uuid,$2,$3,$4,$5)",id,digest[:],expires,r.UserAgent(),ip)
	if err!=nil || tx.Commit()!=nil { writeJSON(w,500,map[string]string{"error":"Não foi possível gravar sessão"}); return }
	http.SetCookie(w,&http.Cookie{Name:platformSessionCookie,Value:token,Path:"/",HttpOnly:true,Secure:h.CookieSecure,SameSite:http.SameSiteStrictMode,Expires:expires,MaxAge:int(ttl.Seconds())})
	writeJSON(w,200,map[string]any{"ok":true,"admin":map[string]string{"id":id,"display_name":displayName},"expires_at":expires.UTC().Format(time.RFC3339)})
}

func (h *PlatformAdminHandler) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		cookie,err:=r.Cookie(platformSessionCookie); if err!=nil || cookie.Value=="" { writeJSON(w,401,map[string]string{"error":"Sessão de plataforma inválida"}); return }
		digest:=sha256.Sum256([]byte(cookie.Value)); var id,name string
		err=h.DB.QueryRowContext(r.Context(),"SELECT a.id::text,a.display_name FROM platform_admin_sessions s JOIN platform_admins a ON a.id=s.admin_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND a.active=true",digest[:]).Scan(&id,&name)
		if err!=nil { clearPlatformCookie(w,h.CookieSecure); writeJSON(w,401,map[string]string{"error":"Sessão de plataforma inválida ou expirada"}); return }
		ctx:=context.WithValue(r.Context(),platformAdminContextKey{},id)
		next.ServeHTTP(w,r.WithContext(ctx))
	})
}


func (h *PlatformAdminHandler) status(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodGet { w.Header().Set("Allow",http.MethodGet); writeJSON(w,405,map[string]string{"error":"Método não permitido"}); return }
	cookie,err:=r.Cookie(platformSessionCookie); if err!=nil { writeJSON(w,401,map[string]bool{"authenticated":false}); return }
	digest:=sha256.Sum256([]byte(cookie.Value)); var name string
	err=h.DB.QueryRowContext(r.Context(),"SELECT a.display_name FROM platform_admin_sessions s JOIN platform_admins a ON a.id=s.admin_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND a.active=true",digest[:]).Scan(&name)
	if err!=nil { writeJSON(w,401,map[string]bool{"authenticated":false}); return }
	writeJSON(w,200,map[string]any{"authenticated":true,"admin":map[string]string{"display_name":name}})
}

func (h *PlatformAdminHandler) logout(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodPost { w.Header().Set("Allow",http.MethodPost); writeJSON(w,405,map[string]string{"error":"Método não permitido"}); return }
	if cookie,err:=r.Cookie(platformSessionCookie); err==nil { digest:=sha256.Sum256([]byte(cookie.Value)); _,_=h.DB.ExecContext(r.Context(),"UPDATE platform_admin_sessions SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL",digest[:]) }
	clearPlatformCookie(w,h.CookieSecure); writeJSON(w,200,map[string]bool{"ok":true})
}

func (h *PlatformAdminHandler) plans(w http.ResponseWriter,r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows,err:=h.DB.QueryContext(r.Context(),"SELECT code,name,active,created_at FROM saas_plans ORDER BY code"); if err!=nil { writeJSON(w,500,map[string]string{"error":"Não foi possível listar planos"}); return }; defer rows.Close()
		type plan struct { Code string `json:"code"`; Name string `json:"name"`; Active bool `json:"active"`; CreatedAt time.Time `json:"created_at"` }
		items:=[]plan{}; for rows.Next(){ var p plan; if rows.Scan(&p.Code,&p.Name,&p.Active,&p.CreatedAt)!=nil { writeJSON(w,500,map[string]string{"error":"Falha ao ler planos"}); return }; items=append(items,p) }; writeJSON(w,200,map[string]any{"plans":items})
	case http.MethodPost:
		var input struct { Code string `json:"code"`; Name string `json:"name"` }
		if !decodeAuthJSON(w,r,&input){return}; input.Code=strings.ToLower(strings.TrimSpace(input.Code)); input.Name=strings.TrimSpace(input.Name)
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,39}$`).MatchString(input.Code)||input.Name=="" { writeJSON(w,400,map[string]string{"error":"Código e nome de plano inválidos"}); return }
		_,err:=h.DB.ExecContext(r.Context(),"INSERT INTO saas_plans(code,name) VALUES($1,$2)",input.Code,input.Name); if err!=nil { writeJSON(w,409,map[string]string{"error":"Código de plano já existe ou não pôde ser criado"}); return }; writeJSON(w,201,map[string]bool{"ok":true})
	default: w.Header().Set("Allow","GET, POST"); writeJSON(w,405,map[string]string{"error":"Método não permitido"})
	}
}

func (h *PlatformAdminHandler) tenants(w http.ResponseWriter,r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows,err:=h.DB.QueryContext(r.Context(),`SELECT t.id::text,t.legal_name,t.display_name,t.document,t.status,s.status,s.seat_limit,p.code
			FROM tenants t JOIN tenant_subscriptions s ON s.tenant_id=t.id LEFT JOIN saas_plans p ON p.id=s.plan_id ORDER BY t.created_at DESC`)
		if err!=nil { writeJSON(w,500,map[string]string{"error":"Não foi possível listar empresas"}); return }
		type tenant struct { ID string `json:"id"`; LegalName string `json:"legal_name"`; DisplayName string `json:"display_name"`; Document string `json:"document"`; Status string `json:"status"`; SubscriptionStatus string `json:"subscription_status"`; SeatLimit int `json:"seat_limit"`; PlanCode sql.NullString `json:"-"`; Plan *string `json:"plan_code"`; ActiveUsers int `json:"active_users"` }
		items:=[]tenant{}
		for rows.Next(){ var x tenant; if err:=rows.Scan(&x.ID,&x.LegalName,&x.DisplayName,&x.Document,&x.Status,&x.SubscriptionStatus,&x.SeatLimit,&x.PlanCode); err!=nil { rows.Close(); writeJSON(w,500,map[string]string{"error":"Falha ao ler empresas"}); return }; if x.PlanCode.Valid { v:=x.PlanCode.String; x.Plan=&v }; items=append(items,x) }
		if err:=rows.Err();err!=nil{rows.Close();writeJSON(w,500,map[string]string{"error":"Falha ao ler empresas"});return}
		rows.Close()
		// tenant_users has RLS: count each company's active accounts inside its own server-scoped transaction.
		for i:=range items {
			err=WithTenantTx(r.Context(),h.DB,items[i].ID,nil,func(tx *sql.Tx)error{
				return tx.QueryRowContext(r.Context(),"SELECT count(*) FROM tenant_users WHERE tenant_id=$1::uuid AND active=true",items[i].ID).Scan(&items[i].ActiveUsers)
			})
			if err!=nil{writeJSON(w,500,map[string]string{"error":"Não foi possível consultar os logins das empresas"});return}
		}
		writeJSON(w,200,map[string]any{"tenants":items})
	case http.MethodPost:
		var in struct { LegalName string `json:"legal_name"`; DisplayName string `json:"display_name"`; Document string `json:"document"`; OwnerUsername string `json:"owner_username"`; OwnerName string `json:"owner_name"`; OwnerPassword string `json:"owner_password"`; PlanCode string `json:"plan_code"`; SeatLimit int `json:"seat_limit"` }
		if !decodeAuthJSON(w,r,&in){return}; in.LegalName=strings.TrimSpace(in.LegalName); in.DisplayName=strings.TrimSpace(in.DisplayName); in.Document=regexp.MustCompile(`[^0-9]`).ReplaceAllString(in.Document,""); in.OwnerUsername=strings.TrimSpace(in.OwnerUsername); in.OwnerName=strings.TrimSpace(in.OwnerName); in.PlanCode=strings.ToLower(strings.TrimSpace(in.PlanCode))
		if in.LegalName==""||in.DisplayName==""||len(in.Document)<11||len(in.Document)>14||in.OwnerUsername==""||in.OwnerName==""||in.SeatLimit<1||in.SeatLimit>10000 { writeJSON(w,400,map[string]string{"error":"Dados da empresa, proprietário, documento ou limite de logins inválidos"}); return }
		hash,err:=HashPassword(in.OwnerPassword); if err!=nil { writeJSON(w,400,map[string]string{"error":err.Error()}); return }
		tx,err:=h.DB.BeginTx(r.Context(),nil); if err!=nil { writeJSON(w,500,map[string]string{"error":"Não foi possível iniciar cadastro"}); return }; defer tx.Rollback()
		var planID string; if err=tx.QueryRowContext(r.Context(),"SELECT id::text FROM saas_plans WHERE code=$1 AND active=true",in.PlanCode).Scan(&planID); err!=nil { writeJSON(w,400,map[string]string{"error":"Plano não encontrado ou inativo"}); return }
		var tenantID,userID string
		err=tx.QueryRowContext(r.Context(),"INSERT INTO tenants(legal_name,display_name,document) VALUES($1,$2,$3) RETURNING id::text",in.LegalName,in.DisplayName,in.Document).Scan(&tenantID)
		if err==nil { _,err=tx.ExecContext(r.Context(),"INSERT INTO tenant_subscriptions(tenant_id,plan_id,status,seat_limit) VALUES($1::uuid,$2::uuid,'active',$3)",tenantID,planID,in.SeatLimit) }
		if err==nil { _,err=tx.ExecContext(r.Context(),"SELECT set_config('app.tenant_id',$1,true)",tenantID) }
		if err==nil { err=tx.QueryRowContext(r.Context(),"INSERT INTO tenant_users(tenant_id,username,display_name,password_hash,role) VALUES($1::uuid,$2,$3,$4,'owner') RETURNING id::text",tenantID,in.OwnerUsername,in.OwnerName,hash).Scan(&userID) }
		if err==nil {
			_,err=tx.ExecContext(r.Context(),`INSERT INTO tenant_audit_events(tenant_id,event_type,entity_type,entity_id,details)
				VALUES($1::uuid,'platform.tenant.created','tenant',$1,jsonb_build_object('platform_admin_id',$2,'seat_limit',$3,'plan_code',$4))`,tenantID,platformAdminIDFromContext(r.Context()),in.SeatLimit,in.PlanCode)
		}
		if err!=nil { writeJSON(w,409,map[string]string{"error":"Empresa não criada: documento ou usuário duplicado, plano inválido ou falha de auditoria"}); return }
		if err=tx.Commit(); err!=nil { writeJSON(w,500,map[string]string{"error":"Não foi possível concluir cadastro"}); return }
		writeJSON(w,201,map[string]any{"ok":true,"tenant_id":tenantID,"owner_user_id":userID})
	default: w.Header().Set("Allow","GET, POST"); writeJSON(w,405,map[string]string{"error":"Método não permitido"})
	}
}

func (h *PlatformAdminHandler) tenantByID(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodPut { w.Header().Set("Allow",http.MethodPut); writeJSON(w,405,map[string]string{"error":"Método não permitido"}); return }
	id:=strings.Trim(strings.TrimPrefix(r.URL.Path,"/api/platform/tenants/"),"/"); if id==""||strings.Contains(id,"/"){writeJSON(w,404,map[string]string{"error":"Empresa não encontrada"});return}
	var in struct { Status *string `json:"status"`; SubscriptionStatus *string `json:"subscription_status"`; SeatLimit *int `json:"seat_limit"` }
	if !decodeAuthJSON(w,r,&in){return}
	if in.Status==nil&&in.SubscriptionStatus==nil&&in.SeatLimit==nil { writeJSON(w,400,map[string]string{"error":"Nenhuma alteração informada"});return }
	if in.Status!=nil&&*in.Status!="active"&&*in.Status!="suspended"&&*in.Status!="cancelled" {writeJSON(w,400,map[string]string{"error":"Status da empresa inválido"});return}
	if in.SubscriptionStatus!=nil&&*in.SubscriptionStatus!="trial"&&*in.SubscriptionStatus!="active"&&*in.SubscriptionStatus!="past_due"&&*in.SubscriptionStatus!="suspended"&&*in.SubscriptionStatus!="cancelled" {writeJSON(w,400,map[string]string{"error":"Status da assinatura inválido"});return}
	if in.SeatLimit!=nil&&(*in.SeatLimit<1||*in.SeatLimit>10000) {writeJSON(w,400,map[string]string{"error":"Limite de logins inválido"});return}
	tx,err:=h.DB.BeginTx(r.Context(),nil);if err!=nil{writeJSON(w,500,map[string]string{"error":"Não foi possível iniciar alteração"});return};defer tx.Rollback()
	var exists bool;if err=tx.QueryRowContext(r.Context(),"SELECT EXISTS(SELECT 1 FROM tenants WHERE id=$1::uuid)",id).Scan(&exists);err!=nil||!exists{writeJSON(w,404,map[string]string{"error":"Empresa não encontrada"});return}
	// The platform route has verified the target tenant; bind that ID before touching RLS-protected users, sessions or audit rows.
	if _,err=tx.ExecContext(r.Context(),"SELECT set_config('app.tenant_id',$1,true)",id);err!=nil{writeJSON(w,500,map[string]string{"error":"Não foi possível definir o contexto da empresa"});return}
	if in.Status!=nil {_,err=tx.ExecContext(r.Context(),"UPDATE tenants SET status=$2,updated_at=now() WHERE id=$1::uuid",id,*in.Status)}
	if err==nil&&in.SeatLimit!=nil {
		var currentSeatLimit,activeUsers int
		err=tx.QueryRowContext(r.Context(),"SELECT seat_limit FROM tenant_subscriptions WHERE tenant_id=$1::uuid FOR UPDATE",id).Scan(&currentSeatLimit)
		if err==nil { err=tx.QueryRowContext(r.Context(),"SELECT count(*) FROM tenant_users WHERE tenant_id=$1::uuid AND active=true",id).Scan(&activeUsers) }
		if err==nil && *in.SeatLimit<activeUsers {
			writeJSON(w,409,map[string]string{"error":"O novo limite não pode ser menor que a quantidade de logins ativos"}); return
		}
	}
	if err==nil&&(in.SubscriptionStatus!=nil||in.SeatLimit!=nil) {_,err=tx.ExecContext(r.Context(),"UPDATE tenant_subscriptions SET status=COALESCE($2,status),seat_limit=COALESCE($3,seat_limit),updated_at=now() WHERE tenant_id=$1::uuid",id,in.SubscriptionStatus,in.SeatLimit)}
	if err==nil {
		revokeSessions:=(in.Status!=nil&&*in.Status!="active")||(in.SubscriptionStatus!=nil&&*in.SubscriptionStatus!="trial"&&*in.SubscriptionStatus!="active")
		if revokeSessions { _,err=tx.ExecContext(r.Context(),`UPDATE tenant_sessions SET revoked_at=now() WHERE tenant_id=$1::uuid AND revoked_at IS NULL`,id) }
	}
	if err==nil {
		var tenantStatus,subscriptionStatus any
		var seatLimit any
		if in.Status!=nil{tenantStatus=*in.Status}
		if in.SubscriptionStatus!=nil{subscriptionStatus=*in.SubscriptionStatus}
		if in.SeatLimit!=nil{seatLimit=*in.SeatLimit}
		_,err=tx.ExecContext(r.Context(),`INSERT INTO tenant_audit_events(tenant_id,event_type,entity_type,entity_id,details)
			VALUES($1::uuid,'platform.tenant.updated','tenant',$1,jsonb_build_object('platform_admin_id',$2,'tenant_status',$3,'subscription_status',$4,'seat_limit',$5))`,
			id,platformAdminIDFromContext(r.Context()),tenantStatus,subscriptionStatus,seatLimit)
	}
	if err!=nil {writeJSON(w,409,map[string]string{"error":"Não foi possível atualizar empresa, licença, sessões ou auditoria"});return}
	if err=tx.Commit();err!=nil{writeJSON(w,500,map[string]string{"error":"Não foi possível concluir alteração"});return}
	writeJSON(w,200,map[string]bool{"ok":true})
}

func clearPlatformCookie(w http.ResponseWriter,secure bool) { http.SetCookie(w,&http.Cookie{Name:platformSessionCookie,Value:"",Path:"/",HttpOnly:true,Secure:secure,SameSite:http.SameSiteStrictMode,MaxAge:-1,Expires:time.Unix(1,0)}) }
func platformRemoteIP(r *http.Request) any { host,_,err:=net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); if err!=nil||host=="" { return nil }; return host }

