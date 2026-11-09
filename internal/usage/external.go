package usage

import (
	"database/sql"
	"encoding/json"
)

type External struct {
	ID               string  `json:"usage_id"`
	Provider         string  `json:"provider"`
	Model            *string `json:"model"`
	ReportedCost     *string `json:"reported_cost"`
	Currency         *string `json:"currency"`
	BillingReference *string `json:"billing_reference"`
	Status           string  `json:"cost_status"`
}

func RecordExternal(tx *sql.Tx, producer string, createdAt int64, items []External) error {
	for _, u := range items {
		var id int
		var oldStatus, oldProvider string
		var name, cost, currency, ref *string
		e := tx.QueryRow("SELECT id,cost_status,provider,model,reported_cost,currency,billing_reference FROM usage_records WHERE producer_id=? AND usage_id=?", producer, u.ID).Scan(&id, &oldStatus, &oldProvider, &name, &cost, &currency, &ref)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if e == sql.ErrNoRows {
			r, e := tx.Exec("INSERT INTO usage_records(origin,producer_id,usage_id,provider,model,reported_cost,currency,billing_reference,cost_status,created_at) VALUES ('external',?,?,?,?,?,?,?,?,?)", producer, u.ID, u.Provider, u.Model, u.ReportedCost, u.Currency, u.BillingReference, u.Status, createdAt)
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

// Backfill 在 003 迁移事务内运行，复用在线接收的去重/补齐规则。
func Backfill(tx *sql.Tx) error {
	after := 0
	for {
		var id int
		var producer string
		var raw []byte
		var received int64
		e := tx.QueryRow("SELECT id,producer_id,payload,created_at FROM research_batches WHERE id>? ORDER BY id LIMIT 1", after).Scan(&id, &producer, &raw, &received)
		if e == sql.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		var payload struct {
			RunReport struct {
				Usage []External `json:"usage"`
			} `json:"run_report"`
		}
		if e = json.Unmarshal(raw, &payload); e != nil {
			return e
		}
		if e = RecordExternal(tx, producer, received, payload.RunReport.Usage); e != nil {
			return e
		}
		after = id
	}
}
