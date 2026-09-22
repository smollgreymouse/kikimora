package netstate

import (
	"context"
	"errors"
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
	Changed func(Change) error
}

func (c *Coalescer) Run(ctx context.Context, invalidations <-chan Invalidation, initial Snapshot) error {
	if c.Settle <= 0 {
		c.Settle = 150 * time.Millisecond
	}
	if c.Maximum <= 0 {
		c.Maximum = time.Second
	}
	if c.Build == nil {
		return errors.New("coalescer snapshot builder is nil")
	}
	if c.Changed == nil {
		return errors.New("coalescer change handler is nil")
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
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.Changed(Change{Snapshot: merged, Reason: reason, Resume: resume})
}
