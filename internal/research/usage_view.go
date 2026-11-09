package research

import (
	"math/big"
	"sort"
	"strings"
	"time"
)

type UsageRecord struct {
	ID                         int
	Origin, Producer, Provider string
	Model, ResponseID          *string
	InputTokens, OutputTokens  *int
	Cost, Currency, Reference  *string
	Status, CreatedAt          string
}
type UsageTotal struct {
	Origin, Currency, Reported, Verified string
	Unknown, Conflicts                   int
}

func (s *Service) Usage(before int) ([]UsageRecord, []UsageTotal, int, error) {
	rows, e := s.db.Query("SELECT u.id,u.origin,coalesce(p.name,''),u.provider,u.model,u.response_id,u.input_tokens,u.output_tokens,u.reported_cost,u.currency,u.billing_reference,u.cost_status,u.created_at FROM usage_records u LEFT JOIN producers p ON p.id=u.producer_id WHERE (?=0 OR u.id<?) ORDER BY u.id DESC LIMIT 51", before, before)
	if e != nil {
		return nil, nil, 0, e
	}
	out := []UsageRecord{}
	for rows.Next() {
		var u UsageRecord
		var stamp int64
		if e = rows.Scan(&u.ID, &u.Origin, &u.Producer, &u.Provider, &u.Model, &u.ResponseID, &u.InputTokens, &u.OutputTokens, &u.Cost, &u.Currency, &u.Reference, &u.Status, &stamp); e != nil {
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
	rows, e = s.db.Query("SELECT origin,currency,reported_cost,cost_status FROM usage_records")
	if e != nil {
		return nil, nil, 0, e
	}
	defer rows.Close()
	type aggregate struct {
		summary            UsageTotal
		reported, verified *big.Rat
		scale              int
		verifiedCount      int
	}
	groups := map[string]*aggregate{}
	for rows.Next() {
		var origin, status string
		var currency, cost *string
		if e = rows.Scan(&origin, &currency, &cost, &status); e != nil {
			return nil, nil, 0, e
		}
		unit := "未知币种"
		if currency != nil {
			unit = *currency
		}
		key := origin + "/" + unit
		g := groups[key]
		if g == nil {
			g = &aggregate{summary: UsageTotal{Origin: origin, Currency: unit}, reported: new(big.Rat), verified: new(big.Rat)}
			groups[key] = g
		}
		if status == "conflict" {
			g.summary.Conflicts++
			continue
		}
		if status == "unknown" || cost == nil || currency == nil {
			g.summary.Unknown++
			continue
		}
		n, ok := new(big.Rat).SetString(*cost)
		if !ok || n.Sign() < 0 {
			g.summary.Unknown++
			continue
		}
		scale := 0
		if dot := strings.IndexByte(*cost, '.'); dot >= 0 {
			scale = len(*cost) - dot - 1
		}
		g.scale = max(g.scale, scale)
		if status == "verified" {
			g.verified.Add(g.verified, n)
			g.verifiedCount++
		} else if status == "reported" {
			g.reported.Add(g.reported, n)
		}
	}
	if e = rows.Err(); e != nil {
		return nil, nil, 0, e
	}
	keys := []string{}
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	totals := []UsageTotal{}
	for _, k := range keys {
		g := groups[k]
		g.summary.Reported = g.reported.FloatString(g.scale)
		g.summary.Verified = "尚未核对"
		if g.verifiedCount > 0 {
			g.summary.Verified = g.verified.FloatString(g.scale)
		}
		totals = append(totals, g.summary)
	}
	return out, totals, next, nil
}
