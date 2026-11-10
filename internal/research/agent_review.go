package research

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yyl1212/creative/internal/jsonutil"
	"github.com/yyl1212/creative/internal/source"
	"github.com/yyl1212/creative/schemas"
)

type reviewEvidence struct {
	ID                  string  `json:"evidence_id"`
	UserKey             *string `json:"user_key"`
	Relevant            bool    `json:"relevant"`
	Original            bool    `json:"original"`
	DirectUser          bool    `json:"direct_user"`
	UnmetQuote          string  `json:"unmet_quote"`
	PaymentQuote        string  `json:"payment_quote"`
	PriceQuote          string  `json:"price_quote"`
	SpendQuote          string  `json:"spend_quote"`
	RepeatQuote         string  `json:"repeat_quote,omitempty"`
	ConditionsSupported bool    `json:"conditions_supported"`
	Reason              string  `json:"reason"`
}
type agentReview struct {
	AgentReview
	RulesVersion    string           `json:"rules_version"`
	Evaluation      *Evaluation      `json:"evaluation,omitempty"`
	CatalogRevision string           `json:"catalog_revision"`
	TaskKey         string           `json:"task_key"`
	Verdict         string           `json:"verdict"`
	SelfService     bool             `json:"self_service"`
	Distinct        bool             `json:"distinct_from_existing"`
	Candidate       candidate        `json:"candidate"`
	Evidence        []reviewEvidence `json:"evidence"`
}
type ReviewCounts struct {
	Users             int `json:"independent_users"`
	Sites             int `json:"original_sites"`
	PaymentUsers      int `json:"payment_users"`
	PriceOrSpendUsers int `json:"price_or_spend_users"`
	Discussions       int `json:"independent_discussions,omitempty"`
	RecentUsers       int `json:"recent_unmet_users,omitempty"`
	RepeatUsers       int `json:"repeat_users,omitempty"`
	PaymentSignals    int `json:"payment_signal_users,omitempty"`
}
type ReviewCheck struct {
	Verdict     string             `json:"verdict"`
	Eligible    bool               `json:"eligible"`
	Counts      ReviewCounts       `json:"counts"`
	Evidence    []EvidenceDecision `json:"evidence"`
	Failures    []string           `json:"failures"`
	ReviewHash  string             `json:"review_sha256"`
	ProjectHash string             `json:"project_sha256,omitempty"`
	Warnings    []string           `json:"warnings,omitempty"`
	ValidUntil  string             `json:"qualification_valid_until,omitempty"`
}

// CheckAgentReview 只核对模型工件的结构、引用和集合算术，不联网、不访问数据库、不作语义判断。
func CheckAgentReview(raw, project []byte, now time.Time) (ReviewCheck, error) {
	out := ReviewCheck{Evidence: []EvidenceDecision{}, Failures: []string{}}
	if len(raw) > 1<<20 || jsonutil.Check(raw) != nil {
		return out, fmt.Errorf("invalid_review_json：JSON 无效、重复字段或超过 1 MiB；须由模型重新生成完整对象")
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	for _, name := range []string{"demand-batch.v1.schema.json", "agent-review.v1.schema.json"} {
		data, err := schemas.Files.ReadFile(name)
		if err != nil {
			return out, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return out, err
		}
		if err = compiler.AddResource(name, doc); err != nil {
			return out, err
		}
	}
	schema, err := compiler.Compile("agent-review.v1.schema.json")
	if err != nil {
		return out, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return out, err
	}
	if secretPattern.MatchString(jsonText(doc)) {
		return out, fmt.Errorf("invalid_review_secret：审查不能包含凭据")
	}
	if err = schema.Validate(doc); err != nil {
		return out, fmt.Errorf("invalid_review_schema：须重新生成完整对象：%w", err)
	}
	var r agentReview
	if err = json.Unmarshal(raw, &r); err != nil {
		return out, err
	}
	v2 := r.RulesVersion == opportunityRulesVersion
	if r.ReviewedAt.After(now) || r.ReviewedAt.Before(now.AddDate(0, 0, -30)) {
		return out, fmt.Errorf("invalid_review_time：审查须在最近 30 天内")
	}
	if strings.TrimSpace(r.TaskKey) == "" {
		return out, fmt.Errorf("invalid_review_task：核心任务标识为空")
	}
	if len(r.Evidence) != len(r.Candidate.Evidence) {
		return out, fmt.Errorf("invalid_review_references：逐条审查数量与来源不一致")
	}
	assessed := map[string]reviewEvidence{}
	for _, a := range r.Evidence {
		if _, exists := assessed[a.ID]; exists {
			return out, fmt.Errorf("invalid_review_references：审查 ID 重复")
		}
		assessed[a.ID] = a
	}
	users, sites, paying, priced := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	authors, quotes := map[string]string{}, map[string]string{}
	unmetUsers := map[string]bool{}
	for _, ev := range r.Candidate.Evidence {
		a, ok := assessed[ev.ID]
		if !ok {
			return out, fmt.Errorf("invalid_review_references：来源重复或缺少对应审查")
		}
		delete(assessed, ev.ID)
		observed, _ := time.Parse(time.RFC3339, ev.ObservedAt)
		published, dateErr := evidenceDate(ev, v2)
		if dateErr != nil {
			return out, dateErr
		}
		if observed.After(r.ReviewedAt) || published.After(observed) {
			return out, fmt.Errorf("invalid_review_time：原文、观察与审查时间顺序错误")
		}
		if v2 {
			if _, err := discussionKey(ev); err != nil {
				return out, err
			}
		}
		u, err := url.Parse(ev.URL)
		if err != nil || u.User != nil {
			return out, fmt.Errorf("invalid_review_source：来源地址无效")
		}
		site, err := source.SiteKey(u.Hostname())
		if err != nil {
			return out, fmt.Errorf("invalid_review_source：来源站点无效")
		}
		for _, q := range append([]string{a.UnmetQuote, a.PaymentQuote, a.PriceQuote, a.SpendQuote, a.RepeatQuote}, ev.Conditions...) {
			if q != "" && !contains(ev.Quote, q) {
				return out, fmt.Errorf("invalid_review_quote：审查引用或条件不在该证据原文中")
			}
		}
		user := ""
		if a.UserKey != nil {
			user = normalized(*a.UserKey)
		}
		author := ""
		if ev.AuthorRef != nil {
			author = authorRefKey(*ev.AuthorRef)
		}
		// 同站已知作者不能被模型拆成多人；跨站别名由模型归到同一个 user_key。
		if author != "" && user != "" {
			key := site + ":" + author
			if old := authors[key]; old != "" && old != user {
				return out, fmt.Errorf("invalid_review_identity：同一作者被拆分为多个用户")
			}
			authors[key] = user
		}
		valid := a.Relevant && a.Original && a.DirectUser && user != "" && author != "" && (v2 || !published.IsZero())
		d := EvidenceDecision{ID: ev.ID, Valid: valid, Reason: a.Reason, AuthorKey: user, Site: site, Fingerprint: fingerprint(source.Normalize(ev.Quote))}
		if valid {
			if a.UnmetQuote != "" {
				unmetUsers[user] = true
			}
			d.Payment = (ev.Signal == "explicit_wtp" || ev.Signal == "currently_paying") && a.PaymentQuote != "" && a.ConditionsSupported && !published.IsZero() && now.Before(published.AddDate(1, 0, 0))
			priceSupported := supportedPrice(ev.Price, a.PriceQuote)
			if v2 {
				priceSupported = opportunityPrice(ev.Price, a.PriceQuote)
			}
			d.Priced = d.Payment && (a.PriceQuote != "" && priceSupported || ev.Signal == "currently_paying" && a.SpendQuote != "")
		}
		out.Evidence = append(out.Evidence, d)
	}
	// 痛点与付款可在同一人的不同回复中，先校验完整证据组再计数，避免依赖顺序。
	for i := range out.Evidence {
		d := &out.Evidence[i]
		d.Valid = d.Valid && unmetUsers[d.AuthorKey]
		d.Payment = d.Valid && d.Payment
		d.Priced = d.Payment && d.Priced
		if !d.Valid {
			continue
		}
		if old := quotes[d.Fingerprint]; old != "" && old != d.AuthorKey {
			return out, fmt.Errorf("invalid_review_identity：重复原文不能当作多个独立用户")
		}
		quotes[d.Fingerprint] = d.AuthorKey
		users[d.AuthorKey] = true
		sites[d.Site] = true
		if d.Payment {
			paying[d.AuthorKey] = true
		}
		if d.Priced {
			priced[d.AuthorKey] = true
		}
	}
	out.Counts = ReviewCounts{Users: len(users), Sites: len(sites), PaymentUsers: len(paying), PriceOrSpendUsers: len(priced)}
	if !r.SelfService {
		out.Failures = append(out.Failures, "not_self_service")
	}
	if !r.Distinct {
		out.Failures = append(out.Failures, "task_identity_unverified")
	}
	if len(users) < 3 {
		out.Failures = append(out.Failures, "independent_users_below_3")
	}
	if !v2 && len(sites) < 2 {
		out.Failures = append(out.Failures, "original_sites_below_2")
	}
	if !v2 && len(paying) < 2 {
		out.Failures = append(out.Failures, "payment_users_below_2")
	}
	if !v2 && len(priced) < 1 {
		out.Failures = append(out.Failures, "price_or_spend_missing")
	}
	if v2 {
		if err := checkOpportunity(r, &out, now); err != nil {
			return out, err
		}
	}
	out.Eligible = len(out.Failures) == 0
	out.Verdict = r.Verdict
	out.ReviewHash = fingerprint(string(raw))
	if r.Verdict == "approved" && !out.Eligible {
		return out, fmt.Errorf("invalid_review_approval：模型通过但引用计数不支持：%s；计数=%s", strings.Join(out.Failures, ","), jsonText(out.Counts))
	}
	if project != nil {
		if r.Verdict != "approved" {
			return out, fmt.Errorf("invalid_review_approval：模型未通过，不能构造立项包")
		}
		base, err := compiler.Compile("demand-batch.v1.schema.json")
		if err != nil {
			return out, err
		}
		b, p, err := (&Service{schema: base}).parseProject(project)
		if err != nil {
			return out, err
		}
		var reviewDoc, projectDoc map[string]any
		_ = json.Unmarshal(raw, &reviewDoc)
		_ = json.Unmarshal(project, &projectDoc)
		if b.RulesVersion != r.RulesVersion || !reflect.DeepEqual(reviewDoc["candidate"], projectDoc["candidates"].([]any)[0]) || p.AgentReview != r.AgentReview || p.CatalogRevision != r.CatalogRevision || p.TaskKey != r.TaskKey {
			return out, fmt.Errorf("invalid_review_package：候选、模型记录、目录或任务与审查不一致")
		}
		if v2 && (!reflect.DeepEqual(p.Evaluation, r.Evaluation) || p.ValidUntil == nil || p.ValidUntil.UTC().Format(time.RFC3339) != out.ValidUntil || p.Approval.Monetization != r.Candidate.Monetization) {
			return out, fmt.Errorf("invalid_review_package：评估、资格截止或变现方式与审查不一致")
		}
		decision, _, err := projectDecision(b.Candidates[0], p, b.RulesVersion, now)
		if err != nil {
			return out, err
		}
		byID := map[string]EvidenceDecision{}
		for _, d := range decision.Evidence {
			byID[d.ID] = d
		}
		for _, d := range out.Evidence {
			p := byID[d.ID]
			if d.Valid != p.Valid || d.Payment != p.Payment || d.Priced != p.Priced || d.Reason != p.Reason {
				return out, fmt.Errorf("invalid_review_package：逐条标记与校验结果不一致")
			}
		}
		out.ProjectHash = fingerprint(string(project))
	}
	return out, nil
}
