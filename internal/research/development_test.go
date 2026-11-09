package research

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/yyl1212/creative/internal/store"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func approvedFixture(t *testing.T) (*Service, *sql.DB, *Need, Approval) {
	t.Helper()
	s, db, p, task, f, r := setupVerification(t)
	submitQualified(t, s, p, task.ID, "approval-batch", "approval-candidate")
	processFixture(t, s, f, r)
	n, e := s.Need(1, 0)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	n.Decision.ValidUntil = now.Add(24 * time.Hour)
	_, e = db.Exec("UPDATE needs SET verified_at=?,valid_until=?,decision=? WHERE id=1", now.Unix(), n.Decision.ValidUntil.Unix(), jsonText(n.Decision))
	if e != nil {
		t.Fatal(e)
	}
	for _, b := range n.Sources {
		b.Block.PublishedAt = now.AddDate(0, -1, 0)
		b.Document.FetchedAt = now
		_, e = db.Exec("UPDATE need_sources SET snapshot=? WHERE need_id=1 AND evidence_id=?", jsonText(b), b.ID)
		if e != nil {
			t.Fatal(e)
		}
	}
	n, e = s.Need(1, 0)
	if e != nil {
		t.Fatal(e)
	}
	a := Approval{VerificationID: n.VerificationID, ReviewNotes: "已逐条复核原文、作者独立性与附带条件", Monetization: "free", Rationale: "免费引流，未来广告条件另行核验", UnitCost: "0.0125", Currency: "USD", Unit: "每次", Price: "0", PaymentFee: "0", Sustainability: "按广告收入零承担实际资源支出", ResourceProtection: "单文件大小与并发限制，拒绝无限任务", SelfService: "上传后直接获得结果，无人工服务", Gap: "现有流程不能满足用户要求", Scope: "只实现一项已验证任务", Exclusions: "不支持批量和定制", Acceptance: "输入隔离样例，获得可核对结果", Acquisition: "已上线后通过对应任务的教程获客"}
	for _, ev := range n.Decision.Evidence {
		if ev.Valid {
			a.ReviewedEvidence = append(a.ReviewedEvidence, ev.ID)
		}
	}
	for i := 0; i < 3; i++ {
		a.Alternatives = append(a.Alternatives, AlternativeReview{Name: fmt.Sprint("替代", i), URL: fmt.Sprintf("https://example.org/tool%d", i), Version: "2026-11", CheckedAt: now.Format("2006-01-02"), Pricing: "USD 5 / month", Result: "亲自操作样例，仍缺少对应功能"})
	}
	return s, db, n, a
}

func designFixture() Design {
	return Design{ProductSlug: "example", ProductionOrigin: "https://example.org", Spec: "docs/products/need-1/spec.md", Architecture: "docs/products/need-1/architecture.md", Plan: "docs/products/need-1/plan.md", Files: []string{"src/pages/en/tools/example.astro", "internal/products/example.go"}, Environment: []string{"OPENAI_API_KEY"}, Repository: "https://github.com/yyl1212/creative", Baseline: strings.Repeat("a", 40), Branch: "codex/need-1-example", Compatibility: "已审查：新增路由，不变更已有接口", ReviewNotes: "范围可实现，验收与文件影响已核对"}
}

func TestApprovalRequiresCurrentEvidence(t *testing.T) {
	for _, scenario := range []string{"expired", "missing_sources", "wrong_verification", "missing_manual_review", "missing_cost", "few_alternatives", "paid_without_basis", "valid"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, n, a := approvedFixture(t)
			switch scenario {
			case "expired":
				db.Exec("UPDATE needs SET valid_until=1 WHERE id=1")
			case "missing_sources":
				db.Exec("DELETE FROM need_sources WHERE need_id=1")
			case "wrong_verification":
				a.VerificationID++
			case "missing_manual_review":
				a.ReviewedEvidence = nil
			case "missing_cost":
				a.UnitCost = ""
			case "few_alternatives":
				a.Alternatives = a.Alternatives[:1]
			case "paid_without_basis":
				a.Monetization = "paid"
				a.Price = "1"
			}
			err := s.ApproveNeed(n.ID, n.Version, a, "owner", "local")
			if scenario == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				d, e := s.Development(1)
				if e != nil || d.State != "approved" {
					t.Fatalf("%+v %v", d, e)
				}
			} else if err == nil {
				t.Fatal("invalid approval accepted")
			}
		})
	}
}

func TestApprovalVersionConflict(t *testing.T) {
	s, db, n, a := approvedFixture(t)
	if e := s.ApproveNeed(1, n.Version, a, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	code(t, s.ApproveNeed(1, n.Version, a, "owner", "local"), 409)
	var count int
	db.QueryRow("SELECT count(*) FROM audit_events WHERE action='need_approved'").Scan(&count)
	if count != 1 {
		t.Fatal("duplicate approval audited as success")
	}
}

func TestApprovalConcurrentVersions(t *testing.T) {
	for attempt := 0; attempt < 6; attempt++ {
		s, db, n, a := approvedFixture(t)
		var seq int
		var name, path string
		if e := db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); e != nil {
			t.Fatal(e)
		}
		second, e := store.Open(path)
		if e != nil {
			t.Fatal(e)
		}
		other, e := New(second)
		if e != nil {
			second.Close()
			t.Fatal(e)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, service := range []*Service{s, other} {
			wg.Go(func() { <-start; results <- service.ApproveNeed(1, n.Version, a, "owner", "local") })
		}
		close(start)
		wg.Wait()
		close(results)
		second.Close()
		successes := 0
		for e := range results {
			if e == nil {
				successes++
			} else {
				code(t, e, 409)
			}
		}
		if successes != 1 {
			t.Fatal("concurrent approvals", successes)
		}
	}
}

func TestDesignProductionOrigin(t *testing.T) {
	for _, origin := range []string{"https://example.org:8443", "https://example.org?", "https://example.org#", "https://127.0.0.1", "https://192.0.2.1"} {
		d := designFixture()
		d.ProductionOrigin = origin
		if e := validDesign(d); e == nil {
			t.Errorf("invalid production origin accepted: %s", origin)
		}
	}
}

func TestApprovalFreesResearchSlot(t *testing.T) {
	for _, state := range []string{"target_reached", "cancelled", "paused_service"} {
		t.Run(state, func(t *testing.T) {
			s, db, n, a := approvedFixture(t)
			for i := 0; i < 2; i++ {
				if _, e := db.Exec("INSERT INTO needs(title,task,status,verified_at,valid_until,rules_version) SELECT title||?,task||?,'qualified',verified_at,valid_until,rules_version FROM needs WHERE id=1", fmt.Sprint(i), fmt.Sprint(i)); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := db.Exec("UPDATE research_tasks SET target_count=3,state=?", state); e != nil {
				t.Fatal(e)
			}
			_, q, _, e := s.Counts()
			if e != nil || q != 3 {
				t.Fatal(q, e)
			}
			if e = s.ApproveNeed(1, n.Version, a, "owner", "local"); e != nil {
				t.Fatal(e)
			}
			var actual string
			if e = db.QueryRow("SELECT state FROM research_tasks WHERE is_current=1").Scan(&actual); e != nil {
				t.Fatal(e)
			}
			expected := state
			if state == "target_reached" {
				expected = "active"
			}
			if actual != expected {
				t.Errorf("task state %s, want %s", actual, expected)
			}
			_, q, _, e = s.Counts()
			if e != nil || q != 2 {
				t.Fatalf("approved need occupies slot: %d %v", q, e)
			}
			tasks, e := s.Tasks()
			if e != nil || len(tasks) != 1 || tasks[0].RemainingGap != 1 {
				t.Fatal(tasks, e)
			}
			if _, e = db.Exec("UPDATE needs SET version=version+1 WHERE id=1"); e != nil {
				t.Fatal(e)
			}
			_, q, _, e = s.Counts()
			if e != nil || q != 3 {
				t.Fatal("newly verified evidence must wait for new approval", q, e)
			}
		})
	}
}

func TestRestoreCannotReviveDevelopmentApproval(t *testing.T) {
	s, _, m := releaseFixture(t)
	dir := t.TempDir()
	backup, e := store.Backup(context.Background(), s.db, filepath.Join(dir, "backups"), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(dir, "restored.db")
	if e = store.RestoreBackup(context.Background(), backup, dest); e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(dest)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	restored, e := New(db)
	if e != nil {
		t.Fatal(e)
	}
	n, e := restored.Need(1, 0)
	if e != nil {
		t.Fatal(e)
	}
	d, e := restored.Development(1)
	if e != nil {
		t.Fatal(e)
	}
	if n.Status != "pending" || n.Version == d.NeedVersion {
		t.Errorf("restored approval remains current: status=%s needVersion=%d approvalVersion=%d", n.Status, n.Version, d.NeedVersion)
	}
	if _, e = restored.TaskBundle(1, "development"); e == nil {
		t.Error("restored development bundle exported")
	}
	if e = restored.recordRelease(1, d.Version, m, nil); e == nil {
		t.Error("restored review_ready directly launched")
	}
	code(t, restored.VerifyRelease(context.Background(), 1, d.Version, "missing", "missing"), 409)
	if e = restored.ConfirmDesign(1, d.Version, d.Design, "owner", "local"); e == nil {
		t.Error("restored design confirmed before new evidence")
	}
	// 模拟证据重新核验后，旧 unverified 批准仍不能直接使用。
	if _, e = db.Exec("UPDATE needs SET status='qualified',valid_until=?,version=? WHERE id=1", time.Now().Add(time.Hour).Unix(), d.NeedVersion); e != nil {
		t.Fatal(e)
	}
	if _, e = restored.TaskBundle(1, "development"); e == nil {
		t.Error("unverified development exported")
	}
	if e = restored.ConfirmDesign(1, d.Version, d.Design, "owner", "local"); e == nil {
		t.Error("unverified approval bypassed")
	}
	if e = restored.ApproveNeed(1, d.NeedVersion, d.Approval, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	fresh, _ := restored.Development(1)
	if e = restored.ConfirmDesign(1, fresh.Version, d.Design, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	if _, e = restored.TaskBundle(1, "development"); e != nil {
		t.Fatal("fresh approval blocked", e)
	}
}

func TestTaskBundleExcludesSecrets(t *testing.T) {
	s, db, n, a := approvedFixture(t)
	if e := s.ApproveNeed(1, n.Version, a, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	raw, e := s.TaskBundle(1, "requirements")
	if e != nil {
		t.Fatal(e)
	}
	var bundle map[string]any
	if json.Unmarshal(raw, &bundle) != nil || bundle["schema_version"] != "creative.requirement-task.v1" {
		t.Fatal(string(raw))
	}
	if _, e = s.TaskBundle(1, "development"); e == nil {
		t.Fatal("unapproved design exported")
	}
	d, _ := s.Development(1)
	design := designFixture()
	if e = s.ConfirmDesign(1, d.Version, design, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	raw, e = s.TaskBundle(1, "development")
	if e != nil || !strings.Contains(string(raw), "OPENAI_API_KEY") || strings.Contains(string(raw), "token_hash") {
		t.Fatalf("bundle %s %v", raw, e)
	}
	d, _ = s.Development(1)
	design.Files = []string{".env.local"}
	if s.ConfirmDesign(1, d.Version, design, "owner", "local") == nil {
		t.Fatal("private file scope accepted")
	}
	design = designFixture()
	design.ReviewNotes = "不要导出 sk-fixture-secret-value-0123456789"
	if s.ConfirmDesign(1, d.Version, design, "owner", "local") == nil {
		t.Fatal("secret accepted")
	}
	db.Exec("UPDATE needs SET task=? WHERE id=1", "source says: sk-fixture-secret-value-0123456789")
	if _, e = s.TaskBundle(1, "development"); e == nil {
		t.Fatal("secret in exported source")
	}
}

func TestDevelopmentRechecksEvidenceAndCommit(t *testing.T) {
	s, db, n, a := approvedFixture(t)
	if e := s.ApproveNeed(1, n.Version, a, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	d, _ := s.Development(1)
	if e := s.ConfirmDesign(1, d.Version, designFixture(), "owner", "local"); e != nil {
		t.Fatal(e)
	}
	d, _ = s.Development(1)
	if e := s.StartDevelopment(1, d.Version, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	code(t, s.StartDevelopment(1, d.Version, "owner", "local"), 409)
	d, _ = s.Development(1)
	result := DevelopmentResult{Commit: strings.Repeat("b", 40), PullRequest: "https://github.com/yyl1212/creative/pull/123", Summary: "实现已批准范围", ReviewCommit: strings.Repeat("b", 40), TestCommit: strings.Repeat("b", 40), ReviewReport: "docs/products/need-1/review.md", TestReport: "docs/products/need-1/tests.md", ChecksPassed: true}
	if e := s.RecordDevelopment(1, d.Version, result, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	d, _ = s.Development(1)
	if d.State != "review_ready" {
		t.Fatal(d.State)
	}
	result.Commit = strings.Repeat("c", 40)
	if e := s.RecordDevelopment(1, d.Version, result, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	d, _ = s.Development(1)
	if d.State == "review_ready" {
		t.Fatal("old checks credited to new commit")
	}
	db.Exec("UPDATE needs SET version=version+1,status='pending',valid_until=0 WHERE id=1")
	if _, e := s.TaskBundle(1, "development"); e == nil {
		t.Fatal("superseded evidence exported")
	}
	if s.StartDevelopment(1, d.Version, "owner", "local") == nil {
		t.Fatal("superseded approval started")
	}
}
