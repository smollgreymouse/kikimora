package xray

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/xtls/xray-core/infra/conf/serial"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

const testRealityPublicKey = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE"

func testConfig(endpoint string) *config.Config {
	return &config.Config{
		Name:      "xray-test",
		Protocol:  config.ProtocolVLESSReality,
		Interface: "kk-xray0",
		Address:   []string{"10.88.0.2/30"},
		MTU:       1380,
		StateDir:  "/tmp/xray-test",
		VLESS: &config.VLESSRealityConfig{
			Endpoint:    endpoint,
			UUID:        "11111111-1111-4111-8111-111111111111",
			ServerName:  "www.example.org",
			PublicKey:   testRealityPublicKey,
			ShortID:     "abcd",
			Flow:        "xtls-rprx-vision",
			Fingerprint: "chrome",
			Transport:   "tcp",
			SpiderX:     "/probe",
		},
	}
}

func TestBuildCoreJSON(t *testing.T) {
	raw, err := buildCoreJSON(testConfig("vps.example:443"))
	if err != nil {
		t.Fatal(err)
	}

	var built xrayConfig
	if err := json.Unmarshal(raw, &built); err != nil {
		t.Fatal(err)
	}
	if len(built.Inbounds) != 1 || built.Inbounds[0].Protocol != "tun" {
		t.Fatalf("unexpected inbounds: %#v", built.Inbounds)
	}
	tun := built.Inbounds[0].Settings
	if tun.Name != "kk-xray0" || tun.MTU != 1380 || len(tun.Gateway) != 1 || tun.Gateway[0] != "10.88.0.2/30" {
		t.Fatalf("unexpected TUN settings: %#v", tun)
	}
	if len(built.Outbounds) != 1 || built.Outbounds[0].Protocol != "vless" {
		t.Fatalf("unexpected outbounds: %#v", built.Outbounds)
	}
	out := built.Outbounds[0]
	if out.StreamSettings.Network != "raw" || out.StreamSettings.Security != "reality" {
		t.Fatalf("unexpected stream settings: %#v", out.StreamSettings)
	}
	reality := out.StreamSettings.RealitySettings
	if reality.ServerName != "www.example.org" || reality.PublicKey != testRealityPublicKey || reality.ShortID != "abcd" || reality.Fingerprint != "chrome" || reality.SpiderX != "/probe" {
		t.Fatalf("REALITY fields lost: %#v", reality)
	}
	vnext := out.Settings.VNext[0]
	if vnext.Address != "vps.example" || vnext.Port != 443 || len(vnext.Users) != 1 {
		t.Fatalf("unexpected VLESS endpoint: %#v", vnext)
	}
	if vnext.Users[0].ID != "11111111-1111-4111-8111-111111111111" || vnext.Users[0].Encryption != "none" || vnext.Users[0].Flow != "xtls-rprx-vision" {
		t.Fatalf("unexpected VLESS user: %#v", vnext.Users[0])
	}

	if _, err := serial.LoadJSONConfig(bytes.NewReader(raw)); err != nil {
		t.Fatalf("pinned Xray rejected generated config: %v", err)
	}
}

func TestBuildCoreJSONIPv6Endpoint(t *testing.T) {
	raw, err := buildCoreJSON(testConfig("[2001:db8::10]:8443"))
	if err != nil {
		t.Fatal(err)
	}
	var built xrayConfig
	if err := json.Unmarshal(raw, &built); err != nil {
		t.Fatal(err)
	}
	vnext := built.Outbounds[0].Settings.VNext[0]
	if vnext.Address != "2001:db8::10" || vnext.Port != 8443 {
		t.Fatalf("IPv6 endpoint parsed incorrectly: %#v", vnext)
	}
}

func TestLifecycleHealthDoesNotContainCredentials(t *testing.T) {
	cfg := testConfig("vps.example:443")
	b := New(cfg)
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	h := b.Health(t.Context())
	for _, secret := range []string{cfg.VLESS.UUID, cfg.VLESS.PublicKey, cfg.VLESS.ShortID} {
		if secret != "" && strings.Contains(h.Reason, secret) {
			t.Fatalf("health reason leaked credential %q", secret)
		}
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close must be idempotent: %v", err)
	}
}
