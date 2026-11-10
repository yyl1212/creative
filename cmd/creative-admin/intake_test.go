package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/yyl1212/creative/internal/auth"
	"github.com/yyl1212/creative/internal/research"
	"github.com/yyl1212/creative/internal/store"
)

func TestIntakeCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	if err := intake("intake-create", []string{"--db", path}); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created database on typo", err)
	}
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, _ := auth.New(db)
	p, _, err := a.CreateProducer("external-codex", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := research.New(db)
	if err = intake("intake-create", []string{"--db", path, "--producer", p.ID, "--topic", "表格转换", "--languages", "zh,en", "--target", "2"}); err != nil {
		t.Fatal(err)
	}
	task, err := s.CurrentTask(p)
	if err != nil || task == nil || !task.ShouldContinue || task.TargetCount != 2 {
		t.Fatal(task, err)
	}
	for _, action := range []string{"cancel", "resume"} {
		args := []string{"--db", path, "--task", task.ID, "--version", strconv.Itoa(task.Version)}
		if err = intake("intake-"+action, args); err != nil {
			t.Fatal(err)
		}
		if err = intake("intake-"+action, args); err == nil {
			t.Fatal("stale version accepted")
		}
		task, err = s.CurrentTask(p)
		if err != nil || task.ShouldContinue != (action == "resume") {
			t.Fatal(task, err)
		}
	}
	var audits int
	if err = db.QueryRow("SELECT count(*) FROM audit_events WHERE account='controlled-cli' AND action IN ('task_created','task_cancel','task_resume')").Scan(&audits); err != nil || audits != 3 {
		t.Fatal(audits, err)
	}
	if _, err = s.ChangeTask(task.ID, task.Version, "cancel", "fixture", "local"); err != nil {
		t.Fatal(err)
	}
	old, err := s.CreateTask(p.ID, []string{"legacy"}, []string{"en"}, 1, "fixture", "local")
	if err != nil {
		t.Fatal(err)
	}
	if err = intake("intake-cancel", []string{"--db", path, "--task", old.ID, "--version", strconv.Itoa(old.Version)}); err != nil {
		t.Fatal(err)
	}
	old, _ = s.CurrentTask(p)
	if err = intake("intake-resume", []string{"--db", path, "--task", old.ID, "--version", strconv.Itoa(old.Version)}); err == nil {
		t.Fatal("legacy mode resumed")
	}
	if err = intake("intake-create", []string{"--db", path, "--producer", p.ID, "--topic", "new", "--target", "1"}); err != nil {
		t.Fatal(err)
	}
}
