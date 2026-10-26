package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// TunnelSpec describes a tunnel an operator wants to add. Only the fields that belong to
// the selected protocol are used; the rest stay empty.
type TunnelSpec struct {
	Name     string   `json:"name"`
	Protocol string   `json:"protocol"`
	Profile  string   `json:"profile,omitempty"`
	Endpoint string   `json:"endpoint"`
	Address  []string `json:"address,omitempty"`

	// amneziawg2
	PrivateKey    string   `json:"private_key,omitempty"`
	PeerPublicKey string   `json:"peer_public_key,omitempty"`
	PresharedKey  string   `json:"preshared_key,omitempty"`
	AllowedIPs    []string `json:"allowed_ips,omitempty"`

	// vless-reality
	UUID       string `json:"uuid,omitempty"`
	ServerName string `json:"server_name,omitempty"`
	PublicKey  string `json:"public_key,omitempty"`
	ShortID    string `json:"short_id,omitempty"`
	Flow       string `json:"flow,omitempty"`

	// openconnect
	Gateway         string `json:"gateway,omitempty"`
	Username        string `json:"username,omitempty"`
	PasswordFile    string `json:"password_file,omitempty"`
	TokenSecretFile string `json:"token_secret_file,omitempty"`
	AuthGroup       string `json:"auth_group,omitempty"`
}

// CreateTunnel writes a new toad configuration into dir and returns its path. The written
// file is loaded back through Load, so an underspecified tunnel is rejected instead of
// leaving a broken config behind.
func CreateTunnel(dir string, spec TunnelSpec) (string, error) {
	cfg, err := spec.toConfig()
	if err != nil {
		return "", err
	}
	// Fields the loader insists on but an operator should not have to repeat: the
	// interface follows the tunnel name, the state directory sits next to toads/.
	cfg.Address = append([]string(nil), spec.Address...)
	cfg.Interface = tunnelInterface(cfg.Name)
	cfg.MTU = 1380
	cfg.StateDir = filepath.Join(filepath.Dir(dir), "state", cfg.Name)
	// A role is its tunnel plus exactly one Leshy list, and the list is named after the role.
	cfg.LeshyZone = cfg.Name

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create toads directory: %w", err)
	}
	path := filepath.Join(dir, cfg.Name+".toml")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("tunnel %q already exists", cfg.Name)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect %s: %w", path, err)
	}

	var buffer bytes.Buffer
	if err := toml.NewEncoder(&buffer).Encode(cfg); err != nil {
		return "", fmt.Errorf("encode tunnel %q: %w", cfg.Name, err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := Load(path); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("tunnel %q is not usable: %w", cfg.Name, err)
	}
	return path, nil
}

func (s TunnelSpec) toConfig() (*Config, error) {
	name := strings.TrimSpace(s.Name)
	if !instanceNameRE.MatchString(name) {
		return nil, fmt.Errorf("invalid tunnel name %q", name)
	}
	protocol := Protocol(strings.TrimSpace(s.Protocol))
	cfg := &Config{Name: name, Protocol: protocol}
	if profile := strings.TrimSpace(s.Profile); profile != "" {
		if !instanceNameRE.MatchString(profile) {
			return nil, fmt.Errorf("invalid profile name %q", profile)
		}
		cfg.Profiles = []string{profile}
	}

	switch protocol {
	case ProtocolAWG2:
		endpoint := strings.TrimSpace(s.Endpoint)
		if endpoint == "" {
			return nil, fmt.Errorf("amneziawg2 tunnel needs an endpoint (host:port)")
		}
		if len(s.Address) == 0 {
			return nil, fmt.Errorf("amneziawg2 tunnel needs at least one address (for example 10.8.1.3/32)")
		}
		if strings.TrimSpace(s.PrivateKey) == "" || strings.TrimSpace(s.PeerPublicKey) == "" {
			return nil, fmt.Errorf("amneziawg2 tunnel needs private_key and peer_public_key")
		}
		allowed := s.AllowedIPs
		if len(allowed) == 0 {
			allowed = []string{"0.0.0.0/0"}
		}
		cfg.AWG2 = &AWG2Config{
			PrivateKey:    strings.TrimSpace(s.PrivateKey),
			PeerPublicKey: strings.TrimSpace(s.PeerPublicKey),
			PresharedKey:  strings.TrimSpace(s.PresharedKey),
			Endpoint:      endpoint,
			AllowedIPs:    allowed,
		}
	case ProtocolVLESSReality:
		if strings.TrimSpace(s.Endpoint) == "" {
			return nil, fmt.Errorf("vless-reality tunnel needs an endpoint (host:port)")
		}
		if len(s.Address) == 0 {
			return nil, fmt.Errorf("vless-reality tunnel needs at least one address (for example 10.9.0.2/32)")
		}
		if strings.TrimSpace(s.UUID) == "" || strings.TrimSpace(s.PublicKey) == "" ||
			strings.TrimSpace(s.ServerName) == "" {
			return nil, fmt.Errorf("vless-reality tunnel needs uuid, server_name and public_key")
		}
		cfg.VLESS = &VLESSRealityConfig{
			Endpoint:   strings.TrimSpace(s.Endpoint),
			UUID:       strings.TrimSpace(s.UUID),
			ServerName: strings.TrimSpace(s.ServerName),
			PublicKey:  strings.TrimSpace(s.PublicKey),
			ShortID:    strings.TrimSpace(s.ShortID),
			Flow:       strings.TrimSpace(s.Flow),
		}
	case ProtocolOpenConnect:
		gateway := strings.TrimSpace(s.Gateway)
		if gateway == "" {
			gateway = strings.TrimSpace(s.Endpoint)
		}
		if gateway == "" {
			return nil, fmt.Errorf("openconnect tunnel needs a gateway")
		}
		if strings.TrimSpace(s.Username) == "" {
			return nil, fmt.Errorf("openconnect tunnel needs a username")
		}
		// Never write a tunnel that cannot authenticate: the role would start, fail the
		// data phase and sit in Recovering, which is exactly the state this guards against.
		password := strings.TrimSpace(s.PasswordFile)
		token := strings.TrimSpace(s.TokenSecretFile)
		if password == "" && token == "" {
			return nil, fmt.Errorf("openconnect tunnel needs password_file or token_secret_file")
		}
		cfg.OpenConnect = &OpenConnectConfig{
			Gateway:         gateway,
			Username:        strings.TrimSpace(s.Username),
			PasswordFile:    password,
			TokenSecretFile: token,
			AuthGroup:       strings.TrimSpace(s.AuthGroup),
		}
	default:
		return nil, fmt.Errorf("unsupported protocol %q", s.Protocol)
	}
	return cfg, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// tunnelInterface derives an interface name from the tunnel name: the loader accepts
// [A-Za-z0-9_.-]{1,63} only, so everything else becomes a dash.
func tunnelInterface(name string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, name)
	if slug == "" {
		slug = "tun"
	}
	iface := "kk-" + slug
	if len(iface) > 63 {
		iface = iface[:63]
	}
	return iface
}
