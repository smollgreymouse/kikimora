//go:build linux

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"os"

	"github.com/smollgreymouse/kikimora/toad/internal/backend/awg2"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
)

func key(fill byte) string {
	buf := make([]byte, 32)
	for i := range buf {
		buf[i] = fill
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func main() {
	cfg := &config.Config{
		Name:      "awg2-smoke",
		Protocol:  config.ProtocolAWG2,
		Interface: "kk-awg0",
		Address:   []string{"10.77.0.2/30"},
		MTU:       1380,
		StateDir:  os.TempDir(),
		AWG2: &config.AWG2Config{
			PrivateKey:          key(0x01),
			PeerPublicKey:       key(0x02),
			Endpoint:            "10.77.0.1:51820",
			AllowedIPs:          []string{"10.77.0.1/32"},
			PersistentKeepalive: 1,
		},
	}

	tunnel, err := platform.CreateTunnel(platform.TunnelSpec{
		Name:      cfg.Interface,
		MTU:       cfg.MTU,
		Addresses: []netip.Prefix{netip.MustParsePrefix("10.77.0.2/30")},
	})
	must(err)
	ownerIndex := tunnel.IfIndex()

	backend := awg2.New(cfg, tunnel)
	must(backend.Start(context.Background()))

	health := backend.Health(context.Background())
	if health.State != "connecting" || health.Connected {
		fail("missing peer must remain connecting, got %+v", health)
	}
	assertIfIndex(cfg.Interface, ownerIndex)

	must(backend.Close())
	must(backend.Close())
	assertIfIndex(cfg.Interface, ownerIndex)

	must(tunnel.Close())
	if _, err := net.InterfaceByName(cfg.Interface); err == nil {
		fail("%s still exists after final owner Close", cfg.Interface)
	}

	fmt.Printf("Toad AWG2 attachment test passed: ifindex=%d state=%s\n", ownerIndex, health.State)
}

func assertIfIndex(name string, want int) {
	iface, err := net.InterfaceByName(name)
	must(err)
	if iface.Index != want {
		fail("ifindex changed for %s: got %d want %d", name, iface.Index, want)
	}
}

func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
