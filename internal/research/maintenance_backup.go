package research

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/yyl1212/creative/internal/store"
)

type BackupStatus struct {
	Time    string
	Success bool
	File    string
}

// 备份失败单独记录，避免磁盘问题被误报为成功；不触发搜寻或模型调用。
func (s *Service) MaintenanceBackup(ctx context.Context, directory string, now time.Time) error {
	deadline, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	file, err := store.Backup(deadline, s.db, directory, now)
	status := BackupStatus{Time: now.UTC().Format(time.RFC3339), Success: err == nil}
	if file != "" {
		status.File = filepath.Base(file)
	}
	_, e := s.db.ExecContext(ctx, "INSERT INTO app_settings(name,value) VALUES ('backup_status',?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,version=version+1,updated_at=unixepoch()", jsonText(status))
	return e
}
func (s *Service) BackupStatus() (*BackupStatus, error) {
	var raw string
	e := s.db.QueryRow("SELECT value FROM app_settings WHERE name='backup_status'").Scan(&raw)
	if e == sql.ErrNoRows {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var status BackupStatus
	e = json.Unmarshal([]byte(raw), &status)
	return &status, e
}
