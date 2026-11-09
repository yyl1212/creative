package research

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/yyl1212/creative/internal/model"
	"github.com/yyl1212/creative/internal/source"
	"strings"
	"testing"
	"time"
)

func TestReviewReachedReport(t *testing.T) {
	for _, state := range []string{"waiting_for_signals", "paused_service"} {
		t.Run(state, func(t *testing.T) {
			s, _, p, task, f, r := setupVerification(t)
			raw := submitQualified(t, s, p, task.ID, "initial", "one")
			processFixture(t, s, f, r)
			raw = change(t, raw, func(v map[string]any) {
				v["batch_id"] = "report"
				v["candidates"] = []any{}
				v["run_report"].(map[string]any)["state"] = state
			})
			if _, e := s.Receive(p, raw); e != nil {
				t.Fatal(e)
			}
			processFixture(t, s, f, r)
			got, e := s.CurrentTask(p)
			if e != nil {
				t.Fatal(e)
			}
			if got.QualifiedCount != 1 || got.State != "target_reached" {
				t.Fatalf("already reached target changed by external report: count=%d state=%s", got.QualifiedCount, got.State)
			}
		})
	}
}
func TestReviewAuthorRef(t *testing.T) {
	c, b, a := qualificationFixture()
	b[0].Block.AuthorRef = "https://identity.example.com/u/same"
	b[1].Block.AuthorRef = b[0].Block.AuthorRef
	c.Evidence[0].AuthorRef = ptr(b[0].Block.AuthorRef)
	c.Evidence[1].AuthorRef = ptr(b[1].Block.AuthorRef)
	d := Evaluate(c, b, a, fixtureNow)
	if d.Outcome == "qualified" {
		t.Fatalf("same verified author identity counted twice: %+v", d)
	}
}
func TestReviewSiteCase(t *testing.T) {
	c, b, a := qualificationFixture()
	for i, host := range []string{"EXAMPLE.COM", "example.com", "example.com"} {
		raw := fmt.Sprintf(`<article><a rel="author">%s</a><time datetime="%s"></time><p>%s</p></article>`, b[i].Block.Author, b[i].Block.PublishedAt.Format(time.RFC3339), c.Evidence[i].Quote)
		d, e := source.Extract([]byte(raw), "https://"+host+fmt.Sprint("/", i))
		if e != nil {
			t.Fatal(e)
		}
		block, e := d.Bind(c.Evidence[i].Quote)
		if e != nil {
			t.Fatal(e)
		}
		d.FetchedAt = fixtureNow
		b[i].Document = d
		b[i].Block = block
	}
	if d := Evaluate(c, b, a, fixtureNow); d.Outcome == "qualified" {
		t.Fatalf("same DNS site counted twice: %s, %s", b[0].Document.Site, b[1].Document.Site)
	}
}
func TestReviewAliasSupplementFence(t *testing.T) {
	s, db, p, task, f, r := setupVerification(t)
	submitQualified(t, s, p, task.ID, "first", "one")
	processFixture(t, s, f, r)
	submitQualified(t, s, p, task.ID, "alias", "two")
	processFixture(t, s, f, r)
	oldRaw := submitQualified(t, s, p, task.ID, "old-alias-supplement", "two")
	lease, e := s.Claim(context.Background(), "worker", time.Now())
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	submitQualified(t, s, p, task.ID, "new-canonical-supplement", "one")
	var b batch
	_ = json.Unmarshal(oldRaw, &b)
	_, sources, a := qualificationFixture()
	a.TaskKey = b.Candidates[0].Job
	d := Evaluate(b.Candidates[0], sources, a, fixtureNow)
	if e = s.checkpoint(*lease, p.ID, b.Candidates[0], sources, a, nil, d, fixtureNow); e != nil {
		t.Fatal(e)
	}
	var state string
	if e = db.QueryRow("SELECT status FROM needs WHERE id=1").Scan(&state); e != nil {
		t.Fatal(e)
	}
	if state == "qualified" {
		t.Fatal("older alias result re-qualified canonical need while newer supplement remains unverified")
	}
}
func TestReviewNormalizedPriceFacts(t *testing.T) {
	for _, tc := range []struct{ amount, currency, unit, quote string }{
		{"20.00", "USD", "per month", "I will pay USD 20 per month."},
		{"20", "CNY", "每月", "我愿意每月支付20元人民币。"},
	} {
		if !supportedPrice(&Price{ptr(tc.amount), ptr(tc.currency), ptr(tc.unit)}, tc.quote) {
			t.Errorf("explicit price with valid normalized facts rejected: %+v", tc)
		}
	}
}
func TestReviewSupplementKeepsVerificationHistory(t *testing.T) {
	s, db, p, task, f, r := setupVerification(t)
	submitQualified(t, s, p, task.ID, "first-history", "one")
	processFixture(t, s, f, r)
	for key, d := range f.docs {
		d.Hash = "new-snapshot"
		f.docs[key] = d
	}
	submitQualified(t, s, p, task.ID, "next-history", "one")
	processFixture(t, s, f, r)
	var old int
	if e := db.QueryRow(`SELECT count(*) FROM need_sources WHERE json_extract(snapshot,'$.document.hash')='0'`).Scan(&old); e != nil {
		t.Fatal(e)
	}
	if old == 0 {
		t.Fatal("prior verified source hash vanished immediately on supplement, before 180-day retention")
	}
}

func TestReviewLargePriorNeedsDoesNotMisreportOutage(t *testing.T) {
	s, db, p, task, f, _ := setupVerification(t)
	for i := 0; i < 100; i++ {
		if _, e := db.Exec("INSERT INTO needs(status,title,task) VALUES ('pending','prior',?)", fmt.Sprint(i)+strings.Repeat("A", 1500)); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("OPENAI_API_KEY", "review-isolated-sentinel")
	r, e := model.New(model.Config{Primary: "openai", Providers: map[string]model.Provider{"openai": {Enabled: true, Validated: true, Model: "gpt-fixture"}}}, s)
	if e != nil {
		t.Fatal(e)
	}
	submitQualified(t, s, p, task.ID, "big-context", "one")
	lease, e := s.Claim(context.Background(), "worker", time.Now())
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	if e = s.Process(context.Background(), *lease, f, r, fixtureNow); e != nil {
		t.Fatal(e)
	}
	var status, reason string
	if e = db.QueryRow("SELECT status,pause_reason FROM research_batches").Scan(&status, &reason); e != nil {
		t.Fatal(e)
	}
	var calls int
	if e = db.QueryRow("SELECT count(*) FROM usage_records").Scan(&calls); e != nil {
		t.Fatal(e)
	}
	if status == "paused_service" {
		t.Fatalf("local context size stalls whole batch as %s; network/model calls=%d", reason, calls)
	}
}

func TestReviewAuthorAliasTransitivity(t *testing.T) {
	c, b, a := qualificationFixture()
	b[0].Block.AuthorRef = "https://profiles.example.org/a"
	b[1].Block.AuthorRef = "https://profiles.example.org/b"
	b[2].Block.Author = "Alice"
	b[2].Block.AuthorRef = b[1].Block.AuthorRef
	c.Evidence[2].AuthorRef = ptr("Alice")
	// 第三条把前两条身份连起来，即使它最后出现也不能已计两名独立用户。
	if d := Evaluate(c, b, a, fixtureNow); d.Outcome == "qualified" {
		t.Fatalf("transitive aliases counted twice %+v", d)
	}
}
func TestReviewVersionedHistoryAndRetention(t *testing.T) {
	s, db, p, task, f, r := setupVerification(t)
	submitQualified(t, s, p, task.ID, "version-one", "one")
	processFixture(t, s, f, r)
	submitQualified(t, s, p, task.ID, "version-two", "one")
	processFixture(t, s, f, r)
	current, e := s.Need(1, 0)
	if e != nil || len(current.History) != 2 || len(current.Sources) != 3 {
		t.Fatalf("current %+v %v", current, e)
	}
	old, e := s.Need(1, current.History[1].ID)
	if e != nil || !old.Historical || len(old.Sources) != 3 {
		t.Fatalf("old %+v %v", old, e)
	}
	if e = s.Maintenance(fixtureNow.AddDate(0, 0, 181)); e != nil {
		t.Fatal(e)
	}
	var n int
	_ = db.QueryRow("SELECT count(*) FROM need_verifications").Scan(&n)
	if n != 0 {
		t.Fatal("history not purged", n)
	}
	_, e = s.Need(1, old.VerificationID)
	code(t, e, 404)
}
func TestReviewSameBatchCanonicalVersions(t *testing.T) {
	s, db, p, task, f, r := setupVerification(t)
	submitQualified(t, s, p, task.ID, "alias-one", "one")
	processFixture(t, s, f, r)
	submitQualified(t, s, p, task.ID, "alias-two", "two")
	processFixture(t, s, f, r)
	submitQualified(t, s, p, task.ID, "two-aliases", "one", "two")
	processFixture(t, s, f, r)
	var n int
	_ = db.QueryRow("SELECT count(*) FROM need_verifications WHERE batch_row=3").Scan(&n)
	if n != 2 {
		t.Fatal("canonical versions lost", n)
	}
}
func TestReviewPriceDoesNotGuessCurrency(t *testing.T) {
	for _, quote := range []string{"I will pay $20 per month.", "I will pay USDT 20 per month.", "I will pay USD 120 per month."} {
		if supportedPrice(&Price{ptr("20"), ptr("USD"), ptr("per month")}, quote) {
			t.Fatal("guessed price", quote)
		}
	}
}

func TestReviewSnapshotsContainOnlyFetchedExcerpts(t *testing.T) {
	s, db, p, task, _, _ := setupVerification(t)
	submitQualified(t, s, p, task.ID, "safe-excerpts", "one")
	lease, e := s.Claim(context.Background(), "worker", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	c, b, a := qualificationFixture()
	c.ExternalID = "one"
	a.Evidence[0].PaymentQuote = "FABRICATED PAYMENT CLAIM"
	d := Evaluate(c, b, a, fixtureNow)
	if e = s.checkpoint(*lease, p.ID, c, b, a, nil, d, fixtureNow); e != nil {
		t.Fatal(e)
	}
	var n int
	_ = db.QueryRow("SELECT count(*) FROM need_sources WHERE snapshot LIKE '%FABRICATED PAYMENT CLAIM%'").Scan(&n)
	if n != 0 {
		t.Fatal("model claim displayed as fetched source")
	}
}
