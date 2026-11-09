package store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"github.com/yyl1212/creative/migrations"
	"path/filepath"
	"testing"
)

func TestResearchMigrationPreservesExistingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := migrations.Files.ReadFile("001_initial.sql")
	if _, err = db.Exec(string(first)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, checksum TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO schema_migrations VALUES (1,?)", fmt.Sprintf("%x", sha256.Sum256(first))); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO needs(status) VALUES ('pending')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRow("SELECT count(*) FROM needs").Scan(&count); err != nil || count != 1 {
		t.Fatalf("existing data lost: %v", err)
	}
	if err = db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 4 {
		t.Fatalf("research migration not applied: count=%d err=%v", count, err)
	}
	if err = db.QueryRow("SELECT count(*) FROM research_batches").Scan(&count); err != nil || count != 0 {
		t.Fatalf("queue missing or seeded: %v", err)
	}
}

func TestMigrationRejectsUnknownOrMissingHistory(t *testing.T) {
	for _, mutation := range []string{"INSERT INTO schema_migrations VALUES (99,'future')", "DELETE FROM schema_migrations WHERE version=1"} {
		t.Run(mutation, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "db")
			db, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			db.Close()
			db, err = Open(path)
			if err == nil {
				db.Close()
				t.Fatal("unsupported history accepted")
			}
		})
	}
}
