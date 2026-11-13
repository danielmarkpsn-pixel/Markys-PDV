package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMasterBootstrapAndOneTimeRecoveryRotation(t *testing.T) {
	app := newCancellationTestApp(t)
	t.Setenv("MARKYS_MODE", "shared")
	t.Setenv("MARKYS_MASTER_USERNAME", "owner")
	t.Setenv("MARKYS_MASTER_NAME", "Proprietário")
	t.Setenv("MARKYS_MASTER_PASSWORD", "senha-inicial-forte-123")
	t.Setenv("MARKYS_MASTER_RECOVERY_CODE", "codigo-inicial-secreto-123456789")
	if err := initMasterAccount(app.db); err != nil { t.Fatal(err) }
	if !app.masterConfigured() { t.Fatal("Master account should be initialized") }

	var storedHash string
	if err := app.db.QueryRow("SELECT recovery_hash FROM master_accounts WHERE id=1").Scan(&storedHash); err != nil { t.Fatal(err) }
	if storedHash == "codigo-inicial-secreto-123456789" || !verifyPassword("codigo-inicial-secreto-123456789", storedHash) {
		t.Fatal("recovery code must be stored only as a password hash")
	}

	body := `{"username":"owner","recovery_code":"codigo-inicial-secreto-123456789","password":"senha-nova-forte-456"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/recover-admin", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	app.authRecoverMaster(rec, req)
	if rec.Code != http.StatusOK { t.Fatalf("recovery status %d: %s", rec.Code, rec.Body.String()) }
	var response struct { NewCode string `json:"new_recovery_code"` }
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil { t.Fatal(err) }
	if response.NewCode == "" || response.NewCode == "codigo-inicial-secreto-123456789" { t.Fatal("recovery must rotate and return a new code") }
	if err := app.db.QueryRow("SELECT recovery_hash FROM master_accounts WHERE id=1").Scan(&storedHash); err != nil { t.Fatal(err) }
	if !verifyPassword(response.NewCode, storedHash) || verifyPassword("codigo-inicial-secreto-123456789", storedHash) {
		t.Fatal("old recovery code must be invalidated and new code stored as a hash")
	}

	loginBody := `{"company_id":0,"username":"owner","password":"senha-nova-forte-456"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	app.authLogin(loginRec, loginReq)
	if loginRec.Code != http.StatusOK || !strings.Contains(loginRec.Body.String(), `"role":"master"`) {
		t.Fatalf("Master login after recovery failed: %d %s", loginRec.Code, loginRec.Body.String())
	}
}

func TestMasterRecoveryRejectsRemoteRequests(t *testing.T) {
	t.Setenv("MARKYS_MODE", "shared")
	app := newCancellationTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/recover-admin", strings.NewReader(`{}`))
	req.RemoteAddr = "192.0.2.10:12345"
	rec := httptest.NewRecorder()
	app.authRecoverMaster(rec, req)
	if rec.Code != http.StatusForbidden { t.Fatalf("expected remote recovery to be forbidden, got %d", rec.Code) }
}


func TestMasterSecurityDirectorySurvivesDataDeletion(t *testing.T) {
	t.Setenv("PDV_MASTER_DB", "")
	root := t.TempDir()
	dataDir := filepath.Join(root, "MARKYS-PDV", "data")
	if err := os.MkdirAll(dataDir, 0700); err != nil { t.Fatal(err) }
	dataPath := filepath.Join(dataDir, "markys-pdv.db")
	masterPath, err := masterDatabasePath(dataPath)
	if err != nil { t.Fatal(err) }
	want := filepath.Join(root, "MARKYS-PDV", "seguranca", ".markys-master.db")
	if masterPath != want { t.Fatalf("Master database path = %q, want %q", masterPath, want) }
	if err := os.MkdirAll(filepath.Dir(masterPath), 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(masterPath, []byte("master identity"), 0600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(masterProvisionMarkerPath(masterPath), []byte("provisioned"), 0600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(dataPath, []byte("operational data"), 0600); err != nil { t.Fatal(err) }

	if err := os.RemoveAll(dataDir); err != nil { t.Fatal(err) }
	if _, err := os.Stat(masterPath); err != nil { t.Fatalf("Master identity was removed with DATA: %v", err) }
	if _, err := os.Stat(masterProvisionMarkerPath(masterPath)); err != nil { t.Fatalf("Master provisioning marker was removed with DATA: %v", err) }
}

func TestMissingProvisionedMasterDatabaseBlocksRebootstrap(t *testing.T) {
	root := t.TempDir()
	securityDir := filepath.Join(root, "seguranca")
	if err := os.MkdirAll(securityDir, 0700); err != nil { t.Fatal(err) }
	masterPath := filepath.Join(securityDir, ".markys-master.db")
	if err := os.WriteFile(masterProvisionMarkerPath(masterPath), []byte("provisioned"), 0600); err != nil { t.Fatal(err) }
	provisioned, err := masterProvisionedState(masterPath)
	if err == nil || !provisioned { t.Fatal("missing provisioned Master database must block startup and rebootstrap") }
}
