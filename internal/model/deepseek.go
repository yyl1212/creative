package model

import "encoding/json"

func deepSeekRequest(model, system, input string) (string, any) {
	return "https://api.deepseek.com/chat/completions", map[string]any{"model": model, "max_tokens": 12000, "thinking": map[string]string{"type": "disabled"}, "response_format": map[string]string{"type": "json_object"}, "messages": []any{map[string]string{"role": "system", "content": system}, map[string]string{"role": "user", "content": input}}}
}
func readDeepSeek(raw []byte) (string, Usage, error) {
	var v struct {
		ID      string `json:"id"`
		Choices []struct {
			Finish  string `json:"finish_reason"`
			Message struct {
				Content *string `json:"content"`
				Refusal *string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			Input  *int `json:"prompt_tokens"`
			Output *int `json:"completion_tokens"`
		} `json:"usage"`
	}
	u := Usage{}
	if e := json.Unmarshal(raw, &v); e != nil {
		return "", u, e
	}
	u.ResponseID = v.ID
	if v.Usage != nil && v.Usage.Input != nil && v.Usage.Output != nil && *v.Usage.Input >= 0 && *v.Usage.Output >= 0 {
		u.InputTokens = *v.Usage.Input
		u.OutputTokens = *v.Usage.Output
		u.TokensKnown = true
	}
	if len(v.Choices) != 1 || v.Choices[0].Finish != "stop" || v.Choices[0].Message.Content == nil || v.Choices[0].Message.Refusal != nil {
		return "", u, ErrUnavailable
	}
	return *v.Choices[0].Message.Content, u, nil
}
