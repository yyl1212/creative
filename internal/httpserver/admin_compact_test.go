package httpserver

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestCompactAdminNavigation(t *testing.T) {
	b, _ := setupResearch(t)
	b.login(t)
	navPattern := regexp.MustCompile(`(?s)<nav class="workspace-nav"[^>]*>(.*?)</nav>`)
	linkPattern := regexp.MustCompile(`<a href="([^"]+)"[^>]*>([^<]+)</a>`)
	paths := []string{"/admin/", "/admin/needs", "/admin/developments", "/admin/batches", "/admin/usage"}
	labels := []string{"总览", "需求池", "开发工作区", "导入记录", "费用与用量"}
	for _, path := range append(paths, "/admin/reports", "/admin/tasks", "/admin/producers") {
		w := b.request("GET", path, nil, nil)
		if w.Code != 200 {
			t.Fatal(path, w.Code)
		}
		page := w.Body.String()
		nav := navPattern.FindStringSubmatch(page)
		if len(nav) != 2 {
			t.Fatal("missing navigation", path)
		}
		links := linkPattern.FindAllStringSubmatch(nav[1], -1)
		if len(links) != 5 {
			t.Fatalf("%s: got %d main links", path, len(links))
		}
		for i, link := range links {
			if link[1] != paths[i] || link[2] != labels[i] {
				t.Fatal("wrong navigation", path, link)
			}
		}
		header := strings.Split(page, "</header>")[0]
		if !strings.Contains(header, `href="/admin/producers"`) {
			t.Fatal("secondary access missing", path)
		}
		if path != "/admin/producers" && strings.Count(nav[1], `aria-current="page"`) != 1 {
			t.Fatal("active navigation missing", path)
		}
		for _, stale := range []string{"请在当前需求详情完成立项复核", "先在需求池逐条复核", "历史合格需求", "访问量、收入与费用"} {
			if strings.Contains(page, stale) {
				t.Fatal("stale workflow copy", path, stale)
			}
		}
	}
}

func TestCompactOverviewPreservesDateAndReport(t *testing.T) {
	b, cfg := setupResearch(t)
	stamp, err := time.Parse(time.RFC3339, "2026-11-09T17:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cfg.DB.Exec("INSERT INTO usage_records(origin,provider,reported_cost,currency,cost_status,created_at) VALUES ('external','deepseek','0.25','USD','reported',?)", stamp.Unix()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/", "/admin/reports"} {
		if w := b.request("GET", path+"?date=2026-11-10", nil, nil); w.Code != 303 {
			t.Fatal("report not private", path, w.Code)
		}
	}
	b.login(t)
	for _, path := range []string{"/admin/", "/admin/reports"} {
		page := b.request("GET", path+"?date=2026-11-10", nil, nil)
		if page.Code != 200 {
			t.Fatal(path, page.Code)
		}
		for _, value := range []string{"需求总数", "经营日报", `value="2026-11-10"`, "0.25", "尚无备份结果"} {
			if !strings.Contains(page.Body.String(), value) {
				t.Fatal("merged report missing", path, value)
			}
		}
		if w := b.request("GET", path+"?date=2026-11-09", nil, nil); w.Code != 200 || strings.Contains(w.Body.String(), ">0.25<") {
			t.Fatal("Shanghai date boundary", path, w.Code)
		}
		if w := b.request("GET", path+"?date=2026-02-30", nil, nil); w.Code != 422 {
			t.Fatal("invalid report date", path, w.Code)
		}
	}
}

func TestCompactImportsPreserveSessionsAndHistory(t *testing.T) {
	b, cfg := setupResearch(t)
	p, _, err := b.app.Auth.CreateProducer("history", "fixture", "local")
	if err != nil {
		t.Fatal(err)
	}
	task, err := b.app.research.CreateTask(p.ID, []string{"历史主题"}, []string{"zh"}, 3, "fixture", "local")
	if err != nil {
		t.Fatal(err)
	}
	raw := httpFixture(t, task.ID)
	if _, err = b.app.research.Receive(p, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = b.app.research.ChangeTask(task.ID, task.Version, "cancel", "fixture", "local"); err != nil {
		t.Fatal(err)
	}
	b.login(t)
	for _, path := range []string{"/admin/batches", "/admin/tasks"} {
		page := b.request("GET", path, nil, nil)
		if page.Code != 200 {
			t.Fatal(path, page.Code)
		}
		for _, value := range []string{"导入记录", "接收会话", task.ID, "已取消", "batch-example-001"} {
			if !strings.Contains(page.Body.String(), value) {
				t.Fatal("history missing", path, value)
			}
		}
	}
	if page := b.request("GET", "/admin/batches/1", nil, nil); page.Code != 200 || !strings.Contains(page.Body.String(), "原始提交包") {
		t.Fatal("raw material unavailable", page.Code)
	}
	var saved []byte
	if err = cfg.DB.QueryRow("SELECT payload FROM research_batches WHERE id=1").Scan(&saved); err != nil || string(saved) != string(raw) {
		t.Fatal("history mutated", err)
	}
}
