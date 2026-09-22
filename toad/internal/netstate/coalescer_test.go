package netstate

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoalescerBurstPublishesOneCanonicalChange(t *testing.T) {
	initial := Snapshot{Epoch: 1, IPv4: path("192.0.2.1", "192.0.2.10", 3)}
	next := Snapshot{IPv4: path("192.0.2.254", "192.0.2.10", 3)}
	invalidations := make(chan Invalidation, 8)
	got := make(chan Change, 4)
	var calls atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	coalescer := &Coalescer{
		Settle:  10 * time.Millisecond,
		Maximum: 100 * time.Millisecond,
		Build: func(context.Context) (Snapshot, error) {
			return next, nil
		},
		Changed: func(change Change) error {
			calls.Add(1)
			got <- change
			return nil
		},
	}
	go func() { _ = coalescer.Run(ctx, invalidations, initial) }()

	for i := 0; i < 5; i++ {
		invalidations <- Invalidation{Source: "netlink"}
	}
	select {
	case change := <-got:
		if change.Reason != ChangeGateway || change.Snapshot.Epoch != 2 || change.Resume {
			t.Fatalf("unexpected coalesced change: %#v", change)
		}
	case <-time.After(time.Second):
		t.Fatal("coalesced change was not delivered")
	}
	time.Sleep(40 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("burst produced %d semantic changes, want 1", got)
	}
}

func TestCoalescerResumeKeepsEpochWhenIdentityIsUnchanged(t *testing.T) {
	initial := Snapshot{
		Epoch:      5,
		IPv4:       path("192.0.2.1", "192.0.2.10", 3),
		ObservedAt: time.Now().Add(-time.Minute),
	}
	next := initial
	next.Epoch = 0
	next.ObservedAt = time.Now()

	invalidations := make(chan Invalidation, 1)
	got := make(chan Change, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	coalescer := &Coalescer{
		Settle:  5 * time.Millisecond,
		Maximum: 50 * time.Millisecond,
		Build: func(context.Context) (Snapshot, error) {
			return next, nil
		},
		Changed: func(change Change) error {
			got <- change
			return nil
		},
	}
	go func() { _ = coalescer.Run(ctx, invalidations, initial) }()

	invalidations <- Invalidation{Source: "resume"}
	select {
	case change := <-got:
		if !change.Resume || change.Reason != ChangeResumeValidation || change.Snapshot.Epoch != 5 {
			t.Fatalf("resume changed canonical identity: %#v", change)
		}
	case <-time.After(time.Second):
		t.Fatal("resume change was not delivered")
	}
}
