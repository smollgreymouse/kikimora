//go:build linux

package platform

import (
	"context"

	"github.com/smollgreymouse/kikimora/toad/internal/platform/linux/logind"
)

type linuxSleepSource struct{ source logind.Source }

func DefaultSleepSource() SleepSource { return linuxSleepSource{} }

func (s linuxSleepSource) Watch(ctx context.Context, out chan<- SleepEvent) error {
	events := make(chan logind.Event)
	errCh := make(chan error, 1)
	go func() { errCh <- s.source.Watch(ctx, events) }()
	for {
		select {
		case event := <-events:
			select {
			case out <- SleepEvent{Preparing: event.Preparing}:
			case <-ctx.Done():
				return ctx.Err()
			}
		case err := <-errCh:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
