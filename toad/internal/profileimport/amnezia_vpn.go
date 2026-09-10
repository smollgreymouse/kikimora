package profileimport

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

const maxAmneziaVPNPayload = 8 << 20

func parseAmneziaVPN(raw string, opts Options) (*config.Config, error) {
	document, err := decodeAmneziaVPN(raw)
	if err != nil {
		return nil, err
	}

	var root any
	if err := json.Unmarshal(document, &root); err != nil {
		return nil, fmt.Errorf("decode Amnezia VPN JSON: invalid document")
	}
	profile, ok := findAmneziaWireGuardConfig(root, 0)
	if !ok {
		return nil, fmt.Errorf("Amnezia VPN profile does not contain an AWG config")
	}
	return parseWireGuardINI(profile, amneziaVPNLabel(root), opts)
}

func decodeAmneziaVPN(raw string) ([]byte, error) {
	encoded := strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(encoded), "vpn://") {
		return nil, fmt.Errorf("invalid Amnezia VPN share URI")
	}
	encoded = encoded[len("vpn://"):]
	if encoded == "" || len(encoded) > maxAmneziaVPNPayload*2 {
		return nil, fmt.Errorf("invalid Amnezia VPN payload size")
	}

	compressed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		compressed, err = base64.URLEncoding.DecodeString(encoded)
	}
	if err != nil {
		return nil, fmt.Errorf("decode Amnezia VPN payload: invalid base64")
	}
	if len(compressed) > maxAmneziaVPNPayload {
		return nil, fmt.Errorf("Amnezia VPN compressed payload is too large")
	}
	if len(compressed) > 0 && (compressed[0] == '{' || compressed[0] == '[') {
		return compressed, nil
	}
	if len(compressed) < 5 {
		return nil, fmt.Errorf("decode Amnezia VPN payload: truncated qCompress data")
	}

	expected := binary.BigEndian.Uint32(compressed[:4])
	if expected == 0 || expected > maxAmneziaVPNPayload {
		return nil, fmt.Errorf("decode Amnezia VPN payload: invalid uncompressed size")
	}
	reader, err := zlib.NewReader(bytes.NewReader(compressed[4:]))
	if err != nil {
		return nil, fmt.Errorf("decode Amnezia VPN payload: invalid zlib stream")
	}
	decompressed, readErr := io.ReadAll(io.LimitReader(reader, maxAmneziaVPNPayload+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("decode Amnezia VPN payload: corrupt zlib stream")
	}
	if len(decompressed) != int(expected) {
		return nil, fmt.Errorf("decode Amnezia VPN payload: uncompressed size mismatch")
	}
	return decompressed, nil
}

func findAmneziaWireGuardConfig(value any, depth int) (string, bool) {
	if depth > 16 {
		return "", false
	}
	switch current := value.(type) {
	case string:
		trimmed := strings.TrimSpace(current)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			var nested any
			if json.Unmarshal([]byte(trimmed), &nested) == nil {
				return findAmneziaWireGuardConfig(nested, depth+1)
			}
		}
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "[interface]") && strings.Contains(lower, "[peer]") {
			return trimmed, true
		}
	case map[string]any:
		for _, key := range []string{"config", "last_config", "awg", "awg2"} {
			if nested, exists := current[key]; exists {
				if profile, ok := findAmneziaWireGuardConfig(nested, depth+1); ok {
					return profile, true
				}
			}
		}
		if containers, ok := current["containers"]; ok {
			if profile, found := findAmneziaWireGuardConfig(containers, depth+1); found {
				return profile, true
			}
		}
	case []any:
		for i := len(current) - 1; i >= 0; i-- {
			if profile, ok := findAmneziaWireGuardConfig(current[i], depth+1); ok {
				return profile, true
			}
		}
	}
	return "", false
}

func amneziaVPNLabel(value any) string {
	root, ok := value.(map[string]any)
	if !ok {
		return "amnezia-vpn"
	}
	for _, key := range []string{"description", "name", "hostName"} {
		if label, ok := root[key].(string); ok && strings.TrimSpace(label) != "" {
			return label
		}
	}
	return "amnezia-vpn"
}
