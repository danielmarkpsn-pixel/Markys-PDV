package saas

import (
 "database/sql"
 "net/http"
 "strings"
 "time"
)

type TenantAdminHandler struct { DB *sql.DB; Auth *HTTPHandler }

func NewTenantAdminHandler(db *sql.DB, auth *HTTPHandler) *TenantAdminHandler {
 return &TenantAdminHandler{DB:db, Auth:auth}
}

// Register exposes only tenant-scoped user and read-only license management.
// Contracted seat counts are intentionally not writable by tenant administrators.
func (h *TenantAdminHandler) Register(mux *http.ServeMux) {
 mux.Handle("/api/admin/users", h.Auth.RequireSession(http.HandlerFunc(h.users)))
 mux.Handle("/api/admin/users/", h.Auth.RequireSession(http.HandlerFunc(h.userByID)))
 mux.Handle("/api/admin/license", h.Auth.RequireSession(http.HandlerFunc(h.license)))
}

func requireTenantAdmin(w http.ResponseWriter, r *http.Request) (SessionIdentity, bool) {
 identity, ok := IdentityFromContext(r.Context())
 if !ok { writeJSON(w,http.StatusUnauthorized,map[string]string{"error":"Sessão inválida"}); return SessionIdentity{},false }
 if identity.Role!="owner" && identity.Role!="admin" {
  writeJSON(w,http.StatusForbidden,map[string]string{"error":"Somente o administrador da empresa pode gerenciar usuários"}); return SessionIdentity{},false
 }
 return identity,true
}

func (h *TenantAdminHandler) license(w http.ResponseWriter,r *http.Request) {
 if r.Method!=http.MethodGet { w.Header().Set("Allow",http.MethodGet); writeJSON(w,405,map[string]string{"error":"Método não permitido"}); return }
 identity,ok:=requireTenantAdmin(w,r); if !ok{return}
 var limit,active int
 var status string
 err:=WithTenantTx(r.Context(),h.DB,identity.TenantID,nil,func(tx *sql.Tx)error{
  return tx.QueryRowContext(r.Context(),`SELECT s.seat_limit,s.status,(SELECT count(*) FROM tenant_users u WHERE u.tenant_id=s.tenant_id AND u.active=true) FROM tenant_subscriptions s WHERE s.tenant_id=$1::uuid`,identity.TenantID).Scan(&limit,&status,&active)
 })
 if err!=nil {writeJSON(w,500,map[string]string{"error":"Não foi possível consultar a licença"});return}
 writeJSON(w,200,map[string]any{"contracted_logins":limit,"active_logins":active,"available_logins":max(0,limit-active),"subscription_status":status,"can_change_limit":false})
}

func (h *TenantAdminHandler) users(w http.ResponseWriter,r *http.Request) {
 identity,ok:=requireTenantAdmin(w,r);if !ok{return}
 switch r.Method {
 case http.MethodGet:
  type user struct {ID string `json:"id"`; Username string `json:"username"`; DisplayName string `json:"display_name"`; Email string `json:"email"`; Role string `json:"role"`; Active bool `json:"active"`; CreatedAt time.Time `json:"created_at"`}
  users:=[]user{}
  err:=WithTenantTx(r.Context(),h.DB,identity.TenantID,nil,func(tx *sql.Tx)error{
   rows,err:=tx.QueryContext(r.Context(),`SELECT id::text,username,display_name,email,role,active,created_at FROM tenant_users WHERE tenant_id=$1::uuid ORDER BY lower(display_name),lower(username)`,identity.TenantID)
   if err!=nil{return err};defer rows.Close()
   for rows.Next(){var u user;if err:=rows.Scan(&u.ID,&u.Username,&u.DisplayName,&u.Email,&u.Role,&u.Active,&u.CreatedAt);err!=nil{return err};users=append(users,u)}
   return rows.Err()
  })
  if err!=nil {writeJSON(w,500,map[string]string{"error":"Não foi possível listar usuários"});return}
  writeJSON(w,200,map[string]any{"users":users})
 case http.MethodPost:
  var input struct{Username string `json:"username"`;DisplayName string `json:"display_name"`;Email string `json:"email"`;Password string `json:"password"`;Role string `json:"role"`}
  if !decodeAuthJSON(w,r,&input){return}
  input.Username=strings.TrimSpace(input.Username);input.DisplayName=strings.TrimSpace(input.DisplayName);input.Email=strings.TrimSpace(input.Email);input.Role=strings.ToLower(strings.TrimSpace(input.Role))
  if input.Username==""||input.DisplayName==""||input.Role=="" {writeJSON(w,400,map[string]string{"error":"Informe usuário, nome e perfil"});return}
  if input.Role!="admin"&&input.Role!="manager"&&input.Role!="cashier"&&input.Role!="stockkeeper" {writeJSON(w,400,map[string]string{"error":"Perfil inválido"});return}
  hash,err:=HashPassword(input.Password);if err!=nil{writeJSON(w,400,map[string]string{"error":err.Error()});return}
  var id string
  err=WithTenantTx(r.Context(),h.DB,identity.TenantID,nil,func(tx *sql.Tx)error{
   return tx.QueryRowContext(r.Context(),`INSERT INTO tenant_users(tenant_id,username,display_name,email,password_hash,role) VALUES($1::uuid,$2,$3,$4,$5,$6) RETURNING id::text`,identity.TenantID,input.Username,input.DisplayName,input.Email,hash,input.Role).Scan(&id)
  })
  if err!=nil {if strings.Contains(strings.ToLower(err.Error()),"unique"){writeJSON(w,409,map[string]string{"error":"Este nome de usuário já existe nesta empresa"});return};if strings.Contains(err.Error(),"Limite de logins contratados"){writeJSON(w,409,map[string]string{"error":"Limite de logins contratados atingido. Solicite a ampliação ao administrador da plataforma."});return};writeJSON(w,500,map[string]string{"error":"Não foi possível criar usuário"});return}
  writeJSON(w,201,map[string]any{"ok":true,"id":id})
 default:w.Header().Set("Allow","GET, POST");writeJSON(w,405,map[string]string{"error":"Método não permitido"})
 }
}

func (h *TenantAdminHandler) userByID(w http.ResponseWriter,r *http.Request) {
 identity,ok:=requireTenantAdmin(w,r);if !ok{return}
 id:=strings.Trim(strings.TrimPrefix(r.URL.Path,"/api/admin/users/"),"/")
 if id==""||strings.Contains(id,"/"){writeJSON(w,404,map[string]string{"error":"Usuário não encontrado"});return}
 if r.Method!=http.MethodPut {w.Header().Set("Allow",http.MethodPut);writeJSON(w,405,map[string]string{"error":"Método não permitido"});return}
 var input struct{DisplayName *string `json:"display_name"`;Email *string `json:"email"`;Password *string `json:"password"`;Role *string `json:"role"`;Active *bool `json:"active"`}
 if !decodeAuthJSON(w,r,&input){return}
 if input.DisplayName==nil&&input.Email==nil&&input.Password==nil&&input.Role==nil&&input.Active==nil{writeJSON(w,400,map[string]string{"error":"Nenhuma alteração informada"});return}
 err:=WithTenantTx(r.Context(),h.DB,identity.TenantID,nil,func(tx *sql.Tx)error{
  var currentRole string
  if err:=tx.QueryRowContext(r.Context(),"SELECT role FROM tenant_users WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE",identity.TenantID,id).Scan(&currentRole);err!=nil{return err}
  if currentRole=="owner" {return errProtectedOwner}
  if input.Role!=nil {role:=strings.ToLower(strings.TrimSpace(*input.Role));if role!="admin"&&role!="manager"&&role!="cashier"&&role!="stockkeeper"{return errInvalidRole};input.Role=&role}
  if input.DisplayName!=nil {v:=strings.TrimSpace(*input.DisplayName);if v==""{return errInvalidUserInput};input.DisplayName=&v}
  var hash any
  if input.Password!=nil {v,e:=HashPassword(*input.Password);if e!=nil{return e};hash=v}
  _,err:=tx.ExecContext(r.Context(),`UPDATE tenant_users SET display_name=COALESCE($3,display_name),email=COALESCE($4,email),role=COALESCE($5,role),active=COALESCE($6,active),password_hash=COALESCE($7,password_hash),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`,identity.TenantID,id,input.DisplayName,input.Email,input.Role,input.Active,hash)
  if err!=nil{return err}
  // Password resets and deactivation must permanently revoke existing sessions.
  // Merely checking active/password state would allow an old session to revive
  // if the account were reactivated before that session expired.
  if input.Password!=nil || (input.Active!=nil && !*input.Active) {
   _,err=tx.ExecContext(r.Context(),`UPDATE tenant_sessions SET revoked_at=now()
    WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL`,identity.TenantID,id)
  }
  return err
 })
 if err!=nil {if err==errProtectedOwner{writeJSON(w,403,map[string]string{"error":"O usuário proprietário principal não pode ser alterado por este recurso"});return};if err==errInvalidRole||err==errInvalidUserInput{writeJSON(w,400,map[string]string{"error":"Dados de alteração inválidos"});return};if strings.Contains(err.Error(),"Limite de logins contratados"){writeJSON(w,409,map[string]string{"error":"Limite de logins contratados atingido"});return};writeJSON(w,404,map[string]string{"error":"Usuário não encontrado ou alteração não permitida"});return}
 writeJSON(w,200,map[string]bool{"ok":true})
}

type adminSentinel string
func (e adminSentinel) Error()string{return string(e)}
const(errProtectedOwner adminSentinel="protected owner";errInvalidRole adminSentinel="invalid role";errInvalidUserInput adminSentinel="invalid user input")
