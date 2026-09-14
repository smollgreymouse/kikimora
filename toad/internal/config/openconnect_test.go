package config

import "testing"

func validOpenConnectConfig() *Config {
	return &Config{
		Name:      "corp-oc",
		Protocol:  ProtocolOpenConnect,
		Interface: "kk-oc0",
		MTU:       1380,
		StateDir:  "/run/kikimora/toads/corp-oc",
		OpenConnect: &OpenConnectConfig{
			Gateway:          "https://vpn.example.test",
			Username:         "test-user",
			PasswordFile:     "/run/kikimora/credentials/corp-oc.password",
			TokenMode:        "totp",
			TokenSecretFile:  "/run/kikimora/credentials/corp-oc.totp",
			ReconnectTimeout: 600,
		},
	}
}

func TestOpenConnectConfigAllowsServerAssignedAddress(t *testing.T) {
	cfg := validOpenConnectConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.OpenConnect.VPNProtocol != "anyconnect" {
		t.Fatalf("VPNProtocol = %q, want anyconnect", cfg.OpenConnect.VPNProtocol)
	}
}

func TestOpenConnectConfigRequiresAbsoluteTOTPFile(t *testing.T) {
	cfg := validOpenConnectConfig()
	cfg.OpenConnect.TokenSecretFile = "relative-token-file"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() unexpectedly accepted a relative TOTP file")
	}
}

func TestOpenConnectConfigRejectsOtherProtocolSection(t *testing.T) {
	cfg := validOpenConnectConfig()
	cfg.VLESS = &VLESSRealityConfig{}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() unexpectedly accepted mixed protocol sections")
	}
}
