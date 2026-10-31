package main

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"sync"
)

var tenantDBCache = struct {
	sync.Mutex
	items map[string]*sql.DB
}{items: map[string]*sql.DB{}}

// appForCompany resolves the tenant database from the master registry. Company 0
// is the existing local database, preserving installations created before multi-company support.
func (a *App) appForCompany(companyID int64) (*App, error) {
	if companyID < 0 { return nil, errors.New("Empresa inválida") }
	if companyID == 0 {
		copy := *a
		copy.companyID = 0
		copy.masterDB = a.db
		if a.masterDB != nil { copy.masterDB = a.masterDB }
		return &copy, nil
	}
	master := a.db
	if a.masterDB != nil { master = a.masterDB }
	var path, status, document string
	if err := master.QueryRow("SELECT database_path,status,document FROM companies WHERE id=?", companyID).Scan(&path,&status,&document); err != nil {
		return nil, errors.New("Empresa não encontrada")
	}
	if status != "active" { return nil, errors.New("Esta empresa está inativa") }

	// Never let SQLite silently create an empty database when a stored path points
	// to an old installation folder. If the registered file moved with the data
	// directory, recover it from the stable companies folder beside the master DB.
	path, err := filepath.Abs(path)
	if err != nil { return nil, errors.New("Caminho do banco da empresa inválido") }
	if info, statErr := os.Stat(path); statErr != nil || !info.Mode().IsRegular() {
		root, rootErr := a.companyStorageRoot()
		if rootErr == nil {
			candidate := filepath.Join(root, document+".db")
			if info, candidateErr := os.Stat(candidate); candidateErr == nil && info.Mode().IsRegular() {
				path, err = filepath.Abs(candidate)
				if err == nil {
					if _, updateErr := master.Exec("UPDATE companies SET database_path=? WHERE id=?", path, companyID); updateErr != nil {
						return nil, errors.New("Banco encontrado, mas não foi possível atualizar o caminho salvo")
					}
				} else { return nil, errors.New("Caminho recuperado do banco da empresa inválido") }
			} else {
				return nil, errors.New("Arquivo do banco da empresa não encontrado. Os dados não foram apagados; restaure o arquivo de dados da empresa antes de entrar.")
			}
		} else {
			return nil, errors.New("Não foi possível localizar a pasta de dados da empresa. Os dados existentes foram preservados.")
		}
	}
	tenantDBCache.Lock()
	db := tenantDBCache.items[path]
	if db == nil {
		db, err = sql.Open("sqlite", path)
		if err == nil {
			db.SetMaxOpenConns(1)
			_, err = db.Exec("PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;")
		}
		if err == nil { err = migrateProductColumns(db) }
		if err != nil {
			if db != nil { _ = db.Close() }
			tenantDBCache.Unlock()
			return nil, errors.New("Não foi possível abrir o banco da empresa")
		}
		tenantDBCache.items[path] = db
	}
	if err := ensureEquipmentTable(db); err != nil {
		tenantDBCache.Unlock()
		return nil, errors.New("Não foi possível preparar o cadastro de equipamentos")
	}
	tenantDBCache.Unlock()
	return &App{db:db, dbPath:path, masterDB:master, companyID:companyID}, nil
}

func (a *App) authCompanies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet { w.Header().Set("Allow","GET"); http.Error(w,"Método não permitido",405); return }
	type companyOption struct { ID int64 `json:"id"`; Name string `json:"name"`; Status string `json:"status"`; ThemeColor string `json:"theme_color"`; LogoDataURL string `json:"logo_data_url"` }
	out := []companyOption{{ID:0,Name:"Empresa principal",Status:"active",ThemeColor:"#1677e8"}}
	rows,err:=a.db.Query("SELECT id,COALESCE(NULLIF(trade_name,''),legal_name),status,theme_color,logo_data_url FROM companies WHERE status='active' ORDER BY legal_name COLLATE NOCASE")
	if err != nil { http.Error(w,"Não foi possível listar empresas",500); return }
	defer rows.Close()
	for rows.Next() { var c companyOption; if err:=rows.Scan(&c.ID,&c.Name,&c.Status,&c.ThemeColor,&c.LogoDataURL);err!=nil { http.Error(w,"Erro ao ler empresas",500);return }; out=append(out,c) }
	if err:=rows.Err();err!=nil { http.Error(w,"Erro ao ler empresas",500);return }
	jsonOut(w,200,out)
}

func (a *App) companyBranding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet { w.Header().Set("Allow","GET");http.Error(w,"Método não permitido",405);return }
	type branding struct { ID int64 `json:"company_id"`; Name string `json:"company_name"`; ThemeColor string `json:"theme_color"`; LogoDataURL string `json:"logo_data_url"` }
	if a.companyID==0 { jsonOut(w,200,branding{ID:0,Name:"Empresa principal",ThemeColor:"#1677e8"});return }
	var b branding
	err:=a.masterDB.QueryRow("SELECT id,COALESCE(NULLIF(trade_name,''),legal_name),theme_color,logo_data_url FROM companies WHERE id=? AND status='active'",a.companyID).Scan(&b.ID,&b.Name,&b.ThemeColor,&b.LogoDataURL)
	if err!=nil { http.Error(w,"Configuração visual da empresa não encontrada",404);return }
	jsonOut(w,200,b)
}

func isMasterRoute(path string) bool {
	switch path {
	case "/api/health","/api/auth/companies","/api/auth/status","/api/auth/setup","/api/auth/login","/api/auth/recover-admin","/api/auth/logout","/api/admin/companies","/api/admin/companies/status","/api/admin/companies/branding","/api/admin/companies/bootstrap":
		return true
	default:
		return false
	}
}

func (a *App) rootHandler(master http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		if strings.HasPrefix(r.URL.Path,"/api/") && !isMasterRoute(r.URL.Path) {
			a.serveTenant(w,r)
			return
		}
		a.authMiddleware(master).ServeHTTP(w,r)
	})
}

func (a *App) serveTenant(w http.ResponseWriter,r *http.Request) {
	cookie,err:=r.Cookie("markys_session")
	if err!=nil { http.Error(w,"Autenticação necessária",http.StatusUnauthorized);return }
	sessionStore.Lock()
	session,ok:=sessionStore.items[cookie.Value]
	if ok && session.expires.Before(nowTime()) { delete(sessionStore.items,cookie.Value);ok=false }
	sessionStore.Unlock()
	if !ok { http.Error(w,"Sessão expirada. Entre novamente.",http.StatusUnauthorized);return }
	app,err:=a.appForCompany(session.companyID)
	if err!=nil { http.Error(w,err.Error(),http.StatusForbidden);return }
	mux:=http.NewServeMux()
	mux.HandleFunc("/api/auth/manager-verify",app.authManagerVerify)
	mux.HandleFunc("/api/company/branding",app.companyBranding)
	mux.HandleFunc("/api/users",app.users)
	mux.HandleFunc("/api/users/status",app.userStatus)
	mux.HandleFunc("/api/users/role",app.userRole)
	mux.HandleFunc("/api/users/password",app.userPasswordReset)
	mux.HandleFunc("/api/admin/audit",app.auditEntries)
	mux.HandleFunc("/api/admin/backup",app.databaseBackup)
	mux.HandleFunc("/api/admin/backup/restore",app.restoreDatabase)
  mux.HandleFunc("/api/admin/equipment",app.equipment)
	mux.HandleFunc("/api/products",app.products)
	mux.HandleFunc("/api/stock-movements",app.stockMovements)
	mux.HandleFunc("/api/reports/stock.csv",app.stockReportCSV)
	mux.HandleFunc("/api/reports/movements.csv",app.movementReportCSV)
	mux.HandleFunc("/api/categories",app.catalog("categories"))
	mux.HandleFunc("/api/suppliers",app.catalog("suppliers"))
	mux.HandleFunc("/api/customers",app.customers)
	mux.HandleFunc("/api/sales",app.sales)
	mux.HandleFunc("/api/quotes",app.quotes)
	mux.HandleFunc("/api/cash",app.cash)
	app.authMiddleware(mux).ServeHTTP(w,r)
}

func nowTime() time.Time { return time.Now() }

func parseCompanyID(raw string) (int64,error) {
	if strings.TrimSpace(raw)=="" { return 0,nil }
	id,err:=strconv.ParseInt(raw,10,64)
	if err!=nil||id<0 { return 0,errors.New("Empresa inválida") }
	return id,nil
}
