package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/yyl1212/creative/migrations"
	"os"
	"path/filepath"
	"testing"
)

func TestReview002UsageMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old002.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL)"); e != nil {
		t.Fatal(e)
	}
	for i, name := range []string{"001_initial.sql", "002_research.sql"} {
		raw, e := migrations.Files.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(raw)); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec("INSERT INTO schema_migrations VALUES (?,?)", i+1, fmt.Sprintf("%x", sha256.Sum256(raw))); e != nil {
			t.Fatal(e)
		}
	}
	for _, q := range []string{
		`INSERT INTO producers(id,name,token_hash) VALUES ('p','fixture','fake')`,
		`INSERT INTO research_tasks(id,producer_id,topics,languages,target_count,rules_version,rules,state) VALUES ('t','p','[]','[]',1,'strong-wtp.v1','{}','active')`,
		`INSERT INTO research_batches(producer_id,batch_id,task_id,rules_version,run_id,payload,payload_hash,reception) VALUES ('p','b','t','strong-wtp.v1','r','{"run_report":{"usage":[{"usage_id":"u","provider":"deepseek","model":"deepseek-fixture","reported_cost":"1.25","currency":"USD","billing_reference":"bill","cost_status":"reported"}]}}','hash','{}')`,
	} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	raw, e := os.ReadFile("../research/testdata/batch.json")
	if e != nil {
		t.Fatal(e)
	}
	var payload map[string]any
	if e = json.Unmarshal(raw, &payload); e != nil {
		t.Fatal(e)
	}
	payload["task_id"] = "t"
	payload["batch_id"] = "b"
	payload["run_id"] = "r"
	payload["run_report"].(map[string]any)["usage"] = []any{map[string]any{"usage_id": "u", "provider": "deepseek", "model": "deepseek-fixture", "reported_cost": "1.25", "currency": "USD", "billing_reference": "bill", "cost_status": "reported"}}
	raw, e = json.Marshal(payload)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("UPDATE research_batches SET payload=?,payload_hash=?", raw, fmt.Sprintf("%x", sha256.Sum256(raw))); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO research_candidates(batch_row,external_id,title,ordinal) VALUES (1,'need-example-001','商品表格模板转换',0)"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("UPDATE research_batches SET created_at=123456"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	db, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var n int
	if e = db.QueryRow("SELECT count(*) FROM usage_records").Scan(&n); e != nil {
		t.Fatal(e)
	}
	var received int64
	if e = db.QueryRow("SELECT created_at FROM usage_records").Scan(&received); e != nil || received != 123456 {
		t.Fatal("original received time lost", received, e)
	}
	if n != 1 {
		t.Fatalf("old 002 reported USD 1.25 absent after 003 migration: usage_records=%d", n)
	}
}
