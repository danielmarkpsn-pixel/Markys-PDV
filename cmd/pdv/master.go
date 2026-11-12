package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// sharedModeEnabled keeps the existing single-company installation as the default.
// The Master hierarchy is activated explicitly with MARKYS_MODE=shared.
func sharedModeEnabled() bool {
	mode := strings.ToLower(strings.TrimSpace(env("MARKYS_MODE", "individual")))
	return mode == "shared" || mode == "compartilhado"
}

// masterStore keeps Master credentials and the company registry outside DATA.
func (a *App) masterStore() *sql.DB {
	if a.masterDB != nil { return a.masterDB }
	return a.db
}

// masterDatabasePath places the protected file beside (not inside) DATA.
// PDV_MASTER_DB can override the default location for managed installations.
func masterDatabasePath(dataPath string) (string, error) {
	if configured := strings.TrimSpace(env("PDV_MASTER_DB", "")); configured != "" {
		return filepath.Abs(configured)
	}
	abs, err := filepath.Abs(dataPath)
	if err != nil { return "", err }
	root := filepath.Dir(filepath.Dir(abs))
	return filepath.Join(root, ".markys-master.db"), nil
}

func ensureMasterAccountTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS master_accounts (
		id INTEGER PRIMARY KEY CHECK(id=1),
		username TEXT NOT NULL UNIQUE COLLATE NOCASE,
		display_name TEXT NOT NULL,
		password_hash TEXT NOT NULL,
		recovery_hash TEXT NOT NULL,
		recovery_attempts INTEGER NOT NULL DEFAULT 0,
		recovery_locked_until TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1 CHECK(active=1),
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	return err
}

// migrateMasterMetadata moves the old Master identity and company registry out
// of the legacy DATA database. Product, sales, stock and user tables stay in DATA.
func migrateMasterMetadata(dataDB, masterDB *sql.DB) error {
	var exists int
	if err := dataDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='companies'").Scan(&exists); err != nil { return err }
	if exists == 1 {
		rows, err := dataDB.Query(`SELECT id,legal_name,trade_name,document,status,database_mode,database_path,sync_mode,sync_status,theme_color,logo_data_url,created_at FROM companies ORDER BY id`)
		if err != nil { return err }
		for rows.Next() {
			var id int64
			var legal, trade, document, status, mode, path, syncMode, syncStatus, color, logo, created string
			if err := rows.Scan(&id,&legal,&trade,&document,&status,&mode,&path,&syncMode,&syncStatus,&color,&logo,&created); err != nil { rows.Close(); return err }
			if _, err := masterDB.Exec(`INSERT OR IGNORE INTO companies(id,legal_name,trade_name,document,status,database_mode,database_path,sync_mode,sync_status,theme_color,logo_data_url,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id,legal,trade,document,status,mode,path,syncMode,syncStatus,color,logo,created); err != nil { rows.Close(); return err }
		}
		if err := rows.Err(); err != nil { rows.Close(); return err }
		rows.Close()
	}
	if err := dataDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='master_accounts'").Scan(&exists); err != nil { return err }
	if exists == 1 {
		rows, err := dataDB.Query(`SELECT id,username,display_name,password_hash,recovery_hash,recovery_attempts,recovery_locked_until,active,created_at,updated_at FROM master_accounts ORDER BY id`)
		if err != nil { return err }
		for rows.Next() {
			var id, attempts, active int
			var username, displayName, passwordHashValue, recoveryHash, locked, created, updated string
			if err := rows.Scan(&id,&username,&displayName,&passwordHashValue,&recoveryHash,&attempts,&locked,&active,&created,&updated); err != nil { rows.Close(); return err }
			if _, err := masterDB.Exec(`INSERT OR IGNORE INTO master_accounts(id,username,display_name,password_hash,recovery_hash,recovery_attempts,recovery_locked_until,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id,username,displayName,passwordHashValue,recoveryHash,attempts,locked,active,created,updated); err != nil { rows.Close(); return err }
		}
		if err := rows.Err(); err != nil { rows.Close(); return err }
		rows.Close()
	}
	// Drop only the two metadata tables after their rows were copied successfully.
	if _, err := dataDB.Exec("DROP TABLE IF EXISTS master_accounts; DROP TABLE IF EXISTS companies;"); err != nil { return err }
	return nil
}

// protectMasterDatabase hides the Master file and restricts its Windows ACL to
// the current owner, SYSTEM and local Administrators. DATA is never modified.
func protectMasterDatabase(path string) error {
	if runtime.GOOS != "windows" { return os.Chmod(path, 0600) }
	who, err := exec.Command("whoami").Output()
	if err != nil { return err }
	account := strings.TrimSpace(string(who))
	if account == "" { return errors.New("não foi possível identificar o usuário do Windows") }
	acl := exec.Command("icacls", path, "/inheritance:r", "/grant:r", account+":(F)", "*S-1-5-18:(F)", "*S-1-5-32-544:(F)")
	if output, err := acl.CombinedOutput(); err != nil { return errors.New(strings.TrimSpace(string(output))) }
	hide := exec.Command("attrib", "+h", "+s", path)
	if output, err := hide.CombinedOutput(); err != nil { return errors.New(strings.TrimSpace(string(output))) }
	return nil
}

// initMasterAccount creates the singleton Master account only once. Secrets are
// supplied by the machine owner through environment variables and are never
// written to source control or returned by an API.
func initMasterAccount(db *sql.DB) error {
	if err := ensureMasterAccountTable(db); err != nil { return err }
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM master_accounts").Scan(&count); err != nil { return err }
	if count > 0 { return nil }
	if !sharedModeEnabled() { return nil }

	username := strings.TrimSpace(env("MARKYS_MASTER_USERNAME", ""))
	displayName := strings.TrimSpace(env("MARKYS_MASTER_NAME", "Proprietário"))
	password := env("MARKYS_MASTER_PASSWORD", "")
	recoveryCode := env("MARKYS_MASTER_RECOVERY_CODE", "")
	// An incomplete environment must not create a weak/default Master account.
	if username == "" && password == "" && recoveryCode == "" { return errors.New("Modo compartilhado exige a configuração inicial da conta Master por MARKYS_MASTER_USERNAME, MARKYS_MASTER_PASSWORD e MARKYS_MASTER_RECOVERY_CODE") }
	if len(username) < 3 || len(username) > 64 || len(password) < 10 || len(password) > 256 || len(recoveryCode) < 16 || len(recoveryCode) > 256 {
		return errors.New("Configuração Master incompleta: defina MARKYS_MASTER_USERNAME, MARKYS_MASTER_PASSWORD (10+ caracteres) e MARKYS_MASTER_RECOVERY_CODE (16+ caracteres)")
	}
	passwordEncoded, err := passwordHash(password); if err != nil { return err }
	recoveryEncoded, err := passwordHash(recoveryCode); if err != nil { return err }
	_, err = db.Exec("INSERT INTO master_accounts(id,username,display_name,password_hash,recovery_hash) VALUES(1,?,?,?,?)", username, displayName, passwordEncoded, recoveryEncoded)
	return err
}

func (a *App) masterConfigured() bool {
	var count int
	return a.masterStore().QueryRow("SELECT COUNT(*) FROM master_accounts WHERE id=1 AND active=1").Scan(&count) == nil && count == 1
}

func newMasterRecoveryCode() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil { return "", err }
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil { host = strings.Trim(strings.TrimSpace(r.RemoteAddr), "[]") }
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// authRecoverMaster is the only password-recovery path. It requires the
// one-time recovery secret and is restricted to the local machine.
func (a *App) authRecoverMaster(w http.ResponseWriter, r *http.Request) {
	if !sharedModeEnabled() { http.Error(w, "A recuperação Master existe somente no modo compartilhado.", http.StatusForbidden); return }
	if r.Method != http.MethodPost { w.Header().Set("Allow", "POST"); http.Error(w, "Método não permitido", 405); return }
	if !isLoopbackRequest(r) { http.Error(w, "A recuperação do Master só pode ser executada neste computador.", http.StatusForbidden); return }
	var req struct {
		Username string `json:"username"`
		RecoveryCode string `json:"recovery_code"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) { return }
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 || len(req.Username) > 64 || strings.TrimSpace(req.RecoveryCode) == "" {
		http.Error(w, "Informe o usuário Master e o código de recuperação.", 400); return
	}
	var username, recoveryHash, lockedUntil string
	var attempts int
	err := a.masterStore().QueryRow("SELECT username,recovery_hash,recovery_attempts,recovery_locked_until FROM master_accounts WHERE id=1 AND active=1").Scan(&username,&recoveryHash,&attempts,&lockedUntil)
	if err == sql.ErrNoRows { http.Error(w, "Conta Master ainda não foi configurada pelo proprietário.", 409); return }
	if err != nil { http.Error(w, "Não foi possível consultar a configuração Master.", 500); return }

	now := time.Now().UTC()
	if lockedUntil != "" {
		if until, parseErr := time.Parse(time.RFC3339, lockedUntil); parseErr == nil && now.Before(until) {
			http.Error(w, "Recuperação temporariamente bloqueada por excesso de tentativas. Tente novamente após o prazo de bloqueio.", 429); return
		}
		if _, err := a.masterStore().Exec("UPDATE master_accounts SET recovery_attempts=0,recovery_locked_until='' WHERE id=1"); err != nil {
			http.Error(w, "Não foi possível liberar o contador de tentativas.", 500); return
		}
		attempts = 0
	}
	valid := strings.EqualFold(req.Username, username) && verifyPassword(req.RecoveryCode, recoveryHash)
	if !valid {
		attempts++
		if attempts >= 5 {
			until := now.Add(30*time.Minute).Format(time.RFC3339)
			_, err = a.masterStore().Exec("UPDATE master_accounts SET recovery_attempts=?,recovery_locked_until=? WHERE id=1", attempts, until)
		} else {
			_, err = a.masterStore().Exec("UPDATE master_accounts SET recovery_attempts=? WHERE id=1", attempts)
		}
		if err != nil { http.Error(w, "Não foi possível registrar a tentativa.", 500); return }
		a.writeAudit(0, "master_recovery_failed", "master_account", "1", "Tentativa de recuperação Master recusada")
		http.Error(w, "Usuário ou código de recuperação inválido.", 401); return
	}

	newPasswordHash, err := passwordHash(req.Password)
	if err != nil { http.Error(w, err.Error(), 400); return }
	rotatedCode, err := newMasterRecoveryCode()
	if err != nil { http.Error(w, "Não foi possível gerar um novo código de recuperação.", 500); return }
	rotatedHash, err := passwordHash(rotatedCode)
	if err != nil { http.Error(w, "Não foi possível proteger o novo código de recuperação.", 500); return }
	result, err := a.masterStore().Exec(`UPDATE master_accounts
		SET password_hash=?, recovery_hash=?, recovery_attempts=0, recovery_locked_until='', updated_at=CURRENT_TIMESTAMP
		WHERE id=1 AND username=? COLLATE NOCASE AND recovery_hash=?`, newPasswordHash, rotatedHash, username, recoveryHash)
	if err != nil { http.Error(w, "Não foi possível concluir a recuperação.", 500); return }
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 { http.Error(w, "A conta Master foi alterada durante a recuperação. Tente novamente.", 409); return }
	a.writeAudit(0, "master_password_recovered", "master_account", "1", "Senha Master recuperada; código de recuperação anterior invalidado e rotacionado")
	jsonOut(w, 200, map[string]any{
		"ok": true,
		"message": "Senha Master redefinida. O código de recuperação anterior foi invalidado.",
		"new_recovery_code": rotatedCode,
		"warning": "Este novo código será exibido somente agora. Guarde-o em local seguro.",
	})
}

// masterDeleteUser is deliberately separate from tenant user-management APIs.
// Only the permanent Master identity can remove a user from a selected company.
func (a *App) masterDeleteUser(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil || u.Role != "master" { http.Error(w, "Somente o Master pode gerenciar usuários de empresas.", 403); return }
	companyID, err := parseCompanyID(r.URL.Query().Get("company_id"))
	if err != nil { http.Error(w, err.Error(), 400); return }
	tenant, err := a.appForCompany(companyID)
	if err != nil { http.Error(w, err.Error(), 404); return }
	switch r.Method {
	case http.MethodGet:
		rows, err := tenant.db.Query("SELECT id,username,display_name,role,active,created_at FROM users ORDER BY id")
		if err != nil { http.Error(w, "Não foi possível consultar usuários.", 500); return }
		defer rows.Close()
		out := []authUser{}
		for rows.Next() {
			var item authUser; var active int
			if err := rows.Scan(&item.ID,&item.Username,&item.DisplayName,&item.Role,&active,&item.CreatedAt); err != nil { http.Error(w, "Erro ao ler usuários.", 500); return }
			item.Active=active==1; item.CompanyID=companyID; out=append(out,item)
		}
		if err := rows.Err(); err != nil { http.Error(w, "Erro ao ler usuários.", 500); return }
		jsonOut(w,200,out)
	case http.MethodDelete:
		userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
		if err != nil || userID <= 0 { http.Error(w, "ID de usuário inválido.", 400); return }
		var username, role string
		if err := tenant.db.QueryRow("SELECT username,role FROM users WHERE id=?", userID).Scan(&username,&role); err != nil {
			if err == sql.ErrNoRows { http.Error(w, "Usuário não encontrado.", 404) } else { http.Error(w, "Não foi possível consultar o usuário.", 500) }
			return
		}
		// Preserve historical foreign-key references (sales, cash sessions and audit)
		// while permanently removing this identity's ability to sign in.
		disabledName := "removido-" + strconv.FormatInt(userID,10) + "-" + username
		if _, err := tenant.db.Exec("UPDATE users SET active=0,username=? WHERE id=?", disabledName, userID); err != nil {
			http.Error(w, "Não foi possível remover o acesso deste usuário.", 500); return
		}
		tenant.writeAudit(0, "user_deleted_by_master", "user", strconv.FormatInt(userID,10), "Acesso do usuário "+username+" ("+role+") removido pelo Master; histórico preservado")
		jsonOut(w, 200, map[string]any{"ok":true,"company_id":companyID,"user_id":userID,"access_removed":true,"history_preserved":true})
	default:
		w.Header().Set("Allow", "GET, DELETE")
		http.Error(w, "Método não permitido", 405)
	}
}
