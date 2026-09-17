package endpoint

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testResolver struct {
	addresses []netip.Addr
	err       error
}

func (r testResolver) LookupNetIP(context.Context, string) ([]netip.Addr, error) {
	return r.addresses, r.err
}

func TestRefreshSpecsCommitsCompleteCandidateSet(t *testing.T) {
	m := Manager{}
	s := m.RefreshSpecs(context.Background(), 7, []EndpointSpec{{Hostname: "vpn.example", Port: 443}}, testResolver{
		addresses: []netip.Addr{netip.MustParseAddr("198.51.100.10"), netip.MustParseAddr("2001:db8::10")},
	})
	if s.State != "ready" || len(s.Resolved) != 2 || s.AppliedUnderlayEpoch != 7 {
		t.Fatalf("unexpected resolved state: %#v", s)
	}
}

func TestRefreshSpecsKeepsLastKnownGoodOnResolveFailure(t *testing.T) {
	m := Manager{}
	good := netip.MustParseAddrPort("198.51.100.10:443")
	m.RefreshSpecs(context.Background(), 7, []EndpointSpec{{Address: good}}, nil)
	s := m.RefreshSpecs(context.Background(), 8, []EndpointSpec{{Hostname: "vpn.example", Port: 443}}, testResolver{err: errors.New("offline")})
	if s.State != "degraded" || s.AppliedUnderlayEpoch != 7 || len(s.Live) != 1 || s.Live[0] != good {
		t.Fatalf("last known good was lost: %#v", s)
	}
}

func TestParseSpecsAcceptsLegacyBareEndpoints(t *testing.T) {
	specs, err := ParseSpecs(strings.NewReader("# kikimora-endpoint-provider-mode: static\n198.51.100.10\nve.example\n"), "udp", 51820)
	if err != nil || len(specs) != 2 {
		t.Fatalf("legacy endpoint list was not parsed: %v %#v", err, specs)
	}
	if !specs[0].Address.IsValid() || specs[0].Address.Port() != 51820 || specs[0].Network != "udp" {
		t.Fatalf("bad numeric legacy endpoint: %#v", specs[0])
	}
	if specs[1].Hostname != "ve.example" || specs[1].Port != 51820 {
		t.Fatalf("bad hostname legacy endpoint: %#v", specs[1])
	}
}

func TestCommandProviderUsesAllowlistedEnvironment(t *testing.T) {
	dir := t.TempDir()
	envDump := filepath.Join(dir, "env.txt")
	providerScript := filepath.Join(dir, "provider.sh")
	script := "#!/bin/sh\nenv > " + envDump + "\nprintf '198.51.100.10:443\\n'\n"
	if err := os.WriteFile(providerScript, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIKIMORA_PROVIDER_SECRET", "must-not-leak")
	if _, err := (CommandProvider{Command: providerScript}).Resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile(envDump)
	if err != nil {
		t.Fatal(err)
	}
	if string(env) == "" || strings.Contains(string(env), "KIKIMORA_PROVIDER_SECRET") {
		t.Fatalf("provider received non-allowlisted environment: %s", env)
	}
}
