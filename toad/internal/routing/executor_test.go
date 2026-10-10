package routing

import (
	"context"
	"sync"
	"testing"
)

type blockingExecutor struct {
	mu        sync.Mutex
	active    int
	maxActive int
}

func (e *blockingExecutor) Apply(context.Context, Transaction) error {
	e.mu.Lock()
	e.active++
	if e.active > e.maxActive {
		e.maxActive = e.active
	}
	e.mu.Unlock()
	e.mu.Lock()
	e.active--
	e.mu.Unlock()
	return nil
}
func (*blockingExecutor) Snapshot(context.Context) (KernelState, error) { return KernelState{}, nil }

func TestSerializedAllowsOneKernelWriter(t *testing.T) {
	inner := &blockingExecutor{}
	serialized := &Serialized{Inner: inner}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = serialized.Apply(context.Background(), Transaction{}) }()
	}
	wg.Wait()
	if inner.maxActive != 1 {
		t.Fatalf("concurrent kernel writes: %d", inner.maxActive)
	}
}

func TestFakeAppliesAndDeletesParking(t *testing.T) {
	f := &Fake{}
	prefix := "203.0.113.8/32"
	_ = f.Apply(context.Background(), Transaction{Operations: []Operation{{Kind: "park", Prefix: prefix, Table: 254, Metric: 42760}}})
	_ = f.Apply(context.Background(), Transaction{Operations: []Operation{{Kind: "delete-park", Prefix: prefix, Table: 254, Metric: 42760}}})
	s, _ := f.Snapshot(context.Background())
	if len(s.Routes) != 0 {
		t.Fatalf("park remained after delete: %#v", s.Routes)
	}
}

func TestFakeReplaceIsIdempotent(t *testing.T) {
	fake := &Fake{}
	tx := Transaction{Operations: []Operation{{Kind: "route", Prefix: "203.0.113.8/32", Table: 254, Metric: 10}}}
	if err := fake.Apply(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err := fake.Apply(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	state, err := fake.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Routes) != 1 {
		t.Fatalf("replace accumulated duplicate routes: %#v", state.Routes)
	}
}
