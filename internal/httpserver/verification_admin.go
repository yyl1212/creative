package httpserver

import (
	"github.com/yyl1212/creative/internal/research"
	"net/http"
	"strconv"
)

func pageNumber(value string, allowEmpty bool) (int, error) {
	if value == "" && allowEmpty {
		return 0, nil
	}
	n, e := strconv.Atoi(value)
	if e != nil || n < 1 {
		return 0, &research.Error{Status: 422, Message: "页面标记无效"}
	}
	return n, nil
}
func (a *App) needsPage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
		return
	}
	before, e := pageNumber(r.URL.Query().Get("before"), true)
	if e != nil {
		a.adminResearchError(w, r, u, e)
		return
	}
	needs, next, e := a.research.Needs(before)
	if e != nil {
		a.adminResearchError(w, r, u, e)
		return
	}
	a.render(w, r, "needs.html", 200, view{Username: u.Username, Page: "needs", NeedItems: needs, NextBefore: next})
}
func (a *App) needPage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
		return
	}
	id, e := pageNumber(r.PathValue("id"), false)
	if e != nil {
		a.adminResearchError(w, r, u, e)
		return
	}
	selected, e := pageNumber(r.URL.Query().Get("verification"), true)
	if e != nil {
		a.adminResearchError(w, r, u, e)
		return
	}
	n, e := a.research.Need(id, selected)
	if e != nil {
		a.adminResearchError(w, r, u, e)
		return
	}
	a.render(w, r, "need.html", 200, view{Username: u.Username, Page: "needs", Need: n})
}
func (a *App) usagePage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.current(w, r)
	if !ok {
		return
	}
	before, e := pageNumber(r.URL.Query().Get("before"), true)
	if e != nil {
		a.adminResearchError(w, r, u, e)
		return
	}
	records, totals, next, e := a.research.Usage(before)
	if e != nil {
		a.adminResearchError(w, r, u, e)
		return
	}
	a.render(w, r, "usage.html", 200, view{Username: u.Username, Page: "usage", UsageRecords: records, UsageTotals: totals, NextBefore: next})
}
func reasonLabel(code string) string {
	labels := map[string]string{"context_requires_review": "材料超过完整语义复核范围，需要管理员复核", "not_self_service": "当前需求不能自助交付", "task_identity_unverified": "核心任务或与已有需求的区别尚不明确", "independent_users_below_3": "不足 3 名独立用户", "original_sites_below_2": "不足 2 个原始站点", "payment_users_below_2": "不足 2 名直接付费用户", "price_or_spend_missing": "缺少具体价格或可定位消费", "source_unavailable": "原文暂不可访问", "metadata_unverified": "作者、日期或引文无法绑定", "semantic_unverified": "语义、原创性或独立性待确认", "unmet_need_unverified": "未满足点缺少原文支持", "duplicate_evidence": "重复用户或引文", "verified": "原文已核验", "superseded_by_supplement": "已有更新补证，旧结果不计入资格", "model_unavailable": "模型未启用或暂不可用", "rules_changed": "规则版本变化，需重新提交", "raw_material_expired": "原始材料已到保留期限，请重新提交", "metadata_expired": "研究资料已到保留期限", "stored_payload_invalid": "存储材料无效", "unknown_duplicate_reference": "模型返回的重复需求无法定位"}
	if label, ok := labels[code]; ok {
		return label
	}
	return code
}
