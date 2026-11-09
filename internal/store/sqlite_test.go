package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationsPersistAndRejectDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "creative.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO needs(status) VALUES ('pending')"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM needs").Scan(&count); err != nil || count != 1 {
		t.Fatal("data lost on restart", err)
	}
	if err = db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=1").Scan(&count); err != nil || count != 1 {
		t.Fatal("migration duplicated", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("database is not private", err)
	}
	if _, err = db.Exec("UPDATE schema_migrations SET checksum='invalid'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if db, err = Open(path); err == nil {
		db.Close()
		t.Fatal("modified migration accepted")
	}
}
