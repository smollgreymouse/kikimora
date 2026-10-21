package legacyconfig

import (
	"strings"
	"testing"
)

func TestParseVPNConf(t *testing.T) {
	cfg, err := Parse(strings.NewReader(`
PRIMARY_INTERFACE="primary0"
PRIMARY_DEVICE_FILE='/run/kikimora/leshy/vpn/primary.dev'
PRIMARY_ENDPOINT_PROVIDER="static"
PRIMARY_ENDPOINT_PROVIDER_ARGS=""
SECONDARY_INTERFACE=secondary0
SECONDARY_ENDPOINT_PROVIDER=happ
SECONDARY_ENDPOINT_PROVIDER_ARGS="xray,sing-box"
VPN_LINK_READY_SUCCESSES=3
`))
	if err != nil {
		t.Fatal(err)
	}
	primary, ok := cfg.Role("PRIMARY")
	if !ok || primary.Interface != "primary0" || primary.DeviceFile == "" || primary.EndpointProvider != "static" {
		t.Fatalf("bad primary role: %#v", primary)
	}
	secondary, ok := cfg.Role("secondary")
	if !ok || secondary.ProviderArgs != "xray,sing-box" {
		t.Fatalf("bad secondary role: %#v", secondary)
	}
	if cfg.VPNLinkReadySuccesses != 3 {
		t.Fatalf("unexpected readiness count: %d", cfg.VPNLinkReadySuccesses)
	}
}

func TestParseVPNConfRejectsShellExpressions(t *testing.T) {
	for _, value := range []string{`$(touch /tmp/pwned)`, "`id`", `foo;bar`, `${HOME}`} {
		if _, err := Parse(strings.NewReader("PRIMARY_INTERFACE=" + value + "\n")); err == nil {
			t.Fatalf("unsafe value accepted: %q", value)
		}
	}
}

func TestParseVPNConfRejectsUnknownProvider(t *testing.T) {
	if _, err := Parse(strings.NewReader("PRIMARY_ENDPOINT_PROVIDER=source\n")); err == nil {
		t.Fatal("unknown endpoint provider accepted")
	}
}
