package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/yyl1212/creative/internal/auth"
	"github.com/yyl1212/creative/internal/research"
)

func (a *App) researchRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/needs", a.needsPage)
	mux.HandleFunc("GET /admin/needs/{id}", a.needPage)
	mux.HandleFunc("GET /admin/usage", a.usagePage)
	mux.HandleFunc("POST /admin/batches/{id}/retry", a.retryBatch)
	mux.HandleFunc("GET /admin/producers", a.producersPage)
	mux.HandleFunc("POST /admin/producers", a.createProducer)
	mux.HandleFunc("POST /admin/producers/{id}/rotate", a.rotateProducer)
	mux.HandleFunc("POST /admin/producers/{id}/revoke", a.revokeProducer)
	mux.HandleFunc("GET /admin/tasks", a.tasksPage)
	mux.HandleFunc("POST /admin/tasks", a.createTask)
	mux.HandleFunc("POST /admin/tasks/{id}/{action}", a.changeTask)
	mux.HandleFunc("GET /admin/batches", a.batchesPage)
	mux.HandleFunc("GET /admin/batches/{id}", a.batchPage)
}
func stateLabel(state string) string {
	switch state {
	case "qualified":
		return "合格"
	case "pending", "needs_evidence":
		return "待补证"
	case "duplicate":
		return "重复需求"
	case "rejected":
		return "不符合要求"
	case "expired":
		return "资格已过期"
	case "unknown":
		return "未知"
	case "reported":
		return "自报，未核对"
	case "verified":
		return "已核对"
	case "conflict":
		return "冲突，未计入合计"
	case "website":
		return "网站调用"
	case "external":
		return "外部执行端"
	case "active":
		return "进行中"
	case "cancelled":
		return "已取消"
	case "target_reached":
		return "已达标"
	case "paused_service":
		return "服务暂停"
	case "waiting_for_signals":
		return "等待线索"
	case "pending_validation":
		return "待验证"
	case "validating":
		return "验证中"
	case "completed":
		return "已验证"
	}
	return "未知"
}
func (a *App) adminResearchError(w http.ResponseWriter, r *http.Request, user auth.User, err error) {
	status := 503
	message := "服务暂不可用，请稍后重试"
	var e *research.Error
	switch {
	case errors.As(err, &e):
		status = e.Status
		message = e.Message
	case errors.Is(err, auth.ErrProducerConflict):
		status = 409
		message = err.Error()
	case errors.Is(err, auth.ErrProducerInput):
		status = 422
		message = err.Error()
	}
	a.render(w, r, "research-error.html", status, view{Username: user.Username, Page: "research", Error: message})
}
func positiveForm(r *http.Request, key string) (int, error) {
	value, err := strconv.Atoi(r.PostForm.Get(key))
	if err != nil || value < 1 {
		return 0, &research.Error{Status: 422, Code: "invalid_form", Field: "/" + key, Message: "请填写有效的正整数"}
	}
	return value, nil
}
func (a *App) showProducers(w http.ResponseWriter, r *http.Request, user auth.User, secret string) {
	ps, err := a.Auth.Producers()
	if err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	a.render(w, r, "producers.html", 200, view{Username: user.Username, Page: "producers", Producers: ps, Secret: secret, APIEnabled: a.apiHost != ""})
}
func (a *App) producersPage(w http.ResponseWriter, r *http.Request) {
	user, ok := a.current(w, r)
	if !ok {
		return
	}
	a.showProducers(w, r, user, "")
}
func (a *App) createProducer(w http.ResponseWriter, r *http.Request) {
	user, ok := a.current(w, r)
	if !ok {
		return
	}
	_, secret, err := a.Auth.CreateProducer(r.PostForm.Get("name"), user.Username, a.source(r))
	if err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	a.showProducers(w, r, user, secret)
}
func (a *App) rotateProducer(w http.ResponseWriter, r *http.Request) {
	user, ok := a.current(w, r)
	if !ok {
		return
	}
	version, err := positiveForm(r, "version")
	if err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	_, secret, err := a.Auth.RotateProducer(r.PathValue("id"), version, user.Username, a.source(r))
	if err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	a.showProducers(w, r, user, secret)
}
func (a *App) revokeProducer(w http.ResponseWriter, r *http.Request) {
	user, ok := a.current(w, r)
	if !ok {
		return
	}
	version, err := positiveForm(r, "version")
	if err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	if err = a.Auth.RevokeProducer(r.PathValue("id"), version, user.Username, a.source(r)); err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	http.Redirect(w, r, "/admin/producers", http.StatusSeeOther)
}
func (a *App) tasksPage(w http.ResponseWriter, r *http.Request) {
	user, ok := a.current(w, r)
	if !ok {
		return
	}
	tasks, err := a.research.Tasks()
	if err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	ps, err := a.Auth.Producers()
	if err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	a.render(w, r, "tasks.html", 200, view{Username: user.Username, Page: "tasks", Tasks: tasks, Producers: ps, APIEnabled: a.apiHost != ""})
}
func (a *App) createTask(w http.ResponseWriter, r *http.Request) {
	user, ok := a.current(w, r)
	if !ok {
		return
	}
	target, err := positiveForm(r, "target_count")
	if err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	topics := []string{}
	for _, line := range strings.Split(r.PostForm.Get("topics"), "\n") {
		if text := strings.TrimSpace(line); text != "" {
			topics = append(topics, text)
		}
	}
	_, err = a.research.CreateTask(r.PostForm.Get("producer_id"), topics, r.PostForm["languages"], target, user.Username, a.source(r))
	if err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	http.Redirect(w, r, "/admin/tasks", http.StatusSeeOther)
}
func (a *App) changeTask(w http.ResponseWriter, r *http.Request) {
	user, ok := a.current(w, r)
	if !ok {
		return
	}
	version, err := positiveForm(r, "version")
	if err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	_, err = a.research.ChangeTask(r.PathValue("id"), version, r.PathValue("action"), user.Username, a.source(r))
	if err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	http.Redirect(w, r, "/admin/tasks", http.StatusSeeOther)
}
func (a *App) batchesPage(w http.ResponseWriter, r *http.Request) {
	user, ok := a.current(w, r)
	if !ok {
		return
	}
	before := 0
	var err error
	if value := r.URL.Query().Get("before"); value != "" {
		before, err = strconv.Atoi(value)
		if err != nil || before < 1 {
			a.adminResearchError(w, r, user, &research.Error{Status: 422, Message: "分页标记无效"})
			return
		}
	}
	batches, next, err := a.research.Batches(before)
	if err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	a.render(w, r, "batches.html", 200, view{Username: user.Username, Page: "batches", Batches: batches, NextBefore: next})
}
func (a *App) batchPage(w http.ResponseWriter, r *http.Request) {
	user, ok := a.current(w, r)
	if !ok {
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 {
		a.adminResearchError(w, r, user, &research.Error{Status: 422, Message: "批次标记无效"})
		return
	}
	batch, err := a.research.Batch(id)
	if err != nil {
		a.adminResearchError(w, r, user, err)
		return
	}
	a.render(w, r, "batch.html", 200, view{Username: user.Username, Page: "batches", Batch: batch})
}
