//go:build windows

package openconnect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRouteFreeVPNScriptIsRouteAndDNSSafe(t *testing.T) {
	// Verify that the embedded script does not install routes,
	// mutate DNS or touch the firewall — only TUN config + state publish.
	for _, banned := range []string{
		"route add",
		"route delete",
		"AddIPAddress",
		"SetDNSServerSearchOrder",
		"SetDynamicDNSRegistration",
		"netsh interface ipv4 set dns",
		"netsh interface ipv6 set dns",
		"netsh advfirewall",
		"iptables",
	} {
		if strings.Contains(strings.ToLower(routeFreeVPNScript), strings.ToLower(banned)) {
			t.Fatalf("script contains banned/route-DNS command %q", banned)
		}
	}
}

func TestRouteFreeVPNScriptContainsRequiredSections(t *testing.T) {
	// The script must configure TUN, publish state and use atomic temp+move.
	for _, want := range []string{
		"netsh interface ip set address",
		"netsh interface ipv4 set subinterface",
		"move /y",
		"openconnect-network.env",
		"echo reason=",
		"tun_dev",
		"mtu",
		"ipv4_address",
	} {
		if !strings.Contains(routeFreeVPNScript, want) {
			t.Fatalf("script missing required section %q", want)
		}
	}
}

func TestWriteRouteFreeVPNScriptProducesExecutableBat(t *testing.T) {
	dir := t.TempDir()
	path, err := writeRouteFreeVPNScript(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".bat") {
		t.Fatalf("script path = %q, want .bat extension", path)
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("script path = %q, want absolute", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("script file is empty")
	}
}
