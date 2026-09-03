package profileimport

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

type Options struct {
	Name      string
	Interface string
	Address   []string
	MTU       int
	StateDir  string
}

func Parse(raw string, opts Options) (*config.Config, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("share profile is empty")
	}

	lower := strings.ToLower(raw)
	var cfg *config.Config
	var err error
	switch {
	case strings.HasPrefix(lower, "vless://"):
		cfg, err = parseVLESS(raw, opts)
	case strings.HasPrefix(lower, "wg://"), strings.HasPrefix(lower, "wireguard://"), strings.HasPrefix(lower, "amneziawg://"), strings.HasPrefix(lower, "[interface]"):
		cfg, err = parseWireGuard(raw, opts)
	default:
		return nil, fmt.Errorf("unsupported share profile format")
	}
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("imported profile is invalid: %w", err)
	}
	return cfg, nil
}

func applyCommon(cfg *config.Config, opts Options, fallbackName, fallbackInterface string, fallbackAddress []string) {
	name := opts.Name
	if name == "" {
		name = sanitizeName(fallbackName)
	}
	if name == "" {
		name = "imported"
	}
	iface := opts.Interface
	if iface == "" {
		iface = fallbackInterface
	}
	addresses := opts.Address
	if len(addresses) == 0 {
		addresses = fallbackAddress
	}
	mtu := opts.MTU
	if mtu == 0 {
		mtu = 1380
	}
	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = filepath.Join("/run/kikimora/toads", name)
	}

	cfg.Name = name
	cfg.Interface = iface
	cfg.Address = append([]string(nil), addresses...)
	cfg.MTU = mtu
	cfg.StateDir = stateDir
}

var invalidNameRun = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func sanitizeName(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = invalidNameRun.ReplaceAllString(raw, "-")
	raw = strings.Trim(raw, "-._")
	if len(raw) > 64 {
		raw = raw[:64]
		raw = strings.TrimRight(raw, "-._")
	}
	if raw == "" {
		return ""
	}
	first := raw[0]
	if !((first >= 'A' && first <= 'Z') || (first >= 'a' && first <= 'z') || (first >= '0' && first <= '9')) {
		raw = "p-" + raw
	}
	return raw
}
