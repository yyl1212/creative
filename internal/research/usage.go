package research

import (
	"context"
	"database/sql"
	"github.com/yyl1212/creative/internal/model"
)

func (s *Service) Start(ctx context.Context, provider, name string) (int64, error) {
	if provider != "openai" && provider != "deepseek" {
		return 0, model.ErrUnavailable
	}
	r, e := s.db.ExecContext(ctx, "INSERT INTO usage_records(origin,provider,model,cost_status) VALUES ('website',?,?,'unknown')", provider, name)
	if e != nil {
		return 0, e
	}
	return r.LastInsertId()
}
func (s *Service) Finish(ctx context.Context, id int64, u model.Usage) error {
	var in, out any
	if u.TokensKnown {
		in = u.InputTokens
		out = u.OutputTokens
	}
	_, e := s.db.ExecContext(ctx, "UPDATE usage_records SET response_id=?,input_tokens=?,output_tokens=? WHERE id=? AND origin='website'", u.ResponseID, in, out, id)
	return e
}
func recordExternalUsage(tx *sql.Tx, producer string, items []ExternalUsage) error {
	for _, u := range items {
		var id int
		var oldStatus, oldProvider string
		var name, cost, currency, ref *string
		e := tx.QueryRow("SELECT id,cost_status,provider,model,reported_cost,currency,billing_reference FROM usage_records WHERE producer_id=? AND usage_id=?", producer, u.ID).Scan(&id, &oldStatus, &oldProvider, &name, &cost, &currency, &ref)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if e == sql.ErrNoRows {
			r, e := tx.Exec("INSERT INTO usage_records(origin,producer_id,usage_id,provider,model,reported_cost,currency,billing_reference,cost_status) VALUES ('external',?,?,?,?,?,?,?,?)", producer, u.ID, u.Provider, u.Model, u.ReportedCost, u.Currency, u.BillingReference, u.Status)
			if e != nil {
				return e
			}
			n, e := r.LastInsertId()
			if e != nil {
				return e
			}
			id = int(n)
		} else if oldStatus != "verified" {
			// 缺失字段允许补齐；已经给出的事实相互矛盾则保留原值并标记冲突。
			conflict := oldProvider != u.Provider || different(name, u.Model) || different(cost, u.ReportedCost) || different(currency, u.Currency) || different(ref, u.BillingReference)
			if conflict || oldStatus == "conflict" {
				_, e = tx.Exec("UPDATE usage_records SET cost_status='conflict' WHERE id=?", id)
			} else {
				status := oldStatus
				if oldStatus == "unknown" && u.Status == "reported" {
					status = "reported"
				}
				_, e = tx.Exec("UPDATE usage_records SET model=coalesce(model,?),reported_cost=coalesce(reported_cost,?),currency=coalesce(currency,?),billing_reference=coalesce(billing_reference,?),cost_status=? WHERE id=?", u.Model, u.ReportedCost, u.Currency, u.BillingReference, status, id)
			}
			if e != nil {
				return e
			}
		}
		if u.BillingReference != nil {
			_, e = tx.Exec("UPDATE usage_records SET cost_status='conflict' WHERE cost_status!='verified' AND provider=? AND billing_reference=? AND EXISTS (SELECT 1 FROM usage_records other WHERE other.provider=usage_records.provider AND other.billing_reference=usage_records.billing_reference AND other.id!=usage_records.id)", u.Provider, *u.BillingReference)
			if e != nil {
				return e
			}
		}
	}
	return nil
}
func different(a, b *string) bool { return a != nil && b != nil && *a != *b }
