//go:build windows && privileged

package platform

import (
	"net"
	"net/netip"
	"testing"

	"golang.org/x/sys/windows"
)

// interfaceAddrs lists the unicast addresses of the interface as netip values.
func interfaceAddrs(link *net.Interface) []netip.Addr {
	var out []netip.Addr
	addresses, err := link.Addrs()
	if err != nil {
		return out
	}
	for _, address := range addresses {
		ipNet, ok := address.(*net.IPNet)
		if !ok {
			continue
		}
		if ip, ok := netip.AddrFromSlice(ipNet.IP); ok {
			out = append(out, ip.Unmap())
		}
	}
	return out
}

// This file holds the privileged Wintun lifecycle tests. They create a real
// adapter and therefore require elevation plus the Wintun driver; they are
// gated behind the `privileged` build tag so ordinary test runs (including
// CI and developer shells) never touch the driver.
//
// Run from an elevated prompt:
//
//	go test -tags privileged ./internal/platform/ -run TestTunnelWindowsPrivileged -v

func requireElevated(t *testing.T) {
	t.Helper()
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("requires an elevated process; run with -tags privileged from an admin prompt")
	}
}

func TestTunnelWindowsPrivilegedLifecycleReuse(t *testing.T) {
	requireElevated(t)
	const name = "kk-tun-lifecycle-test"
	const mtu = 1380
	addresses := []netip.Prefix{netip.MustParsePrefix("10.203.0.1/24")}

	tunnel, err := CreateTunnel(TunnelSpec{Name: name, MTU: mtu, Addresses: addresses})
	if err != nil {
		t.Fatalf("create Wintun tunnel: %v", err)
	}
	if tunnel.Name() != name {
		t.Fatalf("adapter name %q does not match the requested %q", tunnel.Name(), name)
	}
	if tunnel.IfIndex() <= 0 {
		t.Fatalf("adapter %q has invalid ifindex %d", name, tunnel.IfIndex())
	}
	if tunnel.MTU() != mtu {
		t.Fatalf("adapter MTU %d does not match %d", tunnel.MTU(), mtu)
	}
	ifIndex := tunnel.IfIndex()
	if err := tunnel.Close(); err != nil {
		t.Fatalf("close tunnel: %v", err)
	}
	if err := tunnel.Close(); err != nil {
		t.Fatalf("second close must be idempotent: %v", err)
	}

	reused, err := CreateTunnel(TunnelSpec{Name: name, MTU: mtu, Addresses: addresses})
	if err != nil {
		t.Fatalf("recreate Wintun tunnel after close: %v", err)
	}
	defer func() { _ = reused.Close() }()
	if reused.Name() != name {
		t.Fatalf("recreated adapter name %q does not match %q", reused.Name(), name)
	}
	if reused.IfIndex() != ifIndex {
		t.Fatalf("deterministic GUID identity drift: ifindex %d then %d", ifIndex, reused.IfIndex())
	}

	link, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatalf("look up adapter after recreate: %v", err)
	}
	found := false
	for _, addr := range interfaceAddrs(link) {
		if addr == netip.MustParseAddr("10.203.0.1") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("address 10.203.0.1 missing on %q: %v", name, interfaceAddrs(link))
	}
}
