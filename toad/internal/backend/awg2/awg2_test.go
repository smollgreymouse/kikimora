package awg2

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

func testKey(fill byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}

func TestWireGuardKeyToHex(t *testing.T) {
	raw := testKey(0x2a)
	got, err := wireguardKeyToHex(raw)
	if err != nil {
		t.Fatalf("wireguardKeyToHex: %v", err)
	}
	want := strings.Repeat("2a", 32)
	if got != want {
		t.Fatalf("hex key mismatch: got %q want %q", got, want)
	}
}

func TestWireGuardKeyRejectsMalformedWithoutEchoingSecret(t *testing.T) {
	secret := "not-a-valid-secret-key!!!"
	_, err := wireguardKeyToHex(secret)
	if err == nil {
		t.Fatal("malformed key accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("key material leaked through validation error")
	}
}

func TestBuildConfigUAPI(t *testing.T) {
	cfg := &config.AWG2Config{
		PrivateKey:          testKey(0x11),
		PeerPublicKey:       testKey(0x22),
		PresharedKey:        testKey(0x33),
		Endpoint:            "192.0.2.10:51820",
		AllowedIPs:          []string{"0.0.0.0/0", "::/0"},
		PersistentKeepalive: 25,
		JC:                  4,
		JMin:                40,
		JMax:                80,
		S1:                  11,
		S2:                  12,
		S3:                  13,
		S4:                  14,
		H1:                  "1001",
		H2:                  "1002",
		H3:                  "1003",
		H4:                  "1004",
		I1:                  "<r 10><c><t>",
		I2:                  "<r 20>",
		I3:                  "<c>",
		I4:                  "<t>",
		I5:                  "<r 30>",
	}

	payload, err := buildConfigUAPI(cfg)
	if err != nil {
		t.Fatalf("buildConfigUAPI: %v", err)
	}
	for _, want := range []string{
		"private_key=" + strings.Repeat("11", 32),
		"replace_peers=true",
		"jc=4", "jmin=40", "jmax=80",
		"s1=11", "s2=12", "s3=13", "s4=14",
		"h1=1001", "h2=1002", "h3=1003", "h4=1004",
		"i1=<r 10><c><t>", "i2=<r 20>", "i3=<c>", "i4=<t>", "i5=<r 30>",
		"public_key=" + strings.Repeat("22", 32),
		"preshared_key=" + strings.Repeat("33", 32),
		"endpoint=192.0.2.10:51820",
		"persistent_keepalive_interval=25",
		"allowed_ip=0.0.0.0/0",
		"allowed_ip=::/0",
	} {
		if !strings.Contains(payload, want+"\n") {
			t.Fatalf("payload missing %q:\n%s", want, payload)
		}
	}
	if !strings.HasSuffix(payload, "\n\n") {
		t.Fatalf("UAPI operation is not blank-line terminated: %q", payload)
	}
}

func TestBuildConfigUAPIOmitsOptionalPresharedKey(t *testing.T) {
	payload, err := buildConfigUAPI(&config.AWG2Config{
		PrivateKey:    testKey(0x11),
		PeerPublicKey: testKey(0x22),
		Endpoint:      "192.0.2.10:51820",
		AllowedIPs:    []string{"10.0.0.0/8"},
	})
	if err != nil {
		t.Fatalf("buildConfigUAPI: %v", err)
	}
	if strings.Contains(payload, "preshared_key=") {
		t.Fatal("optional preshared key unexpectedly emitted")
	}
}

func TestParseHealthUAPI(t *testing.T) {
	now := time.Unix(2_000_000_000, 500)
	raw := fmt.Sprintf("endpoint=192.0.2.10:51820\nlast_handshake_time_sec=%d\nlast_handshake_time_nsec=500\ntx_bytes=123\nrx_bytes=456\n", now.Add(-30*time.Second).Unix())
	health, err := parseHealthUAPI(raw, now)
	if err != nil {
		t.Fatalf("parseHealthUAPI: %v", err)
	}
	if health.State != "online" || !health.Connected {
		t.Fatalf("unexpected health classification: %+v", health)
	}
	if health.Endpoint != "192.0.2.10:51820" || health.TXBytes != 123 || health.RXBytes != 456 {
		t.Fatalf("health counters/endpoint mismatch: %+v", health)
	}
	if health.LastHandshakeAge == nil || *health.LastHandshakeAge != 30*time.Second {
		t.Fatalf("handshake age mismatch: %+v", health.LastHandshakeAge)
	}
}

func TestParseHealthUAPINoHandshakeIsConnecting(t *testing.T) {
	health, err := parseHealthUAPI("last_handshake_time_sec=0\nrx_bytes=0\ntx_bytes=0\n", time.Now())
	if err != nil {
		t.Fatalf("parseHealthUAPI: %v", err)
	}
	if health.State != "connecting" || health.Connected {
		t.Fatalf("missing handshake must stay connecting: %+v", health)
	}
}

func TestCloseIsIdempotentBeforeStart(t *testing.T) {
	backend := New(nil, nil)
	if err := backend.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := backend.Health(context.Background()).State; got != "stopped" {
		t.Fatalf("health after idempotent Close = %q", got)
	}
}
