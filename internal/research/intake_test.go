package research

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/yyl1212/creative/internal/auth"
	"github.com/yyl1212/creative/internal/store"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func fixture(t *testing.T, task string) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/batch.json")
	if e != nil {
		t.Fatal(e)
	}
	return bytes.ReplaceAll(b, []byte("task-example"), []byte(task))
}
func change(t *testing.T, raw []byte, f func(map[string]any)) []byte {
	t.Helper()
	var v map[string]any
	if e := json.Unmarshal(raw, &v); e != nil {
		t.Fatal(e)
	}
	f(v)
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func code(t *testing.T, err error, want int) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Status != want {
		t.Fatalf("want %d, got %v", want, err)
	}
}

func TestBatchValidation(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := fixture(t, "task-example")
	if _, err = s.parse(raw); err != nil {
		t.Fatal("valid v1 sample", err)
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown", func(v map[string]any) { v["extra"] = "ignored?" }},
		{"missing", func(v map[string]any) { delete(v, "run_id") }},
		{"null candidates", func(v map[string]any) { v["candidates"] = nil }},
		{"version", func(v map[string]any) { v["schema_version"] = "v2" }},
		{"wrong type", func(v map[string]any) { v["run_id"] = 42 }},
		{"date", func(v map[string]any) { v["submitted_at"] = "yesterday" }},
		{"duplicate candidate", func(v map[string]any) { cs := v["candidates"].([]any); v["candidates"] = append(cs, cs[0]) }},
		{"too many", func(v map[string]any) {
			c := v["candidates"].([]any)[0]
			cs := []any{}
			for i := 0; i < 21; i++ {
				cs = append(cs, c)
			}
			v["candidates"] = cs
		}},
		{"nested unknown", func(v map[string]any) { v["candidates"].([]any)[0].(map[string]any)["score"] = 100 }},
		{"third model", func(v map[string]any) {
			v["run_report"].(map[string]any)["usage"] = []any{map[string]any{"usage_id": "call1", "provider": "other", "model": "other-model", "reported_cost": nil, "currency": nil, "billing_reference": nil, "cost_status": "unknown"}}
		}},
		{"private URL", func(v map[string]any) {
			v["candidates"].([]any)[0].(map[string]any)["evidence"].([]any)[0].(map[string]any)["url"] = "http://127.0.0.1/secret"
		}},
		{"URL credentials", func(v map[string]any) {
			v["candidates"].([]any)[0].(map[string]any)["evidence"].([]any)[0].(map[string]any)["url"] = "https://user:secret@example.org/"
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) { _, e := s.parse(change(t, raw, tt.mutate)); code(t, e, 422) })
	}
	for _, invalid := range [][]byte{[]byte("{"), append(raw, []byte(" {}")...), bytes.Replace(raw, []byte(`"run_id":`), []byte(`"run_id":"duplicate","run_id":`), 1), {0xff}} {
		_, err = s.parse(invalid)
		code(t, err, 400)
	}
	unknown := change(t, raw, func(v map[string]any) {
		e := v["candidates"].([]any)[0].(map[string]any)["evidence"].([]any)[0].(map[string]any)
		e["author_ref"] = nil
		e["published_at"] = nil
		e["price"] = nil
	})
	if _, err = s.parse(unknown); err != nil {
		t.Fatal("unknown facts rejected", err)
	}
	empty := change(t, raw, func(v map[string]any) {
		v["candidates"] = []any{}
		v["run_report"].(map[string]any)["state"] = "waiting_for_signals"
	})
	if _, err = s.parse(empty); err != nil {
		t.Fatal("empty report rejected", err)
	}
}

func TestBatchIdempotencyCancellationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	a, err := auth.New(db)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := a.CreateProducer("test", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(p.ID, []string{"自助工具"}, []string{"zh", "en"}, 3, "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	raw := fixture(t, task.ID)
	first, err := s.Receive(p, raw)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.Receive(p, raw)
			if e != nil || !bytes.Equal(first, r) {
				t.Errorf("retry differs: %v", e)
			}
		}()
	}
	wg.Wait()
	_, err = s.Receive(p, append(raw, ' '))
	code(t, err, 409)
	var batches, candidates, needs int
	db.QueryRow("SELECT count(*) FROM research_batches").Scan(&batches)
	db.QueryRow("SELECT count(*) FROM research_candidates").Scan(&candidates)
	db.QueryRow("SELECT count(*) FROM needs").Scan(&needs)
	if batches != 1 || candidates != 1 || needs != 0 {
		t.Fatal("duplicate or unvalidated qualification", batches, candidates, needs)
	}
	task, err = s.ChangeTask(task.ID, task.Version, "cancel", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Receive(p, raw)
	if err != nil || !bytes.Equal(first, r) {
		t.Fatal("cancelled retry lost", err)
	}
	fresh := bytes.Replace(raw, []byte("batch-example-001"), []byte("batch-002"), 1)
	_, err = s.Receive(p, fresh)
	code(t, err, 409)
	for i := 0; i < 2; i++ {
		current, e := s.CurrentTask(p.ID)
		if e != nil || current.State != "cancelled" || current.ShouldContinue {
			t.Fatal("read restored cancelled task", e)
		}
	}
	_, err = s.ChangeTask(task.ID, task.Version-1, "resume", "owner", "local")
	code(t, err, 409)
	task, err = s.ChangeTask(task.ID, task.Version, "resume", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	old := bytes.Replace(fresh, []byte("strong-wtp.v1"), []byte("strong-wtp.old"), 1)
	_, err = s.Receive(p, old)
	code(t, err, 409)
	if _, err = s.Receive(p, fresh); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err = New(db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Receipt(p.ID, "batch-example-001")
	if err != nil || got.Status != "pending_validation" || got.QualifiedCount != 0 || got.RemainingGap != 3 || len(got.Results) != 1 {
		t.Fatalf("restart receipt: %+v %v", got, err)
	}
	var persisted []byte
	if err = db.QueryRow("SELECT payload FROM research_batches WHERE batch_id='batch-example-001'").Scan(&persisted); err != nil || !bytes.Equal(persisted, raw) {
		t.Fatal("raw bytes changed", err)
	}
	_, err = s.Receipt("another-producer", "batch-example-001")
	code(t, err, 404)
	if _, err = db.Exec("UPDATE producers SET token_hash=NULL,version=version+1 WHERE id=?", p.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.Receive(p, raw)
	code(t, err, 401)
}

func TestReceptionTransactionRollback(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a, e := auth.New(db)
	if e != nil {
		t.Fatal(e)
	}
	p, _, e := a.CreateProducer("test", "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(db)
	if e != nil {
		t.Fatal(e)
	}
	task, e := s.CreateTask(p.ID, []string{"test"}, []string{"en"}, 3, "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TRIGGER candidate_failure BEFORE INSERT ON research_candidates BEGIN SELECT RAISE(ABORT,'unavailable'); END"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Receive(p, fixture(t, task.ID)); e == nil {
		t.Fatal("partial batch accepted")
	}
	var count int
	if e = db.QueryRow("SELECT count(*) FROM research_batches").Scan(&count); e != nil || count != 0 {
		t.Fatal("partial transaction persisted", count, e)
	}
}

func TestValidationErrorsDoNotEchoInput(t *testing.T) {
	s, e := New(nil)
	if e != nil {
		t.Fatal(e)
	}
	raw := bytes.Replace(fixture(t, "task-example"), []byte(`"submitted"`), []byte(`"private-input-sentinel"`), 1)
	_, e = s.parse(raw)
	if e == nil || strings.Contains(e.Error(), "private-input-sentinel") {
		t.Fatal("unsafe error", e)
	}
}
