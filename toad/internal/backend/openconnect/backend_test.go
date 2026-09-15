package openconnect

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

func TestBuildArgsUsesFileBackedCredentials(t *testing.T) {
	cfg := &config.Config{
		Interface: "kk-oc0",
		MTU:       1380,
		OpenConnect: &config.OpenConnectConfig{
			Gateway:          "https://vpn.example.test",
			VPNProtocol:      "anyconnect",
			AuthGroup:        "Employees",
			Username:         "test-user",
			PasswordFile:     "/tmp/oc-password-file",
			TokenMode:        "totp",
			TokenSecretFile:  "/tmp/oc-token-file",
			UserAgent:        "AnyConnect Linux",
			ServerCert:       "pin-sha256:test-pin",
			DisableIPv6:      true,
			ReconnectTimeout: 600,
		},
	}

	args := buildArgs(cfg, "/tmp/route-free.sh")
	joined := strings.Join(args, "\n")

	for _, want := range []string{
		"--protocol=anyconnect",
		"--interface=kk-oc0",
		"--script=/tmp/route-free.sh",
		"--user=test-user",
		"--authgroup=Employees",
		"--passwd-on-stdin",
		"--token-mode=totp",
		"--token-secret=@/tmp/oc-token-file",
		"--useragent=AnyConnect Linux",
		"--servercert=pin-sha256:test-pin",
		"--disable-ipv6",
		"--reconnect-timeout=600",
		"https://vpn.example.test",
	} {
		if !slices.Contains(args, want) {
			t.Fatalf("missing argument %q in %#v", want, args)
		}
	}
	if strings.Contains(joined, "/tmp/oc-password-file") {
		t.Fatalf("password file path must not be passed to argv: %q", joined)
	}
	if slices.Contains(args, "--no-dtls") {
		t.Fatalf("DTLS must remain enabled by default")
	}
}

func TestBuildArgsCanDisableUDP(t *testing.T) {
	cfg := &config.Config{
		Interface: "kk-oc0",
		MTU:       1400,
		OpenConnect: &config.OpenConnectConfig{
			Gateway:     "vpn.example.test",
			VPNProtocol: "anyconnect",
			Username:    "test-user",
			TokenMode:   "none",
			DisableUDP:  true,
		},
	}

	args := buildArgs(cfg, "/tmp/route-free.sh")
	if !slices.Contains(args, "--no-dtls") {
		t.Fatalf("expected --no-dtls in %#v", args)
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--token-") {
			t.Fatalf("token argument present for token_mode=none: %q", arg)
		}
		if strings.HasPrefix(arg, "--authgroup=") {
			t.Fatalf("auth group argument present when auth_group is empty: %q", arg)
		}
	}
}

func TestRedactingLineWriterRemovesHTTPSessionSecretsAcrossWrites(t *testing.T) {
	var dst bytes.Buffer
	w := newRedactingLineWriter(&dst)
	chunks := []string{
		"[time] Set-Coo",
		"kie: webvpncontext=secret-value; Secure\n",
		"[time] Cookie: webvpn=another-secret\n",
		"[time] Authorization: Basic hidden\n",
		"[time] harmless status line\n",
	}
	for _, chunk := range chunks {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	got := dst.String()
	for _, secret := range []string{"secret-value", "another-secret", "Basic hidden"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted output still contains %q: %q", secret, got)
		}
	}
	for _, want := range []string{
		"Set-Cookie: <redacted>",
		"Cookie: <redacted>",
		"Authorization: <redacted>",
		"harmless status line",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted output missing %q: %q", want, got)
		}
	}
}
