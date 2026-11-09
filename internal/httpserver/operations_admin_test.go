package httpserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/yyl1212/creative/internal/model"
)

func TestOperationsAdminAccessAndSettings(t *testing.T) {
	b, cfg := setupResearch(t)
	b.app.Close()
	cfg.RuntimeModel = model.Config{Primary: "openai", Providers: map[string]model.Provider{"openai": {Model: "runtime-demo"}}}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	b.app = app
	_, machineToken, err := app.Auth.CreateProducer("operations-machine", "owner", "offline")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/reports", "/admin/settings"} {
		for _, headers := range []map[string]string{nil, {"Authorization": "Bearer " + machineToken}} {
			if w := b.request("GET", path, nil, headers); w.Code != 303 {
				t.Fatalf("private page %s: %d", path, w.Code)
			}
		}
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != 421 {
			t.Fatalf("cross-host page %s: %d", path, w.Code)
		}
	}
	b.login(t)
	page := b.request("GET", "/admin/settings", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "runtime-demo") || !strings.Contains(page.Body.String(), "当前运行") || !strings.Contains(page.Body.String(), "重启") {
		t.Fatalf("settings: %d %s", page.Code, page.Body.String())
	}
	for _, path := range []string{"/admin/settings", "/admin/usage/1/reconcile"} {
		if w := b.request("POST", path, nil, nil); w.Code != 403 {
			t.Fatalf("CSRF not enforced %s: %d", path, w.Code)
		}
	}
	values := url.Values{"gorilla.csrf.Token": {token(t, page)}, "version": {"0"}, "primary": {"deepseek"}, "fallback": {"openai"}, "openai_model": {"saved-openai"}, "deepseek_model": {"saved-deepseek"}, "deepseek_enabled": {"on"}, "deepseek_validated": {"on"}}
	if w := b.request("POST", "/admin/settings", values, map[string]string{"Origin": "https://attacker.example"}); w.Code != 403 {
		t.Fatalf("foreign origin: %d", w.Code)
	}
	if w := b.request("POST", "/admin/settings", values, nil); w.Code != 303 {
		t.Fatalf("save settings: %d %s", w.Code, w.Body.String())
	}
	if w := b.request("POST", "/admin/settings", values, nil); w.Code != 409 {
		t.Fatalf("stale settings: %d", w.Code)
	}
	page = b.request("GET", "/admin/settings", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "saved-deepseek") || !strings.Contains(page.Body.String(), "runtime-demo") {
		t.Fatalf("saved/runtime distinction: %d %s", page.Code, page.Body.String())
	}
	if app.runtimeModel.Primary != "openai" {
		t.Fatal("save changed active runtime config")
	}
	values.Set("version", "1")
	values.Set("primary", "other")
	if w := b.request("POST", "/admin/settings", values, nil); w.Code != 422 {
		t.Fatalf("third provider accepted: %d", w.Code)
	}
	values.Set("primary", "openai")
	values.Set("fallback", "deepseek")
	values.Set("openai_model", "sk-sentinel0123456789")
	if w := b.request("POST", "/admin/settings", values, nil); w.Code != 422 || strings.Contains(w.Body.String(), "sk-sentinel0123456789") {
		t.Fatalf("secret accepted or reflected: %d", w.Code)
	}
	values.Set("openai_model", "demo")
	values.Set("api_key", "sentinel-private-value")
	if w := b.request("POST", "/admin/settings", values, nil); w.Code != 422 {
		t.Fatalf("extra secret field accepted: %d", w.Code)
	}
	values.Del("api_key")
	values["primary"] = []string{"openai", "deepseek"}
	if w := b.request("POST", "/admin/settings", values, nil); w.Code != 422 {
		t.Fatalf("ambiguous form accepted: %d", w.Code)
	}
	unauth := &browser{app: app, cookies: map[string]*http.Cookie{}}
	csrf := token(t, unauth.request("GET", "/admin/login", nil, nil))
	values = url.Values{"gorilla.csrf.Token": {csrf}, "version": {"1"}}
	for _, path := range []string{"/admin/settings", "/admin/usage/1/reconcile"} {
		if w := unauth.request("POST", path, values, map[string]string{"Authorization": "Bearer " + machineToken}); w.Code != 303 {
			t.Fatalf("machine POST has admin access: %s %d", path, w.Code)
		}
	}
}

func TestOperationsAdminReportAndReconciliation(t *testing.T) {
	b, cfg := setupResearch(t)
	b.login(t)
	if w := b.request("GET", "/admin/settings", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `name="version" value="0"`) {
		t.Fatalf("initial disabled settings unavailable: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/admin/reports", "/admin/reports?date=2026-11-09"} {
		w := b.request("GET", path, nil, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "经营日报") || !strings.Contains(w.Body.String(), "没有用量记录") || !strings.Contains(w.Body.String(), "尚无备份结果") {
			t.Fatalf("report: %d %s", w.Code, w.Body.String())
		}
	}
	if w := b.request("GET", "/admin/reports?date=2026-02-30", nil, nil); w.Code != 422 {
		t.Fatalf("invalid report date: %d", w.Code)
	}
	if _, err := cfg.DB.Exec("INSERT INTO app_settings(name,value) VALUES ('backup_status',?)", `{"Time":"2026-11-09T00:00:00Z","Success":false,"File":""}`); err != nil {
		t.Fatal(err)
	}
	if w := b.request("GET", "/admin/reports", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "备份失败") {
		t.Fatalf("backup failure hidden: %d %s", w.Code, w.Body.String())
	}
	var id int
	if err := cfg.DB.QueryRow("INSERT INTO usage_records(origin,provider,reported_cost,currency,cost_status) VALUES ('website','openai','0.1','USD','reported') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	page := b.request("GET", "/admin/usage", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "登记核账") {
		t.Fatalf("reconciliation form absent: %d %s", page.Code, page.Body.String())
	}
	values := url.Values{"gorilla.csrf.Token": {token(t, page)}, "version": {"1"}, "amount": {"0.72"}, "currency": {"CNY"}, "billing_item": {"invoice-row"}, "note": {"离线人工账单"}}
	path := fmt.Sprintf("/admin/usage/%d/reconcile", id)
	if w := b.request("POST", path, values, nil); w.Code != 303 {
		t.Fatalf("reconcile: %d %s", w.Code, w.Body.String())
	}
	if w := b.request("POST", path, values, nil); w.Code != 409 {
		t.Fatalf("stale reconciliation: %d", w.Code)
	}
	page = b.request("GET", "/admin/usage", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "0.1 USD") || !strings.Contains(page.Body.String(), "0.72 CNY") || !strings.Contains(page.Body.String(), "invoice-row") || strings.Contains(page.Body.String(), path+`"`) {
		t.Fatalf("reported/actual display: %d %s", page.Code, page.Body.String())
	}
	if w := b.request("POST", "/admin/usage/no/reconcile", values, nil); w.Code != 422 {
		t.Fatalf("invalid usage ID: %d", w.Code)
	}
	if _, err := cfg.DB.Exec("INSERT INTO usage_records(origin,provider,reported_cost,currency,cost_status) VALUES ('website','openai','2','USD','verified')"); err != nil {
		t.Fatal(err)
	}
	page = b.request("GET", "/admin/usage", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "历史记录缺少实际核账依据") {
		t.Fatalf("unbacked verified state displayed as confirmed: %d %s", page.Code, page.Body.String())
	}
}
