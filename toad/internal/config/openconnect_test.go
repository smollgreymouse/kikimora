package config

import (
	"path/filepath"
	"testing"
)

func absoluteTestPath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	return path
}

func validOpenConnectConfig(t *testing.T) *Config {
	t.Helper()
	return &Config{
		Name:      "corp-oc",
		Protocol:  ProtocolOpenConnect,
		Interface: "kk-oc0",
		MTU:       1380,
		StateDir:  absoluteTestPath(t, "state"),
		OpenConnect: &OpenConnectConfig{
			Gateway:          "https://vpn.example.test",
			AuthGroup:        "Employees",
			Username:         "test-user",
			PasswordFile:     absoluteTestPath(t, "password"),
			TokenMode:        "totp",
			TokenSecretFile:  absoluteTestPath(t, "totp"),
			ReconnectTimeout: 600,
		},
	}
}

func TestOpenConnectConfigAllowsServerAssignedAddress(t *testing.T) {
	cfg := validOpenConnectConfig(t)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.OpenConnect.VPNProtocol != "anyconnect" {
		t.Fatalf("VPNProtocol = %q, want anyconnect", cfg.OpenConnect.VPNProtocol)
	}
}

func TestOpenConnectConfigAllowsEmptyAuthGroupForGenericServers(t *testing.T) {
	cfg := validOpenConnectConfig(t)
	cfg.OpenConnect.AuthGroup = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected empty auth group: %v", err)
	}
}

func TestOpenConnectConfigRejectsAuthGroupLineBreak(t *testing.T) {
	cfg := validOpenConnectConfig(t)
	cfg.OpenConnect.AuthGroup = "Employees\nOther"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() unexpectedly accepted auth group with line break")
	}
}

func TestOpenConnectConfigRequiresAbsoluteTOTPFile(t *testing.T) {
	cfg := validOpenConnectConfig(t)
	cfg.OpenConnect.TokenSecretFile = "relative-token-file"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() unexpectedly accepted a relative TOTP file")
	}
}

func TestOpenConnectConfigRejectsOtherProtocolSection(t *testing.T) {
	cfg := validOpenConnectConfig(t)
	cfg.VLESS = &VLESSRealityConfig{}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() unexpectedly accepted mixed protocol sections")
	}
}

func TestOpenConnectGatewayNormalization(t *testing.T) {
	tests := []struct {
		raw      string
		wantHost string
		wantPort uint16
		wantErr  bool
	}{
		{"ve.example", "ve.example", 443, false},
		{"ve.example:4443", "ve.example", 4443, false},
		{"https://ve.example", "ve.example", 443, false},
		{"https://ve.example:4443/path", "ve.example", 4443, false},
		{"https://user@ve.example", "", 0, true},
		{"https://ve.example:70000", "", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			host, port, err := parseOpenConnectGateway(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseOpenConnectGateway(%q) unexpectedly succeeded", tc.raw)
				}
				return
			}
			if err != nil || host != tc.wantHost || port != tc.wantPort {
				t.Fatalf("parseOpenConnectGateway(%q)=(%q,%d,%v), want (%q,%d,nil)", tc.raw, host, port, err, tc.wantHost, tc.wantPort)
			}
		})
	}
}

func TestOpenConnectTransportEndpointSpecsStripURLSyntax(t *testing.T) {
	cfg := validOpenConnectConfig(t)
	cfg.OpenConnect.Gateway = "https://ve.example:4443/path"
	specs, err := cfg.TransportEndpointSpecs()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].Hostname != "ve.example" || specs[0].Port != 4443 || specs[0].Address.IsValid() {
		t.Fatalf("unexpected normalized endpoint: %#v", specs)
	}
}

func TestOpenConnectDefaultEndpointPolicyIsNotEmpty(t *testing.T) {
	cfg := validOpenConnectConfig(t)
	specs, err := cfg.ResolveTransportEndpoints(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].Hostname != "vpn.example.test" || specs[0].Port != 443 {
		t.Fatalf("unexpected default endpoint set: %#v", specs)
	}
}
