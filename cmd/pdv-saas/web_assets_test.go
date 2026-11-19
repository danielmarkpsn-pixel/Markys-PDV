package main

import (
	"strings"
	"testing"
)

func TestSaaSWebAssetsEmbedded(t *testing.T) {
	for _, name := range []string{"web/index.html", "web/plataforma.html"} {
		data, err := webAssets.ReadFile(name)
		if err != nil { t.Fatalf("asset %s não foi embutido: %v", name, err) }
		if !strings.Contains(string(data), "<!doctype html>") { t.Fatalf("asset %s não contém documento HTML", name) }
		if name=="web/index.html" {
			source:=string(data)
			if !strings.Contains(source,"setInterval") || !strings.Contains(source,"/api/auth/status") || !strings.Contains(source,"Sua conta foi acessada em outro dispositivo. Entre novamente para continuar.") {
				t.Fatal("a interface do PDV deve detectar sessão substituída pelo heartbeat e exibir a mensagem aprovada")
			}
		}
	}
}
