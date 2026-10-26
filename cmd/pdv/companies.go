package main

import (
 "database/sql"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "os"
 "path/filepath"
 "regexp"
 "strings"
 "strconv"
)

func initCompanies(db *sql.DB) error {
 _,err:=db.Exec(`CREATE TABLE IF NOT EXISTS companies (
 id INTEGER PRIMARY KEY, legal_name TEXT NOT NULL, trade_name TEXT NOT NULL DEFAULT '',
 document TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'active' CHECK(status IN('active','inactive')),
 database_mode TEXT NOT NULL CHECK(database_mode IN('create','connect','import')),
 database_path TEXT NOT NULL, sync_mode TEXT NOT NULL DEFAULT 'local_cloud',
 sync_status TEXT NOT NULL DEFAULT 'not_configured', theme_color TEXT NOT NULL DEFAULT '#1677e8',
 logo_data_url TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
 )`)
 if err!=nil{return err}
 for _,col:=range []struct{name,definition string}{{"theme_color","TEXT NOT NULL DEFAULT '#1677e8'"},{"logo_data_url","TEXT NOT NULL DEFAULT ''"}} {
  rows,e:=db.Query("PRAGMA table_info(companies)");if e!=nil{return e};found:=false
  for rows.Next(){var cid,notnull,pk int;var name,typ string;var def any;if e=rows.Scan(&cid,&name,&typ,&notnull,&def,&pk);e!=nil{rows.Close();return e};if name==col.name{found=true}}
  if e=rows.Err();e!=nil{rows.Close();return e};rows.Close()
  if !found{if _,e=db.Exec("ALTER TABLE companies ADD COLUMN "+col.name+" "+col.definition);e!=nil{return e}}
 }
 return nil
}
func (a *App) companyAdmin(w http.ResponseWriter,r *http.Request) bool {
 id:=requestUserID(a,r); if id<=0 {http.Error(w,"Sessão necessária",401);return false}
 var role string
 if err:=a.db.QueryRow("SELECT role FROM users WHERE id=? AND active=1",id).Scan(&role);err!=nil||role!="admin"{http.Error(w,"Somente o administrador geral pode gerenciar empresas",403);return false}
 return true
}
func (a *App) companies(w http.ResponseWriter,r *http.Request) {
 if !a.companyAdmin(w,r){return}
 if r.Method=="GET" {
 rows,err:=a.db.Query("SELECT id,legal_name,trade_name,document,status,database_mode,database_path,sync_mode,sync_status,created_at,theme_color,logo_data_url FROM companies ORDER BY legal_name COLLATE NOCASE")
 if err!=nil{http.Error(w,"Erro ao consultar empresas",500);return};defer rows.Close()
 type C struct{ID int64 `json:"id"`;Legal string `json:"legal_name"`;Trade string `json:"trade_name"`;Doc string `json:"document"`;Status string `json:"status"`;Mode string `json:"database_mode"`;Path string `json:"database_path"`;Sync string `json:"sync_mode"`;SyncStatus string `json:"sync_status"`;Created string `json:"created_at"`;ThemeColor string `json:"theme_color"`;LogoDataURL string `json:"logo_data_url"`}
 out:=[]C{};for rows.Next(){var c C;if rows.Scan(&c.ID,&c.Legal,&c.Trade,&c.Doc,&c.Status,&c.Mode,&c.Path,&c.Sync,&c.SyncStatus,&c.Created,&c.ThemeColor,&c.LogoDataURL)!=nil{http.Error(w,"Erro ao ler empresas",500);return};out=append(out,c)};jsonOut(w,200,out);return
 }
 if r.Method!="POST"{w.Header().Set("Allow","GET, POST");http.Error(w,"Método não permitido",405);return}
 var q struct{Legal string `json:"legal_name"`;Trade string `json:"trade_name"`;Document string `json:"document"`;Mode string `json:"database_mode"`;Path string `json:"database_path"`;ThemeColor string `json:"theme_color"`;LogoDataURL string `json:"logo_data_url"`}
 d:=json.NewDecoder(http.MaxBytesReader(w,r.Body,1<<20));d.DisallowUnknownFields();if d.Decode(&q)!=nil{http.Error(w,"Dados inválidos",400);return}
 q.Legal=strings.TrimSpace(q.Legal);q.Trade=strings.TrimSpace(q.Trade);q.Document=regexp.MustCompile(`[^0-9]`).ReplaceAllString(q.Document,"")
 if q.Legal==""||len(q.Legal)>180||len(q.Trade)>180||len(q.Document)<11||len(q.Document)>14{http.Error(w,"Informe razão social e CPF/CNPJ com 11 a 14 dígitos",400);return};if q.ThemeColor==""{q.ThemeColor="#1677e8"};if !regexp.MustCompile("^#[0-9a-fA-F]{6}$").MatchString(q.ThemeColor){http.Error(w,"Cor inválida: use hexadecimal, por exemplo #1677e8",400);return};if err:=validateCompanyLogo(q.LogoDataURL);err!=nil{http.Error(w,err.Error(),400);return}
 if q.Mode!="create"&&q.Mode!="connect"&&q.Mode!="import"{http.Error(w,"Modo de banco inválido",400);return}
 root,err:=a.companyStorageRoot();if err!=nil{http.Error(w,"Pasta de dados da empresa inválida",500);return};if os.MkdirAll(root,0700)!=nil{http.Error(w,"Não foi possível preparar pasta local",500);return}
 target:=filepath.Join(root,q.Document+".db")
 if q.Mode=="create" {
  if _,e:=os.Stat(target);e==nil{http.Error(w,"Já existe banco para este documento",409);return}
  db,e:=sql.Open("sqlite",target);if e!=nil{http.Error(w,"Não foi possível criar banco",500);return};db.SetMaxOpenConns(1)
  schema,e:=assets.ReadFile("web/schema.sql");if e==nil{_,e=db.Exec(string(schema))};db.Close();if e!=nil{os.Remove(target);http.Error(w,"Falha ao inicializar banco",500);return}
 } else {
  base:=root;if q.Mode=="import"{base=filepath.Join(filepath.Dir(a.dbPath),"imports");if os.MkdirAll(base,0700)!=nil{http.Error(w,"Não foi possível preparar pasta de importação",500);return}}
  base,err=filepath.Abs(base);if err!=nil{http.Error(w,"Pasta inválida",500);return}
  candidate:=q.Path;if !filepath.IsAbs(candidate){candidate=filepath.Join(base,candidate)}
  candidate,err=filepath.Abs(candidate);if err!=nil{http.Error(w,"Caminho inválido",400);return}
  rel,e:=filepath.Rel(base,candidate);if e!=nil||rel==".."||strings.HasPrefix(rel,".."+string(os.PathSeparator)){http.Error(w,"Caminho fora da pasta permitida",400);return}
  if resolved,e:=filepath.EvalSymlinks(candidate);e==nil{candidate=resolved;rel,e=filepath.Rel(base,candidate);if e!=nil||rel==".."||strings.HasPrefix(rel,".."+string(os.PathSeparator)){http.Error(w,"Atalho para arquivo externo não permitido",400);return}}
  test,e:=sql.Open("sqlite",candidate);if e!=nil{http.Error(w,"Banco não encontrado",400);return}
  var check string;e=test.QueryRow("PRAGMA quick_check").Scan(&check);var tables int;if e==nil{e=test.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('products','sales','users')").Scan(&tables)};test.Close()
  if e!=nil||check!="ok"||tables<3{http.Error(w,"Banco inválido: exige integridade SQLite e tabelas products, sales e users",400);return}
  if q.Mode=="connect"{target=candidate}else{
   if _,e=os.Stat(target);e==nil{http.Error(w,"Banco de destino já existe; nada foi sobrescrito",409);return}
   in,e:=os.Open(candidate);if e!=nil{http.Error(w,"Falha ao abrir origem",500);return};out,e:=os.OpenFile(target,os.O_WRONLY|os.O_CREATE|os.O_EXCL,0600);if e!=nil{in.Close();http.Error(w,"Falha ao criar destino sem sobrescrever arquivo existente",500);return};_,copyErr:=io.Copy(out,in);syncErr:=out.Sync();closeOutErr:=out.Close();closeInErr:=in.Close();if copyErr!=nil||syncErr!=nil||closeOutErr!=nil||closeInErr!=nil{os.Remove(target);http.Error(w,"Falha ao importar banco; origem preservada",500);return}
  }
 }
 var linkedCount int;if err:=a.db.QueryRow("SELECT COUNT(*) FROM companies WHERE database_path=?",target).Scan(&linkedCount);err!=nil{http.Error(w,"Não foi possível verificar o banco cadastrado",500);return};if linkedCount>0{http.Error(w,"Este banco já está vinculado a outra empresa; use um arquivo exclusivo para manter os dados isolados",409);return}
 res,err:=a.db.Exec("INSERT INTO companies(legal_name,trade_name,document,status,database_mode,database_path,sync_mode,sync_status,theme_color,logo_data_url) VALUES(?,?,?,'active',?,?,'local_cloud','not_configured',?,?)",q.Legal,q.Trade,q.Document,q.Mode,target,q.ThemeColor,q.LogoDataURL)
 if err!=nil{http.Error(w,"Empresa não cadastrada; documento pode já existir",409);return};id,err:=res.LastInsertId();if err!=nil{http.Error(w,"Não foi possível confirmar cadastro",500);return}
 a.writeAudit(requestUserID(a,r),"company_created","company",fmt.Sprint(id),"Empresa cadastrada: "+q.Mode)
 jsonOut(w,201,map[string]any{"id":id,"legal_name":q.Legal,"trade_name":q.Trade,"document":q.Document,"status":"active","database_mode":q.Mode,"database_path":target,"sync_status":"not_configured","message":"Cadastro local concluído. Sincronização em nuvem não está ativa; nenhum dado foi enviado."})
}
func (a *App) companyStatus(w http.ResponseWriter,r *http.Request){
 if !a.companyAdmin(w,r){return};if r.Method!="PATCH"{http.Error(w,"Método não permitido",405);return}
 var q struct{Active bool `json:"active"`};if json.NewDecoder(http.MaxBytesReader(w,r.Body,1<<20)).Decode(&q)!=nil{http.Error(w,"Dados inválidos",400);return}
 status:="inactive";if q.Active{status="active"};id:=r.URL.Query().Get("id");res,err:=a.db.Exec("UPDATE companies SET status=? WHERE id=?",status,id);if err!=nil{http.Error(w,"Falha ao alterar status",500);return};n,_:=res.RowsAffected();if n==0{http.Error(w,"Empresa não encontrada",404);return};a.writeAudit(requestUserID(a,r),"company_status_changed","company",id,status);jsonOut(w,200,map[string]string{"status":status})
}

func validateCompanyLogo(data string) error {
 if len(data)>700000{return fmt.Errorf("Logo muito grande; limite de 500 KB")}
 if data==""{return nil}
 if !strings.HasPrefix(data,"data:image/png;base64,")&&!strings.HasPrefix(data,"data:image/jpeg;base64,")&&!strings.HasPrefix(data,"data:image/webp;base64,"){return fmt.Errorf("Logo deve ser PNG, JPEG ou WebP")}
 return nil
}

func (a *App) companyBrandingUpdate(w http.ResponseWriter,r *http.Request) {
 if !a.companyAdmin(w,r){return}
 if r.Method!=http.MethodPatch{w.Header().Set("Allow","PATCH");http.Error(w,"Método não permitido",405);return}
 id,err:=strconv.ParseInt(r.URL.Query().Get("id"),10,64);if err!=nil||id<=0{http.Error(w,"Empresa inválida",400);return}
 var q struct{ThemeColor string `json:"theme_color"`;LogoDataURL string `json:"logo_data_url"`;ClearLogo bool `json:"clear_logo"`}
 d:=json.NewDecoder(http.MaxBytesReader(w,r.Body,750000));d.DisallowUnknownFields();if d.Decode(&q)!=nil{http.Error(w,"Dados inválidos",400);return}
 if !regexp.MustCompile("^#[0-9a-fA-F]{6}$").MatchString(q.ThemeColor){http.Error(w,"Cor inválida: use hexadecimal, por exemplo #1677e8",400);return}
 if err:=validateCompanyLogo(q.LogoDataURL);err!=nil{http.Error(w,err.Error(),400);return};if q.LogoDataURL==""&&!q.ClearLogo{if err:=a.db.QueryRow("SELECT logo_data_url FROM companies WHERE id=?",id).Scan(&q.LogoDataURL);err!=nil{http.Error(w,"Empresa não encontrada",404);return}};if q.ClearLogo{q.LogoDataURL=""}
 res,err:=a.db.Exec("UPDATE companies SET theme_color=?,logo_data_url=? WHERE id=?",q.ThemeColor,q.LogoDataURL,id);if err!=nil{http.Error(w,"Não foi possível salvar identidade visual",500);return}
 n,_:=res.RowsAffected();if n==0{http.Error(w,"Empresa não encontrada",404);return}
 a.writeAudit(requestUserID(a,r),"company_branding_updated","company",strconv.FormatInt(id,10),"Cor e logo da empresa atualizadas")
 jsonOut(w,200,map[string]any{"ok":true,"id":id,"theme_color":q.ThemeColor})
}

func (a *App) companyBootstrap(w http.ResponseWriter,r *http.Request) {
 if !a.companyAdmin(w,r){return}
 if r.Method!=http.MethodPost{w.Header().Set("Allow","POST");http.Error(w,"Método não permitido",405);return}
 id,err:=strconv.ParseInt(r.URL.Query().Get("id"),10,64);if err!=nil||id<=0{http.Error(w,"Empresa inválida",400);return}
 var q struct{DisplayName string `json:"display_name"`;Username string `json:"username"`;Password string `json:"password"`}
 if !decodeJSON(w,r,&q){return}
 q.DisplayName=strings.TrimSpace(q.DisplayName);q.Username=strings.TrimSpace(q.Username)
 if q.DisplayName==""||len(q.DisplayName)>120||len(q.Username)<3||len(q.Username)>64{http.Error(w,"Informe nome e usuário válidos",400);return}
 app,err:=a.appForCompany(id);if err!=nil{http.Error(w,err.Error(),400);return}
 var count int;if err:=app.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count);err!=nil{http.Error(w,"Não foi possível verificar usuários da empresa",500);return};if count>0{http.Error(w,"Esta empresa já possui usuários; use a gestão de usuários dela",409);return}
 hash,err:=passwordHash(q.Password);if err!=nil{http.Error(w,err.Error(),400);return}
 res,err:=app.db.Exec("INSERT INTO users(username,password_hash,display_name,role,active) VALUES(?,?,?,'admin',1)",q.Username,hash,q.DisplayName);if err!=nil{http.Error(w,"Não foi possível criar administrador; usuário pode já existir",409);return}
 userID,_:=res.LastInsertId();a.writeAudit(requestUserID(a,r),"company_admin_bootstrapped","company",strconv.FormatInt(id,10),"Administrador inicial criado para empresa")
 jsonOut(w,201,map[string]any{"ok":true,"company_id":id,"username":q.Username,"user_id":userID})
}

// companyStorageRoot keeps tenant databases beside the master database, not in the process working directory.
// This prevents a Windows shortcut or installer update from silently changing the data location.
func (a *App) companyStorageRoot() (string,error) {
 base:=strings.TrimSpace(a.dbPath)
 if base=="" { return "",fmt.Errorf("caminho do banco principal não configurado") }
 abs,err:=filepath.Abs(base);if err!=nil{return "",err}
 return filepath.Join(filepath.Dir(abs),"companies"),nil
}
