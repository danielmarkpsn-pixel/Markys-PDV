package saas

import (
 "net"
 "net/http"
 "strings"
 "sync"
 "time"
)

type loginAttempt struct { count int; reset time.Time }
type LoginRateLimiter struct { mu sync.Mutex; attempts map[string]loginAttempt; limit int; window time.Duration }

func NewLoginRateLimiter(limit int, window time.Duration)*LoginRateLimiter{
 if limit<1{limit=10};if window<=0{window=5*time.Minute}
 return &LoginRateLimiter{attempts:make(map[string]loginAttempt),limit:limit,window:window}
}

// Allow limits authentication attempts per source IP. It deliberately does not trust
// X-Forwarded-For; deployments behind a proxy must configure trusted proxy IPs
// before forwarding client addresses.
func(l *LoginRateLimiter)Allow(remoteAddr string,now time.Time)bool{
 host,_,err:=net.SplitHostPort(strings.TrimSpace(remoteAddr));if err!=nil{host=strings.TrimSpace(remoteAddr)}
 if host==""{host="unknown"}
 l.mu.Lock();defer l.mu.Unlock()
 for key,value:=range l.attempts{if !now.Before(value.reset){delete(l.attempts,key)}}
 current,ok:=l.attempts[host]
 if !ok||!now.Before(current.reset){current=loginAttempt{reset:now.Add(l.window)}}
 if current.count>=l.limit{return false}
 current.count++;l.attempts[host]=current
 if len(l.attempts)>4096{for key:=range l.attempts{delete(l.attempts,key);if len(l.attempts)<=3072{break}}}
 return true
}

func(l *LoginRateLimiter)Middleware(next http.Handler)http.Handler{
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method==http.MethodPost && (r.URL.Path=="/api/auth/login" || r.URL.Path=="/api/platform/login" || r.URL.Path=="/api/platform/bootstrap") && !l.Allow(r.RemoteAddr,time.Now()){
   writeJSON(w,http.StatusTooManyRequests,map[string]string{"error":"Muitas tentativas de acesso. Aguarde alguns minutos e tente novamente.","code":"LOGIN_RATE_LIMIT"})
   return
  }
  next.ServeHTTP(w,r)
 })
}
