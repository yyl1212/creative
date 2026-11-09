package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProjectAPIImportsApprovedPackage(t *testing.T) {
	b, _ := setupResearch(t)
	p, secret, err := b.app.Auth.CreateProducer("project-agent", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	task, err := b.app.research.CreateProjectTask(p.ID, []string{"test"}, []string{"en"}, 1, "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	if w := apiRequest(b.app, "GET", "catalog", "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := apiRequest(b.app, "GET", "catalog", secret, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var catalog struct {
		Revision string `json:"revision"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../research/testdata/project.json")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	_ = json.Unmarshal(raw, &v)
	v["task_id"] = task.ID
	q := v["project"].(map[string]any)
	q["catalog_revision"] = catalog.Revision
	q["reviewed_at"] = time.Now().UTC().Format(time.RFC3339)
	for _, item := range v["candidates"].([]any)[0].(map[string]any)["evidence"].([]any) {
		e := item.(map[string]any)
		e["published_at"] = time.Now().AddDate(0, -1, 0).UTC().Format(time.RFC3339)
		e["observed_at"] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	}
	raw, _ = json.Marshal(v)
	w = apiRequest(b.app, "POST", "projects", secret, raw)
	if w.Code != 202 || !strings.Contains(w.Body.String(), `"development_state":"approved"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = apiRequest(b.app, "GET", "batches/project-example-001", secret, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"completed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = apiRequest(b.app, "GET", "projects", secret, nil); w.Code != 405 || w.Header().Get("Allow") != "POST" {
		t.Fatal(w.Code, w.Body.String())
	}
	b.login(t)
	if w = b.request("GET", "/admin/needs/1", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "外部模型判断") || strings.Contains(w.Body.String(), `action="/admin/needs/1/approve"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func setupResearch(t *testing.T) (*browser, Config) {
	t.Helper()
	b, _, cfg := setup(t)
	b.app.Close()
	cfg.ResearchOrigin = "http://127.0.0.1:8080"
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b.app = app
	t.Cleanup(app.Close)
	return b, cfg
}
func apiRequest(app *App, method, path, secret string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8080/api/research/v1/"+path, bytes.NewReader(body))
	r.RemoteAddr = "192.0.2.2:5000"
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}
func httpFixture(t *testing.T, task string) []byte {
	t.Helper()
	b, e := os.ReadFile("../research/testdata/batch.json")
	if e != nil {
		t.Fatal(e)
	}
	return bytes.ReplaceAll(b, []byte("task-example"), []byte(task))
}

func TestProducerIsolation(t *testing.T) {
	b, _ := setupResearch(t)
	p, secret, e := b.app.Auth.CreateProducer("one", "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	_, other, e := b.app.Auth.CreateProducer("two", "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	task, e := b.app.research.CreateTask(p.ID, []string{"self service"}, []string{"en"}, 3, "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"", "bad-token"} {
		if w := apiRequest(b.app, "GET", "task", key, nil); w.Code != 401 {
			t.Fatal("missing authorization accepted", w.Code)
		}
	}
	b.login(t)
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/research/v1/task", nil)
	for _, c := range b.cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	b.app.ServeHTTP(w, r)
	if w.Code != 401 || len(w.Result().Cookies()) != 0 {
		t.Fatal("cookie substitutes machine auth", w.Code)
	}
	unauth := &browser{app: b.app, cookies: map[string]*http.Cookie{}}
	if w = unauth.request("GET", "/admin/", nil, map[string]string{"Authorization": "Bearer " + secret}); w.Code != 303 {
		t.Fatal("producer substitutes admin")
	}
	if w = apiRequest(b.app, "GET", "task", other, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"task":null`) {
		t.Fatal("other producer sees task", w.Code, w.Body.String())
	}
	if w = apiRequest(b.app, "POST", "batches", secret, httpFixture(t, task.ID)); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = apiRequest(b.app, "GET", "batches/batch-example-001", other, nil); w.Code != 404 {
		t.Fatal("cross-producer receipt", w.Code)
	}
	if w = apiRequest(b.app, "POST", "batches", other, httpFixture(t, task.ID)); w.Code != 404 {
		t.Fatal("cross-producer task", w.Code)
	}
	if w = b.request("GET", "/api/research/v1/task", nil, map[string]string{"Authorization": "Bearer " + secret}); w.Code != 421 {
		t.Fatal("API accepted management host", w.Code)
	}
	if e = b.app.Auth.RevokeProducer(p.ID, p.Version, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	if w = apiRequest(b.app, "GET", "task", secret, nil); w.Code != 401 {
		t.Fatal("revoked token accepted", w.Code)
	}
}

func TestInvalidBatchHTTP(t *testing.T) {
	b, _ := setupResearch(t)
	p, secret, e := b.app.Auth.CreateProducer("one", "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	task, e := b.app.research.CreateTask(p.ID, []string{"test"}, []string{"en"}, 3, "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	raw := httpFixture(t, task.ID)
	for _, tt := range []struct {
		body   []byte
		status int
	}{{[]byte("{"), 400}, {[]byte(`{"schema_version":"bad"}`), 422}, {bytes.Repeat([]byte("x"), (1<<20)+1), 413}, {bytes.Replace(raw, []byte("strong-wtp.v1"), []byte("old-rules"), 1), 409}} {
		w := apiRequest(b.app, "POST", "batches", secret, tt.body)
		if w.Code != tt.status {
			t.Fatal("error code", w.Code, w.Body.String())
		}
		var output map[string]any
		if e = json.Unmarshal(w.Body.Bytes(), &output); e != nil || output["error"] == nil {
			t.Fatal("error envelope", w.Body.String())
		}
	}
	first := apiRequest(b.app, "POST", "batches", secret, raw)
	retry := apiRequest(b.app, "POST", "batches", secret, raw)
	if first.Code != 202 || retry.Code != 202 || first.Body.String() != retry.Body.String() {
		t.Fatal("HTTP retry mismatch")
	}
	if first.Header().Get("Location") != "/api/research/v1/batches/batch-example-001" || len(first.Result().Cookies()) != 0 || first.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unsafe API response")
	}
	w := apiRequest(b.app, "GET", "batches/batch-example-001", secret, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"qualified_count":0`) {
		t.Fatal("accepted counted as qualified", w.Body.String())
	}
	if _, e = b.app.db.Exec("UPDATE research_rate_limits SET attempts=60 WHERE producer_id=?", p.ID); e != nil {
		t.Fatal(e)
	}
	if w = apiRequest(b.app, "GET", "task", secret, nil); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("missing rate protection", w.Code)
	}
	if _, e = b.app.db.Exec("UPDATE research_rate_limits SET expires=0 WHERE producer_id=?", p.ID); e != nil {
		t.Fatal(e)
	}
	if w = apiRequest(b.app, "GET", "task", secret, nil); w.Code != 200 {
		t.Fatal("window did not recover", w.Code)
	}
	b.app.db.Close()
	if w = apiRequest(b.app, "GET", "task", secret, nil); w.Code != 503 || strings.Contains(w.Body.String(), "database") {
		t.Fatal("unavailable database response", w.Code, w.Body.String())
	}
}

func TestResearchHostConfiguration(t *testing.T) {
	b, cfg := setupResearch(t)
	for _, origin := range []string{"http://localhost:8080", "http://api.example.org", "https://user:secret@api.example.org", "https://api.example.org/path"} {
		cfg.ResearchOrigin = origin
		if a, e := New(cfg); e == nil {
			a.Close()
			t.Fatal("unsafe origin accepted", origin)
		}
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/admin/login", nil)
	w := httptest.NewRecorder()
	b.app.ServeHTTP(w, r)
	if w.Code != 421 {
		t.Fatal("API host renders admin", w.Code)
	}
}

func TestProducerRevocationDuringRead(t *testing.T) {
	for _, mutation := range []string{"token_hash=NULL,version=version+1", "token_hash='rotated-test-digest',version=version+1"} {
		for _, path := range []string{"task", "batches/batch-example-001"} {
			t.Run(path+mutation, func(t *testing.T) {
				b, _ := setupResearch(t)
				p, secret, e := b.app.Auth.CreateProducer("race", "owner", "local")
				if e != nil {
					t.Fatal(e)
				}
				task, e := b.app.research.CreateTask(p.ID, []string{"private research"}, []string{"en"}, 3, "owner", "local")
				if e != nil {
					t.Fatal(e)
				}
				if _, e = b.app.research.Receive(p, httpFixture(t, task.ID)); e != nil {
					t.Fatal(e)
				}
				// 在首次认证后、读取资源前失效令牌，复现真实轮换/撤销竞争。
				if _, e = b.app.db.Exec("CREATE TRIGGER revoke_between_auth_and_read AFTER INSERT ON research_rate_limits BEGIN UPDATE producers SET " + mutation + " WHERE id=NEW.producer_id; END"); e != nil {
					t.Fatal(e)
				}
				w := apiRequest(b.app, "GET", path, secret, nil)
				if w.Code != 401 {
					t.Fatalf("invalidated credential can read %s: status=%d", path, w.Code)
				}
				if strings.Contains(w.Body.String(), task.ID) || strings.Contains(w.Body.String(), "need-example-001") {
					t.Fatal("revocation response disclosed resource")
				}
			})
		}
	}
}

func TestReceiptMethodContract(t *testing.T) {
	b, _ := setupResearch(t)
	_, secret, e := b.app.Auth.CreateProducer("method", "owner", "local")
	if e != nil {
		t.Fatal(e)
	}
	for _, method := range []string{"POST", "DELETE", "PUT"} {
		w := apiRequest(b.app, method, "batches/existing-or-missing", secret, nil)
		if w.Code != 405 || w.Header().Get("Allow") != "GET" {
			t.Fatalf("%s receipt method=%d allow=%q", method, w.Code, w.Header().Get("Allow"))
		}
	}
}
