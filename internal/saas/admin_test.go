package saas

import (
 "net/http"
 "net/http/httptest"
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
