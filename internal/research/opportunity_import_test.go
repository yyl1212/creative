package research

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func opportunityPackage(t *testing.T, v map[string]any, now time.Time) map[string]any {
	t.Helper()
	p := reviewPackage(t, v, now)
	p["rules_version"] = "opportunity.v2"
	q := p["project"].(map[string]any)
	check, err := CheckAgentReview(reviewBytes(v), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	_ = json.Unmarshal(reviewBytes(check), &metadata)
	q["evaluation"] = v["evaluation"]
	q["qualification_valid_until"] = metadata["qualification_valid_until"]
	a := q["approval"].(map[string]any)
	a["monetization"] = v["candidate"].(map[string]any)["monetization_proposal"]
	a["price"], a["unit_cost"], a["payment_fee"], a["currency"] = "", "", "", ""
	a["price_basis"] = "尚未定价，原文只有付款意愿；开发验证后再测试报价"
	if a["monetization"] == "free" {
		a["price"], a["payment_fee"] = "0", "0"
	}
	return p
}

func TestOpportunityPackageBinding(t *testing.T) {
	v, now := opportunityFixture()
	p := opportunityPackage(t, v, now)
	got, err := CheckAgentReview(reviewBytes(v), reviewBytes(p), now)
	if err != nil || got.ProjectHash == "" {
		t.Fatalf("bind v2: %+v %v", got, err)
	}
	for _, name := range []string{"evaluation", "expiry", "monetization"} {
		t.Run(name, func(t *testing.T) {
			p := opportunityPackage(t, v, now)
			_ = json.Unmarshal(reviewBytes(p), &p)
			q := p["project"].(map[string]any)
			switch name {
			case "evaluation":
				q["evaluation"].(map[string]any)["feasibility"].(map[string]any)["basis"] = "改写风险后未重新审查"
			case "expiry":
				q["qualification_valid_until"] = "2026-11-11T12:00:00Z"
			case "monetization":
				q["approval"].(map[string]any)["monetization"] = "free"
			}
			if _, err := CheckAgentReview(reviewBytes(v), reviewBytes(p), now); err == nil {
				t.Fatal("changed evaluation/expiry/model accepted")
			}
		})
	}
}

func TestOpportunityImportLifecycle(t *testing.T) {
	s, db, producer, old, _, _ := setupVerification(t)
	if _, err := s.ChangeTask(old.ID, old.Version, "cancel", "owner", "test"); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateProjectTask(producer.ID, []string{"export"}, []string{"en"}, 1, "owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	if task.RulesVersion != "opportunity.v2" {
		t.Fatalf("new task uses %s", task.RulesVersion)
	}
	catalog, err := s.ProjectCatalog(producer)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := opportunityFixture()
	now := time.Now().UTC().Truncate(time.Second)
	v["catalog_revision"], v["reviewed_at"] = catalog.Revision, now.Format(time.RFC3339)
	for i := range 3 {
		reviewSource(v, i)["published_date"] = now.AddDate(0, 0, -5).Format(time.DateOnly)
		reviewSource(v, i)["observed_at"] = now.Add(-time.Minute).Format(time.RFC3339)
	}
	v["candidate"].(map[string]any)["alternatives"].([]any)[0].(map[string]any)["checked_at"] = now.Add(-time.Minute).Format(time.RFC3339)
	p := opportunityPackage(t, v, now)
	p["task_id"] = task.ID
	raw := reviewBytes(p)
	if _, err = CheckAgentReview(reviewBytes(v), raw, now); err != nil {
		t.Fatal(err)
	}
	first, err := s.ImportProject(producer, raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ImportProject(producer, raw)
	if err != nil || string(first) != string(second) {
		t.Fatalf("replay %s %v", second, err)
	}
	if err = s.Maintenance(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	need, err := s.Need(1, 0)
	if err != nil || need.Status != "qualified" {
		t.Fatalf("v2 incorrectly expired %+v %v", need, err)
	}
	var storedRule string
	if err = db.QueryRow("SELECT rules_version FROM needs WHERE id=1").Scan(&storedRule); err != nil || storedRule != "opportunity.v2" {
		t.Fatalf("stored rule %s %v", storedRule, err)
	}
	dev, err := s.Development(1)
	if err != nil || !dev.EvidenceCurrent || dev.State != "approved" {
		t.Fatalf("development %+v %v", dev, err)
	}
	bundle, err := s.TaskBundle(1, "requirements")
	if err != nil {
		t.Fatal(err)
	}
	var exported map[string]any
	_ = json.Unmarshal(bundle, &exported)
	if exported["rules_version"] != "opportunity.v2" || exported["evaluation"] == nil || strings.Contains(string(bundle), "0001-01-01") {
		t.Fatalf("lost rule/evaluation/date: %s", bundle)
	}
	evidence := exported["untrusted_evidence"].([]any)[0].(map[string]any)
	if evidence["published_date"] != now.AddDate(0, 0, -5).Format(time.DateOnly) || evidence["published_at"] != "" {
		t.Fatalf("fabricated timestamp: %v", evidence)
	}
	receipt, err := s.Receipt(producer, p["batch_id"].(string))
	if err != nil || receipt.QualifiedCount != 1 || receipt.TaskState != "target_reached" {
		t.Fatalf("receipt %+v %v", receipt, err)
	}
}

func TestOpportunityRecentEvidenceLimitsQualification(t *testing.T) {
	v, now := opportunityFixture()
	for i := range 3 {
		reviewSource(v, i)["published_date"] = "2026-05-16"
	}
	got, err := CheckAgentReview(reviewBytes(v), nil, now)
	if err != nil || !got.Eligible {
		t.Fatalf("%+v %v", got, err)
	}
	var meta map[string]any
	_ = json.Unmarshal(reviewBytes(got), &meta)
	want := "2026-11-11T10:00:00Z"
	if meta["qualification_valid_until"] != want {
		t.Fatalf("expiry must stop when recent support expires: %v want %s", meta, want)
	}
	// 同一人的新付款回复不能刷新半年前的未满足原文。
	v, now = opportunityFixture()
	for i := range 3 {
		reviewSource(v, i)["published_date"] = "2026-01-01"
	}
	src := map[string]any{}
	for k, value := range reviewSource(v, 1) {
		src[k] = value
	}
	src["evidence_id"], src["published_date"] = "payment-followup", "2026-11-01"
	a := map[string]any{}
	for k, value := range reviewAssessment(v, 1) {
		a[k] = value
	}
	a["evidence_id"], a["unmet_quote"] = "payment-followup", ""
	c := v["candidate"].(map[string]any)
	c["evidence"] = append(c["evidence"].([]any), src)
	v["evidence"] = append(v["evidence"].([]any), a)
	v["verdict"] = "needs_evidence"
	got, err = CheckAgentReview(reviewBytes(v), nil, now)
	if err != nil || got.Eligible || extraCount(got, "recent_unmet_users") != 0 {
		t.Fatalf("payment refreshed old pain: %+v %v", got, err)
	}
}

func TestLegacyProjectReplayAndHistoricalRuleSurviveUpgrade(t *testing.T) {
	s, db, p, old, _, _ := setupVerification(t)
	if _, err := s.ChangeTask(old.ID, old.Version, "cancel", "owner", "test"); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateProjectTask(p.ID, []string{"export"}, []string{"en"}, 2, "owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	// 模拟升级前已经存在的任务，保留原规则而非重新创建历史接口。
	if _, err = db.Exec("UPDATE research_tasks SET rules_version='strong-wtp.v1',rules=json_set(?,'$.decision_mode','agent_project') WHERE id=?", rulesJSON, task.ID); err != nil {
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
	if err = s.Maintenance(time.Now()); err != nil {
		t.Fatal(err)
	}
	second, err := s.ImportProject(p, raw)
	if err != nil || string(first) != string(second) {
		t.Fatalf("legacy replay %s %v", second, err)
	}
	n, err := s.Need(1, 0)
	if err != nil || n.Status != "qualified" {
		t.Fatalf("legacy needs expired %+v %v", n, err)
	}
	var current Decision
	_ = json.Unmarshal([]byte(jsonText(n.Decision)), &current)
	var evaluation Evaluation
	_ = json.Unmarshal(reviewBytes(evaluationFixture()), &evaluation)
	current.Evaluation = &evaluation
	if _, err = db.Exec("UPDATE needs SET rules_version='opportunity.v2',decision=?,verification_id=NULL WHERE id=1", jsonText(current)); err != nil {
		t.Fatal(err)
	}
	history, err := s.Need(1, n.VerificationID)
	if err != nil {
		t.Fatal(err)
	}
	var view map[string]any
	_ = json.Unmarshal(reviewBytes(history), &view)
	if view["RulesVersion"] != "strong-wtp.v1" || history.Decision.Evaluation != nil {
		t.Fatalf("history borrowed current rule/evaluation: %+v", history)
	}
}
