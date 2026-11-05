package routing

import "context"

type Operation struct {
	Kind     string
	Family   int
	Prefix   string
	Table    int
	Priority int
	IfIndex  int
	Metric   uint32
	Gateway  string
	Source   string
	Protocol int
	// InterfaceLuid carries the Windows interface LUID through the neutral
	// row view (08D1 §31.19): the exact Windows identity includes both the
	// LUID and the index. Zero on Linux/macOS.
	InterfaceLuid uint64 `json:"-"`
}
type Transaction struct {
	ID         uint64
	Role       string
	Operations []Operation
}
type KernelState struct {
	Routes []Operation
	Rules  []Operation
}
type Executor interface {
	Apply(context.Context, Transaction) error
	Snapshot(context.Context) (KernelState, error)
}
