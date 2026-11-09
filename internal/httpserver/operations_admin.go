package httpserver

import (
	"net/http"
	"strconv"

	"github.com/yyl1212/creative/internal/model"
	"github.com/yyl1212/creative/internal/research"
)

func (a *App) operationsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/reports", a.reportsPage)
	mux.HandleFunc("GET /admin/settings", a.settingsPage)
	mux.HandleFunc("POST /admin/settings", a.saveSettings)
	mux.HandleFunc("POST /admin/usage/{id}/reconcile", a.reconcileUsage)
}

func (a *App) reportsPage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
		return
	}
	report, err := a.research.DailyReport(r.URL.Query().Get("date"))
	if err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	backup, err := a.research.BackupStatus()
	if err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	a.render(w, r, "reports.html", http.StatusOK, view{Username: u.Username, Page: "reports", Report: &report, Backup: backup})
}

func (a *App) settingsPage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
		return
	}
	saved, err := a.research.ModelConfig()
	if err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	if saved == nil {
		saved = &research.SavedModelConfig{Config: a.runtimeModel}
		if saved.Config.Primary == "" {
			saved.Config.Primary = "openai"
		}
	}
	a.render(w, r, "settings.html", http.StatusOK, view{Username: u.Username, Page: "settings", SavedModel: saved, RuntimeModel: a.runtimeModel})
}

func operationsForm(r *http.Request, fields ...string) error {
	allowed := map[string]bool{"gorilla.csrf.Token": true}
	for _, field := range fields {
		allowed[field] = true
	}
	for field, values := range r.PostForm {
		if !allowed[field] || len(values) != 1 {
			return &research.Error{Status: 422, Code: "invalid_form", Field: "/", Message: "表单含未知或重复字段，请刷新后重试"}
		}
	}
	return nil
}

func (a *App) saveSettings(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
		return
	}
	if err := operationsForm(r, "version", "primary", "fallback", "openai_model", "openai_enabled", "openai_validated", "deepseek_model", "deepseek_enabled", "deepseek_validated"); err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	version, err := strconv.Atoi(r.PostForm.Get("version"))
	if err != nil || version < 0 {
		a.adminResearchError(w, r, u, &research.Error{Status: 422, Message: "配置版本无效，请刷新后重试"})
		return
	}
	cfg := model.Config{Primary: r.PostForm.Get("primary"), Fallback: r.PostForm.Get("fallback"), Providers: map[string]model.Provider{}}
	for _, name := range []string{"openai", "deepseek"} {
		for _, field := range []string{name + "_enabled", name + "_validated"} {
			if value := r.PostForm.Get(field); value != "" && value != "on" {
				a.adminResearchError(w, r, u, &research.Error{Status: 422, Message: "启用或验证状态无效"})
				return
			}
		}
		cfg.Providers[name] = model.Provider{Model: r.PostForm.Get(name + "_model"), Enabled: r.PostForm.Get(name+"_enabled") == "on", Validated: r.PostForm.Get(name+"_validated") == "on"}
	}
	if err := a.research.SaveModelConfig(version, cfg, u.Username, a.source(r)); err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
}

func (a *App) reconcileUsage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
		return
	}
	if err := operationsForm(r, "version", "amount", "currency", "billing_item", "note"); err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	id, err := pageNumber(r.PathValue("id"), false)
	if err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	version, err := positiveForm(r, "version")
	if err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	entry := research.Reconciliation{Amount: r.PostForm.Get("amount"), Currency: r.PostForm.Get("currency"), BillingItem: r.PostForm.Get("billing_item"), Note: r.PostForm.Get("note")}
	if err = a.research.ReconcileUsage(id, version, entry, u.Username, a.source(r)); err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/admin/usage", http.StatusSeeOther)
}
