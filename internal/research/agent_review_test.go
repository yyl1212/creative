package research

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func agentReviewFixture() (map[string]any, time.Time) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	evs, assessments := []any{}, []any{}
	for i, id := range []string{"a", "b", "c"} {
		quote := "I bought the converter. It still loses my table columns."
		signal, payment, spend := "currently_paying", "I bought the converter.", "I bought the converter."
		if i == 1 {
			quote = "Fix the table columns and I would pay for it."
			signal = "explicit_wtp"
			payment = quote
			spend = ""
		}
		if i == 2 {
			quote = "My table columns are missing after every export."
			signal = "pain_only"
			payment = ""
			spend = ""
		}
		host := "forum.example.org"
		if i == 2 {
			host = "issues.example.net"
		}
		evs = append(evs, map[string]any{"evidence_id": id, "url": "https://" + host + "/post/" + id, "author_ref": "person-" + id, "published_at": "2026-10-10T10:00:00Z", "observed_at": "2026-11-10T11:00:00Z", "quote": quote, "signal": signal, "price": nil, "conditions": []any{}, "unmet_need": "导出仍丢列"})
		assessments = append(assessments, map[string]any{"evidence_id": id, "user_key": "user-" + id, "relevant": true, "original": true, "direct_user": true, "unmet_quote": quote, "payment_quote": payment, "price_quote": "", "spend_quote": spend, "conditions_supported": true, "reason": "自制原文已复核"})
	}
	return map[string]any{"schema_version": "creative.agent-review.v1", "rules_version": "strong-wtp.v1", "provider": "deepseek", "model": "deepseek-fixture", "reviewed_at": "2026-11-10T11:30:00Z", "reasoning": "保留条件与反证", "deduplication": "完整目录中没有同一任务", "catalog_revision": strings.Repeat("a", 64), "task_key": "table-columns", "verdict": "approved", "self_service": true, "distinct_from_existing": true, "candidate": map[string]any{"external_id": "table-columns", "title": "表格列导出", "language": "en", "target_user": "表格用户", "job_to_be_done": "完整导出表格列", "constraints": []any{}, "evidence": evs, "alternatives": []any{}, "monetization_proposal": "free", "unknowns": []any{}}, "evidence": assessments}, now
}
func reviewSource(v map[string]any, i int) map[string]any {
	return v["candidate"].(map[string]any)["evidence"].([]any)[i].(map[string]any)
}
func reviewAssessment(v map[string]any, i int) map[string]any {
	return v["evidence"].([]any)[i].(map[string]any)
}
func reviewBytes(v any) []byte { raw, _ := json.Marshal(v); return raw }

func TestAgentReviewCounts(t *testing.T) {
	v, now := agentReviewFixture()
	got, err := CheckAgentReview(reviewBytes(v), nil, now)
	if err != nil || got.Counts.Users != 3 || got.Counts.Sites != 2 || got.Counts.PaymentUsers != 2 || got.Counts.PriceOrSpendUsers != 1 || !got.Eligible {
		t.Fatalf("%+v %v", got, err)
	}
	// A second post by the same author adds payment support, never a second user.
	src := reviewSource(v, 0)
	copied := map[string]any{}
	for k, x := range src {
		copied[k] = x
	}
	copied["evidence_id"] = "a-followup"
	ass := map[string]any{}
	for k, x := range reviewAssessment(v, 0) {
		ass[k] = x
	}
	ass["evidence_id"] = "a-followup"
	c := v["candidate"].(map[string]any)
	c["evidence"] = append(c["evidence"].([]any), copied)
	v["evidence"] = append(v["evidence"].([]any), ass)
	src["signal"] = "pain_only"
	reviewAssessment(v, 0)["payment_quote"] = ""
	reviewAssessment(v, 0)["spend_quote"] = ""
	got, err = CheckAgentReview(reviewBytes(v), nil, now)
	if err != nil || got.Counts.Users != 3 || got.Counts.PaymentUsers != 2 {
		t.Fatalf("followup: %+v %v", got, err)
	}
}

func TestAgentReviewEvidenceBoundaries(t *testing.T) {
	cases := []struct {
		name                      string
		change                    func(map[string]any)
		users, sites, pay, priced int
	}{
		{"conditional", func(v map[string]any) { reviewSource(v, 1)["conditions"] = []any{"Fix the table columns"} }, 3, 2, 2, 1},
		{"unsupported_condition", func(v map[string]any) { reviewAssessment(v, 1)["conditions_supported"] = false }, 3, 2, 1, 1},
		{"unrelated", func(v map[string]any) { reviewAssessment(v, 0)["relevant"] = false }, 2, 2, 1, 0},
		{"satisfied", func(v map[string]any) { reviewAssessment(v, 0)["unmet_quote"] = "" }, 2, 2, 1, 0},
		{"maker_or_quote", func(v map[string]any) { reviewAssessment(v, 0)["direct_user"] = false }, 2, 2, 1, 0},
		{"stale", func(v map[string]any) { reviewSource(v, 0)["published_at"] = "2025-11-10T12:00:00Z" }, 3, 2, 1, 0},
		{"unknown_date", func(v map[string]any) { reviewSource(v, 0)["published_at"] = nil }, 2, 2, 1, 0},
		{"unknown_author", func(v map[string]any) { reviewSource(v, 0)["author_ref"] = nil }, 2, 2, 1, 0},
		{"same_person_cross_site", func(v map[string]any) { reviewAssessment(v, 2)["user_key"] = "user-a" }, 2, 2, 2, 1},
		{"same_site_subdomain", func(v map[string]any) { reviewSource(v, 2)["url"] = "https://other.example.org/c" }, 3, 1, 2, 1},
		{"unknown_currency", func(v map[string]any) {
			reviewSource(v, 0)["quote"] = "I would pay $25 once. It still loses my table columns."
			reviewSource(v, 0)["signal"] = "explicit_wtp"
			reviewSource(v, 0)["price"] = map[string]any{"amount": "25", "currency": nil, "unit": "once"}
			a := reviewAssessment(v, 0)
			a["unmet_quote"] = "It still loses my table columns."
			a["payment_quote"] = "I would pay $25 once."
			a["price_quote"] = "I would pay $25 once."
			a["spend_quote"] = ""
		}, 3, 2, 2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, now := agentReviewFixture()
			v["verdict"] = "needs_evidence"
			tc.change(v)
			got, err := CheckAgentReview(reviewBytes(v), nil, now)
			if err != nil || got.Counts.Users != tc.users || got.Counts.Sites != tc.sites || got.Counts.PaymentUsers != tc.pay || got.Counts.PriceOrSpendUsers != tc.priced {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestAgentReviewRejectsDrift(t *testing.T) {
	for _, name := range []string{"nested_verdict", "missing_bool", "extra_count", "unknown_id", "duplicate_id", "missing_id", "identity_split", "wrong_quote", "invented_condition", "future_date", "weak_approved", "oversized", "truncated", "duplicate_key", "trailing", "fenced"} {
		t.Run(name, func(t *testing.T) {
			v, now := agentReviewFixture()
			var raw []byte
			switch name {
			case "nested_verdict":
				v["overall"] = map[string]any{"verdict": v["verdict"]}
				delete(v, "verdict")
			case "missing_bool":
				delete(reviewAssessment(v, 0), "direct_user")
			case "extra_count":
				v["payment_users"] = 9
			case "unknown_id":
				reviewAssessment(v, 0)["evidence_id"] = "unknown"
			case "duplicate_id":
				reviewSource(v, 1)["evidence_id"] = "a"
			case "missing_id":
				v["evidence"] = v["evidence"].([]any)[:2]
			case "identity_split":
				reviewSource(v, 1)["author_ref"] = "PERSON-A"
			case "wrong_quote":
				reviewAssessment(v, 0)["payment_quote"] = "I will pay 10000."
			case "invented_condition":
				reviewSource(v, 1)["conditions"] = []any{"Unlimited files forever"}
			case "future_date":
				reviewSource(v, 0)["published_at"] = "2026-11-11T12:00:00Z"
			case "weak_approved":
				reviewAssessment(v, 0)["direct_user"] = false
			case "oversized":
				raw = []byte(strings.Repeat(" ", 1<<20+1))
			case "truncated":
				raw = reviewBytes(v)
				raw = raw[:len(raw)-1]
			case "duplicate_key":
				raw = append([]byte(`{"verdict":"rejected",`), reviewBytes(v)[1:]...)
			case "trailing":
				raw = append(reviewBytes(v), []byte(" {}")...)
			case "fenced":
				raw = []byte("```json\n" + string(reviewBytes(v)) + "\n```")
			}
			if raw == nil {
				raw = reviewBytes(v)
			}
			if _, err := CheckAgentReview(raw, nil, now); err == nil {
				t.Fatal("accepted invalid review")
			}
		})
	}
}

func reviewPackage(t *testing.T, v map[string]any, now time.Time) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/project.json")
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	p["candidates"] = []any{v["candidate"]}
	project := p["project"].(map[string]any)
	for _, k := range []string{"provider", "model", "reviewed_at", "reasoning", "deduplication", "catalog_revision", "task_key"} {
		project[k] = v[k]
	}
	check, err := CheckAgentReview(reviewBytes(v), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	ev, ids := []any{}, []any{}
	for _, d := range check.Evidence {
		ev = append(ev, map[string]any{"evidence_id": d.ID, "valid": d.Valid, "payment": d.Payment, "priced": d.Priced, "reason": d.Reason})
		if d.Valid {
			ids = append(ids, d.ID)
		}
	}
	project["evidence"] = ev
	project["approval"].(map[string]any)["reviewed_evidence"] = ids
	return p
}
func TestAgentReviewPackageBinding(t *testing.T) {
	v, now := agentReviewFixture()
	p := reviewPackage(t, v, now)
	result, err := CheckAgentReview(reviewBytes(v), reviewBytes(p), now)
	if err != nil || result.ProjectHash != fingerprint(string(reviewBytes(p))) {
		t.Fatalf("%+v %v", result, err)
	}
	for _, name := range []string{"candidate", "alternative", "metadata", "flags", "references", "not_approved"} {
		t.Run(name, func(t *testing.T) {
			v, now := agentReviewFixture()
			p := reviewPackage(t, v, now)
			// Clone the package so candidate modifications do not mutate the review.
			_ = json.Unmarshal(reviewBytes(p), &p)
			project := p["project"].(map[string]any)
			switch name {
			case "candidate":
				p["candidates"].([]any)[0].(map[string]any)["job_to_be_done"] = "另一个核心任务"
			case "alternative":
				p["candidates"].([]any)[0].(map[string]any)["unknowns"] = []any{"添加新事实后需要复核"}
			case "metadata":
				project["model"] = "deepseek-other"
			case "flags":
				project["evidence"].([]any)[0].(map[string]any)["priced"] = false
			case "references":
				project["approval"].(map[string]any)["reviewed_evidence"] = []any{"missing"}
			case "not_approved":
				v["verdict"] = "needs_evidence"
			}
			if _, err := CheckAgentReview(reviewBytes(v), reviewBytes(p), now); err == nil {
				t.Fatal("changed package accepted")
			}
		})
	}
}

func TestAgentReviewSeparatedFollowup(t *testing.T) {
	for _, name := range []string{"forward", "reverse", "other_user"} {
		t.Run(name, func(t *testing.T) {
			v, now := agentReviewFixture()
			v["verdict"] = "needs_evidence"
			src := reviewSource(v, 0)
			a := reviewAssessment(v, 0)
			src["quote"] = "It still loses my table columns."
			src["signal"] = "pain_only"
			a["unmet_quote"] = src["quote"]
			a["payment_quote"] = ""
			a["spend_quote"] = ""
			follow := map[string]any{}
			for k, x := range src {
				follow[k] = x
			}
			follow["evidence_id"] = "a-followup"
			follow["url"] = "https://forum.example.org/post/a#reply"
			follow["quote"] = "I bought the converter."
			follow["signal"] = "currently_paying"
			review := map[string]any{}
			for k, x := range a {
				review[k] = x
			}
			review["evidence_id"] = "a-followup"
			review["unmet_quote"] = ""
			review["payment_quote"] = follow["quote"]
			review["spend_quote"] = follow["quote"]
			c := v["candidate"].(map[string]any)
			c["evidence"] = append(c["evidence"].([]any), follow)
			v["evidence"] = append(v["evidence"].([]any), review)
			if name == "reverse" {
				evs := c["evidence"].([]any)
				evs[0], evs[3] = evs[3], evs[0]
			}
			pay, priced := 2, 1
			if name == "other_user" {
				follow["author_ref"] = "another-person"
				review["user_key"] = "another-user"
				pay, priced = 1, 0
			}
			got, err := CheckAgentReview(reviewBytes(v), nil, now)
			if err != nil || got.Counts.Users != 3 || got.Counts.PaymentUsers != pay || got.Counts.PriceOrSpendUsers != priced {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}
func TestAgentReviewRejectsEscapedCredentials(t *testing.T) {
	v, now := agentReviewFixture()
	reviewAssessment(v, 0)["reason"] = "sk-fixtureonly000000"
	raw := bytes.ReplaceAll(reviewBytes(v), []byte("sk-"), []byte(`sk\u002d`))
	if _, err := CheckAgentReview(raw, nil, now); err == nil {
		t.Fatal("escaped credential accepted")
	}
}
