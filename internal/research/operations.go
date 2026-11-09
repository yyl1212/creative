package research

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/yyl1212/creative/internal/jsonutil"
	"github.com/yyl1212/creative/internal/model"
)

type SavedModelConfig struct {
	Config    model.Config `json:"config"`
	Version   int          `json:"version"`
	UpdatedAt string       `json:"updated_at"`
}

type Reconciliation struct {
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	BillingItem string `json:"billing_item"`
	Note        string `json:"note"`
}

type DailyReport struct {
	Date         string       `json:"date"`
	Received     int          `json:"received"`
	Verified     int          `json:"verified"`
	Approved     int          `json:"approved"`
	Launched     int          `json:"launched"`
	Failed       int          `json:"failed"`
	UnknownCosts int          `json:"unknown_costs"`
	Costs        []UsageTotal `json:"costs"`
}

var modelNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,199}$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

func operationsInput(message string) error {
	return fail(422, "invalid_operations_input", "/", message)
}

// ParseModelConfig 只接受 model.Config 的非敏感字段；不会读取密钥或调用模型。
func ParseModelConfig(raw []byte) (model.Config, error) {
	var cfg model.Config
	if len(raw) > 16<<10 || jsonutil.Check(raw) != nil || secretPattern.Match(raw) {
		return cfg, operationsInput("模型配置无效、过大或疑似包含密钥")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return cfg, operationsInput("模型配置须为 JSON 对象")
	}
	for key := range fields {
		if key != "primary" && key != "fallback" && key != "providers" {
			return cfg, operationsInput("模型配置包含未知字段")
		}
	}
	var providers map[string]map[string]json.RawMessage
	if err := json.Unmarshal(fields["providers"], &providers); err != nil {
		return cfg, operationsInput("providers 无效")
	}
	for _, provider := range providers {
		for key, value := range provider {
			if key != "enabled" && key != "validated" && key != "model" || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return cfg, operationsInput("厂商配置只允许 enabled、validated、model")
			}
		}
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, operationsInput("模型配置字段类型错误")
	}
	return cfg, validModelConfig(cfg)
}

func validModelConfig(cfg model.Config) error {
	if cfg.Primary != "openai" && cfg.Primary != "deepseek" || cfg.Fallback != "" && (cfg.Fallback == cfg.Primary || cfg.Fallback != "openai" && cfg.Fallback != "deepseek") {
		return operationsInput("仅支持不同的 OpenAI / DeepSeek 主备厂商")
	}
	if _, ok := cfg.Providers[cfg.Primary]; !ok {
		return operationsInput("缺少主厂商配置")
	}
	if cfg.Fallback != "" {
		if _, ok := cfg.Providers[cfg.Fallback]; !ok {
			return operationsInput("缺少备用厂商配置")
		}
	}
	for name, provider := range cfg.Providers {
		if name != "openai" && name != "deepseek" {
			return operationsInput("仅支持 OpenAI / DeepSeek")
		}
		if !safeText(provider.Model, false) || provider.Model != "" && !modelNamePattern.MatchString(provider.Model) || provider.Enabled && (!provider.Validated || provider.Model == "") {
			return operationsInput("启用厂商前请确认模型标识及验证状态；不得填密钥")
		}
	}
	return nil
}

func (s *Service) ModelConfig() (*SavedModelConfig, error) {
	var saved SavedModelConfig
	var raw string
	var stamp int64
	err := s.db.QueryRow("SELECT value,version,updated_at FROM app_settings WHERE name='model_config'").Scan(&raw, &saved.Version, &stamp)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	saved.Config, err = ParseModelConfig([]byte(raw))
	if err != nil {
		return nil, err
	}
	saved.UpdatedAt = time.Unix(stamp, 0).UTC().Format(time.RFC3339)
	return &saved, nil
}

// SaveModelConfig 保存值在重启后由调用方加载；首次保存使用 version=0。
func (s *Service) SaveModelConfig(version int, cfg model.Config, actor, source string) error {
	if version < 0 || !safeText(actor, true) || !safeText(source, true) {
		return operationsInput("配置版本与操作人无效")
	}
	if err := validModelConfig(cfg); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var result sql.Result
	if version == 0 {
		result, err = tx.Exec("INSERT INTO app_settings(name,value) VALUES ('model_config',?) ON CONFLICT(name) DO NOTHING", jsonText(cfg))
	} else {
		result, err = tx.Exec("UPDATE app_settings SET value=?,version=version+1,updated_at=unixepoch() WHERE name='model_config' AND version=?", jsonText(cfg), version)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fail(409, "model_config_version_conflict", "/version", "模型配置已变化，请刷新后重试")
	}
	if _, err = tx.Exec("INSERT INTO audit_events(action,account,source,object_id,details) VALUES ('model_config_saved',?,?,'model_config',?)", actor, source, jsonText(map[string]int{"version": version + 1})); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) ReconcileUsage(id, version int, r Reconciliation, actor, source string) error {
	r.BillingItem = strings.TrimSpace(r.BillingItem)
	if _, err := amount(r.Amount); err != nil {
		return operationsInput("实际金额须为非负十进制金额")
	}
	if id <= 0 || version <= 0 || !currencyPattern.MatchString(r.Currency) || !safeText(r.BillingItem, true) || len(r.BillingItem) > 200 || !safeText(r.Note, true) || !safeText(actor, true) || !safeText(source, true) {
		return operationsInput("请补全版本、实际币种、账单行、核账备注和操作人")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var provider string
	// 首条语句取得写锁并检查版本，避免 SQLite 读快照升级写入时的并发冲突。
	err = tx.QueryRow("UPDATE usage_records SET cost_status='verified',version=version+1 WHERE id=? AND version=? AND cost_status!='verified' RETURNING provider", id, version).Scan(&provider)
	if err == sql.ErrNoRows {
		var exists int
		if err = tx.QueryRow("SELECT count(*) FROM usage_records WHERE id=?", id).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fail(404, "usage_not_found", "/id", "费用记录不存在")
		}
		return fail(409, "usage_version_conflict", "/version", "费用记录已变化或已核账，请刷新")
	}
	if err != nil {
		return err
	}
	if !safeText(provider, true) {
		return operationsInput("费用厂商无效")
	}
	result, err := tx.Exec("INSERT INTO usage_reconciliations(usage_record,provider,amount,currency,billing_item,note,reviewed_by) VALUES (?,?,?,?,?,?,?) ON CONFLICT DO NOTHING", id, provider, r.Amount, r.Currency, r.BillingItem, r.Note, actor)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fail(409, "billing_item_conflict", "/billing_item", "该费用或同厂商账单行已经核账")
	}
	if err = lifecycleAudit(tx, "usage_reconciled", id, version+1, actor, source); err != nil {
		return err
	}
	return tx.Commit()
}

// DailyReport 采用上海自然日；费用按消费发生日汇总，后续核账回填原消费日。
func (s *Service) DailyReport(date string) (DailyReport, error) {
	report := DailyReport{Date: date, Costs: []UsageTotal{}}
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return report, err
	}
	if date == "" {
		date = time.Now().In(zone).Format("2006-01-02")
		report.Date = date
	}
	day, err := time.ParseInLocation("2006-01-02", date, zone)
	if err != nil || day.Format("2006-01-02") != date {
		return report, operationsInput("日期须为有效的 YYYY-MM-DD 上海日期")
	}
	start, end := day.Unix(), day.AddDate(0, 0, 1).Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	for _, count := range []struct {
		query  string
		target *int
	}{
		{"SELECT count(*) FROM research_candidates c JOIN research_batches b ON b.id=c.batch_row WHERE b.created_at>=? AND b.created_at<?", &report.Received},
		{"SELECT count(*) FROM need_verifications WHERE verified_at>=? AND verified_at<?", &report.Verified},
		{"SELECT count(*) FROM audit_events WHERE action='need_approved' AND created_at>=? AND created_at<?", &report.Approved},
		{"SELECT count(DISTINCT need_id) FROM release_records WHERE state='verified' AND verified_at>=? AND verified_at<?", &report.Launched},
		{"SELECT count(DISTINCT need_id) FROM (SELECT need_id,verified_at AS stamp FROM release_records WHERE state='failed' UNION ALL SELECT need_id,updated_at AS stamp FROM development_records WHERE state='failed') WHERE stamp>=? AND stamp<?", &report.Failed},
	} {
		if err = tx.QueryRow(count.query, start, end).Scan(count.target); err != nil {
			return report, err
		}
	}
	report.Costs, report.UnknownCosts, err = dailyCosts(tx, start, end)
	if err != nil {
		return report, err
	}
	return report, tx.Commit()
}

type exactCost struct {
	total        big.Rat
	scale, count int
}

func (c *exactCost) add(raw string, n *big.Rat) {
	c.total.Add(&c.total, n)
	c.count++
	if dot := strings.IndexByte(raw, '.'); dot >= 0 {
		c.scale = max(c.scale, len(raw)-dot-1)
	}
}

func dailyCosts(tx *sql.Tx, start, end int64) ([]UsageTotal, int, error) {
	rows, err := tx.Query("SELECT u.origin,u.reported_cost,u.currency,u.cost_status,r.amount,r.currency FROM usage_records u LEFT JOIN usage_reconciliations r ON r.usage_record=u.id AND r.provider=u.provider WHERE u.created_at>=? AND u.created_at<?", start, end)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	type aggregate struct {
		summary            UsageTotal
		reported, verified exactCost
	}
	groups := map[string]*aggregate{}
	group := func(origin, currency string) *aggregate {
		key := origin + "/" + currency
		if groups[key] == nil {
			groups[key] = &aggregate{summary: UsageTotal{Origin: origin, Currency: currency, Reported: "未知", Verified: "尚未核对"}}
		}
		return groups[key]
	}
	unknown := 0
	for rows.Next() {
		var origin, status string
		var reported, currency, actual, actualCurrency *string
		if err = rows.Scan(&origin, &reported, &currency, &status, &actual, &actualCurrency); err != nil {
			return nil, 0, err
		}
		reportedKnown, actualKnown := false, false
		if reported != nil && currency != nil && currencyPattern.MatchString(*currency) && (status == "reported" || status == "verified") {
			if n, err := amount(*reported); err == nil {
				group(origin, *currency).reported.add(*reported, n)
				reportedKnown = true
			}
		}
		if actual != nil && actualCurrency != nil && currencyPattern.MatchString(*actualCurrency) {
			if n, err := amount(*actual); err == nil {
				group(origin, *actualCurrency).verified.add(*actual, n)
				actualKnown = true
			}
		}
		unit := "未知币种"
		if currency != nil && currencyPattern.MatchString(*currency) {
			unit = *currency
		}
		if !reportedKnown && !actualKnown {
			unknown++
			group(origin, unit).summary.Unknown++
		}
		if status == "conflict" {
			group(origin, unit).summary.Conflicts++
		}
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	totals := make([]UsageTotal, 0, len(keys))
	for _, key := range keys {
		g := groups[key]
		if g.reported.count > 0 {
			g.summary.Reported = g.reported.total.FloatString(g.reported.scale)
		}
		if g.verified.count > 0 {
			g.summary.Verified = g.verified.total.FloatString(g.verified.scale)
		}
		totals = append(totals, g.summary)
	}
	return totals, unknown, nil
}
