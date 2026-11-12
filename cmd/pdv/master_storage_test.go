package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMasterDatabasePathIsOutsideData(t *testing.T) {
	dataPath := filepath.Join(t.TempDir(), "data", "markys-pdv.db")
	path, err := masterDatabasePath(dataPath)
	if err != nil { t.Fatal(err) }
	want := filepath.Join(filepath.Dir(filepath.Dir(dataPath)), ".markys-master.db")
	if path != want { t.Fatalf("master path = %q, want %q", path, want) }
	if filepath.Dir(path) == filepath.Dir(dataPath) { t.Fatalf("Master file must not be inside DATA: %q", path) }
}

func TestMasterPathOverrideCannotPointInsideData(t *testing.T) {
	dataPath := filepath.Join(t.TempDir(), "data", "markys-pdv.db")
	t.Setenv("PDV_MASTER_DB", filepath.Join(filepath.Dir(dataPath), ".markys-master.db"))
	if _, err := masterDatabasePath(dataPath); err == nil { t.Fatal("expected a Master path inside DATA to be rejected") }
}

func TestMasterMetadataMovesOutOfDataWithoutMovingOperationalRows(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0755); err != nil { t.Fatal(err) }
	data, err := sql.Open("sqlite", filepath.Join(dir, "data", "markys-pdv.db"))
	if err != nil { t.Fatal(err) }
	defer data.Close()
	master, err := sql.Open("sqlite", filepath.Join(dir, ".markys-master.db"))
	if err != nil { t.Fatal(err) }
	defer master.Close()
	for _, db := range []*sql.DB{data, master} { db.SetMaxOpenConns(1) }
	schema, err := assets.ReadFile("web/schema.sql")
	if err != nil { t.Fatal(err) }
	if _, err = data.Exec(string(schema)); err != nil { t.Fatal(err) }
	if err = initCompanies(data); err != nil { t.Fatal(err) }
	if err = initCompanies(master); err != nil { t.Fatal(err) }
	if err = ensureMasterAccountTable(data); err != nil { t.Fatal(err) }
	if err = ensureMasterAccountTable(master); err != nil { t.Fatal(err) }
	if _, err = data.Exec("INSERT INTO products(code,name,unit,price_cents,stock,active) VALUES('DATA-1','Produto','UN',100,3,1)"); err != nil { t.Fatal(err) }
	if _, err = data.Exec("INSERT INTO companies(legal_name,trade_name,document,status,database_mode,database_path) VALUES('Empresa','Empresa','12345678901','active','create','data/companies/123.db')"); err != nil { t.Fatal(err) }
	if _, err = data.Exec("INSERT INTO master_accounts(id,username,display_name,password_hash,recovery_hash) VALUES(1,'owner','Proprietário','hash','recovery')"); err != nil { t.Fatal(err) }
	if err = migrateMasterMetadata(data, master); err != nil { t.Fatal(err) }
	var companies, masters, products int
	if err = master.QueryRow("SELECT COUNT(*) FROM companies").Scan(&companies); err != nil { t.Fatal(err) }
	if err = master.QueryRow("SELECT COUNT(*) FROM master_accounts").Scan(&masters); err != nil { t.Fatal(err) }
	if err = data.QueryRow("SELECT COUNT(*) FROM products WHERE code='DATA-1'").Scan(&products); err != nil { t.Fatal(err) }
	if companies != 1 || masters != 1 || products != 1 { t.Fatalf("companies=%d masters=%d products=%d", companies, masters, products) }
	var leftovers int
	if err = data.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('companies','master_accounts')").Scan(&leftovers); err != nil { t.Fatal(err) }
	if leftovers != 0 { t.Fatalf("Master metadata remains in DATA: %d table(s)", leftovers) }
}
