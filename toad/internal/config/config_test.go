package config

import "testing"

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
