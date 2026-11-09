package research

import (
	"database/sql"
	"time"
)

func qualifiedCount(tx *sql.Tx, now time.Time) (int, error) {
	var count int
	e := tx.QueryRow("SELECT count(*) FROM needs n WHERE n.status='qualified' AND n.rules_version=? AND n.verified_at>0 AND n.valid_until>? AND NOT EXISTS (SELECT 1 FROM project_imports i WHERE i.need_id=n.id) AND NOT EXISTS (SELECT 1 FROM development_records d WHERE d.need_id=n.id AND d.need_version=n.version AND d.verification_id=n.verification_id AND d.state!='unverified')", rulesVersion, now.Unix()).Scan(&count)
	return count, e
}

const projectCountSQL = `(SELECT count(DISTINCT i.need_id) FROM project_imports i JOIN research_batches b ON b.id=i.batch_row WHERE b.task_id=research_tasks.id)`

func recomputeTasks(tx *sql.Tx, now time.Time) error {
	count, e := qualifiedCount(tx, now)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`UPDATE research_tasks SET state=CASE WHEN target_count<=? THEN 'target_reached' ELSE 'active' END,version=version+1,updated_at=? WHERE coalesce(json_extract(rules,'$.decision_mode'),'')!='agent_project' AND is_current=1 AND state IN ('active','target_reached') AND state!=CASE WHEN target_count<=? THEN 'target_reached' ELSE 'active' END`, count, now.Unix(), count)
	if e != nil {
		return e
	}
	_, e = tx.Exec("UPDATE research_tasks SET state='target_reached',version=version+1,updated_at=? WHERE coalesce(json_extract(rules,'$.decision_mode'),'')!='agent_project' AND is_current=1 AND state='waiting_for_signals' AND target_count<=?", now.Unix(), count)
	if e != nil {
		return e
	}
	_, e = tx.Exec("UPDATE research_tasks SET state=CASE WHEN target_count<="+projectCountSQL+" THEN 'target_reached' ELSE 'active' END,version=version+1,updated_at=? WHERE json_extract(rules,'$.decision_mode')='agent_project' AND is_current=1 AND (state IN ('active','target_reached') OR state='waiting_for_signals' AND target_count<="+projectCountSQL+") AND state!=CASE WHEN target_count<="+projectCountSQL+" THEN 'target_reached' ELSE 'active' END", now.Unix())
	return e
}
func hydrateTask(tx *sql.Tx, t *Task, now time.Time) error {
	n, e := qualifiedCount(tx, now)
	if e != nil {
		return e
	}
	if projectTask(t) {
		if e = tx.QueryRow("SELECT "+projectCountSQL+" FROM research_tasks WHERE id=?", t.ID).Scan(&n); e != nil {
			return e
		}
	}
	t.QualifiedCount = n
	t.RemainingGap = max(0, t.TargetCount-n)
	t.ShouldContinue = t.Current && t.State == "active" && t.RemainingGap > 0
	return nil
}
func (s *Service) Maintenance(now time.Time) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	statements := []struct {
		query string
		args  []any
	}{
		{"UPDATE needs SET status='pending',version=version+1 WHERE status='qualified' AND (valid_until<=? OR rules_version!=?)", []any{now.Unix(), rulesVersion}},
		{"UPDATE research_tasks SET state='active',version=version+1,updated_at=? WHERE is_current=1 AND state='waiting_for_signals' AND updated_at<=?", []any{now.Unix(), now.Add(-24 * time.Hour).Unix()}},
		{"UPDATE research_batches SET payload=X'',raw_purged=1,version=version+1 WHERE created_at<=? AND raw_purged=0", []any{now.AddDate(0, 0, -30).Unix()}},
		{"UPDATE need_sources SET raw=NULL WHERE fetched_at<=?", []any{now.AddDate(0, 0, -30).Unix()}},
		{"DELETE FROM need_sources WHERE fetched_at<=?", []any{now.AddDate(0, 0, -180).Unix()}},
		{"UPDATE needs SET verification_id=NULL WHERE verification_id IN (SELECT id FROM need_verifications WHERE verified_at<=?)", []any{now.AddDate(0, 0, -180).Unix()}},
		{"DELETE FROM need_verifications WHERE verified_at<=?", []any{now.AddDate(0, 0, -180).Unix()}},
		{"UPDATE needs SET title='',task='',task_key='',task_fingerprint='',decision='{}',version=version+1 WHERE updated_at>0 AND updated_at<=? AND status!='completed' AND title!=''", []any{now.AddDate(0, 0, -180).Unix()}},
		{"DELETE FROM need_aliases WHERE latest_batch IN (SELECT id FROM research_batches WHERE created_at<=?) AND (need_id IS NULL OR need_id IN (SELECT id FROM needs WHERE status!='completed'))", []any{now.AddDate(0, 0, -180).Unix()}},
		{"DELETE FROM research_candidates WHERE batch_row IN (SELECT id FROM research_batches WHERE created_at<=? AND metadata_purged=0)", []any{now.AddDate(0, 0, -180).Unix()}},
		{"UPDATE research_batches SET metadata_purged=1,status=CASE WHEN status='completed' THEN status ELSE 'paused_service' END,pause_reason='metadata_expired',version=version+1 WHERE created_at<=? AND metadata_purged=0", []any{now.AddDate(0, 0, -180).Unix()}},
	}
	for _, st := range statements {
		if _, e = tx.Exec(st.query, st.args...); e != nil {
			return e
		}
	}
	if e = recomputeTasks(tx, now); e != nil {
		return e
	}
	return tx.Commit()
}
