package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yyl1212/creative/internal/research"
	"github.com/yyl1212/creative/internal/store"
)

func TestControlledReleaseGit(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %s", args, out)
		}
		return string(out)
	}
	git("init", "-b", "codex/fixture")
	git("config", "user.email", "fixture@example.invalid")
	git("config", "user.name", "离线测试")
	git("remote", "add", "origin", "https://github.com/yyl1212/creative.git")
	if e := os.WriteFile(filepath.Join(repo, "proof.md"), []byte("# 隔离测试证明\n"), 0600); e != nil {
		t.Fatal(e)
	}
	git("add", "proof.md")
	git("commit", "-m", "离线测试")
	head := strings.TrimSpace(git("rev-parse", "HEAD"))
	d := research.Development{Design: research.Design{Repository: "https://github.com/yyl1212/creative", Branch: "codex/fixture", Baseline: head, Spec: "proof.md", Architecture: "proof.md", Plan: "proof.md"}, Result: research.DevelopmentResult{Commit: head, ReviewCommit: head, TestCommit: head, ReviewReport: "proof.md", TestReport: "proof.md"}}
	if e := checkGit(context.Background(), repo, d); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(repo, "proof.md"), []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := checkGit(context.Background(), repo, d); e == nil {
		t.Fatal("dirty worktree accepted")
	}
	git("checkout", "--", "proof.md")
	d.Result.TestCommit = strings.Repeat("a", 40)
	if e := checkGit(context.Background(), repo, d); e == nil {
		t.Fatal("stale tests accepted")
	}
}
func TestMaintenanceCommands(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "app.db")
	db, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	directory := filepath.Join(root, "backups")
	if e = lifecycle("backup", []string{"--db", path, "--directory", directory}); e != nil {
		t.Fatal(e)
	}
	files, _ := filepath.Glob(filepath.Join(directory, "creative-backup-*.db"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	destination := filepath.Join(root, "restore.db")
	if e = lifecycle("restore", []string{"--source", files[0], "--destination", destination}); e != nil {
		t.Fatal(e)
	}
	if e = lifecycle("restore", []string{"--source", files[0], "--destination", destination}); e == nil {
		t.Fatal("overwritten")
	}
	if e = lifecycle("report", []string{"--db", destination}); e != nil {
		t.Fatal(e)
	}
	if e = lifecycle("release-verify", []string{"--db", destination}); e == nil {
		t.Fatal("release without version accepted")
	}
}
