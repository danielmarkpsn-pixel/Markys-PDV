package saas

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	SessionCookieName = "markys_session"
	ReplacedSessionMessage = "Sua conta foi acessada em outro dispositivo. Entre novamente para continuar."
)

type HTTPHandler struct {
	DB *sql.DB
	CookieSecure bool
	SessionTTL time.Duration
}

type identityContextKey struct{}

func NewHTTPHandler(db *sql.DB, cookieSecure bool) *HTTPHandler {
	return &HTTPHandler{DB: db, CookieSecure: cookieSecure, SessionTTL: 12 * time.Hour}
}

func (h *HTTPHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/login", h.login)
	mux.HandleFunc("/api/auth/status", h.status)
	mux.HandleFunc("/api/auth/logout", h.logout)
}

// RequireSession validates the persistent session on every protected request.
// The tenant identity is derived only from the opaque server-issued cookie.
func (h *HTTPHandler) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := h.identity(r)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": ReplacedSessionMessage, "code": "SESSION_INVALID"})
			return
		}
		ctx := context.WithValue(r.Context(), identityContextKey{}, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func IdentityFromContext(ctx context.Context) (SessionIdentity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(SessionIdentity)
	return identity, ok
}

func (h *HTTPHandler) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Método não permitido"})
		return
	}
	var request struct {
		CompanyDocument string `json:"company_document"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeAuthJSON(w, r, &request) { return }
	document := regexp.MustCompile(`[^0-9]`).ReplaceAllString(request.CompanyDocument, "")
	if len(document) < 11 || len(document) > 14 || strings.TrimSpace(request.Username) == "" || request.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Informe CPF/CNPJ da empresa, usuário e senha"})
		return
	}

	var tenantID, tenantName string
	err := h.DB.QueryRowContext(r.Context(), `
		SELECT t.id::text, COALESCE(NULLIF(t.display_name, ''), t.legal_name)
		FROM tenants t
		JOIN tenant_subscriptions s ON s.tenant_id = t.id
		WHERE regexp_replace(t.document, '[^0-9]', '', 'g') = $1
		  AND t.status = 'active'
		  AND s.status IN ('trial', 'active')
		LIMIT 1
	`, document).Scan(&tenantID, &tenantName)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Empresa não encontrada, inativa ou sem assinatura válida"})
		return
	}

	user, err := AuthenticateUser(r.Context(), h.DB, tenantID, request.Username, request.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Usuário ou senha inválidos"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Não foi possível autenticar neste momento"})
		return
	}

	ttl := h.SessionTTL
	if ttl <= 0 { ttl = 12 * time.Hour }
	expires := time.Now().Add(ttl)
	token, _, err := CreateLoginSession(r.Context(), h.DB, tenantID, user.UserID, r.UserAgent(), remoteIP(r), expires)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "A empresa ou o usuário não está autorizado a iniciar sessão"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: h.CookieSecure, SameSite: http.SameSiteLaxMode,
		Expires: expires, MaxAge: int(ttl.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "company": map[string]string{"name": tenantName},
		"user": map[string]string{"id": user.UserID, "username": user.Username, "display_name": user.DisplayName, "role": user.Role},
		"expires_at": expires.UTC().Format(time.RFC3339),
	})
}

func (h *HTTPHandler) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Método não permitido"})
		return
	}
	identity, err := h.identity(r)
	if err != nil {
		clearSessionCookie(w, h.CookieSecure)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"authenticated": false, "code": "SESSION_INVALID", "message": ReplacedSessionMessage})
		return
	}
	var username, displayName string
	err = WithTenantTx(r.Context(), h.DB, identity.TenantID, nil, func(tx *sql.Tx) error {
		return tx.QueryRowContext(r.Context(), "SELECT username, display_name FROM tenant_users WHERE tenant_id = $1::uuid AND id = $2::uuid AND active = true", identity.TenantID, identity.UserID).Scan(&username, &displayName)
	})
	if err != nil {
		clearSessionCookie(w, h.CookieSecure)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"authenticated": false, "code": "SESSION_INVALID", "message": ReplacedSessionMessage})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user": map[string]string{"id": identity.UserID, "username": username, "display_name": displayName, "role": identity.Role},
		"tenant_id": identity.TenantID,
	})
}

func (h *HTTPHandler) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Método não permitido"})
		return
	}
	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		if identity, validateErr := ValidateLoginSession(r.Context(), h.DB, cookie.Value); validateErr == nil {
			_ = RevokeLoginSession(r.Context(), h.DB, identity.TenantID, cookie.Value)
		}
	}
	clearSessionCookie(w, h.CookieSecure)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *HTTPHandler) identity(r *http.Request) (SessionIdentity, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" { return SessionIdentity{}, ErrInvalidSession }
	return ValidateLoginSession(r.Context(), h.DB, cookie.Value)
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}

func decodeAuthJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Dados de autenticação inválidos"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil { return "" }
	return host
}
