package store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

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
	contents, err := migrations.Files.ReadFile("001_initial.sql")
	if err != nil {
		return err
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256(contents))
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL)"); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM schema_migrations WHERE version != 1").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("数据库版本超出当前程序支持范围")
	}
	var previous string
	err = tx.QueryRow("SELECT checksum FROM schema_migrations WHERE version=1").Scan(&previous)
	switch {
	case err == sql.ErrNoRows:
		if _, err = tx.Exec(string(contents)); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO schema_migrations(version,checksum) VALUES (1,?)", checksum); err != nil {
			return err
		}
	case err != nil:
		return err
	case previous != checksum:
		return fmt.Errorf("迁移 1 校验和不一致，拒绝启动")
	}
	return tx.Commit()
}
