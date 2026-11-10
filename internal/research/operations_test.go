package research

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yyl1212/creative/internal/model"
	"github.com/yyl1212/creative/internal/store"
	"github.com/yyl1212/creative/internal/usage"
)

func operationsFixture(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func modelFixture() model.Config {
	return model.Config{Primary: "openai", Fallback: "deepseek", Providers: map[string]model.Provider{
		"openai":   {Enabled: true, Validated: true, Model: "demo-model"},
		"deepseek": {Enabled: false, Validated: false, Model: "demo-model"},
	}}
}

func TestModelConfigVersionAndSecrets(t *testing.T) {
	s, db := operationsFixture(t)
	if v, err := s.ModelConfig(); err != nil || v != nil {
		t.Fatalf("initial config: %+v %v", v, err)
	}
	cfg := modelFixture()
	if err := s.SaveModelConfig(0, cfg, "admin", "offline"); err != nil {
		t.Fatal(err)
	}
	v, err := s.ModelConfig()
	if err != nil || v.Version != 1 || v.Config.Primary != "openai" || v.UpdatedAt == "" {
		t.Fatalf("saved config: %+v %v", v, err)
	}
	code(t, s.SaveModelConfig(0, cfg, "admin", "offline"), 409)
	cfg.Primary, cfg.Fallback = "deepseek", "openai"
	if err := s.SaveModelConfig(1, cfg, "admin", "offline"); err != nil {
		t.Fatal(err)
	}
	v, err = s.ModelConfig()
	if err != nil || v.Version != 2 || v.Config.Primary != "deepseek" {
		t.Fatalf("updated config: %+v %v", v, err)
	}
	for _, mutation := range []string{"third provider", "secret model", "missing provider", "unvalidated enabled"} {
		bad := modelFixture()
		switch mutation {
		case "third provider":
			bad.Providers["other"] = model.Provider{}
		case "secret model":
			bad.Providers["openai"] = model.Provider{Enabled: true, Validated: true, Model: "sk-sentinel0123456789"}
		case "missing provider":
			delete(bad.Providers, "openai")
		case "unvalidated enabled":
			bad.Providers["openai"] = model.Provider{Enabled: true, Model: "demo"}
		}
		if err := s.SaveModelConfig(2, bad, "admin", "offline"); err == nil {
			t.Fatalf("accepted %s", mutation)
		}
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM audit_events WHERE action='model_config_saved'").Scan(&count); err != nil || count != 2 {
		t.Fatalf("config audit count %d: %v", count, err)
	}
	for _, raw := range []string{
		`{"primary":"openai","providers":{"openai":{"enabled":false,"validated":false,"model":"","api_key":"sentinel"}}}`,
		`{"primary":"openai","providers":{},"api_key":"sentinel"}`,
		`{"primary":"openai","primary":"deepseek","providers":{}}`,
		`{"primary":"openai","Primary":"deepseek","providers":{}}`,
	} {
		if _, err := ParseModelConfig([]byte(raw)); err == nil {
			t.Fatal("unsafe JSON config accepted")
		}
	}
	raw, _ := json.Marshal(modelFixture())
	if _, err := ParseModelConfig(raw); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE app_settings SET value=? WHERE name='model_config'", `{"primary":"other","providers":{}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ModelConfig(); err == nil {
		t.Fatal("corrupted stored config accepted")
	}
}

func usageFixture(t *testing.T, db *sql.DB, provider string, cost, currency any, stamp int64) int {
	t.Helper()
	status := "unknown"
	if cost != nil {
		status = "reported"
	}
	r, err := db.Exec("INSERT INTO usage_records(origin,provider,reported_cost,currency,cost_status,created_at) VALUES ('website',?,?,?,?,?)", provider, cost, currency, status, stamp)
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return int(id)
}

func TestUsageReconciliationPreservesReportedCost(t *testing.T) {
	s, db := operationsFixture(t)
	stamp := time.Date(2026, 11, 8, 16, 0, 0, 0, time.UTC).Unix()
	id := usageFixture(t, db, "openai", "0.10", "USD", stamp)
	r := Reconciliation{Amount: "0.72", Currency: "CNY", BillingItem: "invoice-row-1", Note: "人工测试账单"}
	if err := s.ReconcileUsage(id, 1, r, "admin", "offline"); err != nil {
		t.Fatal(err)
	}
	var original, currency, status, actual, actualCurrency string
	var version int
	if err := db.QueryRow("SELECT u.reported_cost,u.currency,u.cost_status,u.version,r.amount,r.currency FROM usage_records u JOIN usage_reconciliations r ON r.usage_record=u.id WHERE u.id=?", id).Scan(&original, &currency, &status, &version, &actual, &actualCurrency); err != nil {
		t.Fatal(err)
	}
	if original != "0.10" || currency != "USD" || actual != "0.72" || actualCurrency != "CNY" || status != "verified" || version != 2 {
		t.Fatalf("reconciliation overwrote report: %s %s / %s %s (%s v%d)", original, currency, actual, actualCurrency, status, version)
	}
	code(t, s.ReconcileUsage(id, 1, r, "admin", "offline"), 409)
	code(t, s.ReconcileUsage(id, 2, r, "admin", "offline"), 409)
	other := usageFixture(t, db, "openai", nil, nil, stamp)
	code(t, s.ReconcileUsage(other, 1, r, "admin", "offline"), 409)
	if err := db.QueryRow("SELECT version FROM usage_records WHERE id=?", other).Scan(&version); err != nil || version != 1 {
		t.Fatalf("conflict changed version: %d %v", version, err)
	}
	deepseek := usageFixture(t, db, "deepseek", nil, nil, stamp)
	if err := s.ReconcileUsage(deepseek, 1, r, "admin", "offline"); err != nil {
		t.Fatalf("provider-local billing ID: %v", err)
	}
	report, err := s.DailyReport("2026-11-09")
	if err != nil {
		t.Fatal(err)
	}
	var usd, cny UsageTotal
	for _, cost := range report.Costs {
		if cost.Currency == "USD" {
			usd = cost
		}
		if cost.Currency == "CNY" {
			cny = cost
		}
	}
	if usd.Reported != "0.10" || usd.Verified != "尚未核对" || cny.Verified != "1.44" || report.UnknownCosts != 1 {
		t.Fatalf("actual/reported currencies mixed: %+v", report)
	}
	var audited int
	if err := db.QueryRow("SELECT count(*) FROM audit_events WHERE action='usage_reconciled'").Scan(&audited); err != nil || audited != 2 {
		t.Fatalf("reconciliation audit %d %v", audited, err)
	}
	for _, bad := range []Reconciliation{
		{Amount: "-1", Currency: "USD", BillingItem: "row", Note: "test"},
		{Amount: "1e3", Currency: "USD", BillingItem: "row", Note: "test"},
		{Amount: "1", Currency: "usd", BillingItem: "row", Note: "test"},
		{Amount: "1", Currency: "USD", BillingItem: "", Note: "test"},
		{Amount: "1", Currency: "USD", BillingItem: "row", Note: "sk-sentinel0123456789"},
	} {
		code(t, s.ReconcileUsage(other, 1, bad, "admin", "offline"), 422)
	}
}

func TestOperationsAuditFailureRollsBack(t *testing.T) {
	s, db := operationsFixture(t)
	id := usageFixture(t, db, "openai", nil, nil, time.Now().Unix())
	if _, err := db.Exec("CREATE TRIGGER fail_operations_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'offline audit failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveModelConfig(0, modelFixture(), "admin", "offline"); err == nil {
		t.Fatal("audit failure ignored by config save")
	}
	if err := s.ReconcileUsage(id, 1, Reconciliation{Amount: "1", Currency: "USD", BillingItem: "rollback-row", Note: "offline"}, "admin", "offline"); err == nil {
		t.Fatal("audit failure ignored by reconciliation")
	}
	var settings, reconciliations, version int
	var status string
	if err := db.QueryRow("SELECT (SELECT count(*) FROM app_settings),(SELECT count(*) FROM usage_reconciliations),version,cost_status FROM usage_records WHERE id=?", id).Scan(&settings, &reconciliations, &version, &status); err != nil {
		t.Fatal(err)
	}
	if settings != 0 || reconciliations != 0 || version != 1 || status != "unknown" {
		t.Fatalf("audit failure left partial writes: %d %d %d %s", settings, reconciliations, version, status)
	}
}

func TestUsageReconciliationConcurrentVersion(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "concurrent.db")
	first, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	a, err := New(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(second)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		id := usageFixture(t, first, "openai", nil, nil, time.Now().Unix())
		ready := make(chan struct{})
		results := make(chan error, 2)
		var done sync.WaitGroup
		for _, service := range []*Service{a, b} {
			done.Add(1)
			go func(s *Service) {
				defer done.Done()
				<-ready
				results <- s.ReconcileUsage(id, 1, Reconciliation{Amount: "1", Currency: "USD", BillingItem: fmt.Sprintf("concurrent-row-%d", id), Note: "offline"}, "admin", "offline")
			}(service)
		}
		close(ready)
		done.Wait()
		close(results)
		success := 0
		for err := range results {
			if err == nil {
				success++
			} else {
				code(t, err, 409)
			}
		}
		if success != 1 {
			t.Fatalf("concurrent reconciliations succeeded %d times", success)
		}
	}
}

func TestUsageReconciliationRejectsStaleExternalReport(t *testing.T) {
	s, db := operationsFixture(t)
	if _, err := db.Exec("INSERT INTO producers(id,name) VALUES ('test-producer','offline')"); err != nil {
		t.Fatal(err)
	}
	var record int
	if err := db.QueryRow("INSERT INTO usage_records(origin,producer_id,usage_id,provider,cost_status) VALUES ('external','test-producer','call','openai','unknown') RETURNING id").Scan(&record); err != nil {
		t.Fatal(err)
	}
	cost, currency := "0.3", "USD"
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = usage.RecordExternal(tx, "test-producer", time.Now().Unix(), []usage.External{{ID: "call", Provider: "openai", ReportedCost: &cost, Currency: &currency, Status: "reported"}}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	r := Reconciliation{Amount: "0.2", Currency: "USD", BillingItem: "row-2", Note: "offline"}
	code(t, s.ReconcileUsage(record, 1, r, "admin", "offline"), 409)
	if err := s.ReconcileUsage(record, 2, r, "admin", "offline"); err != nil {
		t.Fatal(err)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	cost = "999"
	if err = usage.RecordExternal(tx, "test-producer", time.Now().Unix(), []usage.External{{ID: "call", Provider: "openai", ReportedCost: &cost, Currency: &currency, Status: "reported"}}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var saved string
	if err := db.QueryRow("SELECT reported_cost FROM usage_records WHERE id=?", record).Scan(&saved); err != nil || saved != "0.3" {
		t.Fatalf("late external overwrite: %s %v", saved, err)
	}
}

func TestDailyReportUnknownCurrencyAndDate(t *testing.T) {
	s, db := operationsFixture(t)
	start := time.Date(2026, 11, 8, 16, 0, 0, 0, time.UTC).Unix()
	unknown := usageFixture(t, db, "openai", nil, nil, start)
	usageFixture(t, db, "openai", "0.1", "USD", start)
	usageFixture(t, db, "openai", "0.2", "USD", start+86399)
	usageFixture(t, db, "deepseek", "2.30", "CNY", start)
	usageFixture(t, db, "openai", "999", "USD", start-1)
	usageFixture(t, db, "openai", "999", "USD", start+86400)
	report, err := s.DailyReport("2026-11-09")
	if err != nil {
		t.Fatal(err)
	}
	if report.Date != "2026-11-09" || report.UnknownCosts != 1 {
		t.Fatalf("report: %+v", report)
	}
	byCurrency := map[string]UsageTotal{}
	for _, cost := range report.Costs {
		byCurrency[cost.Currency] = cost
	}
	if byCurrency["USD"].Reported != "0.3" || byCurrency["CNY"].Reported != "2.30" {
		t.Fatalf("currency mixed or date boundary wrong: %+v", report.Costs)
	}
	if byCurrency["未知币种"].Reported == "0" || byCurrency["未知币种"].Unknown != 1 || byCurrency["USD"].Verified == "0" {
		t.Fatalf("unknown treated as zero: %+v", report.Costs)
	}
	if err := s.ReconcileUsage(unknown, 1, Reconciliation{Amount: "0", Currency: "USD", BillingItem: "free-row", Note: "zero confirmed"}, "admin", "offline"); err != nil {
		t.Fatal(err)
	}
	report, err = s.DailyReport("2026-11-09")
	if err != nil || report.UnknownCosts != 0 {
		t.Fatalf("reconciled unknown: %+v %v", report, err)
	}
	for _, cost := range report.Costs {
		if cost.Currency == "USD" && cost.Verified != "0" {
			t.Fatalf("confirmed zero lost: %+v", cost)
		}
	}
	for _, date := range []string{"2026-02-30", "2026-1-1", "2026-11-09T00:00:00Z"} {
		if _, err := s.DailyReport(date); err == nil {
			t.Fatalf("accepted date %s", date)
		}
	}
}

func TestDailyReportDefaultsToShanghaiToday(t *testing.T) {
	s, _ := operationsFixture(t)
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.DailyReport("")
	if err != nil || report.Date != time.Now().In(zone).Format("2006-01-02") {
		t.Fatalf("default day: %+v %v", report, err)
	}
}

func TestDailyReportLifecycleCounts(t *testing.T) {
	s, db, p, task, _, _ := setupVerification(t)
	submitQualified(t, s, p, task.ID, "daily-fixture", "one", "two")
	start := time.Date(2026, 11, 8, 16, 0, 0, 0, time.UTC).Unix()
	if _, err := db.Exec("UPDATE research_batches SET created_at=?", start); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO needs(id,status) VALUES (51,'pending'),(52,'pending'),(53,'pending')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO need_verifications(external_id,need_id,batch_row,decision,verified_at) SELECT 'one',51,id,'{}',? FROM research_batches", start); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO audit_events(action,account,source,created_at) VALUES ('need_approved','admin','offline',?)", start); err != nil {
		t.Fatal(err)
	}
	for _, stamp := range []int64{start - 1, start, start + 86400 - 1, start + 86400} {
		if _, err := db.Exec("INSERT INTO audit_events(action,account,source,created_at) VALUES ('project_imported','agent:fixture','research_api',?)", stamp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO release_records(need_id,record_version,commit_sha,slug,origin,manifest_hash,manifest,state,verified_at) VALUES (51,1,?,'one','https://example.org','hash1','{}','verified',?),(52,1,?,'two','https://example.org','hash2','{}','failed',?)", strings.Repeat("a", 40), start, strings.Repeat("a", 40), start); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO development_records(need_id,state,need_version,verification_id,approval,approved_by,updated_at) VALUES (52,'failed',1,1,'{}','admin',?),(53,'failed',1,1,'{}','admin',?)", start, start); err != nil {
		t.Fatal(err)
	}
	report, err := s.DailyReport("2026-11-09")
	if err != nil || report.Received != 2 || report.Verified != 1 || report.Approved != 3 || report.Launched != 1 || report.Failed != 2 {
		t.Fatalf("lifecycle report: %+v %v", report, err)
	}
}
