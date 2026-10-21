package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

var leshyZoneRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// SetLeshyZone rewrites one role configuration with a new list. A role is its tunnel plus one
// list, and the zone field is where that link lives: bypass is a global rule and is not a role
// zone, so it is rejected here.
func SetLeshyZone(dir, name, zone string) error {
	if !instanceNameRE.MatchString(name) {
		return fmt.Errorf("invalid tunnel name %q", name)
	}
	zone = strings.TrimSpace(strings.ToLower(zone))
	if zone == string(RoutingBypass) {
		return fmt.Errorf("bypass is a global rule, not a role list")
	}
	if zone != "" && !leshyZoneRE.MatchString(zone) {
		return fmt.Errorf("invalid list name %q", zone)
	}

	path := filepath.Join(dir, name+".toml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("tunnel %q is not configured", name)
		}
		return fmt.Errorf("read %s: %w", path, err)
	}

	var cfg Config
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.LeshyZone = zone

	var buffer bytes.Buffer
	if err := toml.NewEncoder(&buffer).Encode(cfg); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}

	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, buffer.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
