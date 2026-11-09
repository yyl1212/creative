package httpserver

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"
	"github.com/gorilla/csrf"
	"github.com/yyl1212/creative/internal/auth"
	"github.com/yyl1212/creative/internal/research"
	adminweb "github.com/yyl1212/creative/web/admin"
)

type Config struct {
	DB             *sql.DB
	Origin         string
	ResearchOrigin string
	CSRFKey        []byte
	ProductsPath   string
	TrustedProxy   string
}
type App struct {
	Auth         *auth.Service
	Sessions     *scs.SessionManager
	db           *sql.DB
	host         string
	origin       string
	secure       bool
	proxy        net.IP
	productCount int
	templates    *template.Template
	handler      http.Handler
	sessionStore *sqlite3store.SQLite3Store
	closeOnce    sync.Once
	research     *research.Service
	apiHost      string
	apiSlots     chan struct{}
}
type view struct {
	CSRF         template.HTML
	Error        string
	Username     string
	Needs        int
	Qualified    int
	Completed    int
	Products     int
	Page         string
	Secret       string
	APIEnabled   bool
	Producers    []auth.Producer
	Tasks        []research.Task
	Batches      []research.BatchSummary
	Batch        *research.BatchDetail
	NeedItems    []research.Need
	Need         *research.Need
	UsageRecords []research.UsageRecord
	UsageTotals  []research.UsageTotal
	NextBefore   int
}

func New(cfg Config) (*App, error) {
	u, err := parseOrigin(cfg.Origin)
	if err != nil {
		return nil, errors.New("CREATIVE_ADMIN_ORIGIN 配置无效；非本机必须 HTTPS")
	}
	apiHost := ""
	if cfg.ResearchOrigin != "" {
		apiURL, err := parseOrigin(cfg.ResearchOrigin)
		if err != nil || apiURL.Host == u.Host {
			return nil, errors.New("CREATIVE_RESEARCH_ORIGIN 必须是独立有效 Host，非本机必须 HTTPS")
		}
		apiHost = apiURL.Host
	}
	if len(cfg.CSRFKey) != 32 {
		return nil, errors.New("CREATIVE_CSRF_KEY 必须为 32 字节密钥")
	}
	var proxy net.IP
	if cfg.TrustedProxy != "" {
		proxy = net.ParseIP(cfg.TrustedProxy)
		if proxy == nil {
			return nil, errors.New("CREATIVE_TRUSTED_PROXY 必须为明确的代理 IP")
		}
	}
	products, err := os.ReadFile(cfg.ProductsPath)
	if err != nil {
		return nil, errors.New("无法读取公开产品清单")
	}
	var catalog []json.RawMessage
	if err = json.Unmarshal(products, &catalog); err != nil || catalog == nil {
		return nil, errors.New("公开产品清单必须为 JSON 数组")
	}
	templates, err := template.New("admin").Funcs(template.FuncMap{"stateLabel": stateLabel, "reasonLabel": reasonLabel}).ParseFS(adminweb.Files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	service, err := auth.New(cfg.DB)
	if err != nil {
		return nil, err
	}
	researchService, err := research.New(cfg.DB)
	if err != nil {
		return nil, err
	}
	sessionStore := sqlite3store.New(cfg.DB)
	sessions := scs.New()
	sessions.Store = sessionStore
	sessions.Lifetime = 12 * time.Hour
	sessions.IdleTimeout = 30 * time.Minute
	sessions.Cookie.Name = "creative_session"
	sessions.Cookie.Path = "/"
	sessions.Cookie.HttpOnly = true
	sessions.Cookie.Secure = u.Scheme == "https"
	sessions.Cookie.SameSite = http.SameSiteLaxMode
	csrfName := "creative_csrf"
	if sessions.Cookie.Secure {
		sessions.Cookie.Name = "__Host-creative_session"
		csrfName = "__Host-creative_csrf"
	}
	sessions.ErrorFunc = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "会话服务暂不可用", http.StatusInternalServerError)
	}
	a := &App{Auth: service, Sessions: sessions, db: cfg.DB, host: u.Host, origin: u.Scheme + "://" + u.Host, secure: u.Scheme == "https", proxy: proxy, productCount: len(catalog), templates: templates, sessionStore: sessionStore, research: researchService, apiHost: apiHost, apiSlots: make(chan struct{}, 8)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/login", a.loginPage)
	mux.HandleFunc("POST /admin/login", a.login)
	mux.HandleFunc("GET /admin/{$}", a.overview)
	mux.HandleFunc("POST /admin/logout", a.logout)
	a.researchRoutes(mux)
	css, _ := adminweb.Files.ReadFile("static/admin.css")
	mux.HandleFunc("GET /admin/static/admin.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Write(css)
	})
	a.handler = csrf.Protect(cfg.CSRFKey, csrf.Secure(a.secure), csrf.Path("/"), csrf.CookieName(csrfName), csrf.SameSite(csrf.SameSiteLaxMode))(sessions.LoadAndSave(mux))
	return a, nil
}
func (a *App) Close() { a.closeOnce.Do(a.sessionStore.StopCleanup) }
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	if a.apiHost != "" && strings.HasPrefix(r.URL.Path, "/api/research/v1/") {
		if r.Host != a.apiHost {
			researchError(w, &research.Error{Status: 421, Code: "host_mismatch", Field: "/", Message: "API 主机不匹配"})
			return
		}
		a.serveResearch(w, r)
		return
	}
	if r.Host != a.host {
		http.Error(w, "管理主机不匹配", http.StatusMisdirectedRequest)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/admin/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
		if origin := r.Header.Get("Origin"); origin != "" && origin != a.origin {
			http.Error(w, "来源不匹配", http.StatusForbidden)
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/x-www-form-urlencoded" {
			http.Error(w, "仅支持表单", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err = r.ParseForm(); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "表单过大", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "表单无效", http.StatusBadRequest)
			}
			return
		}
	}
	if !a.secure {
		r = csrf.PlaintextHTTPRequest(r)
	}
	a.handler.ServeHTTP(w, r)
}
func (a *App) render(w http.ResponseWriter, r *http.Request, name string, status int, data view) {
	data.CSRF = csrf.TemplateField(r)
	var body bytes.Buffer
	if err := a.templates.ExecuteTemplate(&body, name, data); err != nil {
		http.Error(w, "页面暂不可用", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	io.Copy(w, &body)
}
func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "login.html", 200, view{})
}
func (a *App) source(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	if a.proxy != nil && a.proxy.Equal(net.ParseIP(host)) {
		if ip := net.ParseIP(r.Header.Get("X-Real-IP")); ip != nil {
			return ip.String()
		}
	}
	return host
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	user, err := a.Auth.Authenticate(r.PostForm.Get("username"), r.PostForm.Get("password"), a.source(r))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrThrottled):
			w.Header().Set("Retry-After", "900")
			a.render(w, r, "login.html", 429, view{Error: err.Error()})
		case errors.Is(err, auth.ErrInvalid):
			a.render(w, r, "login.html", 401, view{Error: err.Error()})
		default:
			http.Error(w, "登录服务暂不可用", 500)
		}
		return
	}
	if err = a.Sessions.RenewToken(r.Context()); err != nil {
		http.Error(w, "会话服务暂不可用", 500)
		return
	}
	a.Sessions.Put(r.Context(), "admin_id", user.ID)
	a.Sessions.Put(r.Context(), "auth_version", user.Version)
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}
func (a *App) current(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, err := a.Auth.Current(a.Sessions.GetInt(r.Context(), "admin_id"), a.Sessions.GetInt(r.Context(), "auth_version"))
	if errors.Is(err, auth.ErrInvalid) {
		if err = a.Sessions.Destroy(r.Context()); err != nil {
			http.Error(w, "会话服务暂不可用", 500)
			return user, false
		}
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return user, false
	}
	if err != nil {
		http.Error(w, "账号服务暂不可用", 500)
		return user, false
	}
	return user, true
}
func (a *App) overview(w http.ResponseWriter, r *http.Request) {
	user, ok := a.current(w, r)
	if !ok {
		return
	}
	data := view{Username: user.Username, Products: a.productCount, Page: "overview"}
	var err error
	data.Needs, data.Qualified, data.Completed, err = a.research.Counts()
	if err != nil {
		http.Error(w, "数据暂不可用", 500)
		return
	}
	a.render(w, r, "index.html", 200, data)
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	user, ok := a.current(w, r)
	if !ok {
		return
	}
	if err := a.Sessions.Destroy(r.Context()); err != nil {
		http.Error(w, "注销暂不可用", 500)
		return
	}
	if err := a.Auth.Record("logout", user.Username, a.source(r)); err != nil {
		http.Error(w, "审计暂不可用", 500)
		return
	}
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}
func (a *App) String() string { return fmt.Sprintf("Creative 管理入口 %s/admin/", a.origin) }

func parseOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("invalid origin")
	}
	local := u.Hostname() == "localhost" || net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, errors.New("https required")
	}
	return u, nil
}
