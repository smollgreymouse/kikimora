package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateAWG2(t *testing.T) {
	cfg := &Config{
		Name:      "awg-main",
		Protocol:  ProtocolAWG2,
		Interface: "kk-awg0",
		Address:   []string{"10.40.0.2/32"},
		MTU:       1380,
		StateDir:  t.TempDir(),
		AWG2: &AWG2Config{
			PrivateKey:    "private",
			PeerPublicKey: "public",
			Endpoint:      "192.0.2.1:51820",
			AllowedIPs:    []string{"0.0.0.0/0", "::/0"},
			JC:            4,
			JMin:          40,
			JMax:          80,
			S1:            15,
			S2:            15,
			S3:            15,
			S4:            15,
			H1:            "1001",
			H2:            "1002",
			H3:            "1003",
			H4:            "1004",
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid AWG2 config rejected: %v", err)
	}
}

func TestValidateVLESSReality(t *testing.T) {
	cfg := &Config{
		Name:      "xray-main",
		Protocol:  ProtocolVLESSReality,
		Interface: "kk-xray0",
		Address:   []string{"10.41.0.2/30"},
		MTU:       1380,
		StateDir:  t.TempDir(),
		VLESS: &VLESSRealityConfig{
			Endpoint:    "192.0.2.2:443",
			UUID:        "11111111-1111-1111-1111-111111111111",
			ServerName:  "example.com",
			PublicKey:   "public-key",
			ShortID:     "0123456789abcdef",
			Flow:        "xtls-rprx-vision",
			Fingerprint: "chrome",
			Transport:   "raw",
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid VLESS config rejected: %v", err)
	}
}

func TestRejectMixedProtocolSections(t *testing.T) {
	cfg := &Config{
		Name:      "bad",
		Protocol:  ProtocolAWG2,
		Interface: "kk-bad0",
		Address:   []string{"10.40.0.2/32"},
		MTU:       1380,
		StateDir:  t.TempDir(),
		AWG2: &AWG2Config{
			PrivateKey:    "private",
			PeerPublicKey: "public",
			Endpoint:      "192.0.2.1:51820",
			AllowedIPs:    []string{"0.0.0.0/0"},
		},
		VLESS: &VLESSRealityConfig{},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("mixed protocol sections must be rejected")
	}
}

func TestGenericConfigDoesNotEnforceLinuxIFNAMSIZ(t *testing.T) {
	cfg := &Config{
		Name:      "awg-main",
		Protocol:  ProtocolAWG2,
		Interface: "kk-cross-platform-interface",
		Address:   []string{"10.40.0.2/32"},
		MTU:       1380,
		StateDir:  t.TempDir(),
		AWG2: &AWG2Config{
			PrivateKey:    "private",
			PeerPublicKey: "public",
			Endpoint:      "192.0.2.1:51820",
			AllowedIPs:    []string{"0.0.0.0/0"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("generic config must not apply Linux IFNAMSIZ: %v", err)
	}
}

func TestRejectUnsafeInterfaceName(t *testing.T) {
	cfg := &Config{
		Name:      "awg-main",
		Protocol:  ProtocolAWG2,
		Interface: "bad iface",
		Address:   []string{"10.40.0.2/32"},
		MTU:       1380,
		StateDir:  t.TempDir(),
		AWG2: &AWG2Config{
			PrivateKey:    "private",
			PeerPublicKey: "public",
			Endpoint:      "192.0.2.1:51820",
			AllowedIPs:    []string{"0.0.0.0/0"},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("unsafe interface name must be rejected")
	}
}

func TestLoadWithLegacyVpnConfUsesSharedEndpointPolicy(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "endpoints"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(dir, "vpn.conf")
	if err := os.WriteFile(legacyPath, []byte("PRIMARY_INTERFACE=primary0\nPRIMARY_ENDPOINT_PROVIDER=static\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "endpoints", "primary.txt"), []byte("198.51.100.10\nve.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "primary.toml")
	cfg := &Config{
		Name: "primary", Protocol: ProtocolAWG2, Interface: "new0", Address: []string{"10.0.0.2/32"}, MTU: 1380, StateDir: filepath.Join(dir, "state"),
		AWG2: &AWG2Config{PrivateKey: "private", PeerPublicKey: "public", Endpoint: "192.0.2.1:51820", AllowedIPs: []string{"0.0.0.0/0"}},
	}
	var data bytes.Buffer
	if err := Encode(&data, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWithLegacy(cfgPath, legacyPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Interface != "primary0" || loaded.EndpointPolicy.Source != "static" {
		t.Fatalf("legacy role was not applied: %#v", loaded)
	}
	specs, err := loaded.ResolveTransportEndpoints(nil)
	if err != nil || len(specs) != 2 || specs[0].Address.Port() != 51820 || specs[1].Hostname != "ve.example" {
		t.Fatalf("legacy endpoint policy was not resolved: %v %#v", err, specs)
	}
}

func TestTransportEndpointSpecsPreserveProtocolPorts(t *testing.T) {
	awg := &Config{Protocol: ProtocolAWG2, AWG2: &AWG2Config{Endpoint: "awg.example:51820"}}
	specs, err := awg.TransportEndpointSpecs()
	if err != nil || len(specs) != 1 || specs[0].Network != "udp" || specs[0].Hostname != "awg.example" || specs[0].Port != 51820 {
		t.Fatalf("AWG endpoint normalization: err=%v specs=%#v", err, specs)
	}

	xray := &Config{Protocol: ProtocolVLESSReality, VLESS: &VLESSRealityConfig{Endpoint: "xray.example:443"}}
	specs, err = xray.TransportEndpointSpecs()
	if err != nil || len(specs) != 1 || specs[0].Network != "tcp" || specs[0].Hostname != "xray.example" || specs[0].Port != 443 {
		t.Fatalf("Xray endpoint normalization: err=%v specs=%#v", err, specs)
	}

	awg.AWG2.Endpoint = "192.0.2.1:51820"
	specs, err = awg.TransportEndpointSpecs()
	if err != nil || len(specs) != 1 || !specs[0].Address.IsValid() || specs[0].Address.String() != "192.0.2.1:51820" {
		t.Fatalf("numeric AWG endpoint normalization: err=%v specs=%#v", err, specs)
	}
}
