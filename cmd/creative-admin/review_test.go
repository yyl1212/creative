package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewCheckCommand(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "must-not-exist.db")
	t.Setenv("CREATIVE_DB", db)
	input := filepath.Join(dir, "review.json")
	raw := []byte(`{"schema_version":"creative.agent-review.v1","rules_version":"strong-wtp.v1","provider":"openai","model":"gpt-fixture","reviewed_at":"` + time.Now().UTC().Format(time.RFC3339) + `","reasoning":"证据不足","deduplication":"尚未发现同类任务","catalog_revision":"` + strings.Repeat("a", 64) + `","task_key":"export-columns","verdict":"needs_evidence","self_service":true,"distinct_from_existing":true,"candidate":{"external_id":"export-columns","title":"导出列","language":"zh","target_user":"表格用户","job_to_be_done":"完整导出列","constraints":[],"evidence":[],"alternatives":[],"monetization_proposal":"undecided","unknowns":[]},"evidence":[]}`)
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := reviewCheck([]string{"--file", input}, &output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Eligible bool   `json:"eligible"`
		Hash     string `json:"review_sha256"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Eligible || len(result.Hash) != 64 {
		t.Fatalf("%s %v", output.Bytes(), err)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatal("review command accessed database", err)
	}
	for _, args := range [][]string{nil, {"--file", input, "extra"}, {"--file", input, "--project", filepath.Join(dir, "missing")}, {"--file", dir}} {
		output.Reset()
		if err := reviewCheck(args, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid command produced success output", args, err)
		}
	}
	for _, bad := range [][]byte{raw[:len(raw)-1], []byte(strings.Repeat(" ", 1<<20+1))} {
		if err := os.WriteFile(input, bad, 0600); err != nil {
			t.Fatal(err)
		}
		output.Reset()
		if err := reviewCheck([]string{"--file", input}, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid JSON produced success output", err)
		}
	}
}
