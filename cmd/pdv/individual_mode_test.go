package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIndividualModeKeepsInitialAdminSetup(t *testing.T) {
	app := newCancellationTestApp(t)
	t.Setenv("MARKYS_MODE", "individual")
	setup := httptest.NewRecorder()
	app.authSetup(setup, httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"company_id":0,"username":"admin","display_name":"Administrador local","password":"senha-local-forte-123"}`)))
	if setup.Code != http.StatusCreated { t.Fatalf("individual setup should remain available, got %d: %s", setup.Code, setup.Body.String()) }
	if !strings.Contains(setup.Body.String(), `"shared_mode":false`) { t.Fatalf("individual user should be marked as non-shared: %s", setup.Body.String()) }
}
