package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yyl1212/creative/migrations"
)

func backupDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func backupExec(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func backupCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil || got != want {
		t.Fatalf("%s: got %d, want %d: %v", query, got, want, err)
	}
}

func TestRestoreBackup(t *testing.T) {
	t.Run("revokes_authority_without_changing_source", func(t *testing.T) {
		db, _ := backupDB(t)
		backupExec(t, db,
			`INSERT INTO admins(id,username,password_hash,auth_version) VALUES (1,'admin','fixture',4)`,
			`INSERT INTO sessions VALUES ('session',x'00',1234567890)`,
			`INSERT INTO producers(id,name,token_hash,version) VALUES ('p','producer','hash',3)`,
			`INSERT INTO research_tasks(id,producer_id,topics,languages,target_count,rules_version,rules,state,version,is_current) VALUES ('active','p','[]','[]',1,'v1','{}','active',2,1),('cancelled','p','[]','[]',1,'v1','{}','cancelled',7,0)`,
			`INSERT INTO needs(id,status,version) VALUES (1,'completed',5),(2,'qualified',8)`,
			`CREATE TABLE IF NOT EXISTS development_records(need_id INTEGER PRIMARY KEY,version INTEGER,state TEXT,need_version INTEGER,verification_id INTEGER,approval TEXT,design TEXT,result TEXT,approved_by TEXT,updated_at INTEGER)`,
			`CREATE TABLE IF NOT EXISTS release_records(id INTEGER PRIMARY KEY,need_id INTEGER,record_version INTEGER,commit_sha TEXT,slug TEXT,origin TEXT,manifest_hash TEXT,manifest TEXT,state TEXT,verified_at INTEGER,error TEXT)`,
			`CREATE TABLE IF NOT EXISTS app_settings(name TEXT PRIMARY KEY,value TEXT,version INTEGER,updated_at INTEGER)`,
			`INSERT INTO development_records(need_id,version,state,need_version,verification_id,approval,design,result,approved_by,updated_at) VALUES (1,6,'launched',5,100,'{}','{}','{}','admin',1000)`,
			`INSERT INTO release_records(id,need_id,record_version,commit_sha,slug,origin,manifest_hash,manifest,state,verified_at,error) VALUES (1,1,6,'commit','tool','https://example.test','hash','{}','verified',1000,'')`,
			`INSERT INTO app_settings VALUES ('model_config','{"primary":"deepseek","fallback":"openai","providers":{"openai":{"enabled":true,"validated":true,"model":"fixture"},"deepseek":{"enabled":true,"validated":true,"model":"fixture"}}}',4,1000)`,
			`INSERT INTO app_settings VALUES ('unrelated','preserved',2,1000)`,
		)
		source, err := Backup(context.Background(), db, filepath.Join(t.TempDir(), "backups"), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(t.TempDir(), "restored.db")
		if err = RestoreBackup(context.Background(), source, destination); err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(source)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("restore changed the source backup", err)
		}
		restored, err := Open(destination)
		if err != nil {
			t.Fatal(err)
		}
		defer restored.Close()
		backupCount(t, restored, `SELECT count(*) FROM admins WHERE enabled=0 AND auth_version=5`, 1)
		backupCount(t, restored, `SELECT count(*) FROM sessions`, 0)
		backupCount(t, restored, `SELECT count(*) FROM producers WHERE token_hash IS NULL AND version=4`, 1)
		backupCount(t, restored, `SELECT count(*) FROM research_tasks WHERE id='active' AND state='paused_service' AND version=3`, 1)
		backupCount(t, restored, `SELECT count(*) FROM research_tasks WHERE id='cancelled' AND state='cancelled' AND version=7`, 1)
		backupCount(t, restored, `SELECT count(*) FROM needs WHERE id=1 AND status='pending' AND version=6`, 1)
		backupCount(t, restored, `SELECT count(*) FROM needs WHERE id=2 AND status='qualified' AND version=8`, 1)
		backupCount(t, restored, `SELECT count(*) FROM development_records WHERE state='unverified' AND version=7 AND need_version=5 AND verification_id=100 AND approval='{}' AND design='{}' AND result='{}' AND approved_by='admin'`, 1)
		backupCount(t, restored, `SELECT count(*) FROM release_records WHERE state='unverified' AND record_version=6 AND commit_sha='commit' AND manifest_hash='hash'`, 1)
		backupCount(t, restored, `SELECT count(*) FROM app_settings WHERE name='unrelated' AND value='preserved' AND version=2`, 1)
		var config string
		var version int
		if err = restored.QueryRow(`SELECT value,version FROM app_settings WHERE name='model_config'`).Scan(&config, &version); err != nil || version != 5 {
			t.Fatal("model settings version was not invalidated", version, err)
		}
		var value struct {
			Primary   string                                       `json:"primary"`
			Providers map[string]struct{ Enabled, Validated bool } `json:"providers"`
		}
		if err = json.Unmarshal([]byte(config), &value); err != nil || len(value.Providers) != 2 || (value.Primary != "openai" && value.Primary != "deepseek") {
			t.Fatal("invalid restored model config", config, err)
		}
		for _, provider := range []string{"openai", "deepseek"} {
			setting, ok := value.Providers[provider]
			if !ok || setting.Enabled || setting.Validated {
				t.Fatal("restored provider can run", provider, config)
			}
		}
		backupCount(t, db, `SELECT count(*) FROM admins WHERE enabled=1 AND auth_version=4`, 1)
		backupCount(t, db, `SELECT count(*) FROM needs WHERE status='completed'`, 1)
		info, err := os.Stat(destination)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("restored database is not private", err)
		}
	})

	t.Run("wal_snapshot_contains_committed_data", func(t *testing.T) {
		db, path := backupDB(t)
		backupExec(t, db, `PRAGMA wal_autocheckpoint=0`, `INSERT INTO needs(status,title) VALUES ('pending','only in WAL')`)
		info, err := os.Stat(path + "-wal")
		if err != nil || info.Size() == 0 {
			t.Fatal("fixture has no WAL writes", err)
		}
		source, err := Backup(context.Background(), db, t.TempDir(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(t.TempDir(), "restored.db")
		if err = RestoreBackup(context.Background(), source, destination); err != nil {
			t.Fatal(err)
		}
		restored, err := Open(destination)
		if err != nil {
			t.Fatal(err)
		}
		defer restored.Close()
		backupCount(t, restored, `SELECT count(*) FROM needs WHERE title='only in WAL'`, 1)
	})

	t.Run("rejects_corruption_existing_destinations_and_unknown_schema", func(t *testing.T) {
		db, _ := backupDB(t)
		good, err := Backup(context.Background(), db, t.TempDir(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"corrupt", "drift", "future", "empty"} {
			t.Run(kind, func(t *testing.T) {
				source := filepath.Join(t.TempDir(), "source.db")
				data, err := os.ReadFile(good)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "corrupt" {
					data = []byte("not a SQLite database")
				}
				if kind == "empty" {
					data = nil
				}
				if err = os.WriteFile(source, data, 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "drift" || kind == "future" {
					changed, err := sql.Open("sqlite", source)
					if err != nil {
						t.Fatal(err)
					}
					if kind == "drift" {
						backupExec(t, changed, `UPDATE schema_migrations SET checksum='wrong' WHERE version=1`)
					}
					if kind == "future" {
						backupExec(t, changed, `INSERT INTO schema_migrations VALUES (999,'unsupported')`)
					}
					if err = changed.Close(); err != nil {
						t.Fatal(err)
					}
				}
				before, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				destination := filepath.Join(t.TempDir(), "rejected.db")
				if err = RestoreBackup(context.Background(), source, destination); err == nil {
					t.Fatal("invalid backup accepted")
				}
				if _, err = os.Lstat(destination); !os.IsNotExist(err) {
					t.Fatal("failed restore left a destination", err)
				}
				after, err := os.ReadFile(source)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("failed restore changed source", err)
				}
				entries, err := os.ReadDir(filepath.Dir(destination))
				if err != nil || len(entries) != 0 {
					t.Fatal("failed restore left private temporary files", entries, err)
				}
			})
		}
		existing := filepath.Join(t.TempDir(), "existing.db")
		if err = os.WriteFile(existing, []byte("keep me"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = RestoreBackup(context.Background(), good, existing); err == nil {
			t.Fatal("existing destination overwritten")
		}
		data, err := os.ReadFile(existing)
		if err != nil || string(data) != "keep me" {
			t.Fatal("existing destination changed", err)
		}
		if err = RestoreBackup(context.Background(), good, good); err == nil {
			t.Fatal("source can be overwritten")
		}
	})

	t.Run("upgrades_old_schema", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "old.db")
		old, err := sql.Open("sqlite", source)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := migrations.Files.ReadFile("001_initial.sql")
		if err != nil {
			t.Fatal(err)
		}
		backupExec(t, old, string(raw), `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL)`, `INSERT INTO admins(username,password_hash,auth_version) VALUES ('old','fixture',9)`)
		if _, err = old.Exec(`INSERT INTO schema_migrations VALUES (1,?)`, fmt.Sprintf("%x", sha256.Sum256(raw))); err != nil {
			t.Fatal(err)
		}
		if err = old.Close(); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(t.TempDir(), "restored.db")
		if err = RestoreBackup(context.Background(), source, destination); err != nil {
			t.Fatal(err)
		}
		restored, err := Open(destination)
		if err != nil {
			t.Fatal(err)
		}
		defer restored.Close()
		backupCount(t, restored, `SELECT count(*) FROM admins WHERE enabled=0 AND auth_version=10`, 1)
		files, err := migrations.Files.ReadDir(".")
		if err != nil {
			t.Fatal(err)
		}
		backupCount(t, restored, `SELECT count(*) FROM schema_migrations`, len(files))
		after, err := os.ReadFile(source)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("upgrade changed old source", err)
		}
	})
}

func TestBackupRetention7(t *testing.T) {
	db, _ := backupDB(t)
	directory := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(directory, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unrelated.db", "creative-backup-not-ours.db", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var snapshots []string
	for i := 0; i < 9; i++ {
		path, err := Backup(context.Background(), db, directory, time.Date(2026, 1, 1, i, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, path)
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("snapshot not private or not published", err)
		}
	}
	for i, path := range snapshots {
		_, err := os.Stat(path)
		if i < 2 && !os.IsNotExist(err) {
			t.Fatal("old snapshot retained", path, err)
		}
		if i >= 2 && err != nil {
			t.Fatal("recent snapshot lost", path, err)
		}
	}
	for _, name := range []string{"unrelated.db", "creative-backup-not-ours.db", "notes.txt"} {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(data) != "keep" {
			t.Fatal("unrelated file changed", name, err)
		}
	}
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("snapshot directory is not private", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 10 {
		t.Fatal("partial backup files leaked", len(entries), err)
	}
}

func TestBackupRetentionSameTimestamp(t *testing.T) {
	db, _ := backupDB(t)
	directory := t.TempDir()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var prior []string
	for i := 0; i < 7; i++ {
		path, err := Backup(context.Background(), db, directory, now)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(directory, fmt.Sprintf("creative-backup-20260101T000000.000000000Z-%d.db", 7-i))
		if err = os.Rename(path, name); err != nil {
			t.Fatal(err)
		}
		created := now.Add(time.Duration(i) * time.Hour)
		if err = os.Chtimes(name, created, created); err != nil {
			t.Fatal(err)
		}
		prior = append(prior, name)
	}
	if _, err := Backup(context.Background(), db, directory, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(prior[0]); !os.IsNotExist(err) {
		t.Fatal("oldest same-timestamp snapshot was retained", err)
	}
	for _, path := range prior[1:] {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("newer same-timestamp snapshot was removed", path, err)
		}
	}
}

func TestRestoreBackupRejectsDestinationSidecars(t *testing.T) {
	db, _ := backupDB(t)
	source, err := Backup(context.Background(), db, t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "existing.db")
			if err := os.WriteFile(destination+suffix, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := RestoreBackup(context.Background(), source, destination); err == nil {
				t.Fatal("destination with SQLite sidecar accepted")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("destination created despite sidecar", err)
			}
			data, err := os.ReadFile(destination + suffix)
			if err != nil || string(data) != "keep" {
				t.Fatal("existing sidecar changed", err)
			}
		})
	}
}

func TestRestoreBackupWithoutModelOverride(t *testing.T) {
	db, _ := backupDB(t)
	backupExec(t, db, `CREATE TABLE IF NOT EXISTS app_settings(name TEXT PRIMARY KEY,value TEXT,version INTEGER,updated_at INTEGER)`)
	source, err := Backup(context.Background(), db, t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored.db")
	if err = RestoreBackup(context.Background(), source, destination); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	backupCount(t, restored, `SELECT count(*) FROM app_settings WHERE name='model_config' AND version=1 AND json_extract(value,'$.providers.openai.enabled')=0 AND json_extract(value,'$.providers.deepseek.enabled')=0`, 1)
}

func TestBackupDuringWALTransaction(t *testing.T) {
	db, path := backupDB(t)
	backupExec(t, db, `INSERT INTO needs(id,status,title) VALUES (1,'pending','before'),(2,'pending','before')`)
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	tx, err := writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE needs SET title='after' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	first, err := Backup(context.Background(), db, t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE needs SET title='after' WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	second, err := Backup(context.Background(), db, t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for path, title := range map[string]string{first: "before", second: "after"} {
		snapshot, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err = snapshot.QueryRow(`SELECT count(*) FROM needs WHERE title=?`, title).Scan(&count); err != nil || count != 2 {
			t.Fatal("backup contains a partial transaction", title, count, err)
		}
		if err = snapshot.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBackupCancelledContext(t *testing.T) {
	db, _ := backupDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	directory := t.TempDir()
	if _, err := Backup(ctx, db, directory, time.Now()); err == nil {
		t.Fatal("cancelled backup succeeded")
	}
	if err := RestoreBackup(ctx, filepath.Join(directory, "missing"), filepath.Join(directory, "restored")); err == nil {
		t.Fatal("cancelled restore succeeded")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("cancelled operation published data", entries, err)
	}
}

func TestBackupRejectsEmptyDirectory(t *testing.T) {
	db, _ := backupDB(t)
	directory := t.TempDir()
	t.Chdir(directory)
	if _, err := Backup(context.Background(), db, "", time.Now()); err == nil {
		t.Fatal("empty backup directory unexpectedly uses the current directory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("empty directory argument wrote files", entries, err)
	}
}
