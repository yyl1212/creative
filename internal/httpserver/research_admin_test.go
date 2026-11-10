package httpserver

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestResearchAdminWorkflow(t *testing.T) {
	b, _ := setupResearch(t)
	for _, path := range []string{"/admin/tasks", "/admin/producers", "/admin/batches", "/admin/batches/1"} {
		if w := b.request("GET", path, nil, nil); w.Code != 303 {
			t.Fatalf("private page %s: %d", path, w.Code)
		}
	}
	if w := b.login(t); w.Code != 303 {
		t.Fatal("login", w.Code)
	}
	csrf := token(t, b.request("GET", "/admin/producers", nil, nil))
	if w := b.request("POST", "/admin/producers", url.Values{"name": {"test"}}, nil); w.Code != 403 {
		t.Fatal("producer creation without CSRF", w.Code)
	}
	w := b.request("POST", "/admin/producers", url.Values{"name": {"prompt-agent"}, "gorilla.csrf.Token": {csrf}}, nil)
	if w.Code != 200 {
		t.Fatal("create producer", w.Code, w.Body.String())
	}
	secret := regexp.MustCompile(`cr_[A-Za-z0-9_-]{43}`).FindString(w.Body.String())
	if secret == "" {
		t.Fatal("one-time credential missing")
	}
	if strings.Contains(b.request("GET", "/admin/producers", nil, nil).Body.String(), secret) {
		t.Fatal("credential redisplayed")
	}
	ps, e := b.app.Auth.Producers()
	if e != nil || len(ps) != 1 {
		t.Fatal("producer not persisted", e)
	}
	p := ps[0]
	// 历史数据仍可查看，但不再通过后台创建研究任务。
	task, e := b.app.research.CreateTask(p.ID, []string{"表格转换", "批量处理"}, []string{"zh", "en"}, 3, "fixture", "local")
	if e != nil {
		t.Fatal(e)
	}
	raw := bytes.Replace(httpFixture(t, task.ID), []byte("商品表格模板转换"), []byte("<script>alert('x')</script>"), 1)
	if _, e = b.app.research.Receive(p, raw); e != nil {
		t.Fatal("historical fixture", e)
	}
	w = b.request("GET", "/admin/batches", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "batch-example-001") {
		t.Fatal("batch list", w.Code)
	}
	w = b.request("GET", "/admin/batches/1", nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "&lt;script&gt;") || !strings.Contains(w.Body.String(), "待验证") {
		t.Fatal("unsafe material view", w.Code)
	}
	csrf = token(t, b.request("GET", "/admin/producers", nil, nil))
	values := url.Values{"version": {fmt.Sprint(p.Version)}, "gorilla.csrf.Token": {csrf}}
	if w = b.request("POST", "/admin/producers/"+p.ID+"/rotate", values, nil); w.Code != 200 {
		t.Fatal("rotate", w.Code)
	}
	nextSecret := regexp.MustCompile(`cr_[A-Za-z0-9_-]{43}`).FindString(w.Body.String())
	if nextSecret == "" || nextSecret == secret {
		t.Fatal("rotation did not issue new credential")
	}
	if w = apiRequest(b.app, "GET", "task", secret, nil); w.Code != 401 {
		t.Fatal("old credential still valid")
	}
	values.Set("version", fmt.Sprint(p.Version+1))
	if w = b.request("POST", "/admin/producers/"+p.ID+"/revoke", values, nil); w.Code != 303 {
		t.Fatal("revoke", w.Code)
	}
	if w = apiRequest(b.app, "GET", "task", nextSecret, nil); w.Code != 401 {
		t.Fatal("revoked credential still valid")
	}
	if w = b.request("GET", "/admin/", nil, nil); w.Code != 200 || strings.Count(w.Body.String(), `data-count="0"`) != 3 {
		t.Fatal("receipt created a product or qualified need")
	}
	for _, path := range []string{"/admin/tasks/x/cancel", "/admin/producers/x/revoke"} {
		if w = b.request("POST", path, nil, nil); w.Code != http.StatusForbidden {
			t.Fatal("action lacks CSRF", path, w.Code)
		}
	}
}

func TestAdminBatchesPaginationAndInvalidForms(t *testing.T) {
	b, _ := setupResearch(t)
	b.login(t)
	csrf := token(t, b.request("GET", "/admin/tasks", nil, nil))
	if w := b.request("POST", "/admin/tasks", url.Values{"target_count": {"not-a-number"}, "gorilla.csrf.Token": {csrf}}, nil); w.Code != 410 {
		t.Fatal("invalid task input", w.Code)
	}
	for _, path := range []string{"/admin/batches?before=-1", "/admin/batches?before=no", "/admin/batches/no"} {
		if w := b.request("GET", path, nil, nil); w.Code != 422 {
			t.Fatal("invalid page id", path, w.Code)
		}
	}
	if w := b.request("GET", "/admin/batches/999", nil, nil); w.Code != 404 {
		t.Fatal("missing batch", w.Code)
	}
	p, _, e := b.app.Auth.CreateProducer("paging", "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	task, e := b.app.research.CreateTask(p.ID, []string{"test"}, []string{"en"}, 3, "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= 51; i++ {
		raw := bytes.Replace(httpFixture(t, task.ID), []byte("batch-example-001"), []byte(fmt.Sprintf("page-%03d", i)), 1)
		if _, e = b.app.research.Receive(p, raw); e != nil {
			t.Fatal(e)
		}
	}
	w := b.request("GET", "/admin/batches", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "page-051") || strings.Contains(w.Body.String(), "page-001") {
		t.Fatal("first page boundary")
	}
	w = b.request("GET", "/admin/batches?before=2", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "page-001") || strings.Contains(w.Body.String(), "page-002") || !strings.Contains(w.Body.String(), task.ID) {
		t.Fatal("second page boundary")
	}
}

func TestTaskCardsIdentifyProducers(t *testing.T) {
	b, _ := setupResearch(t)
	b.login(t)
	for _, name := range []string{"中文执行端", "English agent"} {
		p, _, e := b.app.Auth.CreateProducer(name, "owner", "local")
		if e != nil {
			t.Fatal(e)
		}
		task, e := b.app.research.CreateTask(p.ID, []string{"相同主题"}, []string{"zh"}, 3, "owner", "local")
		if e != nil {
			t.Fatal(e)
		}
		page := b.request("GET", "/admin/tasks", nil, nil)
		cards := regexp.MustCompile(`(?s)<article class="list-item">.*?</article>`).FindAllString(page.Body.String(), -1)
		found := false
		for _, card := range cards {
			if strings.Contains(card, task.ID) {
				found = true
				if !strings.Contains(card, name) {
					t.Fatalf("task %s does not identify its producer", task.ID)
				}
			}
		}
		if !found {
			t.Fatal("task card missing")
		}
	}
}
