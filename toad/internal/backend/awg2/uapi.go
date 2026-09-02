package awg2

import (
	"encoding/hex"
	"encoding/base64"
	"fmt"
	"strings"
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

func buildUAPI(fields map[string]string) string {
	var b strings.Builder
	for k, v := range fields {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
		b.WriteByte('\n')
	}
	return b.String()
}
