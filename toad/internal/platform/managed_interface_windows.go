//go:build windows

package platform

import (
	"context"
	"fmt"
)

type windowsManagedInterfaceVerifier struct{}

func DefaultManagedInterfaceVerifier() ManagedInterfaceVerifier {
	return windowsManagedInterfaceVerifier{}
}

// EnsureUnmanaged reports whether the named adapter exists. Windows has no
// NetworkManager-equivalent external interface owner, so Managed is always
// false; Present reflects whether the adapter is currently visible in the
// system interface table.
func (windowsManagedInterfaceVerifier) EnsureUnmanaged(_ context.Context, name string) (ManagedInterfaceOwnership, error) {
	if name == "" {
		return ManagedInterfaceOwnership{}, fmt.Errorf("interface name is empty")
	}
	_, _, err := findInterfaceByAlias(name)
	if err != nil {
		return ManagedInterfaceOwnership{Present: false, Managed: false}, nil
	}
	return ManagedInterfaceOwnership{Present: true, Managed: false}, nil
}

// WatchManagedInterfaces is a no-op on Windows: there is no NetworkManager
// equivalent that can asynchronously claim ownership of a Toad-managed
// adapter. The underlay observer already detects adapter disappearance
// through its route/address change notifications.
func (windowsManagedInterfaceVerifier) WatchManagedInterfaces(ctx context.Context, _ chan<- struct{}) error {
	<-ctx.Done()
	return ctx.Err()
}
