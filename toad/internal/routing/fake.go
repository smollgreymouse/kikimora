package routing

import (
	"context"
	"sync"
)

type Fake struct {
	mu           sync.Mutex
	Transactions []Transaction
	State        KernelState
}

func (f *Fake) Apply(_ context.Context, tx Transaction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	tx.Operations = append([]Operation(nil), tx.Operations...)
	f.Transactions = append(f.Transactions, tx)
	for _, op := range tx.Operations {
		switch op.Kind {
		case "route", "unreachable", "park":
			f.State.Routes = removeOperation(f.State.Routes, op)
			f.State.Routes = append(f.State.Routes, op)
		case "delete-route", "delete-park":
			f.State.Routes = removeOperation(f.State.Routes, op)
		case "rule":
			f.State.Rules = removeOperation(f.State.Rules, op)
			f.State.Rules = append(f.State.Rules, op)
		case "delete-rule":
			f.State.Rules = removeOperation(f.State.Rules, op)
		}
	}
	return nil
}
func (f *Fake) Snapshot(_ context.Context) (KernelState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := f.State
	state.Routes = append([]Operation(nil), state.Routes...)
	state.Rules = append([]Operation(nil), state.Rules...)
	return state, nil
}

func removeOperation(items []Operation, target Operation) []Operation {
	out := items[:0]
	for _, item := range items {
		deleteKind := item.Kind == target.Kind || (target.Kind == "delete-park" && item.Kind == "park") || (target.Kind == "delete-route" && item.Kind == "route")
		keyMatches := item.Prefix == target.Prefix && item.Table == target.Table && item.IfIndex == target.IfIndex
		if target.Kind == "rule" || target.Kind == "delete-rule" {
			keyMatches = item.Prefix == target.Prefix && item.Table == target.Table && item.Priority == target.Priority
		}
		if deleteKind && keyMatches {
			continue
		}
		out = append(out, item)
	}
	return out
}
