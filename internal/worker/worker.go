// Package worker 在网站进程内恢复持久核验队列；它不搜寻需求。
package worker

import (
	"context"
	"crypto/rand"
	"errors"
	"time"
	_ "time/tzdata"

	"github.com/yyl1212/creative/internal/research"
)

type Worker struct {
	Service  *research.Service
	Fetcher  research.SourceFetcher
	Reviewer research.SemanticReviewer
}

func (w Worker) Run(ctx context.Context) error { return w.run(ctx, 5*time.Second) }
func (w Worker) run(ctx context.Context, interval time.Duration) error {
	zone, e := time.LoadLocation("Asia/Shanghai")
	if e != nil {
		return e
	}
	owner := rand.Text()
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
			lastDay = day
		}
		lease, e := w.Service.Claim(ctx, owner, now)
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		if lease != nil {
			job, cancel := context.WithCancel(ctx)
			finished := make(chan struct{})
			heartbeat := make(chan error, 1)
			go func() {
				defer close(finished)
				tick := time.NewTicker(20 * time.Second)
				defer tick.Stop()
				for {
					select {
					case <-job.Done():
						return
					case <-tick.C:
						if err := w.Service.Heartbeat(job, *lease, time.Now()); err != nil {
							heartbeat <- err
							cancel()
							return
						}
					}
				}
			}()
			e = w.Service.Process(job, *lease, w.Fetcher, w.Reviewer, now)
			cancel()
			<-finished
			select {
			case err := <-heartbeat:
				e = err
			default:
			}
			if e != nil && !errors.Is(e, research.ErrLeaseLost) && !errors.Is(e, context.Canceled) {
				return e
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}
