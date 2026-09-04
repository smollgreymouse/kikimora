package config

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

type Protocol string

const (
	ProtocolAWG2         Protocol = "amneziawg2"
	ProtocolVLESSReality Protocol = "vless-reality"
)

var (
	instanceNameRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	interfaceNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,63}$`)
)

type Config struct {
	Name      string              `toml:"name"`
	Protocol  Protocol            `toml:"protocol"`
	Interface string              `toml:"interface"`
	Address   []string            `toml:"address"`
	MTU       int                 `toml:"mtu"`
	StateDir  string              `toml:"state_dir"`
	AWG2      *AWG2Config         `toml:"awg2"`
	VLESS     *VLESSRealityConfig `toml:"vless_reality"`
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
	if len(c.Address) == 0 {
		return errors.New("at least one address is required")
	}
	for _, raw := range c.Address {
		if _, err := netip.ParsePrefix(raw); err != nil {
			return fmt.Errorf("invalid address %q: %w", raw, err)
		}
	}

	switch c.Protocol {
	case ProtocolAWG2:
		if c.AWG2 == nil {
			return errors.New("protocol amneziawg2 requires [awg2]")
		}
		if c.VLESS != nil {
			return errors.New("protocol amneziawg2 must not contain [vless_reality]")
		}
		return c.AWG2.validate()
	case ProtocolVLESSReality:
		if c.VLESS == nil {
			return errors.New("protocol vless-reality requires [vless_reality]")
		}
		if c.AWG2 != nil {
			return errors.New("protocol vless-reality must not contain [awg2]")
		}
		return c.VLESS.validate()
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
