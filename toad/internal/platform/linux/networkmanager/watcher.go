//go:build linux

package networkmanager

import "context"

type Watcher struct{}

func (Watcher) Watch(context.Context, chan<- string) error { return nil }
