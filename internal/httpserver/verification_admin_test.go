package httpserver

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestVerificationAdminViewsAndRetiredRetry(t *testing.T) {
	b, cfg := setupResearch(t)
	db := cfg.DB
	for _, path := range []string{"/admin/needs", "/admin/needs/1", "/admin/usage"} {
		if w := b.request("GET", path, nil, nil); w.Code != 303 {
			t.Fatal("private page", path, w.Code)
		}
	}
	b.login(t)
	for _, path := range []string{"/admin/needs", "/admin/usage"} {
		w := b.request("GET", path, nil, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "暂无") {
			t.Fatal("empty", path, w.Code)
		}
	}
	p, _, e := b.app.Auth.CreateProducer("fixture", "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	task, e := b.app.research.CreateTask(p.ID, []string{"export"}, []string{"en"}, 3, "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.app.research.Receive(p, httpFixture(t, task.ID)); e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec("UPDATE research_batches SET status='paused_service',pause_reason='model_unavailable'")
	if e != nil {
		t.Fatal(e)
	}
	page := b.request("GET", "/admin/batches/1", nil, nil)
	if page.Code != 200 || strings.Contains(page.Body.String(), "重试未完成项") || !strings.Contains(page.Body.String(), "旧核验流程已停止") {
		t.Fatal("legacy retry still visible", page.Code)
	}
	if w := b.request("POST", "/admin/batches/1/retry", nil, nil); w.Code != 403 {
		t.Fatal("retry CSRF", w.Code)
	}
	values := url.Values{"version": {"1"}, "gorilla.csrf.Token": {token(t, page)}}
	if w := b.request("POST", "/admin/batches/1/retry", values, nil); w.Code != 410 {
		t.Fatal("retry", w.Code, w.Body.String())
	}
	if w := b.request("POST", "/admin/batches/1/retry", values, nil); w.Code != 410 {
		t.Fatal("stale retry", w.Code)
	}
	var batchState string
	var batchVersion int
	if err := db.QueryRow("SELECT status,version FROM research_batches WHERE id=1").Scan(&batchState, &batchVersion); err != nil || batchState != "paused_service" || batchVersion != 1 {
		t.Fatal("retired retry changed batch", batchState, batchVersion, err)
	}
	_, e = db.Exec("INSERT INTO needs(status,title,task,decision,verified_at,valid_until,rules_version) VALUES ('qualified',?,'CSV',?,unixepoch(),?,'strong-wtp.v1')", "<script>bad</script>", `{"outcome":"qualified","reasons":[],"evidence":[]}`, time.Now().Add(-time.Hour).Unix())
	if e != nil {
		t.Fatal(e)
	}
	w := b.request("GET", "/admin/needs/1", nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "资格已过期") {
		t.Fatal("need detail", w.Code, w.Body.String())
	}
	w = b.request("GET", "/admin/", nil, nil)
	if strings.Count(w.Body.String(), `data-count="0"`) != 3 {
		t.Fatal("expired qualified counted")
	}
	for i := 0; i < 2; i++ {
		_, e = db.Exec("INSERT INTO usage_records(origin,provider,model,reported_cost,currency,cost_status) VALUES ('external','deepseek','deepseek-fixture',?,'USD','reported')", fmt.Sprintf("0.%d", i+1))
		if e != nil {
			t.Fatal(e)
		}
	}
	_, _ = db.Exec("INSERT INTO usage_records(origin,provider,cost_status) VALUES ('website','openai','unknown')")
	w = b.request("GET", "/admin/usage", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "0.3") || !strings.Contains(w.Body.String(), "自报") || !strings.Contains(w.Body.String(), "未知") {
		t.Fatal("usage", w.Code, w.Body.String())
	}
	for _, path := range []string{"/admin/needs?before=bad", "/admin/usage?before=-1", "/admin/needs/no", "/admin/needs/1?verification=bad"} {
		if w = b.request("GET", path, nil, nil); w.Code != 422 {
			t.Fatal("bad id", path, w.Code)
		}
	}
}

func TestHistoricalVerificationPage(t *testing.T) {
	b, cfg := setupResearch(t)
	b.login(t)
	p, _, e := b.app.Auth.CreateProducer("history", "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	task, e := b.app.research.CreateTask(p.ID, []string{"export"}, []string{"en"}, 3, "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.app.research.Receive(p, httpFixture(t, task.ID)); e != nil {
		t.Fatal(e)
	}
	if _, e = cfg.DB.Exec("INSERT INTO needs(status,title) VALUES ('pending','history fixture')"); e != nil {
		t.Fatal(e)
	}
	if _, e = cfg.DB.Exec(`INSERT INTO need_verifications(need_id,batch_row,external_id,decision,verified_at) VALUES (1,1,'one','{"outcome":"needs_evidence","reasons":[],"evidence":[]}',unixepoch())`); e != nil {
		t.Fatal(e)
	}
	w := b.request("GET", "/admin/needs/1?verification=1", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "历史证据") || !strings.Contains(w.Body.String(), "历史结论不会恢复资格") {
		t.Fatal("history", w.Code, w.Body.String())
	}
	if w = b.request("GET", "/admin/needs/1?verification=999", nil, nil); w.Code != 404 {
		t.Fatal("missing history", w.Code)
	}
}
