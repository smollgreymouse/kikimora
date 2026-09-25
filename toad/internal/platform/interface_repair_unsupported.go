//go:build !linux

package platform

func DefaultInterfaceRepairer() InterfaceRepairer { return nil }
