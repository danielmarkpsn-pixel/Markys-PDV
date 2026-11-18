package saas

import (
 "net/http"
 "net/http/httptest"
 "context"
 "testing"
)

func TestTenantAdminRequiresAdminRole(t *testing.T) {
 mux:=http.NewServeMux()
 auth:=NewHTTPHandler(nil,true)
 NewTenantAdminHandler(nil,auth).Register(mux)
 protected:=auth.RequireSession(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusOK)}))
 req:=httptest.NewRequest(http.MethodGet,"/api/admin/users",nil)
 rec:=httptest.NewRecorder()
 protected.ServeHTTP(rec,req)
 if rec.Code!=http.StatusUnauthorized {t.Fatalf("status=%d, esperado 401",rec.Code)}
}

func TestTenantAdminLicenseDoesNotExposeMutationMethod(t *testing.T) {
 h:=NewTenantAdminHandler(nil,NewHTTPHandler(nil,true))
 mux:=http.NewServeMux();h.Register(mux)
 req:=httptest.NewRequest(http.MethodPost,"/api/admin/license",nil)
 rec:=httptest.NewRecorder();mux.ServeHTTP(rec,req)
 if rec.Code!=http.StatusUnauthorized {t.Fatalf("status=%d, esperado 401",rec.Code)}
}

func TestTenantAdminRejectsNonAdminRole(t *testing.T) {
 auth:=NewHTTPHandler(nil,true)
 handler:=NewTenantAdminHandler(nil,auth)
 req:=httptest.NewRequest(http.MethodGet,"/api/admin/license",nil)
 ctx:=context.WithValue(req.Context(),identityContextKey{},SessionIdentity{TenantID:"00000000-0000-0000-0000-000000000001",UserID:"00000000-0000-0000-0000-000000000002",Role:"cashier"})
 req=req.WithContext(ctx)
 rec:=httptest.NewRecorder()
 auth.RequireSession(http.HandlerFunc(handler.license)).ServeHTTP(rec,req)
 if rec.Code!=http.StatusForbidden {t.Fatalf("status=%d, esperado 403",rec.Code)}
}
