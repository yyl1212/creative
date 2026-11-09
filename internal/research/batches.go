package research

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"time"
)

type BatchSummary struct {
	ID             int
	BatchID        string
	TaskID         string
	ProducerName   string
	Status         string
	CandidateCount int
	ReceivedAt     string
}
type BatchDetail struct {
	BatchSummary
	Payload string
}

const batchColumns = "b.id,b.batch_id,b.task_id,p.name,b.status,(SELECT count(*) FROM research_candidates c WHERE c.batch_row=b.id),b.created_at"

func scanBatch(row interface{ Scan(...any) error }) (BatchSummary, error) {
	var b BatchSummary
	var created int64
	err := row.Scan(&b.ID, &b.BatchID, &b.TaskID, &b.ProducerName, &b.Status, &b.CandidateCount, &created)
	b.ReceivedAt = time.Unix(created, 0).UTC().Format(time.RFC3339)
	return b, err
}
func (s *Service) Batches(before int) ([]BatchSummary, int, error) {
	rows, err := s.db.Query("SELECT "+batchColumns+" FROM research_batches b JOIN producers p ON p.id=b.producer_id WHERE (?=0 OR b.id<?) ORDER BY b.id DESC LIMIT 51", before, before)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	batches := []BatchSummary{}
	for rows.Next() {
		b, e := scanBatch(rows)
		if e != nil {
			return nil, 0, e
		}
		batches = append(batches, b)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	next := 0
	if len(batches) > 50 {
		batches = batches[:50]
		next = batches[49].ID
	}
	return batches, next, nil
}
func (s *Service) Batch(id int) (*BatchDetail, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	b, err := scanBatch(tx.QueryRow("SELECT "+batchColumns+" FROM research_batches b JOIN producers p ON p.id=b.producer_id WHERE b.id=?", id))
	if err == sql.ErrNoRows {
		return nil, fail(404, "batch_not_found", "/id", "批次不存在")
	}
	if err != nil {
		return nil, err
	}
	var raw []byte
	if err = tx.QueryRow("SELECT payload FROM research_batches WHERE id=?", id).Scan(&raw); err != nil {
		return nil, err
	}
	var formatted bytes.Buffer
	if err = json.Indent(&formatted, raw, "", "  "); err != nil {
		return nil, err
	}
	return &BatchDetail{BatchSummary: b, Payload: formatted.String()}, tx.Commit()
}
