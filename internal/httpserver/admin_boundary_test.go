package httpserver

import (
	"net/url"
	"strings"
	"testing"
)

func TestAdminResearchBoundary(t *testing.T) {
	b, cfg := setupResearch(t)
	b.login(t)
	for _, path := range []string{"/admin/", "/admin/tasks", "/admin/producers"} {
		w := b.request("GET", path, nil, nil)
		if w.Code != 200 {
			t.Fatal(path, w.Code)
		}
		for _, removed := range []string{"搜寻任务", "模型设置", `action="/admin/tasks"`, `name="decision_mode"`} {
			if strings.Contains(w.Body.String(), removed) {
				t.Fatalf("%s still exposes %s", path, removed)
			}
		}
	}
	page := b.request("GET", "/admin/tasks", nil, nil)
	if !strings.Contains(page.Body.String(), "接收记录") {
		t.Fatal("missing read-only records")
	}
	csrf := token(t, page)
	for _, path := range []string{"/admin/tasks", "/admin/tasks/any/cancel", "/admin/tasks/any/resume", "/admin/settings", "/admin/batches/1/retry", "/admin/needs/1/approve"} {
		if w := b.request("POST", path, url.Values{}, nil); w.Code != 403 {
			t.Fatal("CSRF", path, w.Code)
		}
		if w := b.request("POST", path, url.Values{"gorilla.csrf.Token": {csrf}}, nil); w.Code != 410 {
			t.Fatal("retired action", path, w.Code)
		}
	}
	if w := b.request("GET", "/admin/settings", nil, nil); w.Code != 303 || w.Header().Get("Location") != "/admin/usage" {
		t.Fatal("old bookmark", w.Code)
	}
	var tasks int
	if err := cfg.DB.QueryRow("SELECT count(*) FROM research_tasks").Scan(&tasks); err != nil || tasks != 0 {
		t.Fatal(tasks, err)
	}
}
