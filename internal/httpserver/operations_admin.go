package httpserver

import (
	"net/http"

	"github.com/yyl1212/creative/internal/research"
)

func (a *App) operationsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/reports", a.overview)
	mux.HandleFunc("GET /admin/settings", a.settingsPage)
	mux.HandleFunc("POST /admin/settings", a.retiredResearchAction)
	mux.HandleFunc("POST /admin/usage/{id}/reconcile", a.reconcileUsage)
}

func (a *App) settingsPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.current(w, r); !ok {
		return
	}
	http.Redirect(w, r, "/admin/usage", http.StatusSeeOther)
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
