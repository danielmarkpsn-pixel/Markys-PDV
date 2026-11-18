package saas

import (
 "testing"
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
