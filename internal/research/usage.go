package research

import (
	"context"
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
