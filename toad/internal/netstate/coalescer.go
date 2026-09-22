package netstate

import (
	"context"
	"time"
)

type Invalidation struct{ Source string }

type Change struct {
	Snapshot Snapshot
	Reason   ChangeReason
	Resume   bool
}

type Coalescer struct {
	Settle  time.Duration
	Maximum time.Duration
	Build   func(context.Context) (Snapshot, error)
	Changed chan Change
}

func (c *Coalescer) Run(ctx context.Context, invalidations <-chan Invalidation, initial Snapshot) error {
	if c.Settle <= 0 {
		c.Settle = 150 * time.Millisecond
	}
	if c.Maximum <= 0 {
		c.Maximum = time.Second
	}
	if c.Changed == nil {
		c.Changed = make(chan Change, 1)
	}
	current := initial
	var timer *time.Timer
	var timerC <-chan time.Time
	var burst time.Time
	var resume bool
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case invalidation := <-invalidations:
			if invalidation.Source == "resume" {
				resume = true
			}
			if burst.IsZero() {
				burst = time.Now()
			}
			if timer == nil {
				timer = time.NewTimer(c.Settle)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(c.Settle)
			}
			timerC = timer.C
			if time.Since(burst) >= c.Maximum {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timerC = nil
				if err := c.publish(ctx, &current, resume); err != nil {
					return err
				}
				burst = time.Time{}
				resume = false
			}
		case <-timerC:
			timerC = nil
			if err := c.publish(ctx, &current, resume); err != nil {
				return err
			}
			burst = time.Time{}
			resume = false
		}
	}
}

func (c *Coalescer) publish(ctx context.Context, current *Snapshot, resume bool) error {
	next, err := c.Build(ctx)
	if err != nil {
		return err
	}
	merged, reason, changed := Compare(*current, next)
	if !changed && !resume {
		return nil
	}
	if !changed {
		merged = next
		merged.Epoch = current.Epoch
		reason = ChangeResumeValidation
	}
	*current = merged
	select {
	case c.Changed <- Change{Snapshot: merged, Reason: reason, Resume: resume}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
