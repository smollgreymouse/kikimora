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
