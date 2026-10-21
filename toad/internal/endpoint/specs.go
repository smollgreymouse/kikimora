package endpoint

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ParseSpecs parses the line-oriented provider contract used by the legacy
// endpoint scripts. A line may contain an IP/hostname with an optional port;
// comments and the provider mode header are ignored.
func ParseSpecs(r io.Reader, network string, defaultPort uint16) ([]EndpointSpec, error) {
	if network == "" {
		network = "tcp"
	}
	if defaultPort == 0 {
		defaultPort = 443
	}
	result := make([]EndpointSpec, 0)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if comment := strings.IndexByte(line, '#'); comment >= 0 {
			line = strings.TrimSpace(line[:comment])
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, token := range strings.Fields(line) {
			spec, err := ParseSpec(token, network, defaultPort)
			if err != nil {
				return nil, err
			}
			result = append(result, spec)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// ParseSpec normalizes one IP/hostname endpoint with an optional port.
func ParseSpec(raw, network string, defaultPort uint16) (EndpointSpec, error) {
	if raw == "" || strings.ContainsAny(raw, "/ \t\r\n") {
		return EndpointSpec{}, fmt.Errorf("invalid endpoint %q", raw)
	}
	if addr, err := netip.ParseAddr(raw); err == nil {
		return EndpointSpec{Network: network, Address: netip.AddrPortFrom(addr, defaultPort), Port: defaultPort}, nil
	}
	host := raw
	port := defaultPort
	if parsedHost, parsedPort, err := net.SplitHostPort(raw); err == nil {
		host = parsedHost
		port = parsePort(parsedPort)
		if port == 0 {
			return EndpointSpec{}, fmt.Errorf("invalid endpoint port in %q", raw)
		}
	} else if strings.Count(raw, ":") == 1 {
		parts := strings.SplitN(raw, ":", 2)
		host, port = parts[0], parsePort(parts[1])
		if port == 0 {
			return EndpointSpec{}, fmt.Errorf("invalid endpoint port in %q", raw)
		}
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return EndpointSpec{Network: network, Address: netip.AddrPortFrom(addr, port), Port: port}, nil
	}
	if host == "" || strings.ContainsAny(host, "[]:?") || strings.Contains(host, "..") {
		return EndpointSpec{}, fmt.Errorf("invalid endpoint host %q", host)
	}
	return EndpointSpec{Network: network, Hostname: host, Port: port}, nil
}

func parsePort(raw string) uint16 {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 65535 {
		return 0
	}
	return uint16(value)
}

// ResolveSpecs is the hostname-preserving form used by endpoint.Manager.
func (p StaticProvider) ResolveSpecs(_ context.Context, network string, defaultPort uint16) ([]EndpointSpec, error) {
	if p.Path == "" {
		return nil, fmt.Errorf("static endpoint file is empty")
	}
	out, err := readBounded(p.Path, p.MaxBytes)
	if err != nil {
		return nil, err
	}
	return ParseSpecs(strings.NewReader(string(out)), network, defaultPort)
}

// ResolveSpecs executes a compatibility provider with a deliberately small
// environment and keeps hostnames unresolved for the endpoint manager.
func (p CommandProvider) ResolveSpecs(ctx context.Context, network string, defaultPort uint16) ([]EndpointSpec, error) {
	out, err := p.output(ctx)
	if err != nil {
		return nil, err
	}
	return ParseSpecs(strings.NewReader(string(out)), network, defaultPort)
}

func readBounded(path string, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	out, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(out) > maxBytes {
		return nil, fmt.Errorf("endpoint provider output too large")
	}
	return out, nil
}

func (p CommandProvider) output(ctx context.Context) ([]byte, error) {
	if p.Command == "" {
		return nil, fmt.Errorf("endpoint command is empty")
	}
	if p.Timeout <= 0 {
		p.Timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	args := strings.Fields(p.Command)
	if len(args) == 0 {
		return nil, fmt.Errorf("endpoint command is empty")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = allowlistedEnv(p.Env)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	if p.MaxBytes <= 0 {
		p.MaxBytes = 64 * 1024
	}
	if len(out) > p.MaxBytes {
		return nil, fmt.Errorf("endpoint command output too large")
	}
	return out, nil
}

var allowedProviderEnv = map[string]bool{
	"KIKIMORA_ENDPOINT_ROLE": true, "KIKIMORA_ENDPOINT_INTERFACE": true,
	"KIKIMORA_ENDPOINTS_DIR": true, "KIKIMORA_VPN_CONFIG": true,
	"KIKIMORA_HAPP_STATE_DIR": true, "KIKIMORA_HAPP_PROBE_URL": true,
	"KIKIMORA_HAPP_PROBE_ROUNDS": true, "KIKIMORA_HAPP_PROBE_MIN_BYTES": true,
	"KIKIMORA_HAPP_PROBE_DOMINANCE": true, "KIKIMORA_HAPP_CACHE_TTL": true,
	"KIKIMORA_IP": true, "KIKIMORA_SS": true, "KIKIMORA_CURL": true,
	"KIKIMORA_GETENT": true, "KIKIMORA_SLEEP": true, "KIKIMORA_PGREP": true,
}

func allowlistedEnv(extra map[string]string) []string {
	values := map[string]string{"PATH": os.Getenv("PATH"), "LANG": "C", "LC_ALL": "C"}
	for key, value := range extra {
		if allowedProviderEnv[key] {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}
