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
