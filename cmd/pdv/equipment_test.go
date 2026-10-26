package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEquipmentRequiresAdminAndSupportsCRUD(t *testing.T) {
	app := newCancellationTestApp(t)
	if _, err := app.db.Exec("INSERT INTO users(username,password_hash,display_name,role,active) VALUES('admin','unused','Administrador','admin',1),('cashier','unused','Caixa','cashier',1)"); err != nil {
		t.Fatal(err)
	}
	var adminID, cashierID int64
	if err := app.db.QueryRow("SELECT id FROM users WHERE username='admin'").Scan(&adminID); err != nil { t.Fatal(err) }
	if err := app.db.QueryRow("SELECT id FROM users WHERE username='cashier'").Scan(&cashierID); err != nil { t.Fatal(err) }
	adminToken := base64.RawURLEncoding.EncodeToString([]byte("equipment-admin-session-token-0001"))
	cashierToken := base64.RawURLEncoding.EncodeToString([]byte("equipment-cashier-session-token-01"))
	sessionStore.Lock()
	sessionStore.items[adminToken] = sessionInfo{userID: adminID, companyID: 0, expires: time.Now().Add(time.Hour)}
	sessionStore.items[cashierToken] = sessionInfo{userID: cashierID, companyID: 0, expires: time.Now().Add(time.Hour)}
	sessionStore.Unlock()
	t.Cleanup(func() { sessionStore.Lock(); delete(sessionStore.items, adminToken); delete(sessionStore.items, cashierToken); sessionStore.Unlock() })

	cashierReq := httptest.NewRequest(http.MethodGet, "/api/admin/equipment", nil)
	cashierReq.AddCookie(&http.Cookie{Name: "markys_session", Value: cashierToken})
	cashierRec := httptest.NewRecorder()
	app.authMiddleware(http.HandlerFunc(app.equipment)).ServeHTTP(cashierRec, cashierReq)
	if cashierRec.Code != http.StatusForbidden { t.Fatalf("cashier equipment access should be denied, got %d", cashierRec.Code) }

	body := `{"kind":"barcode_scanner","name":"Leitor USB do caixa","model":"HID genérico","connection_type":"usb","connection_details":"USB HID","protocol":"teclado HID + Enter","status":"pending","notes":"A validar no equipamento físico"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/admin/equipment", strings.NewReader(body))
	createReq.AddCookie(&http.Cookie{Name: "markys_session", Value: adminToken})
	createRec := httptest.NewRecorder()
	app.authMiddleware(http.HandlerFunc(app.equipment)).ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated { t.Fatalf("equipment create status %d: %s", createRec.Code, createRec.Body.String()) }

	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/equipment", nil)
	listReq.AddCookie(&http.Cookie{Name: "markys_session", Value: adminToken})
	listRec := httptest.NewRecorder()
	app.authMiddleware(http.HandlerFunc(app.equipment)).ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), "Leitor USB do caixa") { t.Fatalf("equipment list status %d: %s", listRec.Code, listRec.Body.String()) }
}
