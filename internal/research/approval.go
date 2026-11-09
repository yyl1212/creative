package research

import (
	"database/sql"
	"fmt"
	"math/big"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
)

type AlternativeReview struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Version   string `json:"version"`
	CheckedAt string `json:"checked_at"`
	Pricing   string `json:"pricing"`
	Result    string `json:"result"`
}
type Approval struct {
	VerificationID          int                 `json:"verification_id"`
	ReviewedEvidence        []string            `json:"reviewed_evidence"`
	ReviewNotes             string              `json:"review_notes"`
	Alternatives            []AlternativeReview `json:"alternatives"`
	AlternativeSearch       string              `json:"alternative_search"`
	FewerAlternativesReason string              `json:"fewer_alternatives_reason"`
	Monetization            string              `json:"monetization"`
	Rationale               string              `json:"rationale"`
	UnitCost                string              `json:"unit_cost"`
	Currency                string              `json:"currency"`
	Unit                    string              `json:"unit"`
	Price                   string              `json:"price"`
	PaymentFee              string              `json:"payment_fee"`
	PriceBasis              string              `json:"price_basis"`
	Sustainability          string              `json:"sustainability"`
	ResourceProtection      string              `json:"resource_protection"`
	SelfService             string              `json:"self_service"`
	Gap                     string              `json:"gap"`
	Scope                   string              `json:"scope"`
	Exclusions              string              `json:"exclusions"`
	Acceptance              string              `json:"acceptance"`
	Acquisition             string              `json:"acquisition"`
}

var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)
var secretPattern = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{12,}|cr_[a-z0-9_-]{16,}|bearer[ ]+[a-z0-9._-]{12,}|-----BEGIN[ A-Z]*PRIVATE KEY|(?:api[_-]?key|password|secret|token)[ ]*[:=][ ]*[^\s",}]+)`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

func lifecycleInput(message string) error {
	return fail(422, "invalid_development_input", "/", message)
}
func safeText(value string, required bool) bool {
	return (!required || strings.TrimSpace(value) != "") && len(value) <= 8000 && !secretPattern.MatchString(value) && strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) < 0
}
func safeDocument(value string) bool {
	if value == "" || len(value) > 500 || path.IsAbs(value) || strings.ContainsAny(value, "\\*?\x00") || path.Clean(value) != value || strings.HasPrefix(value, "../") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || part == ".git" || part == ".codex" || part == ".agents" || strings.HasPrefix(part, ".env") || part == "data" || part == "node_modules" {
			return false
		}
	}
	return safeText(value, true)
}
func safeHTTPS(value string) bool {
	u, e := url.Parse(value)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && safeText(value, true)
}
func amount(value string) (*big.Rat, error) {
	if len(value) > 40 || !decimalPattern.MatchString(value) {
		return nil, lifecycleInput("请填写明确的非负十进制金额")
	}
	v, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, lifecycleInput("金额无效")
	}
	return v, nil
}
func (a Approval) Margin() string {
	p, e := amount(a.Price)
	if e != nil {
		return "未知"
	}
	c, e := amount(a.UnitCost)
	if e != nil {
		return "未知"
	}
	f, e := amount(a.PaymentFee)
	if e != nil {
		return "未知"
	}
	scale := 0
	for _, v := range []string{a.Price, a.UnitCost, a.PaymentFee} {
		if i := strings.IndexByte(v, '.'); i >= 0 {
			scale = max(scale, len(v)-i-1)
		}
	}
	return p.Sub(p, c).Sub(p, f).FloatString(scale)
}

func validApproval(a Approval) error {
	if len(jsonText(a)) > 64<<10 || secretPattern.MatchString(jsonText(a)) {
		return lifecycleInput("材料过大或疑似包含凭据；只填写业务资料")
	}
	for _, v := range []string{a.ReviewNotes, a.Rationale, a.Sustainability, a.ResourceProtection, a.SelfService, a.Gap, a.Scope, a.Exclusions, a.Acceptance, a.Acquisition, a.Unit} {
		if !safeText(v, true) {
			return lifecycleInput("请补全原文复核、范围、成本、自助流程和获客依据")
		}
	}
	if a.Monetization != "free" && a.Monetization != "paid" {
		return lifecycleInput("请选择免费或付费产品")
	}
	if !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(a.Currency) {
		return lifecycleInput("请填写三位大写币种")
	}
	p, e := amount(a.Price)
	if e != nil {
		return e
	}
	if _, e = amount(a.UnitCost); e != nil {
		return e
	}
	fee, e := amount(a.PaymentFee)
	if e != nil {
		return e
	}
	if a.Monetization == "free" && (p.Sign() != 0 || fee.Sign() != 0) {
		return lifecycleInput("免费方案按收入与支付费用为零测算")
	}
	if a.Monetization == "paid" && (p.Sign() <= 0 || !safeText(a.PriceBasis, true)) {
		return lifecycleInput("付费方案需明确价格及对应原文依据")
	}
	if len(a.Alternatives) > 20 || len(a.Alternatives) < 3 && (!safeText(a.AlternativeSearch, true) || !safeText(a.FewerAlternativesReason, true)) {
		return lifecycleInput("实测至少三项替代方案；不足时填写搜索范围和原因")
	}
	seen := map[string]bool{}
	today := time.Now().UTC().Format("2006-01-02")
	for _, v := range a.Alternatives {
		date, e := time.Parse("2006-01-02", v.CheckedAt)
		if !safeText(v.Name, true) || !safeHTTPS(v.URL) || !safeText(v.Version, true) || !safeText(v.Pricing, true) || !safeText(v.Result, true) || e != nil || date.Format("2006-01-02") > today || seen[v.URL] {
			return lifecycleInput("替代方案需独立地址、版本、日期、定价和实测结果")
		}
		seen[v.URL] = true
	}
	return nil
}

// 所有开发入口共享同一事务内资格检查；历史核验和旧批准不能恢复资格。
func currentQualified(tx *sql.Tx, id, version, verification int) (Need, error) {
	return currentEvidence(tx, id, version, verification, false)
}
func currentEvidence(tx *sql.Tx, id, version, verification int, allowCompleted bool) (Need, error) {
	n, e := scanNeed(tx.QueryRow("SELECT "+needColumns+" FROM needs WHERE id=?", id))
	if e == sql.ErrNoRows {
		return n, fail(404, "need_not_found", "/id", "需求不存在")
	}
	if e != nil {
		return n, e
	}
	if n.Version != version || n.VerificationID != verification {
		return n, fail(409, "evidence_version_conflict", "/version", "资料或核验版本已变化，请重新复核")
	}
	if allowCompleted && n.Status == "completed" {
		var valid bool
		if e = tx.QueryRow("SELECT rules_version=? AND verified_at>0 AND valid_until>unixepoch() FROM needs WHERE id=?", rulesVersion, id).Scan(&valid); e != nil {
			return n, e
		}
		if valid {
			n.Status = "qualified"
		}
	}
	if n.Status != "qualified" || n.Decision.Outcome != "qualified" {
		return n, fail(409, "evidence_not_current", "/need", "当前需求未取得有效资格")
	}
	var exists int
	if e = tx.QueryRow("SELECT count(*) FROM need_verifications WHERE id=? AND need_id=?", verification, id).Scan(&exists); e != nil {
		return n, e
	}
	if exists != 1 {
		return n, fail(409, "evidence_missing", "/need", "核验材料已不存在，请重新补证")
	}
	rows, e := tx.Query("SELECT snapshot FROM need_sources WHERE need_id=? AND verification_id=?", id, verification)
	if e != nil {
		return n, e
	}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			break
		}
		var b BoundSource
		if e = decodeStored(raw, &b); e != nil {
			break
		}
		n.Sources = append(n.Sources, b)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return n, e
	}
	sources := map[string]BoundSource{}
	for _, b := range n.Sources {
		sources[b.ID] = b
	}
	users, sites := map[string]bool{}, map[string]bool{}
	payment, priced := map[string]bool{}, 0
	now := time.Now()
	for _, ev := range n.Decision.Evidence {
		if !ev.Valid {
			continue
		}
		b, ok := sources[ev.ID]
		if !ok || b.Issue != "" || b.Block.Author == "" || b.Block.Text == "" || b.Document.URL == "" || ev.AuthorKey == "" || ev.Site == "" {
			return n, fail(409, "evidence_missing", "/need", "原文摘要不完整，请重新补证")
		}
		users[ev.AuthorKey] = true
		sites[ev.Site] = true
		if ev.Payment {
			if b.Block.PublishedAt.IsZero() || b.Block.PublishedAt.Before(now.AddDate(-1, 0, 0)) || b.Block.PublishedAt.After(now) {
				return n, fail(409, "payment_evidence_expired", "/need", "付费原文已过期或时间无效")
			}
			payment[ev.AuthorKey] = true
			if ev.Priced {
				priced++
			}
		}
	}
	if len(users) < 3 || len(sites) < 2 || len(payment) < 2 || priced < 1 {
		return n, fail(409, "evidence_below_rules", "/need", "证据未满足当前强付费门槛")
	}
	return n, nil
}
func lifecycleAudit(tx *sql.Tx, action string, id, version int, actor, source string) error {
	_, e := tx.Exec("INSERT INTO audit_events(action,account,source,object_id,details) VALUES (?,?,?,?,?)", action, actor, source, fmt.Sprint(id), jsonText(map[string]int{"version": version}))
	return e
}

func (s *Service) ApproveNeed(id, version int, a Approval, actor, source string) error {
	if e := validApproval(a); e != nil {
		return e
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// 先取得写锁，避免两个读取快照同时升级写事务时返回 SQLITE_BUSY。
	if _, e = tx.Exec("UPDATE needs SET version=version WHERE id=?", id); e != nil {
		return e
	}
	n, e := currentQualified(tx, id, version, a.VerificationID)
	if e != nil {
		return e
	}
	reviewed := map[string]bool{}
	for _, v := range a.ReviewedEvidence {
		if reviewed[v] {
			return lifecycleInput("复核来源不可重复")
		}
		reviewed[v] = true
	}
	expected := 0
	for _, ev := range n.Decision.Evidence {
		if ev.Valid {
			expected++
			if !reviewed[ev.ID] {
				return lifecycleInput("请逐条复核所有计入门槛的来源")
			}
		}
	}
	if len(reviewed) != expected {
		return lifecycleInput("复核来源与当前核验不一致")
	}
	if _, e = tx.Exec("UPDATE needs SET version=version+1 WHERE id=?", id); e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO development_records(need_id,state,need_version,verification_id,approval,approved_by) VALUES (?,'approved',?,?,?,?) ON CONFLICT(need_id) DO UPDATE SET version=development_records.version+1,state='approved',need_version=excluded.need_version,verification_id=excluded.verification_id,approval=excluded.approval,design='{}',result='{}',approved_by=excluded.approved_by,updated_at=unixepoch()`, id, version+1, a.VerificationID, jsonText(a), actor)
	if e != nil {
		return e
	}
	if e = lifecycleAudit(tx, "need_approved", id, version+1, actor, source); e != nil {
		return e
	}
	if e = recomputeTasks(tx, time.Now()); e != nil {
		return e
	}
	return tx.Commit()
}
