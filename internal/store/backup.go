package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var backupName = regexp.MustCompile(`^creative-backup-[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9]+\.db$`)

// Backup 创建独立的一致性快照，仅保留此工具命名的最近七份快照。
func Backup(ctx context.Context, db *sql.DB, directory string, now time.Time) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if db == nil {
		return "", fmt.Errorf("备份数据库不能为空")
	}
	if directory == "" {
		return "", fmt.Errorf("备份目录不能为空")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("备份目录必须是普通目录")
	}
	if err = os.Chmod(directory, 0700); err != nil {
		return "", err
	}
	staged, err := snapshotTemp(directory)
	if err != nil {
		return "", err
	}
	defer removeSnapshotTemp(staged)
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", staged); err != nil {
		return "", fmt.Errorf("创建一致性备份: %w", err)
	}
	check, err := openSnapshot(staged, "ro")
	if err != nil {
		return "", err
	}
	err = validateSnapshot(ctx, check)
	closeErr := check.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	name := "creative-backup-" + now.UTC().Format("20060102T150405.000000000Z") + "-" + strings.TrimPrefix(filepath.Base(staged), ".creative-snapshot-") + ".db"
	path := filepath.Join(directory, name)
	if err = publishSnapshot(staged, path); err != nil {
		return "", err
	}
	if err = retainBackups(directory, name); err != nil {
		return path, err
	}
	return path, nil
}

// RestoreBackup 仅发布到不存在的新路径，恢复后的权限和发布状态均须重新确认。
func RestoreBackup(ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if _, err = os.Lstat(destination + suffix); err == nil {
			return fmt.Errorf("恢复目标或 SQLite 辅助文件已存在，拒绝覆盖")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("备份源必须是普通文件")
	}
	original, err := openSnapshot(source, "ro")
	if err != nil {
		return err
	}
	defer original.Close()
	if err = validateSnapshot(ctx, original); err != nil {
		return err
	}
	var versions int
	if err = original.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&versions); err != nil {
		return fmt.Errorf("备份缺少有效迁移历史: %w", err)
	}
	if versions == 0 {
		return fmt.Errorf("备份迁移历史不能为空")
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	staged, err := snapshotTemp(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer removeSnapshotTemp(staged)
	if _, err = original.ExecContext(ctx, "VACUUM INTO ?", staged); err != nil {
		return fmt.Errorf("读取备份快照: %w", err)
	}
	restored, err := openSnapshot(staged, "rw")
	if err != nil {
		return err
	}
	defer restored.Close()
	if err = validateSnapshot(ctx, restored); err != nil {
		return err
	}
	if err = migrate(restored); err != nil {
		return fmt.Errorf("校验并升级备份迁移: %w", err)
	}
	if err = invalidateRestoredAuthority(ctx, restored); err != nil {
		return err
	}
	if err = validateSnapshot(ctx, restored); err != nil {
		return err
	}
	if err = restored.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return publishSnapshot(staged, destination)
}

func openSnapshot(path, mode string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: absolute}
	q := u.Query()
	q.Set("mode", mode)
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	if mode == "rw" {
		q.Add("_pragma", "journal_mode(DELETE)")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func validateSnapshot(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("备份完整性校验: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("备份完整性校验失败: %s", result)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("备份外键校验失败")
	}
	return rows.Err()
}

func invalidateRestoredAuthority(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		"UPDATE admins SET enabled=0,auth_version=auth_version+1",
		"DELETE FROM sessions",
		"UPDATE producers SET token_hash=NULL,version=version+1",
		"UPDATE research_tasks SET state='paused_service',version=version+1,updated_at=unixepoch() WHERE state!='cancelled'",
		"UPDATE needs SET status='pending',valid_until=0,version=version+1,updated_at=unixepoch() WHERE status='completed' OR id IN (SELECT need_id FROM development_records)",
	} {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for _, update := range []struct{ table, statement string }{
		{"development_records", "UPDATE development_records SET state='unverified',version=version+1,updated_at=unixepoch()"},
		{"release_records", "UPDATE release_records SET state='unverified'"},
		{"app_settings", `INSERT INTO app_settings(name,value,version,updated_at) VALUES ('model_config','{"primary":"deepseek","fallback":"","providers":{"openai":{"enabled":false,"validated":false,"model":""},"deepseek":{"enabled":false,"validated":false,"model":""}}}',1,unixepoch()) ON CONFLICT(name) DO UPDATE SET value=excluded.value,version=app_settings.version+1,updated_at=excluded.updated_at`},
	} {
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name=?)", update.table).Scan(&exists); err != nil {
			return err
		}
		if exists {
			if _, err = tx.ExecContext(ctx, update.statement); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func snapshotTemp(directory string) (string, error) {
	file, err := os.CreateTemp(directory, ".creative-snapshot-")
	if err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

func removeSnapshotTemp(path string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		os.Remove(path + suffix)
	}
}

func publishSnapshot(staged, destination string) error {
	file, err := os.OpenFile(staged, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	err = file.Sync()
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// 硬链接在目标已存在时失败，避免检查与发布之间覆盖其他进程的数据。
	if err = os.Link(staged, destination); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func retainBackups(directory, latest string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	var snapshots []os.FileInfo
	for _, entry := range entries {
		if entry.Type().IsRegular() && backupName.MatchString(entry.Name()) && entry.Name() != latest {
			info, err := entry.Info()
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			snapshots = append(snapshots, info)
		}
	}
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].ModTime().Equal(snapshots[j].ModTime()) {
			return snapshots[i].Name() > snapshots[j].Name()
		}
		return snapshots[i].ModTime().After(snapshots[j].ModTime())
	})
	for i := 6; i < len(snapshots); i++ {
		if err = os.Remove(filepath.Join(directory, snapshots[i].Name())); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("清理旧备份: %w", err)
		}
	}
	return nil
}
