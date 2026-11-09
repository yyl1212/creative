package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type journal struct {
	started, finished int
	usage             []Usage
}

func (j *journal) Start(context.Context, string, string) (int64, error) {
	j.started++
	return int64(j.started), nil
}
func (j *journal) Finish(_ context.Context, _ int64, u Usage) error {
	j.finished++
	j.usage = append(j.usage, u)
	return nil
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const answer = `{"self_service":true,"task_key":"batch-export","duplicate_need_id":null,"distinct_from_existing":true,"evidence":[]}`

func configured(t *testing.T, p string, j *journal) *Reviewer {
	t.Helper()
	t.Setenv(strings.ToUpper(p)+"_API_KEY", "fixture-secret")
	r, err := New(Config{Primary: p, Providers: map[string]Provider{p: {Enabled: true, Validated: true, Model: map[string]string{"openai": "gpt-fixture", "deepseek": "deepseek-fixture"}[p]}}}, j)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestProviderResponses(t *testing.T) {
	for _, p := range []string{"openai", "deepseek"} {
		t.Run(p, func(t *testing.T) {
			j := &journal{}
			r := configured(t, p, j)
			r.client.Transport = transport(func(req *http.Request) (*http.Response, error) {
				var body map[string]any
				_ = json.NewDecoder(req.Body).Decode(&body)
				if req.Header.Get("Authorization") != "Bearer fixture-secret" {
					t.Fatal("missing credential")
				}
				if p == "openai" {
					if req.URL.Path != "/v1/responses" || body["store"] != false || body["text"] == nil {
						t.Fatalf("request %+v %s", body, req.URL)
					}
				} else {
					if req.URL.Path != "/chat/completions" || body["response_format"] == nil {
						t.Fatalf("request %+v", body)
					}
				}
				var payload any
				if p == "openai" {
					payload = map[string]any{"id": "response-1", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": answer}}}}, "usage": map[string]any{"input_tokens": 12, "output_tokens": 9}}
				} else {
					payload = map[string]any{"id": "response-1", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": answer}}}, "usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 9}}
				}
				b, _ := json.Marshal(payload)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b))), Header: http.Header{}}, nil
			})
			a, err := r.Review(context.Background(), Input{})
			if err != nil || !a.SelfService || j.started != 1 || j.finished != 1 || j.usage[0].InputTokens != 12 {
				t.Fatalf("%+v %v %+v", a, err, j)
			}
		})
	}
	for _, content := range []string{" ", "{", strings.Replace(answer, "true", "true,\"extra\":0", 1), `{"self_service":true}`} {
		j := &journal{}
		r := configured(t, "deepseek", j)
		r.client.Transport = transport(func(*http.Request) (*http.Response, error) {
			b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": content}}}})
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
		})
		if _, err := r.Review(context.Background(), Input{}); err == nil || j.started != 2 {
			t.Fatalf("bad content accepted/retries %q: %v %+v", content, err, j)
		}
	}
}
func TestProviderConfig(t *testing.T) {
	if _, err := New(Config{Primary: "other", Providers: map[string]Provider{"other": {}}}, &journal{}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	r, err := New(Config{Primary: "openai", Providers: map[string]Provider{"openai": {}}}, &journal{})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(r.Ready(), ErrUnavailable) {
		t.Fatal("disabled ready")
	}
}
func TestUnknownCallAfterInterruption(t *testing.T) {
	j := &journal{}
	r := configured(t, "openai", j)
	ctx, cancel := context.WithCancel(context.Background())
	r.client.Transport = transport(func(*http.Request) (*http.Response, error) { cancel(); return nil, context.Canceled })
	if _, err := r.Review(ctx, Input{}); err == nil || j.started != 1 || j.finished != 0 {
		t.Fatalf("interrupted call lost: %+v %v", j, err)
	}
}

func TestProviderFailureBoundaries(t *testing.T) {
	for _, p := range []string{"openai", "deepseek"} {
		for _, payload := range []string{`{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"{}"}]}]}`, `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"no"}]}]}`, `{"choices":[{"finish_reason":"length","message":{"content":"{}"}}]}`, `{"choices":[{"finish_reason":"stop","message":{"content":"{}","refusal":"no"}}]}`} {
			j := &journal{}
			r := configured(t, p, j)
			r.client.Transport = transport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}, nil
			})
			if _, err := r.Review(context.Background(), Input{}); err == nil || j.started != 1 {
				t.Fatalf("refusal/truncation accepted: %s %v", payload, err)
			}
		}
	}
	j := &journal{}
	r := configured(t, "openai", j)
	r.client.Transport = transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
	})
	if _, err := r.Review(context.Background(), Input{}); err == nil || j.started != 3 || j.finished != 0 {
		t.Fatalf("retry or unknown usage: %+v %v", j, err)
	}
}
