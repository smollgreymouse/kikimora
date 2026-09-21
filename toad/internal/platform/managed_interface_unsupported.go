//go:build !linux

package platform

func DefaultManagedInterfaceVerifier() ManagedInterfaceVerifier { return nil }
