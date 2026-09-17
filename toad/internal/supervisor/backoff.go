package supervisor

import "time"

var RetryDelays = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}

type Backoff struct {
	Attempt     int
	StableSince time.Time
}

func (b *Backoff) Next() time.Duration {
	n := b.Attempt
	if n < 0 {
		n = 0
	}
	if n >= len(RetryDelays) {
		n = len(RetryDelays) - 1
	}
	b.Attempt++
	return RetryDelays[n]
}
func (b *Backoff) ResetIfStable(now time.Time) {
	if !b.StableSince.IsZero() && now.Sub(b.StableSince) >= 60*time.Second {
		b.Attempt = 0
	}
}
func (b *Backoff) MarkReady(now time.Time) {
	if b.StableSince.IsZero() {
		b.StableSince = now
	}
}
func (b *Backoff) ClearReady() { b.StableSince = time.Time{} }
