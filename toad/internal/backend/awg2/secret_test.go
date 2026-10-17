package awg2

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestHealthStateDoesNotExposeKeyMaterial(t *testing.T) {
	const privateSecret = "private-secret-material"
	const presharedSecret = "preshared-secret-material"
	raw := "private_key=" + privateSecret + "\n" +
		"preshared_key=" + presharedSecret + "\n" +
		"endpoint=192.0.2.10:51820\n" +
		"last_handshake_time_sec=0\n" +
		"rx_bytes=0\n" +
		"tx_bytes=0\n"

	health, err := parseHealthUAPI(raw, time.Now())
	if err != nil {
		t.Fatalf("parseHealthUAPI: %v", err)
	}
	dump := fmt.Sprintf("%+v", health)
	for _, secret := range []string{privateSecret, presharedSecret} {
		if strings.Contains(dump, secret) {
			t.Fatalf("health state leaked key material: %s", dump)
		}
	}
}
