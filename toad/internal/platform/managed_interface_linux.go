//go:build linux

package platform

import (
	"context"

	"github.com/smollgreymouse/kikimora/toad/internal/platform/linux/networkmanager"
)

type linuxManagedInterfaceVerifier struct{}

func DefaultManagedInterfaceVerifier() ManagedInterfaceVerifier {
	return linuxManagedInterfaceVerifier{}
}

func (linuxManagedInterfaceVerifier) EnsureUnmanaged(ctx context.Context, name string) (ManagedInterfaceOwnership, error) {
	state, err := (networkmanager.Manager{}).EnsureUnmanaged(ctx, name)
	return ManagedInterfaceOwnership{Present: state.Present, Managed: state.Managed}, err
}

func (linuxManagedInterfaceVerifier) WatchManagedInterfaces(ctx context.Context, out chan<- struct{}) error {
	return (networkmanager.Manager{}).Watch(ctx, out)
}
