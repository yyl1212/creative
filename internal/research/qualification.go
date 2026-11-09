package research

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yyl1212/creative/internal/model"
	"github.com/yyl1212/creative/internal/source"
	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"
)

type BoundSource struct {
	ID       string          `json:"evidence_id"`
	Document source.Document `json:"document"`
	Block    source.Block    `json:"bound_block"`
	Issue    string          `json:"issue"`
}
type EvidenceDecision struct {
	ID          string `json:"evidence_id"`
	Valid       bool   `json:"valid"`
	Payment     bool   `json:"payment"`
	Priced      bool   `json:"priced"`
	Reason      string `json:"reason"`
	AuthorKey   string `json:"author_key"`
	Site        string `json:"site"`
	Fingerprint string `json:"fingerprint"`
}
type Decision struct {
	Outcome         string             `json:"outcome"`
	Reasons         []string           `json:"reasons"`
	Evidence        []EvidenceDecision `json:"evidence"`
	TaskKey         string             `json:"task_key"`
	TaskFingerprint string             `json:"task_fingerprint"`
	ValidUntil      time.Time          `json:"valid_until"`
}

func normalized(s string) string  { return strings.ToLower(source.Normalize(norm.NFKC.String(s))) }
func fingerprint(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
func taskFingerprint(c candidate) string {
	constraints := append([]string(nil), c.Constraints...)
	for i := range constraints {
		constraints[i] = normalized(constraints[i])
	}
	sort.Strings(constraints)
	return fingerprint(normalized(c.TargetUser) + "\n" + normalized(c.Job) + "\n" + strings.Join(constraints, "\n"))
}
func contains(text, quote string) bool {
	return source.Normalize(quote) != "" && strings.Contains(source.Normalize(text), source.Normalize(quote))
}

var priceNumbers = regexp.MustCompile(`[0-9]+(?:,[0-9]{3})*(?:\.[0-9]+)?`)

func supportedPrice(p *Price, quote string) bool {
	if p == nil || p.Amount == nil || p.Currency == nil || p.Unit == nil || *p.Amount == "" || *p.Currency == "" || *p.Unit == "" {
		return false
	}
	amount, ok := new(big.Rat).SetString(*p.Amount)
	if !ok || amount.Sign() <= 0 {
		return false
	}
	text := norm.NFKC.String(quote)
	numberOK := false
	for _, literal := range priceNumbers.FindAllString(text, -1) {
		n, ok := new(big.Rat).SetString(strings.ReplaceAll(literal, ",", ""))
		if ok && n.Cmp(amount) == 0 {
			numberOK = true
		}
	}
	currencyOK := regexp.MustCompile(`(^|[^A-Za-z])` + regexp.QuoteMeta(*p.Currency) + `([^A-Za-z]|$)`).MatchString(text)
	names := map[string][]string{"USD": {"美元", "美金", "US$", "US dollars"}, "CNY": {"人民币", "RMB"}, "EUR": {"欧元"}, "GBP": {"英镑"}, "JPY": {"日元"}}
	for _, name := range names[*p.Currency] {
		if contains(text, name) {
			currencyOK = true
		}
	}
	unitOK := contains(normalized(text), normalized(*p.Unit))
	periods := [][]string{{"per month", "monthly", "每月", "/month", "/月"}, {"per year", "yearly", "annually", "每年", "/year", "/年"}, {"per day", "daily", "每天", "每日", "/day", "/天"}, {"per use", "per export", "每次", "/次"}, {"once", "one-time", "一次性"}}
	for _, group := range periods {
		matches := false
		for _, unit := range group {
			if normalized(*p.Unit) == unit {
				matches = true
			}
		}
		if matches {
			for _, unit := range group {
				if contains(normalized(text), unit) {
					unitOK = true
				}
			}
		}
	}
	return numberOK && currencyOK && unitOK
}
func authorRefKey(ref string) string {
	u, e := url.Parse(ref)
	if e != nil || u.Host == "" {
		return normalized(ref)
	}
	host, e := idna.Lookup.ToASCII(strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")))
	if e != nil {
		return normalized(ref)
	}
	u.Scheme = ""
	u.Host = host
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	u.RawQuery = u.Query().Encode()
	return normalized(u.String())
}

// 先建立所有已绑定身份的连通关系，避免 A/B/C 的别名顺序影响独立人数。
func authorGroups(sources []BoundSource) map[string]string {
	parents := map[string]string{}
	var find func(string) string
	find = func(k string) string {
		p, ok := parents[k]
		if !ok {
			parents[k] = k
			return k
		}
		if p != k {
			parents[k] = find(p)
		}
		return parents[k]
	}
	for _, b := range sources {
		if b.Block.Author == "" || b.Block.AuthorRef == "" {
			continue
		}
		name, ref := find("name:"+normalized(b.Block.Author)), find("ref:"+authorRefKey(b.Block.AuthorRef))
		if name != ref {
			if name < ref {
				parents[ref] = name
			} else {
				parents[name] = ref
			}
		}
	}
	out := map[string]string{}
	for _, b := range sources {
		out[b.ID] = find("name:" + normalized(b.Block.Author))
	}
	return out
}
func Evaluate(c candidate, sources []BoundSource, a model.Assessment, now time.Time) Decision {
	d := Decision{Outcome: "needs_evidence", Reasons: []string{}, Evidence: []EvidenceDecision{}, TaskKey: normalized(a.TaskKey), TaskFingerprint: taskFingerprint(c)}
	groups := authorGroups(sources)
	bySource := map[string]BoundSource{}
	for _, b := range sources {
		bySource[b.ID] = b
	}
	byAssessment := map[string]model.Evidence{}
	for _, e := range a.Evidence {
		byAssessment[e.ID] = e
	}
	users, sites, quotes := map[string]bool{}, map[string]bool{}, map[string]bool{}
	paying, priced := 0, 0
	expiry := now.AddDate(0, 0, 30)
	for _, e := range c.Evidence {
		b, found := bySource[e.ID]
		m, assessed := byAssessment[e.ID]
		site, _ := source.SiteKey(b.Document.Site)
		r := EvidenceDecision{ID: e.ID, Reason: "metadata_unverified", Site: site, AuthorKey: groups[e.ID], Fingerprint: fingerprint(normalized(b.Block.Author) + "\n" + normalized(e.Quote))}
		date := time.Time{}
		if e.PublishedAt != nil {
			date, _ = time.Parse(time.RFC3339, *e.PublishedAt)
		}
		switch {
		case !found || b.Issue == "source_unavailable":
			r.Reason = "source_unavailable"
		case b.Issue != "":
		case !contains(b.Block.Text, e.Quote) || e.AuthorRef == nil || date.IsZero() || b.Block.PublishedAt.IsZero() || !date.Equal(b.Block.PublishedAt) || b.Document.FetchedAt.IsZero() || b.Document.FetchedAt.After(now.Add(time.Minute)) || b.Block.PublishedAt.After(now) || b.Block.Author == "" || b.Block.AuthorRef == "" || site == "":
		case normalized(*e.AuthorRef) != normalized(b.Block.Author) && normalized(*e.AuthorRef) != normalized(b.Block.AuthorRef):
		case !assessed || !m.Relevant || !m.Original || !m.DirectUser || !m.Independent:
			r.Reason = "semantic_unverified"
		case !contains(b.Block.Text, m.UnmetQuote):
			r.Reason = "unmet_need_unverified"
		case users[r.AuthorKey] || quotes[normalized(e.Quote)]:
			r.Reason = "duplicate_evidence"
		default:
			r.Valid = true
			r.Reason = "verified"
			users[r.AuthorKey] = true
			sites[r.Site] = true
			quotes[normalized(e.Quote)] = true
			conditionsOK := true
			for _, condition := range append(append([]string{}, e.Conditions...), m.Conditions...) {
				if !contains(b.Block.Text, condition) {
					conditionsOK = false
				}
			}
			if (m.Signal == "explicit_wtp" || m.Signal == "currently_paying") && contains(b.Block.Text, m.PaymentQuote) && conditionsOK && now.Before(b.Block.PublishedAt.AddDate(1, 0, 0)) {
				r.Payment = true
				paying++
				if limit := b.Block.PublishedAt.AddDate(1, 0, 0); limit.Before(expiry) {
					expiry = limit
				}
				r.Priced = contains(b.Block.Text, m.PriceQuote) && supportedPrice(e.Price, m.PriceQuote) || m.Signal == "currently_paying" && contains(b.Block.Text, m.SpendQuote)
				if r.Priced {
					priced++
				}
			}
		}
		d.Evidence = append(d.Evidence, r)
	}
	if !a.SelfService {
		d.Outcome = "rejected"
		d.Reasons = append(d.Reasons, "not_self_service")
	}
	if d.TaskKey == "" || !a.DistinctFromExisting {
		d.Reasons = append(d.Reasons, "task_identity_unverified")
	}
	if len(users) < 3 {
		d.Reasons = append(d.Reasons, "independent_users_below_3")
	}
	if len(sites) < 2 {
		d.Reasons = append(d.Reasons, "original_sites_below_2")
	}
	if paying < 2 {
		d.Reasons = append(d.Reasons, "payment_users_below_2")
	}
	if priced < 1 {
		d.Reasons = append(d.Reasons, "price_or_spend_missing")
	}
	if len(d.Reasons) == 0 {
		d.Outcome = "qualified"
		d.ValidUntil = expiry
	}
	return d
}
