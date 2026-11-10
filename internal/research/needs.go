package research

import (
	"database/sql"
	"encoding/json"
	"time"
)

type VerificationSummary struct {
	ID, BatchID         int
	VerifiedAt, Outcome string
}
type Need struct {
	ID                     int
	Title, Task, Status    string
	RulesVersion           string
	Version                int
	VerifiedAt, ValidUntil string
	Decision               Decision
	Sources                []BoundSource
	VerificationID         int
	Historical             bool
	History                []VerificationSummary
}

const needColumns = "id,title,task,status,version,verified_at,valid_until,decision,rules_version,coalesce(verification_id,0)"

func scanNeed(row interface{ Scan(...any) error }) (Need, error) {
	var n Need
	var verified, until int64
	var decision, rule string
	e := row.Scan(&n.ID, &n.Title, &n.Task, &n.Status, &n.Version, &verified, &until, &decision, &rule, &n.VerificationID)
	if e != nil {
		return n, e
	}
	n.RulesVersion = rule
	if n.Status == "qualified" && (!supportedRules(rule) || verified == 0 || until <= time.Now().Unix()) {
		n.Status = "expired"
	}
	if verified > 0 {
		n.VerifiedAt = time.Unix(verified, 0).UTC().Format(time.RFC3339)
	}
	if until > 0 {
		n.ValidUntil = time.Unix(until, 0).UTC().Format(time.RFC3339)
	}
	e = json.Unmarshal([]byte(decision), &n.Decision)
	if n.Status == "pending" && n.Decision.Outcome == "rejected" {
		n.Status = "rejected"
	}
	if n.Status == "pending" && until > 0 && until <= time.Now().Unix() {
		n.Status = "expired"
	}
	return n, e
}
func (s *Service) Needs(before int) ([]Need, int, error) {
	rows, e := s.db.Query("SELECT "+needColumns+" FROM needs WHERE (?=0 OR id<?) ORDER BY id DESC LIMIT 51", before, before)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []Need{}
	for rows.Next() {
		n, e := scanNeed(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, n)
	}
	if e = rows.Err(); e != nil {
		return nil, 0, e
	}
	next := 0
	if len(out) > 50 {
		out = out[:50]
		next = out[49].ID
	}
	return out, next, nil
}
func (s *Service) Need(id, selected int) (*Need, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	n, e := scanNeed(tx.QueryRow("SELECT "+needColumns+" FROM needs WHERE id=?", id))
	if e == sql.ErrNoRows {
		return nil, fail(404, "need_not_found", "/id", "需求不存在")
	}
	if e != nil {
		return nil, e
	}
	history, e := tx.Query("SELECT id,batch_row,verified_at,decision FROM need_verifications WHERE need_id=? ORDER BY id DESC LIMIT 50", id)
	if e != nil {
		return nil, e
	}
	for history.Next() {
		var v VerificationSummary
		var stamp int64
		var raw string
		if e = history.Scan(&v.ID, &v.BatchID, &stamp, &raw); e != nil {
			history.Close()
			return nil, e
		}
		var d Decision
		if e = json.Unmarshal([]byte(raw), &d); e != nil {
			history.Close()
			return nil, e
		}
		v.Outcome = d.Outcome
		v.VerifiedAt = time.Unix(stamp, 0).UTC().Format(time.RFC3339)
		n.History = append(n.History, v)
	}
	e = history.Err()
	history.Close()
	if e != nil {
		return nil, e
	}
	if selected > 0 {
		var decision string
		var stamp int64
		e = tx.QueryRow("SELECT v.decision,v.verified_at,b.rules_version FROM need_verifications v JOIN research_batches b ON b.id=v.batch_row WHERE v.need_id=? AND v.id=?", id, selected).Scan(&decision, &stamp, &n.RulesVersion)
		if e == sql.ErrNoRows {
			return nil, fail(404, "verification_not_found", "/verification", "核验版本不存在或已过保留期限")
		}
		if e != nil {
			return nil, e
		}
		n.Historical = selected != n.VerificationID
		n.VerificationID = selected
		n.Decision = Decision{}
		if e = json.Unmarshal([]byte(decision), &n.Decision); e != nil {
			return nil, e
		}
	}
	rows, e := tx.Query("SELECT snapshot FROM need_sources WHERE need_id=? AND verification_id=? ORDER BY evidence_id", id, n.VerificationID)
	if e != nil {
		return nil, e
	}
	n.Sources = []BoundSource{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return nil, e
		}
		var b BoundSource
		if e = json.Unmarshal([]byte(raw), &b); e != nil {
			rows.Close()
			return nil, e
		}
		n.Sources = append(n.Sources, b)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	return &n, tx.Commit()
}
func (s *Service) Counts() (needs, qualified, completed int, err error) {
	tx, e := s.db.Begin()
	if e != nil {
		err = e
		return
	}
	defer tx.Rollback()
	err = tx.QueryRow("SELECT count(*),coalesce(sum(status='completed'),0) FROM needs").Scan(&needs, &completed)
	if err != nil {
		return
	}
	qualified, err = qualifiedCount(tx, time.Now())
	if err != nil {
		return
	}
	err = tx.Commit()
	return
}
