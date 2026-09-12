package awg2

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

func wireguardKeyToHex(raw string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", fmt.Errorf("decode wireguard key: %w", err)
	}
	if len(decoded) != 32 {
		return "", fmt.Errorf("wireguard key must be 32 bytes, got %d", len(decoded))
	}
	return hex.EncodeToString(decoded), nil
}

func buildConfigUAPI(cfg *config.AWG2Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("nil AWG2 config")
	}

	privateKey, err := wireguardKeyToHex(cfg.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("private key: %w", err)
	}
	publicKey, err := wireguardKeyToHex(cfg.PeerPublicKey)
	if err != nil {
		return "", fmt.Errorf("peer public key: %w", err)
	}

	var presharedKey string
	if strings.TrimSpace(cfg.PresharedKey) != "" {
		presharedKey, err = wireguardKeyToHex(cfg.PresharedKey)
		if err != nil {
			return "", fmt.Errorf("preshared key: %w", err)
		}
	}

	var b strings.Builder
	write := func(key, value string) {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(value)
		b.WriteByte('\n')
	}
	writeInt := func(key string, value int) {
		write(key, strconv.Itoa(value))
	}

	write("private_key", privateKey)
	write("replace_peers", "true")
	writeInt("jc", cfg.JC)
	writeInt("jmin", cfg.JMin)
	writeInt("jmax", cfg.JMax)
	writeInt("s1", cfg.S1)
	writeInt("s2", cfg.S2)
	writeInt("s3", cfg.S3)
	writeInt("s4", cfg.S4)

	for _, field := range []struct {
		name  string
		value string
	}{
		{"h1", cfg.H1},
		{"h2", cfg.H2},
		{"h3", cfg.H3},
		{"h4", cfg.H4},
		{"i1", cfg.I1},
		{"i2", cfg.I2},
		{"i3", cfg.I3},
		{"i4", cfg.I4},
		{"i5", cfg.I5},
	} {
		if field.value != "" {
			write(field.name, field.value)
		}
	}

	write("public_key", publicKey)
	if presharedKey != "" {
		write("preshared_key", presharedKey)
	}
	write("endpoint", cfg.Endpoint)
	writeInt("persistent_keepalive_interval", cfg.PersistentKeepalive)
	for _, allowedIP := range cfg.AllowedIPs {
		write("allowed_ip", allowedIP)
	}

	// A blank line explicitly terminates one official UAPI set operation.
	b.WriteByte('\n')
	return b.String(), nil
}
