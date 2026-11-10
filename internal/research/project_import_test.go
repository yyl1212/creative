package research

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yyl1212/creative/internal/auth"
)

func projectInput(t *testing.T, s *Service, taskID, revision string) []byte {
	t.Helper()
	return change(t, fixture(t, taskID), func(v map[string]any) {
		v["schema_version"] = "creative.project-package.v1"
		v["batch_id"] = "project-one"
		c, _, a := qualificationFixture()
		c.Language, c.Monetization, c.Unknowns = "en", "free", []string{}
		c.Alternatives = nil
		var cs map[string]any
		_ = json.Unmarshal([]byte(jsonText(c)), &cs)
		cs["alternatives"] = []any{}
		for _, e := range cs["evidence"].([]any) {
			x := e.(map[string]any)
			x["observed_at"] = time.Now().UTC().Format(time.RFC3339)
			x["published_at"] = time.Now().AddDate(0, -1, 0).UTC().Format(time.RFC3339)
			x["conditions"] = []any{}
		}
		v["candidates"] = []any{cs}
		approval := Approval{ReviewedEvidence: []string{"0", "1", "2"}, ReviewNotes: "逐条检查并保留反证", AlternativeSearch: "官方目录和公开工具页", FewerAlternativesReason: "本测试没有真实替代品", Monetization: "free", Rationale: "免费工具引流", UnitCost: "0", Currency: "USD", Unit: "每次", Price: "0", PaymentFee: "0", Sustainability: "本地计算", ResourceProtection: "限制文件大小", SelfService: "上传并下载结果", Gap: "准确导出", Scope: "一个核心任务", Exclusions: "不提供人工处理", Acceptance: "给定样例输出准确", Acquisition: "搜索教程"}
		ev := []any{}
		for _, e := range a.Evidence {
			ev = append(ev, map[string]any{"evidence_id": e.ID, "valid": true, "payment": e.Signal == "explicit_wtp", "priced": e.PriceQuote != "", "reason": "原文已审查"})
		}
		v["project"] = map[string]any{"verdict": "approved", "provider": "openai", "model": "fixture-model", "reviewed_at": time.Now().UTC().Format(time.RFC3339), "catalog_revision": revision, "task_key": "invoice-csv-export", "reasoning": "证据支持明确任务和自助交付", "deduplication": "已比较完整目录，未发现同一任务", "evidence": ev, "approval": approval}
	})
}

func TestProjectImportCreatesApprovedDevelopment(t *testing.T) {
	s, db, p, old, _, _ := setupVerification(t)
	if _, err := s.ChangeTask(old.ID, old.Version, "cancel", "owner", "local"); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateProjectTask(p.ID, []string{"export"}, []string{"en"}, 1, "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.ProjectCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	raw := projectInput(t, s, task.ID, catalog.Revision)
	first, err := s.ImportProject(p, raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ImportProject(p, raw)
	if err != nil || string(first) != string(second) {
		t.Fatalf("retry: %v", err)
	}
	report, err := s.DailyReport("")
	if err != nil || report.Approved != 1 {
		t.Fatalf("import or replay miscounted in daily report: %+v %v", report, err)
	}
	r, err := s.Receipt(p, "project-one")
	if err != nil || r.Status != "completed" || r.QualifiedCount != 1 || r.TaskState != "target_reached" || r.ShouldContinue {
		t.Fatalf("receipt %+v %v", r, err)
	}
	d, err := s.Development(1)
	if err != nil || d.State != "approved" || !strings.HasPrefix(d.ApprovedBy, "agent:") {
		t.Fatalf("development %+v %v", d, err)
	}
	bundle, err := s.TaskBundle(1, "requirements")
	if err != nil || !strings.Contains(string(bundle), "fixture-model") {
		t.Fatalf("bundle %s %v", bundle, err)
	}
	lease, err := s.Claim(context.Background(), "test", time.Now())
	if err != nil || lease != nil {
		t.Fatalf("import queued for website review: %+v %v", lease, err)
	}
	var n int
	db.QueryRow("SELECT count(*) FROM development_records").Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	code(t, func() error { _, e := s.ImportProject(p, append(raw, ' ')); return e }(), 409)
}

func TestProjectImportBoundaries(t *testing.T) {
	for _, scenario := range []string{"legacy", "cancelled", "stale_catalog", "unknown_provider", "missing_evidence", "bad_reference", "reject", "legacy_batch", "missing_fields"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, p, task, _, _ := setupVerification(t)
			if scenario != "legacy" {
				_, err := s.ChangeTask(task.ID, task.Version, "cancel", "owner", "local")
				if err != nil {
					t.Fatal(err)
				}
				task, err = s.CreateProjectTask(p.ID, []string{"export"}, []string{"en"}, 2, "owner", "local")
				if err != nil {
					t.Fatal(err)
				}
			}
			catalog, err := s.ProjectCatalog(p)
			if err != nil {
				t.Fatal(err)
			}
			raw := projectInput(t, s, task.ID, catalog.Revision)
			if scenario == "cancelled" {
				_, err = s.ChangeTask(task.ID, task.Version, "cancel", "owner", "local")
				if err != nil {
					t.Fatal(err)
				}
			}
			raw = change(t, raw, func(v map[string]any) {
				q := v["project"].(map[string]any)
				switch scenario {
				case "stale_catalog":
					q["catalog_revision"] = strings.Repeat("0", 64)
				case "unknown_provider":
					q["provider"] = "other"
				case "missing_evidence":
					q["evidence"] = []any{}
				case "bad_reference":
					q["evidence"].([]any)[0].(map[string]any)["evidence_id"] = "fake"
				case "reject":
					q["verdict"] = "rejected"
				case "missing_fields":
					delete(q, "deduplication")
				case "legacy_batch":
					delete(v, "project")
					v["schema_version"] = "creative.demand-batch.v1"
				}
			})
			if scenario == "legacy_batch" {
				_, err = s.Receive(p, raw)
			} else {
				_, err = s.ImportProject(p, raw)
			}
			if err == nil {
				t.Fatal("invalid import accepted")
			}
			var n int
			db.QueryRow("SELECT count(*) FROM needs").Scan(&n)
			if n != 0 {
				t.Fatal("partial import persisted")
			}
		})
	}
}

func TestProjectReassessmentAndLegacyProtection(t *testing.T) {
	s, db, p, old, _, _ := setupVerification(t)
	_, err := s.ChangeTask(old.ID, old.Version, "cancel", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateProjectTask(p.ID, []string{"export"}, []string{"en"}, 3, "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.ProjectCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportProject(p, projectInput(t, s, task.ID, catalog.Revision)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE needs SET status='pending',valid_until=1,version=version+1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TaskBundle(1, "requirements"); err == nil {
		t.Fatal("expired bundle exported")
	}
	catalog, err = s.ProjectCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	raw := change(t, projectInput(t, s, task.ID, catalog.Revision), func(v map[string]any) {
		v["batch_id"] = "project-review"
		v["project"].(map[string]any)["replaces"] = map[string]any{"need_id": 1, "version": 2}
	})
	if _, err = s.ImportProject(p, raw); err != nil {
		t.Fatal(err)
	}
	r, err := s.Receipt(p, "project-review")
	if err != nil || r.QualifiedCount != 1 {
		t.Fatalf("reassessment counted twice: %+v %v", r, err)
	}
	if _, err = s.TaskBundle(1, "requirements"); err != nil {
		t.Fatal(err)
	}
	catalog, err = s.ProjectCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	raw = change(t, raw, func(v map[string]any) {
		v["batch_id"] = "project-in-progress"
		q := v["project"].(map[string]any)
		q["catalog_revision"] = catalog.Revision
		q["replaces"] = map[string]any{"need_id": 1, "version": 3}
	})
	db.Exec("UPDATE development_records SET state='developing' WHERE need_id=1")
	if _, err = s.ImportProject(p, raw); err == nil {
		t.Fatal("active development overwritten")
	}
	var n int
	db.QueryRow("SELECT count(*) FROM need_verifications").Scan(&n)
	if n != 2 {
		t.Fatal("failed update was not atomic")
	}
	if err = s.Maintenance(time.Now().AddDate(0, 0, 181)); err != nil {
		t.Fatal("retention blocked by import metadata", err)
	}
}

func TestProjectConcurrentImportsRequireFreshCatalog(t *testing.T) {
	s, db, p, old, _, _ := setupVerification(t)
	_, err := s.ChangeTask(old.ID, old.Version, "cancel", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateProjectTask(p.ID, []string{"export"}, []string{"en"}, 3, "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.ProjectCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	first := projectInput(t, s, task.ID, catalog.Revision)
	second := change(t, first, func(v map[string]any) {
		v["batch_id"] = "different-project"
		c := v["candidates"].([]any)[0].(map[string]any)
		c["external_id"] = "different"
		c["job_to_be_done"] = "different core task"
		v["project"].(map[string]any)["task_key"] = "different-key"
	})
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for _, raw := range [][]byte{first, second} {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.ImportProject(p, raw); errors <- e }()
	}
	wg.Wait()
	close(errors)
	success := 0
	for e := range errors {
		if e == nil {
			success++
		} else {
			code(t, e, 409)
		}
	}
	if success != 1 {
		t.Fatalf("same catalog imported %d projects", success)
	}
	var n int
	db.QueryRow("SELECT count(*) FROM needs").Scan(&n)
	if n != 1 {
		t.Fatal("partial concurrent import")
	}
}

func TestDuplicateAliasDoesNotGrantProjectOwnership(t *testing.T) {
	s, db, owner, old, f, r := setupVerification(t)
	_, err := s.ChangeTask(old.ID, old.Version, "cancel", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateProjectTask(owner.ID, []string{"export"}, []string{"en"}, 2, "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.ProjectCatalog(owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportProject(owner, projectInput(t, s, task.ID, catalog.Revision)); err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(db)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := a.CreateProducer("other-agent", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.CreateTask(other.ID, []string{"export"}, []string{"en"}, 3, "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	submitQualified(t, s, other, legacy.ID, "other-duplicate", "export")
	processFixture(t, s, f, r)
	var linked int
	if err = db.QueryRow("SELECT need_id FROM need_aliases WHERE producer_id=? AND external_id='export'", other.ID).Scan(&linked); err != nil || linked != 1 {
		t.Fatalf("duplicate not linked: %d %v", linked, err)
	}
	legacy, err = s.CurrentTask(other)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ChangeTask(legacy.ID, legacy.Version, "cancel", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	otherTask, err := s.CreateProjectTask(other.ID, []string{"export"}, []string{"en"}, 2, "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err = s.ProjectCatalog(other)
	if err != nil {
		t.Fatal(err)
	}
	raw := change(t, projectInput(t, s, otherTask.ID, catalog.Revision), func(v map[string]any) {
		v["project"].(map[string]any)["replaces"] = map[string]any{"need_id": 1, "version": 1}
	})
	if _, err = s.ImportProject(other, raw); err == nil {
		t.Fatal("duplicate alias granted project ownership")
	}
	d, err := s.Development(1)
	if err != nil || !strings.HasPrefix(d.ApprovedBy, "agent:"+owner.ID+":") {
		t.Fatalf("original approval overwritten: %+v %v", d, err)
	}
}

func TestExpiredDesignCanBeReassessed(t *testing.T) {
	s, db, p, old, _, _ := setupVerification(t)
	_, err := s.ChangeTask(old.ID, old.Version, "cancel", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateProjectTask(p.ID, []string{"export"}, []string{"en"}, 3, "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.ProjectCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportProject(p, projectInput(t, s, task.ID, catalog.Revision)); err != nil {
		t.Fatal(err)
	}
	if err = s.ConfirmDesign(1, 1, designFixture(), "owner", "local"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE needs SET valid_until=1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err = s.Maintenance(time.Now()); err != nil {
		t.Fatal(err)
	}
	catalog, err = s.ProjectCatalog(p)
	if err != nil {
		t.Fatal(err)
	}
	raw := change(t, projectInput(t, s, task.ID, catalog.Revision), func(v map[string]any) {
		v["batch_id"] = "design-reassessment"
		v["project"].(map[string]any)["replaces"] = map[string]any{"need_id": 1, "version": 2}
	})
	if _, err = s.ImportProject(p, raw); err != nil {
		t.Fatal("unstarted design cannot be reassessed", err)
	}
	d, err := s.Development(1)
	if err != nil || d.State != "approved" || d.Design.Spec != "" {
		t.Fatalf("design was not reset for confirmation: %+v %v", d, err)
	}
}
