package httpserver

import (
	"bytes"
	"database/sql"
	"github.com/yyl1212/creative/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type browser struct {
	app     *App
	cookies map[string]*http.Cookie
}

func (b *browser) request(method, path string, values url.Values, headers map[string]string) *httptest.ResponseRecorder {
	var body string
	if values != nil {
		body = values.Encode()
	}
	r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.1:4000"
	if method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "http://localhost:8080")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	for _, c := range b.cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	b.app.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c
		}
	}
	return w
}
func token(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	m := regexp.MustCompile(`name="gorilla.csrf.Token" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(m) < 2 {
		t.Fatalf("missing CSRF: %d %s", w.Code, w.Body.String())
	}
	return m[1]
}
func setup(t *testing.T) (*browser, *sql.DB, Config) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "creative.db"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "products.json")
	if err = os.WriteFile(manifest, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{DB: db, Origin: "http://localhost:8080", CSRFKey: bytes.Repeat([]byte{1}, 32), ProductsPath: manifest}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Auth.CreateAdmin("owner", "test-only-correct-password"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close(); db.Close() })
	return &browser{app: app, cookies: map[string]*http.Cookie{}}, db, cfg
}
func (b *browser) login(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	csrf := token(t, b.request("GET", "/admin/login", nil, nil))
	return b.request("POST", "/admin/login", url.Values{"username": {"owner"}, "password": {"test-only-correct-password"}, "gorilla.csrf.Token": {csrf}}, nil)
}
func TestAdminAccessBoundaries(t *testing.T) {
	b, _, _ := setup(t)
	for _, h := range []map[string]string{nil, {"Authorization": "Bearer research-token"}} {
		if w := b.request("GET", "/admin/", nil, h); w.Code != 303 {
			t.Fatalf("unauthorized: %d", w.Code)
		}
	}
	if w := b.request("GET", "/api/research/v1/task", nil, nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("GET", "http://attacker.example/admin/login", nil)
	w := httptest.NewRecorder()
	b.app.ServeHTTP(w, r)
	if w.Code != 421 {
		t.Fatal("foreign host accepted")
	}
	if w = b.login(t); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = b.request("GET", "/admin/", nil, nil)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private overview unavailable")
	}
	c := b.cookies[b.app.Sessions.Cookie.Name]
	if c == nil || !c.HttpOnly || c.Domain != "" || c.SameSite != http.SameSiteLaxMode {
		t.Fatal("cookie boundary")
	}
	if w = b.request("GET", "/admin/logout", nil, nil); w.Code != 405 {
		t.Fatal("GET logout changes state")
	}
}
func TestSessionLifecycle(t *testing.T) {
	b, db, cfg := setup(t)
	if w := b.login(t); w.Code != 303 {
		t.Fatal(w.Code)
	}
	old := *b.cookies[b.app.Sessions.Cookie.Name]
	b.login(t)
	if old.Value == b.cookies[old.Name].Value {
		t.Fatal("token not rotated")
	}
	stale := &browser{app: b.app, cookies: map[string]*http.Cookie{old.Name: &old}}
	if stale.request("GET", "/admin/", nil, nil).Code != 303 {
		t.Fatal("old token valid")
	}
	b.app.Close()
	next, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	b.app = next
	if b.request("GET", "/admin/", nil, nil).Code != 200 {
		t.Fatal("session lost after service restart")
	}
	if err = next.Auth.ResetPassword("owner", "test-only-correct-password"); err != nil {
		t.Fatal(err)
	}
	if b.request("GET", "/admin/", nil, nil).Code != 303 {
		t.Fatal("reset did not revoke session")
	}
	b.login(t)
	if err = next.Auth.Disable("owner"); err != nil {
		t.Fatal(err)
	}
	if b.request("GET", "/admin/", nil, nil).Code != 303 {
		t.Fatal("disabled account accepted")
	}
	if err = next.Auth.ResetPassword("owner", "test-only-correct-password"); err != nil {
		t.Fatal(err)
	}
	b.login(t)
	csrf := token(t, b.request("GET", "/admin/", nil, nil))
	old = *b.cookies[next.Sessions.Cookie.Name]
	if b.request("POST", "/admin/logout", url.Values{"gorilla.csrf.Token": {csrf}}, nil).Code != 303 {
		t.Fatal("logout failed")
	}
	b.cookies[old.Name] = &old
	if b.request("GET", "/admin/", nil, nil).Code != 303 {
		t.Fatal("logged out token accepted")
	}
	b.login(t)
	if _, err = db.Exec("UPDATE sessions SET expiry=0"); err != nil {
		t.Fatal(err)
	}
	if b.request("GET", "/admin/", nil, nil).Code != 303 {
		t.Fatal("expired session accepted")
	}
	for _, idle := range []bool{false, true} {
		next.Sessions.Lifetime = time.Hour
		next.Sessions.IdleTimeout = time.Hour
		if idle {
			next.Sessions.IdleTimeout = 20 * time.Millisecond
		} else {
			next.Sessions.Lifetime = 20 * time.Millisecond
		}
		b.login(t)
		time.Sleep(35 * time.Millisecond)
		if b.request("GET", "/admin/", nil, nil).Code != 303 {
			t.Fatalf("expiry idle=%v", idle)
		}
	}
}
func TestAdminWriteRequiresCSRF(t *testing.T) {
	b, _, _ := setup(t)
	for _, csrf := range []string{"", "invalid"} {
		w := b.request("POST", "/admin/login", url.Values{"username": {"owner"}, "password": {"test-only-correct-password"}, "gorilla.csrf.Token": {csrf}}, nil)
		if w.Code != 403 {
			t.Fatalf("csrf: %d", w.Code)
		}
	}
	b.login(t)
	csrf := token(t, b.request("GET", "/admin/", nil, nil))
	if b.request("POST", "/admin/logout", url.Values{"gorilla.csrf.Token": {csrf}}, map[string]string{"Origin": "https://evil.example"}).Code != 403 {
		t.Fatal("cross-origin accepted")
	}
	if b.request("POST", "/admin/logout", nil, nil).Code != 403 {
		t.Fatal("logout lacks CSRF")
	}
	if b.request("GET", "/admin/", nil, nil).Code != 200 {
		t.Fatal("invalid logout removed session")
	}
}
func TestInitialOverview(t *testing.T) {
	b, db, _ := setup(t)
	b.login(t)
	w := b.request("GET", "/admin/", nil, nil)
	for _, label := range []string{"需求总数", "历史合格需求", "已完成需求", "产品数量"} {
		if !strings.Contains(w.Body.String(), label) {
			t.Fatal(label)
		}
	}
	if strings.Count(w.Body.String(), `data-count="0"`) != 4 || !strings.Contains(w.Body.String(), "未接入 / 未知") {
		t.Fatal("initial state not real zero/unknown")
	}
	if _, err := db.Exec("INSERT INTO needs(status,verified_at,valid_until,rules_version) VALUES ('qualified',unixepoch(),unixepoch()+3600,'strong-wtp.v1')"); err != nil {
		t.Fatal(err)
	}
	w = b.request("GET", "/admin/", nil, nil)
	if strings.Count(w.Body.String(), `data-count="1"`) != 2 {
		t.Fatal("counts not read from DB")
	}
}
func TestConfigurationAndProxy(t *testing.T) {
	b, _, cfg := setup(t)
	for _, origin := range []string{"http://public.example", "https://user:pass@example.com", "https://example.com/path"} {
		cfg.Origin = origin
		if a, err := New(cfg); err == nil {
			a.Close()
			t.Fatal("invalid origin accepted")
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.1:4000"
	r.Header.Set("X-Real-IP", "203.0.113.1")
	if b.app.source(r) != "192.0.2.1" {
		t.Fatal("spoofed proxy accepted")
	}
	cfg.Origin = "https://admin.example.com"
	cfg.TrustedProxy = "192.0.2.1"
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.source(r) != "203.0.113.1" || !a.Sessions.Cookie.Secure || a.Sessions.Cookie.Name != "__Host-creative_session" {
		t.Fatal("HTTPS/proxy config")
	}
}
