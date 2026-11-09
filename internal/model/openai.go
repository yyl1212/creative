package model

import "encoding/json"

func openAIRequest(model, system, input string) (string, any) {
	return "https://api.openai.com/v1/responses", map[string]any{"model": model, "store": false, "max_output_tokens": 12000, "input": []any{map[string]string{"role": "system", "content": system}, map[string]string{"role": "user", "content": input}}, "text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "demand_review", "strict": true, "schema": json.RawMessage(assessmentSchema)}}}
}
func readOpenAI(raw []byte) (string, Usage, error) {
	var v struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  any    `json:"error"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage *struct {
			Input  *int `json:"input_tokens"`
			Output *int `json:"output_tokens"`
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
	if v.Status != "completed" || v.Error != nil {
		return "", u, ErrUnavailable
	}
	text := ""
	for _, o := range v.Output {
		if o.Type != "message" {
			continue
		}
		for _, c := range o.Content {
			if c.Type != "output_text" {
				return "", u, ErrUnavailable
			}
			text += c.Text
		}
	}
	return text, u, nil
}
