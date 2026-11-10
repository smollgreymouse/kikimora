package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

var lowerHex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

// LeshyServer holds the parts of the router configuration that do not depend on roles.
type LeshyServer struct {
	ListenAddress string
	Upstreams     []string
	// DNSProtocol is the transport used to reach the upstreams: "udp" (the
	// Leshy default when the key is absent) or "tcp". An empty value means the
	// operator has not pinned a transport and the key is not written.
	DNSProtocol string
	// RoutingBackend selects the FIB writer: "local" (the Leshy default,
	// key not written) or "kikimora" — the managed mode where the core is
	// the single writer for Leshy-selected routes.
	RoutingBackend string
	// KikimoraSocket is the control socket the managed backend talks to;
	// required when RoutingBackend is "kikimora".
	KikimoraSocket string
	// WriterLock is the process-lifetime writer fence Leshy takes before
	// constructing its RouteManager (08D1 §31.2). The core owns the value:
	// regeneration always writes the platform default, keeping the digest
	// deterministic (§31.23).
	WriterLock string
	// KikimoraAuthFile is the privileged-control credential Leshy presents on
	// the managed backend (privileged_control_auth_v1). Managed-only.
	KikimoraAuthFile string
	// KikimoraActivationID is the 128-bit lowercase-hex activation identity of
	// the current managed generation (08D1 31.4). Managed-only.
	KikimoraActivationID string
	// KikimoraModeGeneration is the committed monotonic generation the config
	// belongs to; zero in local mode. Managed-only.
	KikimoraModeGeneration uint64
}

// LeshyWriterLockPath is the OS fence path for Kikimora-generated Leshy
// installations (08D1 §31.2).
func LeshyWriterLockPath() string {
	if runtime.GOOS == "windows" {
		return `C:\ProgramData\Kikimora\state\leshy-writer.lock`
	}
	return "/run/kikimora/leshy-writer.lock"
}

// LeshyManagedDefaultSocket is the socket the generated managed
// configuration points at on Linux.
const LeshyManagedDefaultSocket = "/run/kikimora/core.sock"

// ValidateDNSProtocol accepts only the transports the Leshy runtime implements.
// There is deliberately no "auto": promising a fallback the resolver does not
// have would turn UDP-blocked networks into silent failures.
func ValidateDNSProtocol(protocol string) error {
	switch protocol {
	case "", "udp", "tcp":
		return nil
	}
	return fmt.Errorf("unsupported dns_protocol %q: want \"\", \"udp\" or \"tcp\"", protocol)
}

// LeshyZoneSpec is one generated zone: a role, the device file the core publishes for it, and the
// entries of that role's list.
type LeshyZoneSpec struct {
	Name         string
	Mode         string
	RouteTarget  string
	Domains      []string
	Patterns     []string
	StaticRoutes []string
	// IPv6 is the zone's AAAA enable policy written into the generated
	// configuration. nil means the generator default (enabled): the
	// controller's per-family authority still decides what actually routes.
	IPv6 *bool
}

// DefaultLeshyIPv6 is the product default: dual-stack zones, with the
// controller denying AAAA intents whenever IPv6 is not actually Ready.
const DefaultLeshyIPv6 = true

// DefaultLeshyVPNDir is where the core publishes <role>.dev files that zones point at.
func DefaultLeshyVPNDir() string {
	if runtime.GOOS == "windows" {
		return `C:\ProgramData\Kikimora\leshy\vpn`
	}
	return "/run/kikimora/leshy/vpn"
}

// ReadLeshyServer keeps the operator's server settings when the configuration is regenerated. A
// missing file simply yields the defaults.
func ReadLeshyServer(path string) LeshyServer {
	server := LeshyServer{
		ListenAddress: "127.0.0.1:53053",
		Upstreams:     []string{"1.1.1.1:53", "8.8.8.8:53"},
		WriterLock:    LeshyWriterLockPath(),
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return server
	}
	var parsed struct {
		Server struct {
			ListenAddress  string   `toml:"listen_address"`
			Upstream       []string `toml:"default_upstream"`
			DNSProtocol    string   `toml:"dns_protocol"`
			RoutingBackend string   `toml:"routing_backend"`
			KikimoraSocket string   `toml:"kikimora_socket"`
			WriterLock     string   `toml:"writer_lock"`
		} `toml:"server"`
	}
	if _, err := toml.Decode(string(data), &parsed); err != nil {
		return server
	}
	if parsed.Server.ListenAddress != "" {
		server.ListenAddress = parsed.Server.ListenAddress
	}
	if len(parsed.Server.Upstream) > 0 {
		server.Upstreams = parsed.Server.Upstream
	}
	if parsed.Server.DNSProtocol != "" {
		server.DNSProtocol = parsed.Server.DNSProtocol
	}
	server.RoutingBackend = parsed.Server.RoutingBackend
	server.KikimoraSocket = parsed.Server.KikimoraSocket
	return server
}

// WriteLeshyConfig generates the router configuration from the roles and their lists. The file is
// a product of the core, not a place to hand-edit: every role is one zone, and the zone points at
// the device file the core publishes when the role comes up.
func WriteLeshyConfig(path string, server LeshyServer, zones []LeshyZoneSpec) error {
	data, err := RenderLeshyConfig(server, zones)
	if err != nil {
		return err
	}
	return fsutil.WriteAtomicFile(path, data, 0o644)
}

// RenderLeshyConfig returns the exact bytes of the generated router
// configuration, including the final newline. It is deterministic for one
// input: the mode transaction records its SHA-256, and a re-render after a
// crash must reproduce the same digest.
func RenderLeshyConfig(server LeshyServer, zones []LeshyZoneSpec) ([]byte, error) {
	if server.ListenAddress == "" {
		server.ListenAddress = "127.0.0.1:53053"
	}
	if len(server.Upstreams) == 0 {
		server.Upstreams = []string{"1.1.1.1:53", "8.8.8.8:53"}
	}
	if err := ValidateDNSProtocol(server.DNSProtocol); err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("# Generated by Kikimora from the roles and their Leshy lists. Do not edit:\n")
	b.WriteString("# change the list on the role page, the core rewrites this file.\n")
	b.WriteString("[server]\n")
	fmt.Fprintf(&b, "listen_address = %q\n", server.ListenAddress)
	b.WriteString("default_upstream = [\n")
	for _, upstream := range server.Upstreams {
		fmt.Fprintf(&b, "    %q,\n", upstream)
	}
	b.WriteString("]\n")
	if server.DNSProtocol != "" {
		fmt.Fprintf(&b, "dns_protocol = %q\n", server.DNSProtocol)
	}
	if server.WriterLock != "" {
		fmt.Fprintf(&b, "writer_lock = %q\n", server.WriterLock)
	}
	switch server.RoutingBackend {
	case "kikimora":
		if server.KikimoraSocket == "" {
			return nil, fmt.Errorf("routing_backend = \"kikimora\" requires kikimora_socket")
		}
		if err := validateManagedModeIdentity(server); err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "routing_backend = \"kikimora\"\n")
		fmt.Fprintf(&b, "kikimora_socket = %q\n", server.KikimoraSocket)
		fmt.Fprintf(&b, "kikimora_auth_file = %q\n", server.KikimoraAuthFile)
		fmt.Fprintf(&b, "kikimora_activation_id = %q\n", server.KikimoraActivationID)
		fmt.Fprintf(&b, "kikimora_mode_generation = %d\n", server.KikimoraModeGeneration)
	case "":
		// local is the Leshy default; no managed-only field may leak into a
		// local configuration.
		if server.KikimoraSocket != "" || server.KikimoraAuthFile != "" ||
			server.KikimoraActivationID != "" || server.KikimoraModeGeneration != 0 {
			return nil, fmt.Errorf("local mode must not carry managed-only fields (kikimora_socket, kikimora_auth_file, kikimora_activation_id, kikimora_mode_generation)")
		}
	default:
		return nil, fmt.Errorf("unknown routing backend %q", server.RoutingBackend)
	}

	for _, zone := range zones {
		// Leshy rejects a zone without a single domain, pattern or route, and a role
		// whose list is empty has nothing to route: such a role simply does not appear.
		if len(zone.Domains) == 0 && len(zone.Patterns) == 0 && len(zone.StaticRoutes) == 0 {
			continue
		}
		mode := zone.Mode
		if mode == "" {
			mode = "inclusive"
		}
		b.WriteString("\n[[zones]]\n")
		fmt.Fprintf(&b, "name = %q\n", zone.Name)
		fmt.Fprintf(&b, "mode = %q\n", mode)
		b.WriteString("dns_servers = []\n")
		b.WriteString("route_type = \"dev\"\n")
		fmt.Fprintf(&b, "route_target = %q\n", zone.RouteTarget)
		ipv6 := DefaultLeshyIPv6
		if zone.IPv6 != nil {
			ipv6 = *zone.IPv6
		}
		fmt.Fprintf(&b, "ipv6 = %v\n", ipv6)
		b.WriteString("domains = [\n")
		for _, domain := range zone.Domains {
			fmt.Fprintf(&b, "    %q,\n", domain)
		}
		b.WriteString("]\n")
		b.WriteString("patterns = [\n")
		for _, pattern := range zone.Patterns {
			fmt.Fprintf(&b, "    %q,\n", pattern)
		}
		b.WriteString("]\n")
		b.WriteString("static_routes = [\n")
		for _, route := range zone.StaticRoutes {
			fmt.Fprintf(&b, "    %q,\n", route)
		}
		b.WriteString("]\n")
	}

	return []byte(b.String()), nil
}

// validateManagedModeIdentity enforces the managed activation fence the
// generated configuration carries (08D1 31.4): a privileged auth file, a
// 128-bit lowercase-hex activation id and a positive committed generation.
func validateManagedModeIdentity(server LeshyServer) error {
	if server.KikimoraAuthFile == "" {
		return fmt.Errorf("managed configuration requires kikimora_auth_file")
	}
	if !lowerHex32.MatchString(server.KikimoraActivationID) {
		return fmt.Errorf("managed configuration requires a 32 lowercase-hex kikimora_activation_id")
	}
	if server.KikimoraModeGeneration == 0 {
		return fmt.Errorf("managed configuration requires a positive kikimora_mode_generation")
	}
	return nil
}

// RoleZoneTarget is the device file a role's zone points at.
func RoleZoneTarget(vpnDir, role string) string {
	return filepath.Join(vpnDir, role+".dev")
}
