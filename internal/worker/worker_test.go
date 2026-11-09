package worker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yyl1212/creative/internal/auth"
	"github.com/yyl1212/creative/internal/model"
	"github.com/yyl1212/creative/internal/research"
	"github.com/yyl1212/creative/internal/source"
	"github.com/yyl1212/creative/internal/store"
)

func TestWorkerDisabledProviderAndShutdown(t *testing.T) {
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
	r, e := model.New(model.Config{Primary: "deepseek", Providers: map[string]model.Provider{"deepseek": {}}}, s)
	if e != nil {
		t.Fatal(e)
	}
	w := Worker{Service: s, Fetcher: source.New(), Reviewer: r}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.run(ctx, 10*time.Millisecond) }()
	deadline := time.Now().Add(2 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		if e = db.QueryRow("SELECT status FROM research_batches").Scan(&status); e != nil {
			t.Fatal(e)
		}
		if status == "paused_service" {
			break
		}
		time.Sleep(10 * time.Millisecond)
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
	if status != "paused_service" {
		t.Fatal("disabled model did not pause", status)
	}
	var calls int
	_ = db.QueryRow("SELECT count(*) FROM usage_records WHERE origin='website'").Scan(&calls)
	if calls != 0 {
		t.Fatal("disabled model invoked")
	}
}
