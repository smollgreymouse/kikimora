package profileimport

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

type wgINI struct {
	iface map[string]string
	peer  map[string]string
}

func parseWireGuard(raw string, opts Options) (*config.Config, error) {
	payload, label, err := wireGuardPayload(raw)
	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(strings.TrimSpace(payload), "[") {
		return parseWireGuardINI(payload, label, opts)
	}
	return parseWireGuardQuery(raw, label, opts)
}

func wireGuardPayload(raw string) (payload, label string, err error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(trimmed), "[interface]") {
		return trimmed, "wireguard", nil
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return "", "", fmt.Errorf("parse WireGuard share URI: %w", err)
	}
	label = u.Fragment

	encoded := strings.TrimPrefix(trimmed, u.Scheme+"://")
	if i := strings.IndexByte(encoded, '#'); i >= 0 {
		encoded = encoded[:i]
	}
	if i := strings.IndexByte(encoded, '?'); i >= 0 {
		encoded = encoded[:i]
	}
	encoded = strings.TrimPrefix(encoded, "/")

	if decoded, decErr := url.PathUnescape(encoded); decErr == nil && strings.Contains(strings.ToLower(decoded), "[interface]") {
		return decoded, label, nil
	}
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.StdEncoding, base64.RawStdEncoding} {
		decoded, decErr := enc.DecodeString(encoded)
		if decErr == nil && strings.Contains(strings.ToLower(string(decoded)), "[interface]") {
			return string(decoded), label, nil
		}
	}
	return "", label, nil
}

func parseWireGuardINI(raw, label string, opts Options) (*config.Config, error) {
	parsed, err := scanWGIni(raw)
	if err != nil {
		return nil, err
	}
	privateKey := first(parsed.iface, "privatekey", "private_key")
	publicKey := first(parsed.peer, "publickey", "public_key")
	endpoint := first(parsed.peer, "endpoint")
	allowed := splitList(first(parsed.peer, "allowedips", "allowed_ips"))
	addresses := splitList(first(parsed.iface, "address", "addresses"))

	mtu := opts.MTU
	if mtu == 0 {
		mtu = intValue(first(parsed.iface, "mtu"))
	}
	localOpts := opts
	localOpts.MTU = mtu
	if len(localOpts.Address) == 0 {
		localOpts.Address = addresses
	}

	cfg := &config.Config{
		Protocol: config.ProtocolAWG2,
		AWG2: &config.AWG2Config{
			PrivateKey:          privateKey,
			PeerPublicKey:       publicKey,
			PresharedKey:        first(parsed.peer, "presharedkey", "preshared_key"),
			Endpoint:            endpoint,
			AllowedIPs:          allowed,
			PersistentKeepalive: intValue(first(parsed.peer, "persistentkeepalive", "persistent_keepalive")),
			JC:                  intValue(first(parsed.iface, "jc")),
			JMin:                intValue(first(parsed.iface, "jmin")),
			JMax:                intValue(first(parsed.iface, "jmax")),
			S1:                  intValue(first(parsed.iface, "s1")),
			S2:                  intValue(first(parsed.iface, "s2")),
			S3:                  intValue(first(parsed.iface, "s3")),
			S4:                  intValue(first(parsed.iface, "s4")),
			H1:                  first(parsed.iface, "h1"),
			H2:                  first(parsed.iface, "h2"),
			H3:                  first(parsed.iface, "h3"),
			H4:                  first(parsed.iface, "h4"),
			I1:                  first(parsed.iface, "i1"),
			I2:                  first(parsed.iface, "i2"),
			I3:                  first(parsed.iface, "i3"),
			I4:                  first(parsed.iface, "i4"),
			I5:                  first(parsed.iface, "i5"),
		},
	}
	applyCommon(cfg, localOpts, label, "kk-awg0", addresses)
	return cfg, nil
}

func parseWireGuardQuery(raw, label string, opts Options) (*config.Config, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse WireGuard share URI: %w", err)
	}
	q := lowerQuery(u.Query())
	endpoint := queryFirst(q, "endpoint")
	if endpoint == "" && u.Host != "" {
		endpoint = u.Host
	}
	addresses := splitList(queryFirst(q, "address", "addresses"))
	allowed := splitList(queryFirst(q, "allowed_ips", "allowedips"))

	cfg := &config.Config{
		Protocol: config.ProtocolAWG2,
		AWG2: &config.AWG2Config{
			PrivateKey:          queryFirst(q, "private_key", "privatekey"),
			PeerPublicKey:       queryFirst(q, "public_key", "peer_public_key", "publickey"),
			PresharedKey:        queryFirst(q, "preshared_key", "presharedkey", "psk"),
			Endpoint:            endpoint,
			AllowedIPs:          allowed,
			PersistentKeepalive: intValue(queryFirst(q, "persistent_keepalive", "persistentkeepalive")),
			JC:                  intValue(queryFirst(q, "jc")),
			JMin:                intValue(queryFirst(q, "jmin")),
			JMax:                intValue(queryFirst(q, "jmax")),
			S1:                  intValue(queryFirst(q, "s1")),
			S2:                  intValue(queryFirst(q, "s2")),
			S3:                  intValue(queryFirst(q, "s3")),
			S4:                  intValue(queryFirst(q, "s4")),
			H1:                  queryFirst(q, "h1"),
			H2:                  queryFirst(q, "h2"),
			H3:                  queryFirst(q, "h3"),
			H4:                  queryFirst(q, "h4"),
			I1:                  queryFirst(q, "i1"),
			I2:                  queryFirst(q, "i2"),
			I3:                  queryFirst(q, "i3"),
			I4:                  queryFirst(q, "i4"),
			I5:                  queryFirst(q, "i5"),
		},
	}
	applyCommon(cfg, opts, label, "kk-awg0", addresses)
	return cfg, nil
}

func scanWGIni(raw string) (wgINI, error) {
	out := wgINI{iface: map[string]string{}, peer: map[string]string{}}
	section := ""
	peerCount := 0
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if section == "peer" {
				peerCount++
				if peerCount > 1 {
					return wgINI{}, fmt.Errorf("current Toad config supports exactly one WireGuard peer")
				}
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return wgINI{}, fmt.Errorf("invalid WireGuard config line %q", line)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch section {
		case "interface":
			out.iface[key] = value
		case "peer":
			out.peer[key] = value
		default:
			return wgINI{}, fmt.Errorf("WireGuard key outside [Interface]/[Peer]")
		}
	}
	if err := scanner.Err(); err != nil {
		return wgINI{}, fmt.Errorf("scan WireGuard config: %w", err)
	}
	return out, nil
}

func first(values map[string]string, names ...string) string {
	for _, name := range names {
		if value := values[strings.ToLower(name)]; value != "" {
			return value
		}
	}
	return ""
}

func intValue(raw string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(raw))
	return v
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func lowerQuery(in url.Values) map[string][]string {
	out := make(map[string][]string, len(in))
	for key, values := range in {
		out[strings.ToLower(key)] = values
	}
	return out
}

func queryFirst(values map[string][]string, names ...string) string {
	for _, name := range names {
		if vals := values[strings.ToLower(name)]; len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}
