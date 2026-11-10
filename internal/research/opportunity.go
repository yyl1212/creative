package research

import (
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/yyl1212/creative/internal/source"
	"golang.org/x/text/unicode/norm"
)

const opportunityRulesVersion = "opportunity.v2"
const supportedRulesSQL = "('strong-wtp.v1','opportunity.v2')"
const opportunityRulesJSON = `{"decision_mode":"agent_project","independent_users_min":3,"independent_discussions_min":2,"recent_unmet_users_min":1,"recent_unmet_days":180,"paid_payment_users_min":1,"free_repeat_users_min":2,"payment_evidence_max_age_months":12,"qualification_valid_days":30,"requirements":["外部模型负责语义与立项；网站校验契约并管理数据","优先恢复已有候选，按证据缺口深入，不设搜索次数上限","独立讨论可来自同一网站；作者、原文、后续和反证必须核查","付款条件与拟交付范围一致即可保留意愿；技术可行性另行评估","免费广告工具核查重复使用、获客与经济性，不强制付款承诺","未知日期、币种和成本保留未知；不能编造实测或收入","通过仅表示已立项待开发设计，不表示已开发或可发布"]}`

func supportedRules(version string) bool {
	return version == rulesVersion || version == opportunityRulesVersion
}

type Evaluation struct {
	Depth struct {
		ContextComplete        bool   `json:"context_complete"`
		CounterevidenceChecked bool   `json:"counterevidence_checked"`
		AlternativesChecked    bool   `json:"alternatives_checked"`
		Notes                  string `json:"notes"`
	} `json:"depth"`
	Differentiation struct {
		Summary                 string   `json:"summary"`
		GapEvidenceIDs          []string `json:"gap_evidence_ids"`
		FewerAlternativesReason string   `json:"fewer_alternatives_reason"`
	} `json:"differentiation"`
	Feasibility struct {
		Status         string   `json:"status"`
		Basis          string   `json:"basis"`
		Risks          []string `json:"risks"`
		ValidationPlan string   `json:"validation_plan"`
	} `json:"feasibility"`
	Acquisition struct {
		Channel     string   `json:"channel"`
		EvidenceIDs []string `json:"evidence_ids"`
		Rationale   string   `json:"rationale"`
	} `json:"acquisition"`
	Economics struct {
		CostBasis         string `json:"cost_basis"`
		RevenueHypothesis string `json:"revenue_hypothesis"`
		ValidationPlan    string `json:"validation_plan"`
	} `json:"economics"`
}

// 日期只用于保守的期限运算；原始日期独立保存，不把范围下界冒充发布时间。
func evidenceDate(ev Evidence, v2 bool) (time.Time, error) {
	if ev.PublishedAt != nil {
		if ev.PublishedDate != nil {
			return time.Time{}, fmt.Errorf("invalid_review_time：不能同时填写精确时间和日期")
		}
		t, err := time.Parse(time.RFC3339, *ev.PublishedAt)
		return t, err
	}
	if v2 && ev.PublishedDate != nil {
		t, err := time.Parse(time.DateOnly, *ev.PublishedDate)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid_review_time：发布日期无效")
		}
		return t.Add(-14 * time.Hour), nil
	}
	return time.Time{}, nil
}

func discussionKey(ev Evidence) (string, error) {
	if !publicURL(ev.URL) || !publicURL(ev.DiscussionURL) {
		return "", fmt.Errorf("invalid_review_discussion：来源与原始讨论须为公开 HTTP(S) 地址")
	}
	u, _ := url.Parse(ev.DiscussionURL)
	v, _ := url.Parse(ev.URL)
	a, err := source.SiteKey(u.Hostname())
	b, err2 := source.SiteKey(v.Hostname())
	if err != nil || err2 != nil || a != b {
		return "", fmt.Errorf("invalid_review_discussion：原始讨论须与证据同站")
	}
	u.Fragment = ""
	u.Scheme = "https"
	u.Host = strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = u.Query().Encode()
	return u.String(), nil
}

func opportunityPrice(p *Price, quote string) bool {
	if p == nil || p.Amount == nil || p.Currency == nil {
		return false
	}
	amount, ok := new(big.Rat).SetString(*p.Amount)
	if !ok || amount.Sign() <= 0 {
		return false
	}
	text := norm.NFKC.String(quote)
	numberOK := false
	for _, literal := range priceNumbers.FindAllString(text, -1) {
		value, ok := new(big.Rat).SetString(strings.ReplaceAll(literal, ",", ""))
		if ok && amount.Cmp(value) == 0 {
			numberOK = true
		}
	}
	currencyOK := regexp.MustCompile(`(^|[^A-Za-z])` + regexp.QuoteMeta(*p.Currency) + `([^A-Za-z]|$)`).MatchString(text)
	for _, literal := range map[string][]string{"GBP": {"£", "英镑"}, "EUR": {"€", "欧元"}, "USD": {"US$", "美元", "美金", "US dollars"}, "CNY": {"人民币", "RMB"}, "JPY": {"日元"}}[*p.Currency] {
		if contains(text, literal) {
			currencyOK = true
		}
	}
	return numberOK && currencyOK
}

func evaluationFailures(c candidate, evaluation *Evaluation, valid, gaps map[string]bool) ([]string, error) {
	failures := []string{}
	if evaluation == nil {
		return nil, fmt.Errorf("invalid_evaluation：缺少分项评估")
	}
	e := evaluation
	if len(e.Differentiation.GapEvidenceIDs) == 0 {
		failures = append(failures, "differentiation_unproven")
	}
	if len(e.Acquisition.EvidenceIDs) == 0 {
		failures = append(failures, "acquisition_unproven")
	}
	if !e.Depth.ContextComplete || !e.Depth.CounterevidenceChecked || !e.Depth.AlternativesChecked {
		failures = append(failures, "research_depth_incomplete")
	}
	if e.Feasibility.Status != "plausible" && e.Feasibility.Status != "validated" {
		failures = append(failures, "feasibility_unresolved")
	}
	if e.Feasibility.Status == "plausible" && len(e.Feasibility.Risks) == 0 {
		failures = append(failures, "feasibility_risks_missing")
	}
	if len(c.Alternatives) == 0 {
		failures = append(failures, "alternatives_missing")
	}
	if len(c.Alternatives) < 3 && strings.TrimSpace(e.Differentiation.FewerAlternativesReason) == "" {
		failures = append(failures, "alternatives_coverage_missing")
	}
	for _, ref := range []struct {
		ids     []string
		allowed map[string]bool
	}{{e.Differentiation.GapEvidenceIDs, gaps}, {e.Acquisition.EvidenceIDs, valid}} {
		for _, id := range ref.ids {
			if !ref.allowed[id] {
				return nil, fmt.Errorf("invalid_evaluation_reference：差异和获客引用必须指向对应有效用户证据：%s", id)
			}
		}
	}
	return failures, nil
}

func checkOpportunity(r agentReview, out *ReviewCheck, now time.Time) error {
	assessments := map[string]reviewEvidence{}
	for _, a := range r.Evidence {
		assessments[a.ID] = a
	}
	discussions, recent, repeat, signals := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	valid, gaps := map[string]bool{}, map[string]bool{}
	var recentUntil, paymentUntil time.Time
	unknownDates := false
	for i, ev := range r.Candidate.Evidence {
		d, a := out.Evidence[i], assessments[ev.ID]
		if !d.Valid {
			continue
		}
		valid[ev.ID] = true
		gaps[ev.ID] = a.UnmetQuote != ""
		key, err := discussionKey(ev)
		if err != nil {
			return err
		}
		// 只有包含本人未满足点的讨论才增加独立讨论数，付款补充不能凑第二讨论。
		if a.UnmetQuote != "" {
			discussions[key] = true
		}
		published, _ := evidenceDate(ev, true)
		unknownDates = unknownDates || published.IsZero()
		if a.UnmetQuote != "" && !published.IsZero() && now.Before(published.AddDate(0, 0, 180)) {
			recent[d.AuthorKey] = true
			if until := published.AddDate(0, 0, 180); until.After(recentUntil) {
				recentUntil = until
			}
		}
		if a.RepeatQuote != "" {
			repeat[d.AuthorKey] = true
		}
		if (ev.Signal == "explicit_wtp" || ev.Signal == "currently_paying") && a.PaymentQuote != "" && !published.IsZero() && now.Before(published.AddDate(1, 0, 0)) {
			signals[d.AuthorKey] = true
		}
		if d.Payment {
			if until := published.AddDate(1, 0, 0); until.After(paymentUntil) {
				paymentUntil = until
			}
		}
	}
	checkedURLs := map[string]bool{}
	for _, a := range r.Candidate.Alternatives {
		checked, err := time.Parse(time.RFC3339, a.CheckedAt)
		if err != nil || checked.After(r.ReviewedAt) || !publicURL(a.URL) {
			return fmt.Errorf("invalid_alternative：替代方案地址或核查时间无效")
		}
		u, _ := url.Parse(a.URL)
		u.Fragment = ""
		key := u.String()
		if checkedURLs[key] {
			return fmt.Errorf("invalid_alternative：重复地址不能算多个替代方案")
		}
		checkedURLs[key] = true
	}
	if len(discussions) < 2 {
		out.Failures = append(out.Failures, "independent_discussions_below_2")
	}
	if len(recent) < 1 {
		out.Failures = append(out.Failures, "recent_unmet_users_below_1")
	}
	switch r.Candidate.Monetization {
	case "paid":
		if out.Counts.PaymentUsers < 1 {
			out.Failures = append(out.Failures, "payment_users_below_1")
		}
		if out.Counts.PaymentUsers == 1 {
			out.Warnings = append(out.Warnings, "single_payment_user")
		}
		if out.Counts.PriceOrSpendUsers == 0 {
			out.Warnings = append(out.Warnings, "price_or_spend_unconfirmed")
		}
	case "free":
		if len(repeat) < 2 {
			out.Failures = append(out.Failures, "repeat_users_below_2")
		}
	default:
		out.Failures = append(out.Failures, "monetization_undecided")
	}
	failures, err := evaluationFailures(r.Candidate, r.Evaluation, valid, gaps)
	if err != nil {
		return err
	}
	out.Failures = append(out.Failures, failures...)
	out.Counts.Discussions, out.Counts.RecentUsers, out.Counts.RepeatUsers, out.Counts.PaymentSignals = len(discussions), len(recent), len(repeat), len(signals)
	if out.Counts.Sites == 1 {
		out.Warnings = append(out.Warnings, "single_original_site")
	}
	if unknownDates {
		out.Warnings = append(out.Warnings, "unknown_publication_dates")
	}
	if r.Evaluation.Feasibility.Status == "plausible" {
		out.Warnings = append(out.Warnings, "technical_validation_pending")
	}
	if len(out.Failures) == 0 {
		until := r.ReviewedAt.AddDate(0, 0, 30)
		if recentUntil.Before(until) {
			until = recentUntil
		}
		if r.Candidate.Monetization == "paid" && paymentUntil.Before(until) {
			until = paymentUntil
		}
		out.ValidUntil = until.UTC().Format(time.RFC3339)
	}
	return nil
}
