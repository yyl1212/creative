package research

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yyl1212/creative/internal/model"
	"github.com/yyl1212/creative/internal/source"
)

var ErrLeaseLost = errors.New("verification lease lost")

type Lease struct {
	Owner   string
	Fence   int64
	BatchID int
}
type SourceFetcher interface {
	Fetch(context.Context, string) (source.Document, error)
}
type SemanticReviewer interface {
	Ready() error
	Review(context.Context, model.Input) (model.Assessment, error)
}

func (s *Service) Claim(ctx context.Context, owner string, now time.Time) (*Lease, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	r, e := tx.Exec("UPDATE research_worker SET owner=?,fence=fence+1,expires_at=? WHERE id=1 AND expires_at<=?", owner, now.Add(2*time.Minute).Unix(), now.Unix())
	if e != nil {
		return nil, e
	}
	n, e := r.RowsAffected()
	if e != nil || n == 0 {
		return nil, e
	}
	l := &Lease{Owner: owner}
	if e = tx.QueryRow("SELECT fence FROM research_worker WHERE id=1").Scan(&l.Fence); e != nil {
		return nil, e
	}
	e = tx.QueryRow("SELECT id FROM research_batches WHERE status IN ('pending_validation','validating') ORDER BY id LIMIT 1").Scan(&l.BatchID)
	if e == sql.ErrNoRows {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec("UPDATE research_batches SET status='validating',version=version+1 WHERE id=?", l.BatchID); e != nil {
		return nil, e
	}
	return l, tx.Commit()
}
func (s *Service) Heartbeat(ctx context.Context, l Lease, now time.Time) error {
	r, e := s.db.ExecContext(ctx, "UPDATE research_worker SET expires_at=? WHERE id=1 AND owner=? AND fence=? AND expires_at>?", now.Add(2*time.Minute).Unix(), l.Owner, l.Fence, now.Unix())
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
func checkLease(tx *sql.Tx, l Lease) error {
	var n int
	e := tx.QueryRow("SELECT count(*) FROM research_worker WHERE id=1 AND owner=? AND fence=? AND expires_at>?", l.Owner, l.Fence, time.Now().Unix()).Scan(&n)
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (s *Service) Process(ctx context.Context, l Lease, f SourceFetcher, r SemanticReviewer, now time.Time) error {
	started := time.Now()
	if e := s.Heartbeat(ctx, l, time.Now()); e != nil {
		return e
	}
	var raw []byte
	var producer, rule string
	if e := s.db.QueryRowContext(ctx, "SELECT payload,producer_id,rules_version FROM research_batches WHERE id=?", l.BatchID).Scan(&raw, &producer, &rule); e != nil {
		return e
	}
	if len(raw) == 0 {
		return s.finishBatch(l, "paused_service", "raw_material_expired", "")
	}
	var b batch
	if e := json.Unmarshal(raw, &b); e != nil {
		return s.finishBatch(l, "paused_service", "stored_payload_invalid", "")
	}
	if rule != rulesVersion {
		return s.finishBatch(l, "paused_service", "rules_changed", "")
	}
	if len(b.Candidates) > 0 && r.Ready() != nil {
		return s.finishBatch(l, "paused_service", "model_unavailable", "")
	}
	for _, c := range b.Candidates {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var done bool
		if e := s.db.QueryRowContext(ctx, "SELECT result IS NOT NULL FROM research_candidates WHERE batch_row=? AND external_id=?", l.BatchID, c.ExternalID).Scan(&done); e != nil {
			return e
		}
		if done {
			continue
		}
		if e := s.Heartbeat(ctx, l, time.Now()); e != nil {
			return e
		}
		sources := make([]BoundSource, 0, len(c.Evidence))
		for _, ev := range c.Evidence {
			d, e := f.Fetch(ctx, ev.URL)
			bound := BoundSource{ID: ev.ID, Document: d}
			if e != nil {
				bound.Issue = "source_unavailable"
			} else {
				bound.Block, e = d.Bind(ev.Quote)
				if e != nil {
					bound.Issue = "metadata_unverified"
				}
			}
			sources = append(sources, bound)
		}
		prior, e := s.priorNeeds(ctx, producer, c.ExternalID)
		if e != nil && !errors.Is(e, model.ErrContextTooLarge) {
			return e
		}
		reviewSources := append([]BoundSource(nil), sources...)
		for i := range reviewSources {
			reviewSources[i].Document.Text = ""
			reviewSources[i].Document.Blocks = nil
			reviewSources[i].Document.Raw = nil
		}
		var assessment model.Assessment
		if e == nil {
			assessment, e = r.Review(ctx, model.Input{Candidate: c, Sources: reviewSources, PriorNeeds: prior})
		}
		if e != nil && !errors.Is(e, model.ErrContextTooLarge) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return s.finishBatch(l, "paused_service", "model_unavailable", "")
		}
		verifiedAt := now.Add(time.Since(started))
		decision := Evaluate(c, sources, assessment, verifiedAt)
		if errors.Is(e, model.ErrContextTooLarge) {
			decision.Outcome = "needs_evidence"
			decision.Reasons = []string{"context_requires_review"}
		}
		if e = s.checkpoint(l, producer, c, sources, assessment, prior, decision, verifiedAt); e != nil {
			return e
		}
	}
	return s.finishBatch(l, "completed", "", b.RunReport.State)
}
func (s *Service) priorNeeds(ctx context.Context, producer, external string) ([]model.PriorNeed, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT id,task FROM needs WHERE task!='' AND id!=coalesce((SELECT need_id FROM need_aliases WHERE producer_id=? AND external_id=?),0) ORDER BY id", producer, external)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.PriorNeed{}
	size := 0
	for rows.Next() {
		var n model.PriorNeed
		if e = rows.Scan(&n.ID, &n.Task); e != nil {
			return nil, e
		}
		size += len(n.Task) + 32
		if size > 96<<10 {
			return nil, model.ErrContextTooLarge
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Service) finishBatch(l Lease, status, reason, reportState string) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = checkLease(tx, l); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE research_batches SET status=?,pause_reason=?,version=version+1 WHERE id=?", status, reason, l.BatchID); e != nil {
		return e
	}
	state := ""
	if status == "paused_service" || reportState == "paused_service" {
		state = "paused_service"
	} else if reportState == "waiting_for_signals" {
		state = "waiting_for_signals"
	}
	if state != "" {
		if _, e = tx.Exec("UPDATE research_tasks SET state=?,version=version+1,updated_at=unixepoch() WHERE id=(SELECT task_id FROM research_batches WHERE id=?) AND state!='cancelled' AND is_current=1 AND (?='paused_service' OR state!='target_reached')", state, l.BatchID, status); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("UPDATE research_worker SET expires_at=0 WHERE id=1 AND owner=? AND fence=?", l.Owner, l.Fence); e != nil {
		return e
	}
	if e = recomputeTasks(tx, time.Now()); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) RetryBatch(id, version int, actor, origin string) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var state, taskState string
	var currentVersion, purged int
	e = tx.QueryRow("SELECT b.status,b.version,b.raw_purged,t.state FROM research_batches b JOIN research_tasks t ON t.id=b.task_id WHERE b.id=?", id).Scan(&state, &currentVersion, &purged, &taskState)
	if e == sql.ErrNoRows {
		return fail(404, "batch_not_found", "/id", "批次不存在")
	}
	if e != nil {
		return e
	}
	if currentVersion != version || state != "paused_service" || purged != 0 || taskState == "cancelled" {
		return fail(409, "batch_changed", "/version", "批次不可重试，请刷新；过期材料须重新提交")
	}
	if _, e = tx.Exec("UPDATE research_batches SET status='pending_validation',pause_reason='',version=version+1 WHERE id=?", id); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE research_tasks SET state='active',version=version+1,updated_at=unixepoch() WHERE id=(SELECT task_id FROM research_batches WHERE id=?) AND state='paused_service' AND is_current=1", id); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO audit_events(action,account,source,object_id,details) VALUES ('batch_retry',?,?,?,?)", actor, origin, fmt.Sprint(id), fmt.Sprintf(`{"version":%d}`, version)); e != nil {
		return e
	}
	return tx.Commit()
}
