package research

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/yyl1212/creative/internal/auth"
	"github.com/yyl1212/creative/internal/model"
	"github.com/yyl1212/creative/internal/source"
	"github.com/yyl1212/creative/internal/store"
)

type fixtureFetcher struct{ docs map[string]source.Document }

func (f fixtureFetcher) Fetch(_ context.Context, url string) (source.Document, error) {
	d, ok := f.docs[url]
	if !ok {
		return d, source.ErrUnavailable
	}
	return d, nil
}

type fixtureReviewer struct {
	calls, failAt int
	assessment    model.Assessment
}

func (f *fixtureReviewer) Ready() error { return nil }
func (f *fixtureReviewer) Review(_ context.Context, in model.Input) (model.Assessment, error) {
	f.calls++
	if f.calls == f.failAt {
		return model.Assessment{}, model.ErrUnavailable
	}
	a := f.assessment
	a.TaskKey = in.Candidate.(candidate).Job
	return a, nil
}
func setupVerification(t *testing.T) (*Service, *sql.DB, auth.Producer, *Task, fixtureFetcher, *fixtureReviewer) {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s, e := New(db)
	if e != nil {
		t.Fatal(e)
	}
	a, e := auth.New(db)
	if e != nil {
		t.Fatal(e)
	}
	p, _, e := a.CreateProducer("fixture", "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	task, e := s.CreateTask(p.ID, []string{"export"}, []string{"en"}, 1, "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	_, sources, assessment := qualificationFixture()
	fetch := fixtureFetcher{docs: map[string]source.Document{}}
	for _, v := range sources {
		fetch.docs[v.Document.URL] = v.Document
	}
	return s, db, p, task, fetch, &fixtureReviewer{assessment: assessment}
}
func submitQualified(t *testing.T, s *Service, p auth.Producer, taskID, batchID string, ids ...string) []byte {
	t.Helper()
	raw := fixture(t, taskID)
	raw = change(t, raw, func(v map[string]any) {
		v["batch_id"] = batchID
		cs := []any{}
		for _, id := range ids {
			c, _, _ := qualificationFixture()
			c.ExternalID = id
			c.Language = "en"
			c.Monetization = "undecided"
			c.Unknowns = []string{}
			for i := range c.Evidence {
				c.Evidence[i].ObservedAt = fixtureNow.Format(time.RFC3339)
				c.Evidence[i].Conditions = []string{}
			}
			b, _ := json.Marshal(c)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			m["alternatives"] = []any{}
			cs = append(cs, m)
		}
		v["candidates"] = cs
	})
	if _, e := s.Receive(p, raw); e != nil {
		t.Fatal(e)
	}
	return raw
}
func processFixture(t *testing.T, s *Service, f fixtureFetcher, r *fixtureReviewer) {
	t.Helper()
	lease, e := s.Claim(context.Background(), "test", time.Now())
	if e != nil || lease == nil {
		t.Fatalf("claim %v %v", lease, e)
	}
	if e = s.Process(context.Background(), *lease, f, r, fixtureNow); e != nil {
		t.Fatal(e)
	}
}
func TestVerificationRestart(t *testing.T) {
	s, db, p, task, f, r := setupVerification(t)
	submitQualified(t, s, p, task.ID, "mixed", "one", "two")
	r.failAt = 2
	processFixture(t, s, f, r)
	receipt, e := s.Receipt(p, "mixed")
	if e != nil || receipt.Status != "paused_service" || receipt.Results[0].Outcome == nil || receipt.Results[1].Outcome != nil {
		t.Fatalf("partial %+v %v", receipt, e)
	}
	s, e = New(db)
	if e != nil {
		t.Fatal(e)
	}
	var version int
	_ = db.QueryRow("SELECT version FROM research_batches WHERE batch_id='mixed'").Scan(&version)
	if e = s.RetryBatch(1, version, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	r.failAt = 0
	processFixture(t, s, f, r)
	receipt, e = s.Receipt(p, "mixed")
	if e != nil || receipt.Status != "completed" || r.calls != 3 {
		t.Fatalf("checkpoint restart: %+v calls=%d %v", receipt, r.calls, e)
	}
}
func TestLeaseFencing(t *testing.T) {
	s, db, p, task, f, r := setupVerification(t)
	submitQualified(t, s, p, task.ID, "lease", "one")
	old, e := s.Claim(context.Background(), "old", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if l, _ := s.Claim(context.Background(), "other", time.Now()); l != nil {
		t.Fatal("double claim")
	}
	_, _ = db.Exec("UPDATE research_worker SET expires_at=0")
	current, e := s.Claim(context.Background(), "new", time.Now())
	if e != nil || current == nil {
		t.Fatalf("reclaim %v", e)
	}
	if e = s.Heartbeat(context.Background(), *old, time.Now()); !errors.Is(e, ErrLeaseLost) {
		t.Fatal("old heartbeat", e)
	}
	if e = s.Process(context.Background(), *old, f, r, fixtureNow); !errors.Is(e, ErrLeaseLost) {
		t.Fatal("old result accepted", e)
	}
	if e = s.Process(context.Background(), *current, f, r, fixtureNow); e != nil {
		t.Fatal(e)
	}
}
func TestCrossBatchDedup(t *testing.T) {
	s, db, p, task, f, r := setupVerification(t)
	submitQualified(t, s, p, task.ID, "first", "one")
	processFixture(t, s, f, r)
	submitQualified(t, s, p, task.ID, "second", "two")
	processFixture(t, s, f, r)
	var count int
	_ = db.QueryRow("SELECT count(*) FROM needs").Scan(&count)
	if count != 1 {
		t.Fatalf("duplicate need %d", count)
	}
	got, e := s.Receipt(p, "second")
	if e != nil || *got.Results[0].Outcome != "duplicate" {
		t.Fatalf("%+v %v", got, e)
	}
	// 补证接收即撤销旧资格，不能在等待核验时继续计数。
	submitQualified(t, s, p, task.ID, "supplement", "one")
	var status string
	_ = db.QueryRow("SELECT status FROM needs LIMIT 1").Scan(&status)
	if status == "qualified" {
		t.Fatal("supplement retained old qualification")
	}
	processFixture(t, s, f, r)
	_, _ = db.Exec("UPDATE needs SET status='completed'")
	submitQualified(t, s, p, task.ID, "completed-supplement", "one")
	processFixture(t, s, f, r)
	_ = db.QueryRow("SELECT status FROM needs LIMIT 1").Scan(&status)
	if status != "completed" {
		t.Fatal("overwrote completed")
	}
}
func TestRecomputeTarget(t *testing.T) {
	s, db, p, task, _, _ := setupVerification(t)
	_, _ = db.Exec("UPDATE research_tasks SET target_count=3 WHERE id=?", task.ID)
	_, e := db.Exec("INSERT INTO needs(status,verified_at,valid_until,rules_version,title) VALUES ('qualified',?,?,?,'fixture')", time.Now().Unix(), time.Now().Add(time.Hour).Unix(), rulesVersion)
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.CurrentTask(p)
	if e != nil || got.QualifiedCount != 1 || got.RemainingGap != 2 || !got.ShouldContinue {
		t.Fatalf("premature target %+v %v", got, e)
	}
	for i := 0; i < 2; i++ {
		_, e = db.Exec("INSERT INTO needs(status,verified_at,valid_until,rules_version,title) VALUES ('qualified',?,?,?,'fixture')", time.Now().Unix(), time.Now().Add(time.Hour).Unix(), rulesVersion)
		if e != nil {
			t.Fatal(e)
		}
	}
	got, e = s.CurrentTask(p)
	if e != nil || got.QualifiedCount != 3 || got.State != "target_reached" || got.ShouldContinue {
		t.Fatalf("%+v %v", got, e)
	}
	_, _ = db.Exec("UPDATE needs SET valid_until=0")
	got, e = s.CurrentTask(p)
	if e != nil || got.State != "active" || got.RemainingGap != 3 {
		t.Fatalf("expiry %+v %v", got, e)
	}
	_, e = s.ChangeTask(task.ID, got.Version, "cancel", "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Maintenance(time.Now().Add(48 * time.Hour)); e != nil {
		t.Fatal(e)
	}
	got, e = s.CurrentTask(p)
	if e != nil || got.State != "cancelled" {
		t.Fatal("cancel resumed", e)
	}
}
func TestUnknownCost(t *testing.T) {
	s, db, p, task, _, _ := setupVerification(t)
	id, e := s.Start(context.Background(), "openai", "gpt-fixture")
	if e != nil {
		t.Fatal(e)
	}
	var status string
	_ = db.QueryRow("SELECT cost_status FROM usage_records WHERE id=?", id).Scan(&status)
	if status != "unknown" {
		t.Fatal(status)
	}
	if e = s.Finish(context.Background(), id, model.Usage{ResponseID: "response1", InputTokens: 100, OutputTokens: 20, TokensKnown: true}); e != nil {
		t.Fatal(e)
	}
	for i, cost := range []any{nil, "0.10", "0.10"} {
		raw := change(t, fixture(t, task.ID), func(v map[string]any) {
			v["batch_id"] = fmt.Sprint("cost", i)
			v["candidates"] = []any{}
			state := "reported"
			if cost == nil {
				state = "unknown"
			}
			v["run_report"].(map[string]any)["usage"] = []any{map[string]any{"usage_id": "external-call", "provider": "deepseek", "model": "deepseek-fixture", "reported_cost": cost, "currency": "USD", "billing_reference": "bill1", "cost_status": state}}
		})
		if _, e = s.Receive(p, raw); e != nil {
			t.Fatal(e)
		}
	}
	var count int
	_ = db.QueryRow("SELECT count(*) FROM usage_records WHERE origin='external'").Scan(&count)
	if count != 1 {
		t.Fatal("duplicate cost", count)
	}
	_ = db.QueryRow("SELECT cost_status FROM usage_records WHERE origin='website'").Scan(&status)
	if status != "unknown" {
		t.Fatal("guessed cost")
	}
}
func TestRetentionAndRetry(t *testing.T) {
	s, db, p, task, _, _ := setupVerification(t)
	raw := submitQualified(t, s, p, task.ID, "retention", "one")
	first, e := s.Receive(p, raw)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = db.Exec("UPDATE research_batches SET created_at=?", time.Now().AddDate(0, 0, -31).Unix())
	if e = s.Maintenance(time.Now()); e != nil {
		t.Fatal(e)
	}
	var size int
	_ = db.QueryRow("SELECT length(payload) FROM research_batches").Scan(&size)
	if size != 0 {
		t.Fatal("raw not purged")
	}
	got, e := s.Receive(p, raw)
	if e != nil || !bytes.Equal(first, got) {
		t.Fatal("tombstone retry", e)
	}
	if _, e = s.Batch(1); e != nil {
		t.Fatal("purged detail", e)
	}
}

func TestSupplementFencesInFlightResult(t *testing.T) {
	s, db, p, task, _, _ := setupVerification(t)
	submitQualified(t, s, p, task.ID, "old-flight", "one")
	lease, e := s.Claim(context.Background(), "worker", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	c, bs, a := qualificationFixture()
	c.ExternalID = "one"
	d := Evaluate(c, bs, a, fixtureNow)
	submitQualified(t, s, p, task.ID, "new-flight", "one")
	if e = s.checkpoint(*lease, p.ID, c, bs, a, nil, d, fixtureNow); e != nil {
		t.Fatal(e)
	}
	var n int
	_ = db.QueryRow("SELECT count(*) FROM needs WHERE status='qualified'").Scan(&n)
	if n != 0 {
		t.Fatal("stale result qualified")
	}
	receipt, e := s.Receipt(p, "old-flight")
	if e != nil || receipt.Results[0].ReasonCodes[0] != "superseded_by_supplement" {
		t.Fatalf("%+v %v", receipt, e)
	}
}
func TestDuplicateBillingReferenceAndConflict(t *testing.T) {
	s, db, p, task, _, _ := setupVerification(t)
	for i, cost := range []string{"1.25", "1.25", "2.50"} {
		raw := change(t, fixture(t, task.ID), func(v map[string]any) {
			v["batch_id"] = fmt.Sprint("bill", i)
			v["candidates"] = []any{}
			id := fmt.Sprint("charge", min(i, 1))
			v["run_report"].(map[string]any)["usage"] = []any{map[string]any{"usage_id": id, "provider": "deepseek", "model": "deepseek-fixture", "reported_cost": cost, "currency": "USD", "billing_reference": "same-bill", "cost_status": "reported"}}
		})
		if _, e := s.Receive(p, raw); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	_ = db.QueryRow("SELECT count(*) FROM usage_records WHERE cost_status='conflict'").Scan(&n)
	if n != 2 {
		t.Fatal("duplicated bill not excluded", n)
	}
	var value string
	_ = db.QueryRow("SELECT reported_cost FROM usage_records WHERE usage_id='charge1'").Scan(&value)
	if value != "1.25" {
		t.Fatal("silently replaced cost", value)
	}
}
func TestSingleSourceFailureAndWaiting(t *testing.T) {
	s, db, p, task, f, r := setupVerification(t)
	raw := submitQualified(t, s, p, task.ID, "source-missing", "one")
	var parsed batch
	_ = json.Unmarshal(raw, &parsed)
	delete(f.docs, parsed.Candidates[0].Evidence[1].URL)
	processFixture(t, s, f, r)
	got, e := s.Receipt(p, "source-missing")
	if e != nil || got.Status != "completed" || *got.Results[0].Outcome != "needs_evidence" {
		t.Fatalf("%+v %v", got, e)
	}
	_, _ = db.Exec("UPDATE research_tasks SET state='waiting_for_signals',updated_at=?", time.Now().Add(-25*time.Hour).Unix())
	if e = s.Maintenance(time.Now()); e != nil {
		t.Fatal(e)
	}
	current, e := s.CurrentTask(p)
	if e != nil || current.State != "active" {
		t.Fatalf("%+v %v", current, e)
	}
}
