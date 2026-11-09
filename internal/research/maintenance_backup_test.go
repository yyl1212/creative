package research

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMaintenanceBackupStatus(t *testing.T) {
	s, _, _, _ := approvedFixture(t)
	dir := filepath.Join(t.TempDir(), "backups")
	if e := s.MaintenanceBackup(context.Background(), dir, time.Now()); e != nil {
		t.Fatal(e)
	}
	status, e := s.BackupStatus()
	if e != nil || !status.Success || status.File == "" {
		t.Fatalf("%+v %v", status, e)
	}
	bad := filepath.Join(t.TempDir(), "file")
	if e = os.WriteFile(bad, []byte("occupied"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.MaintenanceBackup(context.Background(), bad, time.Now()); e != nil {
		t.Fatal(e)
	}
	status, e = s.BackupStatus()
	if e != nil || status.Success {
		t.Fatal("backup failure hidden", status, e)
	}
}
