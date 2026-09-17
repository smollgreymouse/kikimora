package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/legacyconfig"
)

type Protocol string

const (
	ProtocolAWG2         Protocol = "amneziawg2"
	ProtocolVLESSReality Protocol = "vless-reality"
	ProtocolOpenConnect  Protocol = "openconnect"
)

var (
	instanceNameRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	interfaceNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,63}$`)
)

type Config struct {
	Name           string              `toml:"name"`
	Protocol       Protocol            `toml:"protocol"`
	Interface      string              `toml:"interface"`
	Address        []string            `toml:"address"`
	MTU            int                 `toml:"mtu"`
	StateDir       string              `toml:"state_dir"`
	AWG2           *AWG2Config         `toml:"awg2"`
	VLESS          *VLESSRealityConfig `toml:"vless_reality"`
	OpenConnect    *OpenConnectConfig  `toml:"openconnect"`
	LeshyZone      string              `toml:"leshy_zone"`
	EndpointPolicy EndpointPolicy      `toml:"endpoint_policy"`
	legacyVPNPath  string
}

type EndpointPolicy struct {
	Source       string `toml:"source"`
	StaticFile   string `toml:"static_file"`
	Command      string `toml:"command"`
	RulePriority int    `toml:"rule_priority"`
}

func (c *Config) EffectiveEndpointPolicy() endpoint.Policy {
	if c == nil {
		return endpoint.Policy{}
	}
	zone, priority := c.LeshyZone, c.EndpointPolicy.RulePriority
	if zone == "" {
		switch c.Name {
		case "primary":
			zone = "primary"
		case "secondary":
			zone = "secondary"
		}
	}
	if priority == 0 {
		switch c.Name {
		case "primary":
			priority = 50
		case "secondary":
			priority = 51
		}
	}
	return endpoint.Policy{Role: c.Name, Zone: zone, Priority: priority, Source: endpoint.Source(c.EndpointPolicy.Source), StaticFile: c.EndpointPolicy.StaticFile, Command: c.EndpointPolicy.Command}
}

func ValidateEndpointPolicies(configs []*Config) error {
	seen := map[int]string{}
	for _, c := range configs {
		p := c.EffectiveEndpointPolicy()
		if p.Priority == 0 && p.Zone == "" {
			continue
		}
		if !p.Valid() {
			return fmt.Errorf("endpoint policy for %q requires zone and positive rule_priority", c.Name)
		}
		if previous, ok := seen[p.Priority]; ok {
			return fmt.Errorf("endpoint rule priority %d is used by %q and %q", p.Priority, previous, c.Name)
		}
		seen[p.Priority] = c.Name
	}
	return nil
}

// ConfiguredTransportEndpoints returns the protocol-neutral configured
// transport target. Resolution is intentionally performed by endpoint.Manager.
func (c *Config) ConfiguredTransportEndpoints() []endpoint.EndpointSpec {
	if c == nil {
		return nil
	}
	var raw string
	network, defaultPort := c.transportDefaults()
	switch c.Protocol {
	case ProtocolAWG2:
		if c.AWG2 != nil {
			raw = c.AWG2.Endpoint
		}
	case ProtocolVLESSReality:
		if c.VLESS != nil {
			raw = c.VLESS.Endpoint
		}
	case ProtocolOpenConnect:
		if c.OpenConnect != nil {
			raw = c.OpenConnect.Gateway
		}
	}
	if raw == "" {
		return nil
	}
	specs, err := endpoint.ParseSpecs(strings.NewReader(raw), network, defaultPort)
	if err != nil {
		return nil
	}
	return specs
}

func (c *Config) transportDefaults() (string, uint16) {
	network, port := "tcp", uint16(443)
	if c != nil && c.Protocol == ProtocolAWG2 {
		network, port = "udp", 51820
	}
	var raw string
	if c != nil {
		switch c.Protocol {
		case ProtocolAWG2:
			if c.AWG2 != nil {
				raw = c.AWG2.Endpoint
			}
		case ProtocolVLESSReality:
			if c.VLESS != nil {
				raw = c.VLESS.Endpoint
			}
		case ProtocolOpenConnect:
			if c.OpenConnect != nil {
				raw = c.OpenConnect.Gateway
			}
		}
	}
	if addr, err := netip.ParseAddrPort(raw); err == nil {
		return network, addr.Port()
	}
	if _, portText, err := net.SplitHostPort(raw); err == nil {
		if parsed, parseErr := strconv.Atoi(portText); parseErr == nil && parsed > 0 && parsed <= 65535 {
			port = uint16(parsed)
		}
	}
	return network, port
}

// ResolveTransportEndpoints resolves the active endpoint policy while
// preserving hostnames for endpoint.Manager's injected resolver.
func (c *Config) ResolveTransportEndpoints(ctx context.Context) ([]endpoint.EndpointSpec, error) {
	if c == nil {
		return nil, errors.New("config is nil")
	}
	network, port := c.transportDefaults()
	policy := c.EffectiveEndpointPolicy()
	switch policy.Source {
	case endpoint.SourceStatic:
		return (endpoint.StaticProvider{Path: policy.StaticFile}).ResolveSpecs(ctx, network, port)
	case endpoint.SourceCommandCompat, endpoint.SourceHapp:
		env := map[string]string{
			"KIKIMORA_ENDPOINT_ROLE":      c.Name,
			"KIKIMORA_ENDPOINT_INTERFACE": c.Interface,
			"KIKIMORA_VPN_CONFIG":         c.legacyVPNPath,
		}
		return (endpoint.CommandProvider{Command: policy.Command, Env: env}).ResolveSpecs(ctx, network, port)
	default:
		return c.ConfiguredTransportEndpoints(), nil
	}
}

type AWG2Config struct {
	PrivateKey          string   `toml:"private_key"`
	PeerPublicKey       string   `toml:"peer_public_key"`
	PresharedKey        string   `toml:"preshared_key"`
	Endpoint            string   `toml:"endpoint"`
	AllowedIPs          []string `toml:"allowed_ips"`
	PersistentKeepalive int      `toml:"persistent_keepalive"`
	JC                  int      `toml:"jc"`
	JMin                int      `toml:"jmin"`
	JMax                int      `toml:"jmax"`
	S1                  int      `toml:"s1"`
	S2                  int      `toml:"s2"`
	S3                  int      `toml:"s3"`
	S4                  int      `toml:"s4"`
	H1                  string   `toml:"h1"`
	H2                  string   `toml:"h2"`
	H3                  string   `toml:"h3"`
	H4                  string   `toml:"h4"`
	I1                  string   `toml:"i1"`
	I2                  string   `toml:"i2"`
	I3                  string   `toml:"i3"`
	I4                  string   `toml:"i4"`
	I5                  string   `toml:"i5"`
}

type VLESSRealityConfig struct {
	Endpoint    string `toml:"endpoint"`
	UUID        string `toml:"uuid"`
	ServerName  string `toml:"server_name"`
	PublicKey   string `toml:"public_key"`
	ShortID     string `toml:"short_id"`
	Flow        string `toml:"flow"`
	Fingerprint string `toml:"fingerprint"`
	Transport   string `toml:"transport"`
	SpiderX     string `toml:"spider_x"`
}

// OpenConnectConfig contains only non-secret values and paths to secret files.
// Passwords and token seeds must never be embedded into the normalized Toad
// config, process argv, state snapshots, diagnostics or committed fixtures.
type OpenConnectConfig struct {
	Gateway           string `toml:"gateway"`
	VPNProtocol       string `toml:"vpn_protocol"`
	AuthGroup         string `toml:"auth_group"`
	Username          string `toml:"username"`
	PasswordFile      string `toml:"password_file"`
	TokenMode         string `toml:"token_mode"`
	TokenSecretFile   string `toml:"token_secret_file"`
	UserAgent         string `toml:"user_agent"`
	ServerCert        string `toml:"server_cert"`
	DisableUDP        bool   `toml:"disable_udp"`
	DisableIPv6       bool   `toml:"disable_ipv6"`
	ReconnectTimeout  int    `toml:"reconnect_timeout"`
	OpenConnectBinary string `toml:"openconnect_binary"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, fmt.Errorf("decode TOML: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadWithLegacy loads the new protocol credentials and overlays role and
// endpoint-provider settings from the original vpn.conf. The legacy file is
// parsed as data and is never sourced or executed by a shell.
func LoadWithLegacy(path, legacyPath, providerDir string) (*Config, error) {
	cfg, err := Load(path)
	if err != nil || legacyPath == "" {
		return cfg, err
	}
	legacy, err := legacyconfig.Load(legacyPath)
	if err != nil {
		return nil, err
	}
	role, ok := legacy.Role(cfg.Name)
	if !ok {
		return cfg, nil
	}
	if role.Interface != "" {
		cfg.Interface = role.Interface
	}
	if role.EndpointProvider == "" {
		// The shell runtime has always defaulted an omitted provider to static.
		// Preserve that compatibility behavior without evaluating vpn.conf.
		role.EndpointProvider = "static"
	}
	cfg.EndpointPolicy.Source = role.EndpointProvider
	if role.EndpointProvider == "command" {
		cfg.EndpointPolicy.Source = string(endpoint.SourceCommandCompat)
	}
	cfg.legacyVPNPath = legacyPath
	switch role.EndpointProvider {
	case "static":
		cfg.EndpointPolicy.StaticFile = filepath.Join(filepath.Dir(legacyPath), "endpoints", role.Name+".txt")
	case "command", "happ":
		if providerDir == "" {
			return nil, fmt.Errorf("provider directory is required for legacy %s provider", role.EndpointProvider)
		}
		provider := filepath.Join(providerDir, role.EndpointProvider)
		cfg.EndpointPolicy.Command = strings.Join(append([]string{provider, role.Name}, strings.Fields(role.ProviderArgs)...), " ")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Encode(w io.Writer, cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := toml.NewEncoder(w).Encode(cfg); err != nil {
		return fmt.Errorf("encode TOML: %w", err)
	}
	return nil
}

func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if !instanceNameRE.MatchString(c.Name) {
		return fmt.Errorf("invalid name %q", c.Name)
	}
	if !interfaceNameRE.MatchString(c.Interface) {
		return fmt.Errorf("invalid interface name %q", c.Interface)
	}
	if c.MTU < 576 || c.MTU > 9000 {
		return fmt.Errorf("mtu must be in range 576..9000, got %d", c.MTU)
	}
	if c.StateDir == "" || !filepath.IsAbs(c.StateDir) {
		return errors.New("state_dir must be an absolute path")
	}
	for _, raw := range c.Address {
		if _, err := netip.ParsePrefix(raw); err != nil {
			return fmt.Errorf("invalid address %q: %w", raw, err)
		}
	}

	switch c.Protocol {
	case ProtocolAWG2:
		if len(c.Address) == 0 {
			return errors.New("at least one address is required")
		}
		if c.AWG2 == nil {
			return errors.New("protocol amneziawg2 requires [awg2]")
		}
		if c.VLESS != nil || c.OpenConnect != nil {
			return errors.New("protocol amneziawg2 must not contain other protocol sections")
		}
		return c.AWG2.validate()
	case ProtocolVLESSReality:
		if len(c.Address) == 0 {
			return errors.New("at least one address is required")
		}
		if c.VLESS == nil {
			return errors.New("protocol vless-reality requires [vless_reality]")
		}
		if c.AWG2 != nil || c.OpenConnect != nil {
			return errors.New("protocol vless-reality must not contain other protocol sections")
		}
		return c.VLESS.validate()
	case ProtocolOpenConnect:
		if c.OpenConnect == nil {
			return errors.New("protocol openconnect requires [openconnect]")
		}
		if c.AWG2 != nil || c.VLESS != nil {
			return errors.New("protocol openconnect must not contain other protocol sections")
		}
		return c.OpenConnect.validate()
	default:
		return fmt.Errorf("unsupported protocol %q", c.Protocol)
	}
}

func (c *AWG2Config) validate() error {
	if strings.TrimSpace(c.PrivateKey) == "" {
		return errors.New("awg2.private_key is required")
	}
	if strings.TrimSpace(c.PeerPublicKey) == "" {
		return errors.New("awg2.peer_public_key is required")
	}
	if strings.TrimSpace(c.Endpoint) == "" {
		return errors.New("awg2.endpoint is required")
	}
	if len(c.AllowedIPs) == 0 {
		return errors.New("awg2.allowed_ips must not be empty")
	}
	for _, raw := range c.AllowedIPs {
		if _, err := netip.ParsePrefix(raw); err != nil {
			return fmt.Errorf("invalid awg2.allowed_ips entry %q: %w", raw, err)
		}
	}
	if c.PersistentKeepalive < 0 || c.PersistentKeepalive > 65535 {
		return errors.New("awg2.persistent_keepalive must be in range 0..65535")
	}
	if c.JC < 0 || c.JMin < 0 || c.JMax < 0 || c.JMin > c.JMax {
		return errors.New("invalid AWG2 Jc/Jmin/Jmax values")
	}
	for name, value := range map[string]int{"s1": c.S1, "s2": c.S2, "s3": c.S3, "s4": c.S4} {
		if value < 0 || value > 65535 {
			return fmt.Errorf("awg2.%s must be in range 0..65535", name)
		}
	}
	return nil
}

func (c *VLESSRealityConfig) validate() error {
	for name, value := range map[string]string{
		"endpoint":    c.Endpoint,
		"uuid":        c.UUID,
		"server_name": c.ServerName,
		"public_key":  c.PublicKey,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("vless_reality.%s is required", name)
		}
	}
	if c.Transport == "" {
		c.Transport = "raw"
	}
	if c.Transport != "raw" && c.Transport != "tcp" {
		return fmt.Errorf("vless_reality.transport %q is not supported in Stage 0", c.Transport)
	}
	if c.Flow != "" && c.Flow != "xtls-rprx-vision" {
		return fmt.Errorf("vless_reality.flow %q is not supported in Stage 0", c.Flow)
	}
	return nil
}

func (c *OpenConnectConfig) validate() error {
	if strings.TrimSpace(c.Gateway) == "" {
		return errors.New("openconnect.gateway is required")
	}
	if strings.ContainsAny(c.Gateway, " \t\r\n") {
		return errors.New("openconnect.gateway must not contain whitespace")
	}
	if strings.ContainsAny(c.AuthGroup, "\r\n") {
		return errors.New("openconnect.auth_group must not contain line breaks")
	}
	if strings.TrimSpace(c.Username) == "" {
		return errors.New("openconnect.username is required")
	}
	if c.VPNProtocol == "" {
		c.VPNProtocol = "anyconnect"
	}
	if c.VPNProtocol != "anyconnect" {
		return fmt.Errorf("openconnect.vpn_protocol %q is not supported yet", c.VPNProtocol)
	}
	if c.PasswordFile != "" && !filepath.IsAbs(c.PasswordFile) {
		return errors.New("openconnect.password_file must be absolute when set")
	}
	if c.TokenMode == "" {
		c.TokenMode = "none"
	}
	switch c.TokenMode {
	case "none":
		if c.TokenSecretFile != "" {
			return errors.New("openconnect.token_secret_file requires a token_mode")
		}
	case "totp":
		if c.TokenSecretFile == "" || !filepath.IsAbs(c.TokenSecretFile) {
			return errors.New("openconnect.token_secret_file must be an absolute path for TOTP")
		}
	default:
		return fmt.Errorf("openconnect.token_mode %q is not supported yet", c.TokenMode)
	}
	if c.ReconnectTimeout < 0 || c.ReconnectTimeout > 86400 {
		return errors.New("openconnect.reconnect_timeout must be in range 0..86400")
	}
	return nil
}
