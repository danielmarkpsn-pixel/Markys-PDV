package saas

import (
 "testing"
 "net/http"
 "net/http/httptest"
 "time"
)

func TestLoginRateLimiterBlocksAfterConfiguredAttempts(t *testing.T){
 limiter:=NewLoginRateLimiter(2,time.Minute)
 now:=time.Date(2026,1,1,0,0,0,0,time.UTC)
 if !limiter.Allow("192.0.2.10:1234",now){t.Fatal("primeira tentativa deveria ser permitida")}
 if !limiter.Allow("192.0.2.10:1234",now){t.Fatal("segunda tentativa deveria ser permitida")}
 if limiter.Allow("192.0.2.10:1234",now){t.Fatal("terceira tentativa deveria ser bloqueada")}
 if !limiter.Allow("192.0.2.11:1234",now){t.Fatal("outro IP deve ter limite independente")}
 if !limiter.Allow("192.0.2.10:1234",now.Add(time.Minute)){t.Fatal("limite deve reiniciar após janela")}
}

func TestLoginRateLimiterOnlyLimitsAuthenticationEndpoints(t *testing.T) {
	limiter:=NewLoginRateLimiter(1,time.Minute)
	next:=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusNoContent)})
	handler:=limiter.Middleware(next)
	request:=func(path string)int{req:=httptest.NewRequest(http.MethodPost,path,nil);req.RemoteAddr="192.0.2.50:1234";rec:=httptest.NewRecorder();handler.ServeHTTP(rec,req);return rec.Code}
	if got:=request("/api/catalog/products");got!=http.StatusNoContent{t.Fatalf("rota de catálogo não deve consumir limite de login: %d",got)}
	if got:=request("/api/platform/login");got!=http.StatusNoContent{t.Fatalf("primeira autenticação deveria passar: %d",got)}
	if got:=request("/api/auth/login");got!=http.StatusTooManyRequests{t.Fatalf("tentativa seguinte deveria ser limitada entre endpoints de autenticação: %d",got)}
}
