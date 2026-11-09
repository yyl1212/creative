package store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/yyl1212/creative/internal/usage"
	"github.com/yyl1212/creative/migrations"
	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = file.Chmod(0600); err != nil {
		file.Close()
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: absolute}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err = migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	files, err := migrations.Files.ReadDir(".")
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL)"); err != nil {
		return err
	}
	rows, err := tx.Query("SELECT version,checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return err
	}
	var previous []string
	for rows.Next() {
		var version int
		var checksum string
		if err = rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			return err
		}
		if version != len(previous)+1 || version > len(files) {
			rows.Close()
			return fmt.Errorf("数据库迁移历史缺号或超出当前程序支持范围")
		}
		previous = append(previous, checksum)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for i, file := range files {
		version := i + 1
		contents, err := migrations.Files.ReadFile(file.Name())
		if err != nil {
			return err
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(contents))
		if i < len(previous) {
			if previous[i] != checksum {
				return fmt.Errorf("迁移 %d 校验和不一致，拒绝启动", version)
			}
			continue
		}
		if _, err = tx.Exec(string(contents)); err != nil {
			return err
		}
		if version == 3 {
			if err = usage.Backfill(tx); err != nil {
				return err
			}
		}
		if _, err = tx.Exec("INSERT INTO schema_migrations(version,checksum) VALUES (?,?)", version, checksum); err != nil {
			return err
		}
	}
	return tx.Commit()
}
