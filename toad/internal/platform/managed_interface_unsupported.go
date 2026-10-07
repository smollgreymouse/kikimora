//go:build !linux && !windows

package platform

func DefaultManagedInterfaceVerifier() ManagedInterfaceVerifier { return nil }
