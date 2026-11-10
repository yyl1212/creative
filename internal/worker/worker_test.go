package worker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yyl1212/creative/internal/auth"
	"github.com/yyl1212/creative/internal/research"
	"github.com/yyl1212/creative/internal/store"
)

func TestMaintenanceDoesNotProcessResearchQueue(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s, e := research.New(db)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := auth.New(db)
	p, _, e := a.CreateProducer("worker", "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	task, e := s.CreateTask(p.ID, []string{"export"}, []string{"en"}, 3, "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../research/testdata/batch.json")
	if e != nil {
		t.Fatal(e)
	}
	raw = bytes.ReplaceAll(raw, []byte("task-example"), []byte(task.ID))
	if _, e = s.Receive(p, raw); e != nil {
		t.Fatal(e)
	}
	maintained := make(chan struct{}, 1)
	w := Worker{Service: s, Maintain: func(context.Context, time.Time) error { maintained <- struct{}{}; return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.run(ctx, 10*time.Millisecond) }()
	select {
	case <-maintained:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not run")
	}
	time.Sleep(30 * time.Millisecond)
	var status string
	if e = db.QueryRow("SELECT status FROM research_batches").Scan(&status); e != nil {
		t.Fatal(e)
	}
	cancel()
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
	if status != "pending_validation" {
		t.Fatal("maintenance consumed a legacy research batch", status)
	}
	var calls int
	_ = db.QueryRow("SELECT count(*) FROM usage_records WHERE origin='website'").Scan(&calls)
	if calls != 0 {
		t.Fatal("disabled model invoked")
	}
}
