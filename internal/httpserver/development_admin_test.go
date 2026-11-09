package httpserver

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/creative/internal/research"
	"github.com/yyl1212/creative/internal/source"
)

func lifecycleHTTPFixture(t *testing.T) (*browser, Config) {
	t.Helper()
	b, cfg := setupResearch(t)
	p, _, err := b.app.Auth.CreateProducer("lifecycle", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	task, err := b.app.research.CreateTask(p.ID, []string{"table"}, []string{"en"}, 3, "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.app.research.Receive(p, httpFixture(t, task.ID)); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	decision := research.Decision{Outcome: "qualified", ValidUntil: now.Add(time.Hour)}
	for i := 1; i <= 3; i++ {
		decision.Evidence = append(decision.Evidence, research.EvidenceDecision{ID: fmt.Sprint("e", i), Valid: true, Payment: i < 3, Priced: i == 1, Reason: "verified", AuthorKey: fmt.Sprint("author", i), Site: fmt.Sprint("site", i%2)})
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cfg.DB.Exec(`INSERT INTO needs(id,status,title,task,verified_at,valid_until,rules_version,decision) VALUES (1,'qualified','表格转换 <script>','自助转换表格',?,?,'strong-wtp.v1',?)`, now.Unix(), now.Add(time.Hour).Unix(), string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = cfg.DB.Exec(`INSERT INTO need_verifications(id,need_id,batch_row,external_id,decision,verified_at) VALUES (1,1,1,'current',?,?),(2,1,1,'history',?,?)`, string(raw), now.Unix(), string(raw), now.Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = cfg.DB.Exec(`UPDATE needs SET verification_id=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		bound := research.BoundSource{ID: fmt.Sprint("e", i), Document: source.Document{URL: fmt.Sprintf("https://example.org/%d", i), Hash: "fixture", FetchedAt: now}, Block: source.Block{Author: fmt.Sprint("author", i), Text: "需要自助转换，愿意每次支付 USD 5", PublishedAt: now.Add(-time.Hour)}}
		raw, err := json.Marshal(bound)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = cfg.DB.Exec(`INSERT INTO need_sources(need_id,verification_id,evidence_id,fingerprint,snapshot,fetched_at) VALUES (1,1,?,?,?,?)`, bound.ID, fmt.Sprint("fingerprint", i), string(raw), now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	return b, cfg
}

func approvalHTTPValues() url.Values {
	v := url.Values{"version": {"1"}, "verification_id": {"1"}, "reviewed_evidence": {"e1", "e2", "e3"}, "review_notes": {"已逐条核对原文"}, "monetization": {"free"}, "rationale": {"免费工具支持自助获客"}, "unit_cost": {"0.01"}, "currency": {"USD"}, "unit": {"每次转换"}, "price": {"0"}, "payment_fee": {"0"}, "sustainability": {"按广告收入为零承担资源支出"}, "resource_protection": {"文件限额和并发限制"}, "self_service": {"上传后直接下载结果"}, "gap": {"现有工具缺少字段映射"}, "scope": {"单文件字段转换"}, "exclusions": {"不做定制服务"}, "acceptance": {"样例输出与预期一致"}, "acquisition": {"发布教程和任务入口"}}
	for i := 0; i < 3; i++ {
		prefix := fmt.Sprintf("alternative_%d_", i)
		for key, value := range map[string]string{"name": fmt.Sprint("替代", i), "url": fmt.Sprintf("https://example.org/tool%d", i), "version": "2026-11", "checked_at": time.Now().UTC().Format("2006-01-02"), "pricing": "USD 5 / month", "result": "亲自操作，缺少指定字段映射"} {
			v.Set(prefix+key, value)
		}
	}
	return v
}

func designHTTPValues() url.Values {
	return url.Values{"version": {"1"}, "spec": {"docs/need/spec.md"}, "architecture": {"docs/need/architecture.md"}, "plan": {"docs/need/plan.md"}, "files": {"src/pages/en/tools/table.astro\nsrc/pages/zh/tools/table.astro"}, "environment_names": {"OPENAI_API_KEY"}, "repository": {"https://github.com/yyl1212/creative"}, "baseline": {strings.Repeat("a", 40)}, "branch": {"codex/need-1"}, "compatibility": {"新增入口，既有接口保持兼容"}, "review_notes": {"方案可实现，文件范围和测试已审查"}, "product_slug": {"table"}, "production_origin": {"https://tools.example.org"}}
}

func TestDevelopmentAdminWorkflow(t *testing.T) {
	b, _ := lifecycleHTTPFixture(t)
	for _, path := range []string{"/admin/developments", "/admin/developments/1", "/admin/developments/1/bundle/requirements"} {
		if w := b.request("GET", path, nil, nil); w.Code != 303 {
			t.Fatal("unauthenticated lifecycle page", path, w.Code)
		}
	}
	b.login(t)
	if w := b.request("GET", "/admin/developments", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "暂无") {
		t.Fatal("empty development list", w.Code, w.Body.String())
	}
	page := b.request("GET", "/admin/needs/1", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `name="reviewed_evidence"`) || strings.Contains(page.Body.String(), "<script>") {
		t.Fatal("approval form unavailable or unescaped", page.Code, page.Body.String())
	}
	values := approvalHTTPValues()
	values.Set("review_notes", strings.Repeat("x", 5000))
	values.Set("gorilla.csrf.Token", token(t, page))
	if w := b.request("POST", "/admin/needs/1/approve", values, nil); w.Code != 303 {
		t.Fatal("approval", w.Code, w.Body.String())
	}
	if w := b.request("POST", "/admin/needs/1/approve", values, nil); w.Code != 409 {
		t.Fatal("stale approval", w.Code)
	}
	if w := b.request("GET", "/admin/developments/1/bundle/development", nil, nil); w.Code != 409 {
		t.Fatal("unconfirmed development bundle", w.Code)
	}
	w := b.request("GET", "/admin/developments/1/bundle/requirements", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") || !strings.Contains(w.Header().Get("Content-Type"), "application/json") || !strings.Contains(w.Body.String(), "creative.requirement-task.v1") {
		t.Fatal("requirements download", w.Code, w.Body.String())
	}
	page = b.request("GET", "/admin/developments/1", nil, nil)
	values = designHTTPValues()
	values.Set("gorilla.csrf.Token", token(t, page))
	if w = b.request("POST", "/admin/developments/1/design", values, nil); w.Code != 303 {
		t.Fatal("design", w.Code, w.Body.String())
	}
	if w = b.request("POST", "/admin/developments/1/design", values, nil); w.Code != 409 {
		t.Fatal("stale design", w.Code)
	}
	if w = b.request("GET", "/admin/developments/1/bundle/development", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "creative.development-task.v1") {
		t.Fatal("development download", w.Code, w.Body.String())
	}
	page = b.request("GET", "/admin/developments/1", nil, nil)
	values = url.Values{"version": {"2"}, "gorilla.csrf.Token": {token(t, page)}}
	if w = b.request("POST", "/admin/developments/1/start", values, nil); w.Code != 303 {
		t.Fatal("start", w.Code, w.Body.String())
	}
	if w = b.request("POST", "/admin/developments/1/start", values, nil); w.Code != 409 {
		t.Fatal("stale start", w.Code)
	}
	page = b.request("GET", "/admin/developments/1", nil, nil)
	sha := strings.Repeat("b", 40)
	values = url.Values{"version": {"3"}, "commit": {sha}, "pull_request": {"https://github.com/yyl1212/creative/pull/5"}, "summary": {"实现表格转换"}, "review_commit": {sha}, "test_commit": {sha}, "review_report": {"docs/need/review.md"}, "test_report": {"docs/need/tests.md"}, "checks_passed": {"yes"}, "gorilla.csrf.Token": {token(t, page)}}
	if w = b.request("POST", "/admin/developments/1/result", values, nil); w.Code != 303 {
		t.Fatal("result", w.Code, w.Body.String())
	}
	d, err := b.app.research.Development(1)
	if err != nil || d.State != "review_ready" || d.Version != 4 {
		t.Fatal("development not recorded", d, err)
	}
	if w = b.request("GET", "/admin/developments/1", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "待上线核验") {
		t.Fatal("result detail", w.Code, w.Body.String())
	}
	for _, action := range []string{"launch", "release", "complete"} {
		if w = b.request("POST", "/admin/developments/1/"+action, values, nil); w.Code != 404 {
			t.Fatal("HTTP exposes publishing", action, w.Code)
		}
	}
	_, _, completed, err := b.app.research.Counts()
	if err != nil || completed != 0 {
		t.Fatal("UI recorded a production release", completed, err)
	}
}

func TestDevelopmentHTTPBoundaries(t *testing.T) {
	b, cfg := lifecycleHTTPFixture(t)
	b.login(t)
	csrf := token(t, b.request("GET", "/admin/needs/1", nil, nil))
	for _, path := range []string{"/admin/needs/1/approve", "/admin/developments/1/design", "/admin/developments/1/start", "/admin/developments/1/result"} {
		if w := b.request("POST", path, nil, nil); w.Code != 403 {
			t.Fatal("CSRF missing", path, w.Code)
		}
		if w := b.request("POST", path, url.Values{"gorilla.csrf.Token": {csrf}}, map[string]string{"Origin": "https://attacker.example"}); w.Code != 403 {
			t.Fatal("cross origin", path, w.Code)
		}
	}
	for _, path := range []string{"/admin/needs/1/approve", "/admin/developments/1/design", "/admin/developments/1/result"} {
		values := url.Values{"gorilla.csrf.Token": {csrf}, "review_notes": {strings.Repeat("x", 5000)}}
		if w := b.request("POST", path, values, nil); w.Code == 413 {
			t.Fatal("large lifecycle form uses old limit", path)
		}
		values.Set("review_notes", strings.Repeat("x", 65536))
		if w := b.request("POST", path, values, nil); w.Code != 413 {
			t.Fatal("large lifecycle form unbounded", path, w.Code)
		}
	}
	for _, path := range []string{"/admin/logout", "/admin/producers", "/admin/developments/1/start"} {
		if w := b.request("POST", path, url.Values{"gorilla.csrf.Token": {csrf}, "padding": {strings.Repeat("x", 5000)}}, nil); w.Code != 413 {
			t.Fatal("ordinary form limit changed", path, w.Code)
		}
	}
	if w := b.request("GET", "/admin/needs/1?verification=2", nil, nil); w.Code != 200 || strings.Contains(w.Body.String(), `action="/admin/needs/1/approve"`) {
		t.Fatal("historical evidence can be approved", w.Code)
	}
	values := approvalHTTPValues()
	values.Set("gorilla.csrf.Token", csrf)
	if w := b.request("POST", "/admin/needs/1/approve", values, nil); w.Code != 303 {
		t.Fatal("approval fixture", w.Code, w.Body.String())
	}
	if _, err := cfg.DB.Exec(`UPDATE needs SET valid_until=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if w := b.request("GET", "/admin/developments/1", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "需重新复核") || !strings.Contains(w.Body.String(), "disabled") {
		t.Fatal("expired development controls enabled", w.Code, w.Body.String())
	}
	if w := b.request("GET", "/admin/developments/1/bundle/requirements", nil, nil); w.Code != 409 {
		t.Fatal("expired requirements exported", w.Code)
	}
	design := designHTTPValues()
	design.Set("gorilla.csrf.Token", csrf)
	if w := b.request("POST", "/admin/developments/1/design", design, nil); w.Code != 409 {
		t.Fatal("expired evidence changed design", w.Code, w.Body.String())
	}
	if w := b.request("GET", "/admin/needs/1", nil, nil); strings.Contains(w.Body.String(), `action="/admin/needs/1/approve"`) {
		t.Fatal("expired approval controls shown")
	}
	if err := b.app.Auth.Disable("owner"); err != nil {
		t.Fatal(err)
	}
	if w := b.request("GET", "/admin/developments/1/bundle/requirements", nil, nil); w.Code != 303 {
		t.Fatal("disabled admin exported bundle", w.Code)
	}
}
