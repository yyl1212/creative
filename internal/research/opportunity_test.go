package research

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func evaluationFixture() map[string]any {
	return map[string]any{
		"depth":           map[string]any{"context_complete": true, "counterevidence_checked": true, "alternatives_checked": true, "notes": "已核对原帖与作者后续，未解决者与满意用户分开"},
		"differentiation": map[string]any{"summary": "现有工具在所述多列表格中持续丢列", "gap_evidence_ids": []any{"a"}, "fewer_alternatives_reason": "自制测试资料只有一个替代工具，记录搜索范围"},
		"feasibility":     map[string]any{"status": "plausible", "basis": "标准CSV读写能力支持该范围，尚未实测复杂样本", "risks": []any{"复杂表格可能失败"}, "validation_plan": "用自制多列样本核对每个单元格，失败则收缩范围"},
		"acquisition":     map[string]any{"channel": "表格导出教程", "evidence_ids": []any{"a", "c"}, "rationale": "两个讨论中用户主动寻找导出方案，流量规模未知"},
		"economics":       map[string]any{"cost_basis": "优先浏览器计算，托管开销待测", "revenue_hypothesis": "测试按次收费或广告展示，收入尚未验证", "validation_plan": "核对真实使用成本和自助转化，不可持续则停止扩展"},
	}
}

func opportunityFixture() (map[string]any, time.Time) {
	v, now := agentReviewFixture()
	v["rules_version"] = "opportunity.v2"
	v["evaluation"] = evaluationFixture()
	c := v["candidate"].(map[string]any)
	c["monetization_proposal"] = "paid"
	c["alternatives"] = []any{map[string]any{"name": "自制替代工具", "url": "https://tools.example.com/converter", "checked_at": "2026-11-10T10:00:00Z", "method": "research_only", "limitations": "多列表格仍需人工整理", "basis": "自制官方说明样例，未实际操作"}}
	for i := range 3 {
		src, a := reviewSource(v, i), reviewAssessment(v, i)
		src["url"] = "https://forum.example.org/comment/" + string(rune('a'+i))
		src["discussion_url"] = "https://forum.example.org/threads/one"
		if i == 2 {
			src["discussion_url"] = "https://forum.example.org/threads/two"
		}
		src["published_at"] = nil
		src["published_date"] = "2026-10-10"
		a["repeat_quote"] = ""
	}
	src, a := reviewSource(v, 0), reviewAssessment(v, 0)
	src["quote"], src["signal"] = "My columns disappear every week.", "pain_only"
	a["unmet_quote"], a["repeat_quote"] = src["quote"], "every week"
	a["payment_quote"], a["spend_quote"] = "", ""
	reviewAssessment(v, 2)["repeat_quote"] = "after every export"
	return v, now
}

func extraCount(got ReviewCheck, key string) int {
	var m map[string]int
	_ = json.Unmarshal(reviewBytes(got.Counts), &m)
	return m[key]
}

func TestOpportunityPaidAllowsScopedIntentBeforePrototype(t *testing.T) {
	v, now := opportunityFixture()
	got, err := CheckAgentReview(reviewBytes(v), nil, now)
	if err != nil || !got.Eligible || got.Counts.Users != 3 || got.Counts.Sites != 1 || got.Counts.PaymentUsers != 1 || got.Counts.PriceOrSpendUsers != 0 || extraCount(got, "independent_discussions") != 2 || extraCount(got, "recent_unmet_users") != 3 {
		t.Fatalf("plausible scoped paid opportunity: %+v %v", got, err)
	}
}

func TestOpportunityAdvertisingNeedsRepeatDemandWithoutPayment(t *testing.T) {
	v, now := opportunityFixture()
	v["candidate"].(map[string]any)["monetization_proposal"] = "free"
	reviewSource(v, 1)["signal"] = "pain_only"
	reviewAssessment(v, 1)["payment_quote"] = ""
	got, err := CheckAgentReview(reviewBytes(v), nil, now)
	if err != nil || !got.Eligible || got.Counts.PaymentUsers != 0 || extraCount(got, "repeat_users") != 2 {
		t.Fatalf("advertising opportunity: %+v %v", got, err)
	}
	reviewAssessment(v, 2)["repeat_quote"] = ""
	v["verdict"] = "needs_evidence"
	got, err = CheckAgentReview(reviewBytes(v), nil, now)
	if err != nil || got.Eligible || !strings.Contains(strings.Join(got.Failures, ","), "repeat_users_below_2") {
		t.Fatalf("one repeat user must not pass: %+v %v", got, err)
	}
}

func TestOpportunityBlockingGaps(t *testing.T) {
	cases := []struct {
		name    string
		change  func(map[string]any)
		failure string
	}{
		{"same_discussion", func(v map[string]any) {
			reviewSource(v, 2)["discussion_url"] = "https://forum.example.org/threads/one#reply-c"
		}, "independent_discussions_below_2"},
		{"same_user", func(v map[string]any) { reviewAssessment(v, 2)["user_key"] = "user-a" }, "independent_users_below_3"},
		{"payment_out_of_scope", func(v map[string]any) { reviewAssessment(v, 1)["conditions_supported"] = false }, "payment_users_below_1"},
		{"unknown_payment_date", func(v map[string]any) { delete(reviewSource(v, 1), "published_date") }, "payment_users_below_1"},
		{"no_recent_pain", func(v map[string]any) {
			for i := range 3 {
				reviewSource(v, i)["published_date"] = "2026-01-01"
			}
		}, "recent_unmet_users_below_1"},
		{"incomplete_context", func(v map[string]any) {
			v["evaluation"].(map[string]any)["depth"].(map[string]any)["context_complete"] = false
		}, "research_depth_incomplete"},
		{"no_counterevidence", func(v map[string]any) {
			v["evaluation"].(map[string]any)["depth"].(map[string]any)["counterevidence_checked"] = false
		}, "research_depth_incomplete"},
		{"blocked_technology", func(v map[string]any) {
			v["evaluation"].(map[string]any)["feasibility"].(map[string]any)["status"] = "blocked"
		}, "feasibility_unresolved"},
		{"plausible_without_risks", func(v map[string]any) {
			v["evaluation"].(map[string]any)["feasibility"].(map[string]any)["risks"] = []any{}
		}, "feasibility_risks_missing"},
		{"no_alternative", func(v map[string]any) { v["candidate"].(map[string]any)["alternatives"] = []any{} }, "alternatives_missing"},
		{"unclear_business", func(v map[string]any) { v["candidate"].(map[string]any)["monetization_proposal"] = "undecided" }, "monetization_undecided"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, now := opportunityFixture()
			v["verdict"] = "needs_evidence"
			tc.change(v)
			got, err := CheckAgentReview(reviewBytes(v), nil, now)
			if err != nil || got.Eligible || !strings.Contains(strings.Join(got.Failures, ","), tc.failure) {
				t.Fatalf("%+v %v", got, err)
			}
			if tc.name == "payment_out_of_scope" && extraCount(got, "payment_signal_users") != 1 {
				t.Fatal("raw intent must survive an out-of-scope decision")
			}
		})
	}
}

func TestOpportunityRejectsInvalidEvidenceMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"future_date", func(v map[string]any) { reviewSource(v, 0)["published_date"] = "2026-11-12" }},
		{"both_precisions", func(v map[string]any) { reviewSource(v, 0)["published_at"] = "2026-10-10T10:00:00Z" }},
		{"invalid_date", func(v map[string]any) { reviewSource(v, 0)["published_date"] = "2026-02-30" }},
		{"cross_site_thread", func(v map[string]any) { reviewSource(v, 0)["discussion_url"] = "https://other.example.net/topic" }},
		{"private_thread", func(v map[string]any) { reviewSource(v, 0)["discussion_url"] = "http://127.0.0.1/topic" }},
		{"invented_repeat", func(v map[string]any) { reviewAssessment(v, 0)["repeat_quote"] = "every month" }},
		{"unknown_gap_reference", func(v map[string]any) {
			v["evaluation"].(map[string]any)["differentiation"].(map[string]any)["gap_evidence_ids"] = []any{"missing"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, now := opportunityFixture()
			tc.change(v)
			if _, err := CheckAgentReview(reviewBytes(v), nil, now); err == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
}

func TestOpportunityDatesAndPriceAreNotPaymentIntent(t *testing.T) {
	v, now := opportunityFixture()
	delete(reviewSource(v, 0), "published_date")
	got, err := CheckAgentReview(reviewBytes(v), nil, now)
	if err != nil || !got.Eligible || got.Counts.Users != 3 || extraCount(got, "recent_unmet_users") != 2 {
		t.Fatalf("unknown date still supports historical pain: %+v %v", got, err)
	}
	src, a := reviewSource(v, 1), reviewAssessment(v, 1)
	src["quote"] = "Fix the columns and I would pay £100."
	src["price"] = map[string]any{"amount": "100", "currency": "GBP", "unit": nil}
	a["unmet_quote"], a["payment_quote"], a["price_quote"] = src["quote"], "I would pay £100", "£100"
	got, err = CheckAgentReview(reviewBytes(v), nil, now)
	if err != nil || !got.Eligible || got.Counts.PriceOrSpendUsers != 1 {
		t.Fatalf("pounds without invented billing unit: %+v %v", got, err)
	}
	src["quote"] = "Fix the columns and I would pay $100."
	src["price"].(map[string]any)["currency"] = nil
	a["unmet_quote"], a["payment_quote"], a["price_quote"] = src["quote"], "I would pay $100", "$100"
	got, err = CheckAgentReview(reviewBytes(v), nil, now)
	if err != nil || !got.Eligible || got.Counts.PaymentUsers != 1 || got.Counts.PriceOrSpendUsers != 0 {
		t.Fatalf("unknown dollar currency retains intent: %+v %v", got, err)
	}
}

func TestOpportunitySchemaErrorIdentifiesField(t *testing.T) {
	v, now := opportunityFixture()
	delete(v["evaluation"].(map[string]any)["economics"].(map[string]any), "cost_basis")
	_, err := CheckAgentReview(reviewBytes(v), nil, now)
	if err == nil || !strings.Contains(err.Error(), "/evaluation/economics") || !strings.Contains(err.Error(), "cost_basis") {
		t.Fatalf("field-level regeneration feedback: %v", err)
	}
}

func TestOpportunityCanRecordInsufficientEvidence(t *testing.T) {
	v, now := opportunityFixture()
	v["verdict"] = "needs_evidence"
	v["candidate"].(map[string]any)["evidence"] = []any{}
	v["evidence"] = []any{}
	e := v["evaluation"].(map[string]any)
	e["differentiation"].(map[string]any)["gap_evidence_ids"] = []any{}
	e["acquisition"].(map[string]any)["evidence_ids"] = []any{}
	got, err := CheckAgentReview(reviewBytes(v), nil, now)
	if err != nil || got.Eligible {
		t.Fatalf("lack of evidence is a decision, not malformed JSON: %+v %v", got, err)
	}
}

func TestOpportunityDuplicateAlternativesDoNotProveDepth(t *testing.T) {
	v, now := opportunityFixture()
	c := v["candidate"].(map[string]any)
	a := c["alternatives"].([]any)[0]
	c["alternatives"] = []any{a, a, a}
	if _, err := CheckAgentReview(reviewBytes(v), nil, now); err == nil {
		t.Fatal("duplicate alternatives accepted")
	}
}
