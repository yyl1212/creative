package research

import (
	"database/sql"
	"fmt"
	"github.com/yyl1212/creative/internal/model"
	"strings"
	"time"
)

func (s *Service) checkpoint(l Lease, producer string, c candidate, sources []BoundSource, a model.Assessment, prior []model.PriorNeed, d Decision, now time.Time) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = checkLease(tx, l); e != nil {
		return e
	}
	var result *string
	var existing *int
	var latest int
	if e = tx.QueryRow("SELECT result FROM research_candidates WHERE batch_row=? AND external_id=?", l.BatchID, c.ExternalID).Scan(&result); e != nil {
		return e
	}
	if result != nil {
		return nil
	}
	e = tx.QueryRow("SELECT need_id,latest_batch FROM need_aliases WHERE producer_id=? AND external_id=?", producer, c.ExternalID).Scan(&existing, &latest)
	if e != nil {
		return e
	}
	outcome := d.Outcome
	needID := existing
	if latest != l.BatchID {
		outcome = "needs_evidence"
		d.Reasons = []string{"superseded_by_supplement"}
	} else {
		if existing == nil {
			var found int
			e = tx.QueryRow("SELECT id FROM needs WHERE task_fingerprint=? OR (task_key!='' AND task_key=?) ORDER BY id LIMIT 1", d.TaskFingerprint, d.TaskKey).Scan(&found)
			if e != nil && e != sql.ErrNoRows {
				return e
			}
			if e == nil {
				needID = &found
				outcome = "duplicate"
			}
			if needID == nil && a.DuplicateNeedID != nil {
				for _, p := range prior {
					if p.ID == *a.DuplicateNeedID {
						found = p.ID
						needID = &found
						outcome = "duplicate"
						break
					}
				}
				if needID == nil {
					d.Outcome = "needs_evidence"
					outcome = d.Outcome
					d.Reasons = append(d.Reasons, "unknown_duplicate_reference")
				}
			}
			// 相同用户原文跨批重复使用，不能为另一个标题重新贡献资格。
			if needID == nil {
				for _, ev := range d.Evidence {
					if !ev.Valid {
						continue
					}
					e = tx.QueryRow("SELECT need_id FROM need_sources WHERE fingerprint=? LIMIT 1", ev.Fingerprint).Scan(&found)
					if e != nil && e != sql.ErrNoRows {
						return e
					}
					if e == nil {
						needID = &found
						outcome = "duplicate"
						break
					}
				}
			}
		}
		completed := false
		if needID != nil {
			var status string
			if e = tx.QueryRow("SELECT status FROM needs WHERE id=?", *needID).Scan(&status); e != nil {
				return e
			}
			completed = status == "completed"
			if completed {
				outcome = "duplicate"
			}
		}
		if outcome != "duplicate" && !completed {
			status := "pending"
			validUntil := int64(0)
			if outcome == "qualified" {
				status = "qualified"
				validUntil = d.ValidUntil.Unix()
			}
			if needID == nil {
				r, e := tx.Exec("INSERT INTO needs(status,title,task,task_key,task_fingerprint,rules_version,verified_at,valid_until,decision,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)", status, c.Title, c.Job, d.TaskKey, d.TaskFingerprint, rulesVersion, now.Unix(), validUntil, jsonText(d), now.Unix())
				if e != nil {
					return e
				}
				id, e := r.LastInsertId()
				if e != nil {
					return e
				}
				n := int(id)
				needID = &n
			} else {
				if _, e = tx.Exec("UPDATE needs SET status=?,title=?,task=?,task_key=?,task_fingerprint=?,rules_version=?,verified_at=?,valid_until=?,decision=?,updated_at=?,version=version+1 WHERE id=? AND status!='completed'", status, c.Title, c.Job, d.TaskKey, d.TaskFingerprint, rulesVersion, now.Unix(), validUntil, jsonText(d), now.Unix(), *needID); e != nil {
					return e
				}
				if _, e = tx.Exec("DELETE FROM need_sources WHERE need_id=?", *needID); e != nil {
					return e
				}
			}
			for i, b := range sources {
				snapshot := b
				snapshot.Document.Text = ""
				snapshot.Document.Blocks = nil
				snapshot.Document.Raw = nil
				excerpts := []string{c.Evidence[i].Quote}
				for _, m := range a.Evidence {
					if m.ID == b.ID {
						excerpts = append(excerpts, m.PaymentQuote, m.UnmetQuote, m.PriceQuote, m.SpendQuote)
						excerpts = append(excerpts, m.Conditions...)
					}
				}
				snapshot.Block.Text = strings.Join(excerpts, "\n")
				if _, e = tx.Exec("INSERT INTO need_sources(need_id,evidence_id,fingerprint,snapshot,raw,fetched_at) VALUES (?,?,?,?,?,?)", *needID, b.ID, d.Evidence[i].Fingerprint, jsonText(snapshot), b.Document.Raw, now.Unix()); e != nil {
					return e
				}
			}
		}
		if _, e = tx.Exec("UPDATE need_aliases SET need_id=? WHERE producer_id=? AND external_id=? AND latest_batch=?", needID, producer, c.ExternalID, l.BatchID); e != nil {
			return e
		}
	}
	issues := []string{}
	for _, ev := range d.Evidence {
		if !ev.Valid {
			issues = append(issues, ev.ID+": "+ev.Reason)
		}
	}
	suggestions := []string{}
	if outcome == "needs_evidence" {
		suggestions = append(suggestions, "请补充独立用户原始链接、可定位作者/日期及完整付费条件；不要推测未知事实")
	}
	r := Result{ExternalID: c.ExternalID, NeedID: needID, Outcome: &outcome, ValidationState: "completed", ReasonCodes: d.Reasons, Issues: issues, Suggestions: suggestions}
	if _, e = tx.Exec("UPDATE research_candidates SET result=?,need_id=? WHERE batch_row=? AND external_id=? AND result IS NULL", jsonText(r), needID, l.BatchID, c.ExternalID); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO audit_events(action,account,source,object_id,details) VALUES ('candidate_verified','worker','local',?,?)", fmt.Sprint(l.BatchID), jsonText(map[string]any{"external_id": c.ExternalID, "outcome": outcome, "need_id": needID})); e != nil {
		return e
	}
	if e = recomputeTasks(tx, now); e != nil {
		return e
	}
	return tx.Commit()
}
