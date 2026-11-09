package research

import (
	"math"
	"time"
)

type UsageRecord struct {
	ID, Version                                         int
	Origin, Producer, Provider                          string
	Model, ResponseID                                   *string
	InputTokens, OutputTokens                           *int
	Cost, Currency, Reference                           *string
	Status, CreatedAt                                   string
	ActualCost, ActualCurrency, BillingItem, ReviewNote *string
}
type UsageTotal struct {
	Origin, Currency, Reported, Verified string
	Unknown, Conflicts                   int
}

func (s *Service) Usage(before int) ([]UsageRecord, []UsageTotal, int, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return nil, nil, 0, e
	}
	defer tx.Rollback()
	rows, e := tx.Query("SELECT u.id,u.origin,coalesce(p.name,''),u.provider,u.model,u.response_id,u.input_tokens,u.output_tokens,u.reported_cost,u.currency,u.billing_reference,u.cost_status,u.created_at,u.version,r.amount,r.currency,r.billing_item,r.note FROM usage_records u LEFT JOIN producers p ON p.id=u.producer_id LEFT JOIN usage_reconciliations r ON r.usage_record=u.id WHERE (?=0 OR u.id<?) ORDER BY u.id DESC LIMIT 51", before, before)
	if e != nil {
		return nil, nil, 0, e
	}
	out := []UsageRecord{}
	for rows.Next() {
		var u UsageRecord
		var stamp int64
		if e = rows.Scan(&u.ID, &u.Origin, &u.Producer, &u.Provider, &u.Model, &u.ResponseID, &u.InputTokens, &u.OutputTokens, &u.Cost, &u.Currency, &u.Reference, &u.Status, &stamp, &u.Version, &u.ActualCost, &u.ActualCurrency, &u.BillingItem, &u.ReviewNote); e != nil {
			rows.Close()
			return nil, nil, 0, e
		}
		u.CreatedAt = time.Unix(stamp, 0).UTC().Format(time.RFC3339)
		out = append(out, u)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, nil, 0, e
	}
	next := 0
	if len(out) > 50 {
		out = out[:50]
		next = out[49].ID
	}
	totals, _, e := dailyCosts(tx, math.MinInt64, math.MaxInt64)
	if e != nil {
		return nil, nil, 0, e
	}
	return out, totals, next, tx.Commit()
}
