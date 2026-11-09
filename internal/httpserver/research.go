package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yyl1212/creative/internal/auth"
	"github.com/yyl1212/creative/internal/research"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func researchError(w http.ResponseWriter, err error) {
	var failure *research.Error
	if !errors.As(err, &failure) {
		failure = &research.Error{Status: 503, Code: "service_unavailable", Field: "/", Message: "服务暂不可用，请稍后重试"}
		w.Header().Set("Retry-After", "30")
	}
	writeJSON(w, failure.Status, map[string]any{"error": failure})
}
func (a *App) serveResearch(w http.ResponseWriter, r *http.Request) {
	select {
	case a.apiSlots <- struct{}{}:
		defer func() { <-a.apiSlots }()
	default:
		w.Header().Set("Retry-After", "1")
		researchError(w, &research.Error{Status: 429, Code: "too_many_requests", Field: "/", Message: "并发请求过多，请稍后重试"})
		return
	}
	authorization := r.Header.Values("Authorization")
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(authorization) != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		a.invalidProducer(w)
		return
	}
	producer, err := a.Auth.AuthenticateProducer(parts[1])
	if errors.Is(err, auth.ErrProducerInvalid) {
		a.invalidProducer(w)
		return
	}
	if err != nil {
		researchError(w, err)
		return
	}
	// 按滚动分钟恢复的传输保护，不限制总搜索次数或费用。
	now := time.Now().Unix()
	var attempts int
	var expires int64
	err = a.db.QueryRow(`INSERT INTO research_rate_limits(producer_id,attempts,expires) VALUES (?,1,?)
 ON CONFLICT(producer_id) DO UPDATE SET attempts=CASE WHEN expires<=? THEN 1 ELSE attempts+1 END, expires=CASE WHEN expires<=? THEN excluded.expires ELSE expires END RETURNING attempts,expires`, producer.ID, now+60, now, now).Scan(&attempts, &expires)
	if err != nil {
		researchError(w, err)
		return
	}
	if attempts > 60 {
		w.Header().Set("Retry-After", strconv.FormatInt(max(1, expires-now), 10))
		researchError(w, &research.Error{Status: 429, Code: "too_many_requests", Field: "/", Message: "请求过于频繁，请按 Retry-After 重试"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/research/v1/")
	switch {
	case path == "task" && r.Method == http.MethodGet:
		task, err := a.research.CurrentTask(producer.ID)
		if err != nil {
			researchError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"task": task})
	case path == "batches" && r.Method == http.MethodPost:
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			researchError(w, &research.Error{Status: 422, Code: "invalid_content_type", Field: "/", Message: "请使用 application/json"})
			return
		}
		if encoding := r.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
			researchError(w, &research.Error{Status: 422, Code: "invalid_encoding", Field: "/", Message: "请发送未压缩的原始 JSON 字节"})
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				researchError(w, &research.Error{Status: 413, Code: "body_too_large", Field: "/", Message: "请求不能超过 1 MiB"})
			} else {
				researchError(w, &research.Error{Status: 400, Code: "invalid_body", Field: "/", Message: "无法完整读取请求体"})
			}
			return
		}
		reception, err := a.research.Receive(producer, raw)
		if err != nil {
			researchError(w, err)
			return
		}
		var receipt struct {
			URL string `json:"receipt_url"`
		}
		if err = json.Unmarshal(reception, &receipt); err != nil {
			researchError(w, err)
			return
		}
		w.Header().Set("Location", receipt.URL)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(202)
		w.Write(reception)
	case strings.HasPrefix(path, "batches/") && r.Method == http.MethodGet:
		id := strings.TrimPrefix(path, "batches/")
		if id == "" || len(id) > 128 || strings.Contains(id, "/") {
			researchError(w, &research.Error{Status: 404, Code: "batch_not_found", Field: "/batch_id", Message: "回执不存在"})
			return
		}
		receipt, err := a.research.Receipt(producer.ID, id)
		if err != nil {
			researchError(w, err)
			return
		}
		writeJSON(w, 200, receipt)
	case path == "task" || path == "batches":
		allow := "GET"
		if path == "batches" {
			allow = "POST"
		}
		w.Header().Set("Allow", allow)
		researchError(w, &research.Error{Status: 405, Code: "method_not_allowed", Field: "/", Message: "请求方法不支持"})
	default:
		researchError(w, &research.Error{Status: 404, Code: "not_found", Field: "/", Message: "接口不存在"})
	}
}
func (a *App) invalidProducer(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="creative-research"`)
	researchError(w, &research.Error{Status: 401, Code: "invalid_token", Field: "/", Message: "请使用有效执行端 Bearer 令牌"})
}
