// Package worker 仅执行数据保留与备份维护，不处理研究队列。
package worker

import (
	"context"
	"time"
	_ "time/tzdata"

	"github.com/yyl1212/creative/internal/research"
)

type Worker struct {
	Service  *research.Service
	Maintain func(context.Context, time.Time) error
}

func (w Worker) Run(ctx context.Context) error { return w.run(ctx, 5*time.Second) }
func (w Worker) run(ctx context.Context, interval time.Duration) error {
	zone, e := time.LoadLocation("Asia/Shanghai")
	if e != nil {
		return e
	}
	lastDay := ""
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		now := time.Now()
		local := now.In(zone)
		day := local.Format("2006-01-02")
		if lastDay == "" || (day != lastDay && local.Hour() >= 8) {
			if e = w.Service.Maintenance(now); e != nil {
				return e
			}
			if w.Maintain != nil {
				if e = w.Maintain(ctx, now); e != nil {
					return e
				}
			}
			lastDay = day
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}
