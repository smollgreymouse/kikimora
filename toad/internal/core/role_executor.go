package core

import (
	"context"
	"sync"
)

type RoleWork func(context.Context) OperationResult
type RoleExecutor struct {
	mu     sync.Mutex
	next   map[string]uint64
	cancel map[string]context.CancelFunc
	done   chan OperationCompleted
}

func NewRoleExecutor(done chan OperationCompleted) *RoleExecutor {
	return &RoleExecutor{next: map[string]uint64{}, cancel: map[string]context.CancelFunc{}, done: done}
}
func (e *RoleExecutor) Submit(ctx context.Context, role string, op, epoch uint64, work RoleWork) {
	workCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	if previous := e.cancel[role]; previous != nil {
		previous()
	}
	e.next[role] = op
	e.cancel[role] = cancel
	e.mu.Unlock()
	go func() {
		defer func() {
			e.mu.Lock()
			if e.next[role] == op {
				delete(e.cancel, role)
			}
			e.mu.Unlock()
		}()
		r := work(workCtx)
		select {
		case e.done <- OperationCompleted{Role: role, Operation: op, Epoch: epoch, Result: r}:
		case <-workCtx.Done():
		}
	}()
}

func (e *RoleExecutor) Cancel(role string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cancel := e.cancel[role]; cancel != nil {
		cancel()
		delete(e.cancel, role)
	}
}
func (e *RoleExecutor) Current(role string) uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.next[role]
}
