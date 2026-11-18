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
	}
}
