package research

import (
	"database/sql"
	"github.com/yyl1212/creative/internal/auth"
	"strings"
	"testing"
	"time"
)

func reportFixture(t *testing.T) (*Service, *sql.DB, auth.Producer, *Task, []byte) {
	t.Helper()
	s, db, p, old, _, _ := setupVerification(t)
	if _, e := s.ChangeTask(old.ID, old.Version, "cancel", "owner", "test"); e != nil {
		t.Fatal(e)
	}
	task, e := s.CreateProjectTask(p.ID, []string{"export"}, []string{"en"}, 1, "owner", "test")
	if e != nil {
		t.Fatal(e)
	}
	raw := change(t, fixture(t, task.ID), func(v map[string]any) {
		v["rules_version"] = task.RulesVersion
		v["candidates"] = []any{}
		report := v["run_report"].(map[string]any)
		report["usage"] = []any{map[string]any{"usage_id": "report-charge", "provider": "deepseek", "model": "deepseek-fixture", "reported_cost": nil, "currency": nil, "billing_reference": nil, "cost_status": "unknown"}}
	})
	return s, db, p, task, raw
}

func TestExternalReportCompletesAtomicallyAndReplays(t *testing.T) {
	for _, state := range []string{"submitted", "waiting_for_signals", "paused_service"} {
		t.Run(state, func(t *testing.T) {
			s, db, p, _, raw := reportFixture(t)
			raw = change(t, raw, func(v map[string]any) { v["run_report"].(map[string]any)["state"] = state })
			first, e := s.ReceiveReport(p, raw)
			if e != nil || !strings.Contains(string(first), `"status":"completed"`) {
				t.Fatalf("%s %v", first, e)
			}
			expected := state
			if state == "submitted" {
				expected = "active"
			}
			receipt, e := s.Receipt(p, "batch-example-001")
			if e != nil || receipt.Status != "completed" || len(receipt.Results) != 0 || receipt.TaskState != expected || receipt.RemainingGap != 1 {
				t.Fatalf("%+v %v", receipt, e)
			}
			second, e := s.ReceiveReport(p, raw)
			if e != nil || string(first) != string(second) {
				t.Fatalf("replay %s %v", second, e)
			}
			_, e = s.ReceiveReport(p, append(raw, ' '))
			code(t, e, 409)
			var n int
			if e = db.QueryRow("SELECT count(*) FROM usage_records").Scan(&n); e != nil || n != 1 {
				t.Fatalf("usage %d %v", n, e)
			}
			if e = db.QueryRow("SELECT count(*) FROM research_batches WHERE status!='completed'").Scan(&n); e != nil || n != 0 {
				t.Fatalf("queued %d %v", n, e)
			}
		})
	}
}

func TestExternalReportRejectsInactiveAndLegacyTasks(t *testing.T) {
	for _, state := range []string{"cancelled", "waiting_for_signals", "paused_service", "target_reached", "superseded"} {
		t.Run(state, func(t *testing.T) {
			s, db, p, task, raw := reportFixture(t)
			switch state {
			case "target_reached":
				catalog, e := s.ProjectCatalog(p)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.ImportProject(p, projectInput(t, s, task.ID, catalog.Revision)); e != nil {
					t.Fatal(e)
				}
			case "superseded":
				if _, e := db.Exec("UPDATE research_tasks SET is_current=0 WHERE id=?", task.ID); e != nil {
					t.Fatal(e)
				}
			default:
				if _, e := db.Exec("UPDATE research_tasks SET state=? WHERE id=?", state, task.ID); e != nil {
					t.Fatal(e)
				}
			}
			_, e := s.ReceiveReport(p, raw)
			code(t, e, 409)
			var n int
			db.QueryRow("SELECT count(*) FROM research_batches WHERE batch_id='batch-example-001'").Scan(&n)
			if n != 0 {
				t.Fatal("rejected report persisted")
			}
		})
	}
	s, _, p, task, _, _ := setupVerification(t)
	empty := change(t, fixture(t, task.ID), func(v map[string]any) { v["candidates"] = []any{} })
	_, e := s.ReceiveReport(p, empty)
	code(t, e, 409)
}

func TestExternalReportRetiresCandidatesButPreservesOldReplay(t *testing.T) {
	s, _, p, task, _, _ := setupVerification(t)
	raw := fixture(t, task.ID)
	_, e := s.ReceiveReport(p, raw)
	code(t, e, 410)
	first, e := s.Receive(p, raw)
	if e != nil {
		t.Fatal(e)
	} // 历史版本写入的记录。
	if _, e = s.ChangeTask(task.ID, task.Version, "cancel", "owner", "test"); e != nil {
		t.Fatal(e)
	}
	retry, e := s.ReceiveReport(p, raw)
	if e != nil || string(first) != string(retry) {
		t.Fatalf("historical replay %s %v", retry, e)
	}
	_, e = s.ReceiveReport(p, append(raw, ' '))
	code(t, e, 409)
	receipt, e := s.Receipt(p, "batch-example-001")
	if e != nil || receipt.ShouldContinue {
		t.Fatalf("legacy instructed to search %+v %v", receipt, e)
	}
	if len(receipt.Results) != 1 || !strings.Contains(strings.Join(receipt.Results[0].Suggestions, " "), "旧核验已停用") {
		t.Fatalf("legacy receipt still waits for website: %+v", receipt.Results)
	}
}

func TestExternalReportFailureRollsBackStateAndBatch(t *testing.T) {
	s, db, p, task, raw := reportFixture(t)
	raw = change(t, raw, func(v map[string]any) { v["run_report"].(map[string]any)["state"] = "waiting_for_signals" })
	if _, e := db.Exec("CREATE TRIGGER reject_usage BEFORE INSERT ON usage_records BEGIN SELECT RAISE(ABORT,'fixture storage failure'); END"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReceiveReport(p, raw); e == nil {
		t.Fatal("storage failure ignored")
	}
	var count int
	db.QueryRow("SELECT count(*) FROM research_batches").Scan(&count)
	if count != 0 {
		t.Fatal("partial batch committed")
	}
	current, e := s.CurrentTask(p)
	if e != nil || current.State != "active" || current.Version != task.Version {
		t.Fatalf("partial state %+v %v", current, e)
	}
}

func TestMaintenanceDoesNotResumeExternalResearch(t *testing.T) {
	s, db, p, task, _ := reportFixture(t)
	if _, e := db.Exec("UPDATE research_tasks SET state='waiting_for_signals',updated_at=? WHERE id=?", time.Now().Add(-48*time.Hour).Unix(), task.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.Maintenance(time.Now()); e != nil {
		t.Fatal(e)
	}
	current, e := s.CurrentTask(p)
	if e != nil || current.State != "waiting_for_signals" || current.ShouldContinue {
		t.Fatalf("unexpected resume %+v %v", current, e)
	}
}
