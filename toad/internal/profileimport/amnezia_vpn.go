package profileimport

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
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
	if ok {
		return parseWireGuardINI(profile, amneziaVPNLabel(root), opts)
	}
	xrayProfile, ok := findAmneziaXrayConfig(root, 0)
	if ok {
		return parseAmneziaXray(xrayProfile, amneziaVPNLabel(root), opts)
	}
	return nil, fmt.Errorf("Amnezia VPN profile does not contain a supported AWG or Xray VLESS Reality config")
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

type amneziaXrayConfig struct {
	Outbounds []struct {
		Protocol string `json:"protocol"`
		Settings struct {
			VNext []struct {
				Address string          `json:"address"`
				Port    json.RawMessage `json:"port"`
				Users   []struct {
					ID         string `json:"id"`
					Flow       string `json:"flow"`
					Encryption string `json:"encryption"`
				} `json:"users"`
			} `json:"vnext"`
		} `json:"settings"`
		StreamSettings struct {
			Network         string `json:"network"`
			Security        string `json:"security"`
			RealitySettings struct {
				Fingerprint string `json:"fingerprint"`
				ServerName  string `json:"serverName"`
				PublicKey   string `json:"publicKey"`
				ShortID     string `json:"shortId"`
				SpiderX     string `json:"spiderX"`
			} `json:"realitySettings"`
		} `json:"streamSettings"`
	} `json:"outbounds"`
}

func findAmneziaXrayConfig(value any, depth int) (string, bool) {
	if depth > 16 {
		return "", false
	}
	switch current := value.(type) {
	case string:
		trimmed := strings.TrimSpace(current)
		if !strings.HasPrefix(trimmed, "{") {
			return "", false
		}
		var nested any
		if json.Unmarshal([]byte(trimmed), &nested) == nil {
			return findAmneziaXrayConfig(nested, depth+1)
		}
	case map[string]any:
		if _, exists := current["outbounds"]; exists {
			encoded, err := json.Marshal(current)
			if err == nil {
				var candidate amneziaXrayConfig
				if json.Unmarshal(encoded, &candidate) == nil {
					for _, outbound := range candidate.Outbounds {
						if strings.EqualFold(outbound.Protocol, "vless") {
							return string(encoded), true
						}
					}
				}
			}
		}
		for _, key := range []string{"xray", "last_config", "config", "containers"} {
			if nested, exists := current[key]; exists {
				if profile, ok := findAmneziaXrayConfig(nested, depth+1); ok {
					return profile, true
				}
			}
		}
	case []any:
		for i := len(current) - 1; i >= 0; i-- {
			if profile, ok := findAmneziaXrayConfig(current[i], depth+1); ok {
				return profile, true
			}
		}
	}
	return "", false
}

func parseAmneziaXray(raw, label string, opts Options) (*config.Config, error) {
	var document amneziaXrayConfig
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return nil, fmt.Errorf("decode Amnezia Xray config: invalid document")
	}
	for _, outbound := range document.Outbounds {
		if !strings.EqualFold(outbound.Protocol, "vless") {
			continue
		}
		if !strings.EqualFold(outbound.StreamSettings.Security, "reality") {
			return nil, fmt.Errorf("Amnezia Xray config is not VLESS Reality")
		}
		if len(outbound.Settings.VNext) != 1 || len(outbound.Settings.VNext[0].Users) != 1 {
			return nil, fmt.Errorf("Amnezia Xray config must contain exactly one VLESS server and user")
		}
		server := outbound.Settings.VNext[0]
		user := server.Users[0]
		port, err := amneziaXrayPort(server.Port)
		if err != nil {
			return nil, err
		}
		if encryption := strings.ToLower(strings.TrimSpace(user.Encryption)); encryption != "" && encryption != "none" {
			return nil, fmt.Errorf("Amnezia Xray config has unsupported VLESS encryption")
		}
		host := strings.Trim(strings.TrimSpace(server.Address), "[]")
		if host == "" {
			return nil, fmt.Errorf("Amnezia Xray config is missing the VLESS server address")
		}
		cfg := &config.Config{
			Protocol: config.ProtocolVLESSReality,
			VLESS: &config.VLESSRealityConfig{
				Endpoint:    net.JoinHostPort(host, port),
				UUID:        user.ID,
				ServerName:  outbound.StreamSettings.RealitySettings.ServerName,
				PublicKey:   outbound.StreamSettings.RealitySettings.PublicKey,
				ShortID:     outbound.StreamSettings.RealitySettings.ShortID,
				Flow:        user.Flow,
				Fingerprint: outbound.StreamSettings.RealitySettings.Fingerprint,
				Transport:   strings.ToLower(outbound.StreamSettings.Network),
				SpiderX:     outbound.StreamSettings.RealitySettings.SpiderX,
			},
		}
		applyCommon(cfg, opts, label, "kk-xray0", []string{"10.255.0.2/30"})
		return cfg, nil
	}
	return nil, fmt.Errorf("Amnezia Xray config does not contain a VLESS outbound")
}

func amneziaXrayPort(raw json.RawMessage) (string, error) {
	var number int
	if err := json.Unmarshal(raw, &number); err == nil && number > 0 && number <= 65535 {
		return strconv.Itoa(number), nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		port, convErr := strconv.Atoi(text)
		if convErr == nil && port > 0 && port <= 65535 {
			return strconv.Itoa(port), nil
		}
	}
	return "", fmt.Errorf("Amnezia Xray config has an invalid VLESS server port")
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
