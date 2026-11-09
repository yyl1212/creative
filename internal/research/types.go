package research

import (
	"encoding/json"
	"github.com/yyl1212/creative/internal/usage"
)

type batch struct {
	SchemaVersion string      `json:"schema_version"`
	RulesVersion  string      `json:"rules_version"`
	TaskID        string      `json:"task_id"`
	BatchID       string      `json:"batch_id"`
	RunID         string      `json:"run_id"`
	Candidates    []candidate `json:"candidates"`
	RunReport     struct {
		State string          `json:"state"`
		Usage []ExternalUsage `json:"usage"`
	} `json:"run_report"`
}
type candidate struct {
	ExternalID   string     `json:"external_id"`
	Title        string     `json:"title"`
	Language     string     `json:"language"`
	TargetUser   string     `json:"target_user"`
	Job          string     `json:"job_to_be_done"`
	Constraints  []string   `json:"constraints"`
	Evidence     []Evidence `json:"evidence"`
	Alternatives []struct {
		URL string `json:"url"`
	} `json:"alternatives"`
	Monetization string   `json:"monetization_proposal"`
	Unknowns     []string `json:"unknowns"`
}
type Price struct {
	Amount   *string `json:"amount"`
	Currency *string `json:"currency"`
	Unit     *string `json:"unit"`
}
type Evidence struct {
	ID          string   `json:"evidence_id"`
	URL         string   `json:"url"`
	AuthorRef   *string  `json:"author_ref"`
	PublishedAt *string  `json:"published_at"`
	ObservedAt  string   `json:"observed_at"`
	Quote       string   `json:"quote"`
	Signal      string   `json:"signal"`
	Price       *Price   `json:"price"`
	Conditions  []string `json:"conditions"`
	UnmetNeed   string   `json:"unmet_need"`
}
type ExternalUsage = usage.External

func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }
