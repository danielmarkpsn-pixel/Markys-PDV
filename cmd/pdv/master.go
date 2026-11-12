package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// initMasterAccount creates the singleton Master account only once. Secrets are
// supplied by the machine owner through environment variables and are never
// written to source control or returned by an API.
func initMasterAccount(db *sql.DB) error {
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
	if err != nil { return err }
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM master_accounts").Scan(&count); err != nil { return err }
	if count > 0 { return nil }

	username := strings.TrimSpace(env("MARKYS_MASTER_USERNAME", ""))
	displayName := strings.TrimSpace(env("MARKYS_MASTER_NAME", "Proprietário"))
	password := env("MARKYS_MASTER_PASSWORD", "")
	recoveryCode := env("MARKYS_MASTER_RECOVERY_CODE", "")
	// An incomplete environment must not create a weak/default Master account.
	if username == "" && password == "" && recoveryCode == "" { return nil }
	if len(username) < 3 || len(username) > 64 || len(password) < 10 || len(password) > 256 || len(recoveryCode) < 16 || len(recoveryCode) > 256 {
		return errors.New("Configuração Master incompleta: defina MARKYS_MASTER_USERNAME, MARKYS_MASTER_PASSWORD (10+ caracteres) e MARKYS_MASTER_RECOVERY_CODE (16+ caracteres)")
	}
	passwordHash, err := passwordHash(password); if err != nil { return err }
	recoveryHash, err := passwordHash(recoveryCode); if err != nil { return err }
	_, err = db.Exec("INSERT INTO master_accounts(id,username,display_name,password_hash,recovery_hash) VALUES(1,?,?,?,?)", username, displayName, passwordHash, recoveryHash)
	return err
}

func (a *App) masterConfigured() bool {
	var count int
	return a.db.QueryRow("SELECT COUNT(*) FROM master_accounts WHERE id=1 AND active=1").Scan(&count) == nil && count == 1
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
	newPasswordHash, err := passwordHash(req.Password)
	if err != nil { http.Error(w, err.Error(), 400); return }

	var username, recoveryHash, lockedUntil string
	var attempts int
	err = a.db.QueryRow("SELECT username,recovery_hash,recovery_attempts,recovery_locked_until FROM master_accounts WHERE id=1 AND active=1").Scan(&username,&recoveryHash,&attempts,&lockedUntil)
	if err == sql.ErrNoRows { http.Error(w, "Conta Master ainda não foi configurada pelo proprietário.", 409); return }
	if err != nil { http.Error(w, "Não foi possível consultar a configuração Master.", 500); return }

	now := time.Now().UTC()
	if lockedUntil != "" {
		if until, parseErr := time.Parse(time.RFC3339, lockedUntil); parseErr == nil && now.Before(until) {
			http.Error(w, "Recuperação temporariamente bloqueada por excesso de tentativas. Tente novamente após o prazo de bloqueio.", 429); return
		}
		if _, err := a.db.Exec("UPDATE master_accounts SET recovery_attempts=0,recovery_locked_until='' WHERE id=1"); err != nil {
			http.Error(w, "Não foi possível liberar o contador de tentativas.", 500); return
		}
		attempts = 0
	}
	valid := strings.EqualFold(req.Username, username) && verifyPassword(req.RecoveryCode, recoveryHash)
	if !valid {
		attempts++
		if attempts >= 5 {
			until := now.Add(30*time.Minute).Format(time.RFC3339)
			_, err = a.db.Exec("UPDATE master_accounts SET recovery_attempts=?,recovery_locked_until=? WHERE id=1", attempts, until)
		} else {
			_, err = a.db.Exec("UPDATE master_accounts SET recovery_attempts=? WHERE id=1", attempts)
		}
		if err != nil { http.Error(w, "Não foi possível registrar a tentativa.", 500); return }
		a.writeAudit(0, "master_recovery_failed", "master_account", "1", "Tentativa de recuperação Master recusada")
		http.Error(w, "Usuário ou código de recuperação inválido.", 401); return
	}

	rotatedCode, err := newMasterRecoveryCode()
	if err != nil { http.Error(w, "Não foi possível gerar um novo código de recuperação.", 500); return }
	rotatedHash, err := passwordHash(rotatedCode)
	if err != nil { http.Error(w, "Não foi possível proteger o novo código de recuperação.", 500); return }
	result, err := a.db.Exec(`UPDATE master_accounts
		SET password_hash=?, recovery_hash=?, recovery_attempts=0, recovery_locked_until='', updated_at=CURRENT_TIMESTAMP
		WHERE id=1 AND username=? COLLATE NOCASE`, newPasswordHash, rotatedHash, username)
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
	if r.Method != http.MethodDelete { w.Header().Set("Allow", "DELETE"); http.Error(w, "Método não permitido", 405); return }
	u := a.currentUser(r)
	if u == nil || u.Role != "master" { http.Error(w, "Somente o Master pode excluir usuários.", 403); return }
	companyID, err := parseCompanyID(r.URL.Query().Get("company_id"))
	if err != nil { http.Error(w, err.Error(), 400); return }
	userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil || userID <= 0 { http.Error(w, "ID de usuário inválido.", 400); return }
	tenant, err := a.appForCompany(companyID)
	if err != nil { http.Error(w, err.Error(), 404); return }
	var username, role string
	if err := tenant.db.QueryRow("SELECT username,role FROM users WHERE id=?", userID).Scan(&username,&role); err != nil {
		if err == sql.ErrNoRows { http.Error(w, "Usuário não encontrado.", 404) } else { http.Error(w, "Não foi possível consultar o usuário.", 500) }
		return
	}
	if _, err := tenant.db.Exec("DELETE FROM users WHERE id=?", userID); err != nil {
		http.Error(w, "Não foi possível excluir o usuário. Verifique se há registros vinculados; se houver, desative o acesso em vez de apagar o histórico.", 409); return
	}
	tenant.writeAudit(0, "user_deleted_by_master", "user", strconv.FormatInt(userID,10), "Usuário "+username+" ("+role+") excluído pelo Master")
	jsonOut(w, 200, map[string]any{"ok":true,"company_id":companyID,"user_id":userID})
}
