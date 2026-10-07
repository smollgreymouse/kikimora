//go:build !linux && !windows

package platform

func DefaultInterfaceRepairer() InterfaceRepairer { return nil }
