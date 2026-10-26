package main

import (
	"fmt"
	"net/http"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (a *App) auditEntries(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodGet {w.Header().Set("Allow","GET");http.Error(w,"Method not allowed",405);return}
	u:=a.currentUser(r);if u==nil||u.Role!="admin"{http.Error(w,"Acesso negado",403);return}
	limit:=200
	if raw:=strings.TrimSpace(r.URL.Query().Get("limit"));raw!=""{v,err:=strconv.Atoi(raw);if err!=nil||v<1||v>500{http.Error(w,"Limite inválido (1 a 500)",400);return};limit=v}
	rows,err:=a.db.Query(`SELECT l.id,COALESCE(u.username,'sistema'),COALESCE(u.display_name,'Sistema'),l.action,l.entity_type,l.entity_id,l.details,l.created_at FROM audit_log l LEFT JOIN users u ON u.id=l.user_id ORDER BY l.id DESC LIMIT ?`,limit)
	if err!=nil{http.Error(w,"Database error",500);return};defer rows.Close()
	type entry struct{ID int64 `json:"id"`;Username string `json:"username"`;DisplayName string `json:"display_name"`;Action string `json:"action"`;EntityType string `json:"entity_type"`;EntityID string `json:"entity_id"`;Details string `json:"details"`;CreatedAt string `json:"created_at"`}
	out:=[]entry{}
	for rows.Next(){var e entry;if err:=rows.Scan(&e.ID,&e.Username,&e.DisplayName,&e.Action,&e.EntityType,&e.EntityID,&e.Details,&e.CreatedAt);err!=nil{http.Error(w,"Read error",500);return};out=append(out,e)}
	if err:=rows.Err();err!=nil{http.Error(w,"Read error",500);return}
	jsonOut(w,200,out)
}

func (a *App) databaseBackup(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodGet {w.Header().Set("Allow","GET");http.Error(w,"Method not allowed",405);return}
	u:=a.currentUser(r);if u==nil||u.Role!="admin"{http.Error(w,"Acesso negado",403);return}
	if strings.TrimSpace(a.dbPath)==""||a.dbPath==":memory:"{http.Error(w,"Backup não disponível para banco em memória",409);return}
	dir:=filepath.Join(filepath.Dir(a.dbPath),"backups")
	if err:=os.MkdirAll(dir,0750);err!=nil{http.Error(w,"Não foi possível preparar a pasta de backup",500);return}
	name:=fmt.Sprintf("markys-pdv-backup-%s.db",time.Now().UTC().Format("20060102-150405"))
	path:=filepath.Join(dir,name)
	if _,err:=a.db.Exec("VACUUM INTO ?",path);err!=nil{http.Error(w,"Não foi possível gerar backup consistente do SQLite",500);return}
	defer os.Remove(path)
	a.writeAudit(u.ID,"database_backup_created","database",name,"Backup SQLite consistente gerado para download")
	w.Header().Set("Content-Type","application/vnd.sqlite3")
	w.Header().Set("Content-Disposition",fmt.Sprintf("attachment; filename=%q",name))
	w.Header().Set("Cache-Control","no-store")
	http.ServeFile(w,r,path)
}


// restoreDatabase imports a previously generated MARKYS SQLite backup. It validates the
// uploaded database first, creates a safety copy of the current database, and restores
// compatible tables in a single transaction.
func (a *App) restoreDatabase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { w.Header().Set("Allow","POST"); http.Error(w,"Método não permitido",405); return }
	u:=a.currentUser(r); if u==nil||u.Role!="admin" { http.Error(w,"Acesso negado",403); return }
	if strings.TrimSpace(a.dbPath)==""||a.dbPath==":memory:" { http.Error(w,"Restauração não disponível para banco em memória",409); return }
	r.Body=http.MaxBytesReader(w,r.Body,100<<20)
	if err:=r.ParseMultipartForm(100<<20);err!=nil { http.Error(w,"Arquivo inválido ou maior que 100 MB",400);return }
	file,header,err:=r.FormFile("backup");if err!=nil { http.Error(w,"Selecione um arquivo de backup .db",400);return };defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename),".db") { http.Error(w,"O arquivo precisa ter extensão .db",400);return }
	tmp,err:=os.CreateTemp(filepath.Dir(a.dbPath),"markys-restore-*.db");if err!=nil {http.Error(w,"Não foi possível preparar a restauração",500);return}
	tmpPath:=tmp.Name();defer os.Remove(tmpPath)
	if _,err=io.Copy(tmp,file);err!=nil {tmp.Close();http.Error(w,"Falha ao receber o backup",400);return};if err=tmp.Close();err!=nil {http.Error(w,"Falha ao salvar o arquivo enviado",500);return}
	check,err:=sql.Open("sqlite",tmpPath);if err!=nil {http.Error(w,"Arquivo SQLite inválido",400);return}
	check.SetMaxOpenConns(1)
	var integrity string
	if err=check.QueryRow("PRAGMA integrity_check").Scan(&integrity);err!=nil||integrity!="ok" {check.Close();http.Error(w,"Backup inválido ou corrompido",400);return}
	for _,table:=range []string{"products","categories","users","sales","cash_sessions","customers","audit_log"} { var n int; if err=check.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",table).Scan(&n);err!=nil||n!=1 {check.Close();http.Error(w,"Backup não parece ser um banco MARKYS PDV válido",400);return} }
	check.Close()
	safetyDir:=filepath.Join(filepath.Dir(a.dbPath),"backups")
	if err=os.MkdirAll(safetyDir,0750);err!=nil {http.Error(w,"Não foi possível criar a cópia de segurança",500);return}
	safetyPath:=filepath.Join(safetyDir,fmt.Sprintf("antes-restauracao-%s.db",time.Now().UTC().Format("20060102-150405")))
	if _,err=a.db.Exec("VACUUM INTO ?",safetyPath);err!=nil {http.Error(w,"Não foi possível gerar cópia de segurança antes da restauração",500);return}
	if _,err=a.db.Exec("ATTACH DATABASE ? AS restoredb",tmpPath);err!=nil {http.Error(w,"Não foi possível abrir o backup para restauração",400);return}
	defer a.db.Exec("DETACH DATABASE restoredb")
	if _,err=a.db.Exec("PRAGMA foreign_keys=OFF");err!=nil {http.Error(w,"Não foi possível preparar o banco para restauração",500);return}
	defer a.db.Exec("PRAGMA foreign_keys=ON")
	tables:=[]string{"store_settings","users","categories","suppliers","customers","products","product_lots","cash_sessions","sales","sale_items","payments","stock_movements","fiscal_documents","license_state","audit_log"}
	for _,table:=range tables { var present int; if err=a.db.QueryRow("SELECT COUNT(*) FROM restoredb.sqlite_master WHERE type='table' AND name=?",table).Scan(&present);err!=nil||present==0 { http.Error(w,"Backup incompatível: tabela ausente "+table,400);return } }
	tx,err:=a.db.Begin();if err!=nil {http.Error(w,"Não foi possível iniciar restauração",500);return};defer tx.Rollback()
	for i:=len(tables)-1;i>=0;i-- { if _,err=tx.Exec("DELETE FROM main."+tables[i]);err!=nil {http.Error(w,"Não foi possível limpar tabela "+tables[i]+"; restauração cancelada",500);return} }
	for _,table:=range tables {
		var present int
		if err=tx.QueryRow("SELECT COUNT(*) FROM restoredb.sqlite_master WHERE type='table' AND name=?",table).Scan(&present);err!=nil||present==0 { continue }
		mainCols:=[]string{};rows,e:=tx.Query("PRAGMA main.table_info("+table+")");if e!=nil {http.Error(w,"Não foi possível ler estrutura de "+table,500);return}
		for rows.Next(){var cid,notnull,pk int;var name,typ string;var def any;if e=rows.Scan(&cid,&name,&typ,&notnull,&def,&pk);e!=nil{rows.Close();http.Error(w,"Estrutura inválida",500);return};mainCols=append(mainCols,name)};rows.Close()
		restoreCols:=map[string]bool{};rows,e=tx.Query("PRAGMA restoredb.table_info("+table+")");if e!=nil{http.Error(w,"Não foi possível ler backup",500);return}
		for rows.Next(){var cid,notnull,pk int;var name,typ string;var def any;if e=rows.Scan(&cid,&name,&typ,&notnull,&def,&pk);e!=nil{rows.Close();http.Error(w,"Estrutura inválida",500);return};restoreCols[name]=true};rows.Close()
		common:=[]string{};for _,col:=range mainCols{if restoreCols[col]{common=append(common,col)}}
		if len(common)==0 {continue}
		quoted:=make([]string,len(common));for i,col:=range common{quoted[i]="\""+col+"\""}
		cols:=strings.Join(quoted,",")
		if _,err=tx.Exec("INSERT INTO main."+table+" ("+cols+") SELECT "+cols+" FROM restoredb."+table);err!=nil {http.Error(w,"Falha ao restaurar tabela "+table+"; consulte a cópia de segurança criada",500);return}
	}
	if err=tx.Commit();err!=nil {http.Error(w,"Não foi possível confirmar restauração",500);return}
	a.writeAudit(u.ID,"database_backup_restored","database",header.Filename,"Backup restaurado; cópia anterior salva em "+filepath.Base(safetyPath))
	jsonOut(w,200,map[string]any{"ok":true,"message":"Backup restaurado. A cópia anterior foi preservada na pasta backups.","safety_backup":filepath.Base(safetyPath)})
}
