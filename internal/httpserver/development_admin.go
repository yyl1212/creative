package httpserver

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/yyl1212/creative/internal/research"
)

func (a *App) developmentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/developments", a.developmentsPage)
	mux.HandleFunc("GET /admin/developments/{id}", a.developmentPage)
	mux.HandleFunc("POST /admin/needs/{id}/approve", a.approveNeed)
	mux.HandleFunc("POST /admin/developments/{id}/design", a.confirmDesign)
	mux.HandleFunc("POST /admin/developments/{id}/start", a.startDevelopment)
	mux.HandleFunc("POST /admin/developments/{id}/result", a.recordDevelopment)
	mux.HandleFunc("GET /admin/developments/{id}/bundle/{kind}", a.downloadTaskBundle)
}

func largeLifecycleForm(path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 5 || parts[1] != "admin" {
		return false
	}
	id, err := strconv.Atoi(parts[3])
	return err == nil && id > 0 && (parts[2] == "needs" && parts[4] == "approve" || parts[2] == "developments" && (parts[4] == "design" || parts[4] == "result"))
}

func (a *App) developmentsPage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
		return
	}
	items, err := a.research.Developments()
	if err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	a.render(w, r, "developments.html", 200, view{Username: u.Username, Page: "developments", Developments: items})
}

func (a *App) developmentPage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
		return
	}
	id, err := pageNumber(r.PathValue("id"), false)
	if err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	d, err := a.research.Development(id)
	if err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	n, err := a.research.Need(id, 0)
	if err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	a.render(w, r, "development.html", 200, view{Username: u.Username, Page: "developments", Development: d, Need: n})
}

func (a *App) approveNeed(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
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
	verification, err := positiveForm(r, "verification_id")
	if err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	f := r.PostForm
	approval := research.Approval{VerificationID: verification, ReviewedEvidence: f["reviewed_evidence"], ReviewNotes: f.Get("review_notes"), AlternativeSearch: f.Get("alternative_search"), FewerAlternativesReason: f.Get("fewer_alternatives_reason"), Monetization: f.Get("monetization"), Rationale: f.Get("rationale"), UnitCost: f.Get("unit_cost"), Currency: f.Get("currency"), Unit: f.Get("unit"), Price: f.Get("price"), PaymentFee: f.Get("payment_fee"), PriceBasis: f.Get("price_basis"), Sustainability: f.Get("sustainability"), ResourceProtection: f.Get("resource_protection"), SelfService: f.Get("self_service"), Gap: f.Get("gap"), Scope: f.Get("scope"), Exclusions: f.Get("exclusions"), Acceptance: f.Get("acceptance"), Acquisition: f.Get("acquisition")}
	for i := 0; i < 3; i++ {
		prefix := fmt.Sprintf("alternative_%d_", i)
		v := research.AlternativeReview{Name: f.Get(prefix + "name"), URL: f.Get(prefix + "url"), Version: f.Get(prefix + "version"), CheckedAt: f.Get(prefix + "checked_at"), Pricing: f.Get(prefix + "pricing"), Result: f.Get(prefix + "result")}
		if v.Name != "" || v.URL != "" || v.Version != "" || v.CheckedAt != "" || v.Pricing != "" || v.Result != "" {
			approval.Alternatives = append(approval.Alternatives, v)
		}
	}
	if err = a.research.ApproveNeed(id, version, approval, u.Username, a.source(r)); err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/developments/%d", id), http.StatusSeeOther)
}

func formLines(value string) []string {
	lines := []string{}
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func (a *App) confirmDesign(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
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
	f := r.PostForm
	d := research.Design{Spec: f.Get("spec"), Architecture: f.Get("architecture"), Plan: f.Get("plan"), Files: formLines(f.Get("files")), Environment: formLines(f.Get("environment_names")), Repository: f.Get("repository"), Baseline: f.Get("baseline"), Branch: f.Get("branch"), Compatibility: f.Get("compatibility"), ReviewNotes: f.Get("review_notes"), ProductSlug: f.Get("product_slug"), ProductionOrigin: f.Get("production_origin")}
	if err = a.research.ConfirmDesign(id, version, d, u.Username, a.source(r)); err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/developments/%d", id), http.StatusSeeOther)
}

func (a *App) startDevelopment(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
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
	if err = a.research.StartDevelopment(id, version, u.Username, a.source(r)); err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/developments/%d", id), http.StatusSeeOther)
}

func (a *App) recordDevelopment(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
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
	f := r.PostForm
	result := research.DevelopmentResult{Commit: f.Get("commit"), PullRequest: f.Get("pull_request"), Summary: f.Get("summary"), ReviewCommit: f.Get("review_commit"), TestCommit: f.Get("test_commit"), ReviewReport: f.Get("review_report"), TestReport: f.Get("test_report"), ChecksPassed: f.Get("checks_passed") == "yes", RemainingIssues: f.Get("remaining_issues")}
	if err = a.research.RecordDevelopment(id, version, result, u.Username, a.source(r)); err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/developments/%d", id), http.StatusSeeOther)
}

func (a *App) downloadTaskBundle(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
		return
	}
	id, err := pageNumber(r.PathValue("id"), false)
	if err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	kind := r.PathValue("kind")
	raw, err := a.research.TaskBundle(id, kind)
	if err != nil {
		a.adminResearchError(w, r, u, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="creative-need-%d-%s.json"`, id, kind))
	w.Write(raw)
}
