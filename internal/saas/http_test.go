package saas

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPAuthRejectsMalformedLoginBeforeDatabaseAccess(t *testing.T) {
	h := NewHTTPHandler(nil, true)
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"company_document":"123","username":"","password":""}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest { t.Fatalf("status = %d, esperado 400; body=%s", rec.Code, rec.Body.String()) }
}

func TestHTTPAuthRejectsUnsupportedMethod(t *testing.T) {
	h := NewHTTPHandler(nil, true)
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed { t.Fatalf("status = %d, esperado 405", rec.Code) }
	if rec.Header().Get("Allow") != http.MethodPost { t.Fatalf("Allow = %q, esperado POST", rec.Header().Get("Allow")) }
}

func TestProtectedRouteRejectsMissingSession(t *testing.T) {
	h := NewHTTPHandler(nil, true)
	protected := h.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("rota protegida não deveria ser executada sem sessão")
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/private", nil)
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized { t.Fatalf("status = %d, esperado 401", rec.Code) }
	if !strings.Contains(rec.Body.String(), ReplacedSessionMessage) { t.Fatalf("mensagem de sessão inválida ausente: %s", rec.Body.String()) }
}
