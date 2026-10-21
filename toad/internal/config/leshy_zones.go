package config

import (
	"fmt"
	"os"
	"runtime"

	"github.com/BurntSushi/toml"
)

// LeshyZone is the part of a Leshy configuration that explains where a routing list goes. The
// lists are zones of the router, not properties of a role: the zone names a device file that the
// core publishes when a role comes up, and Leshy resolves it at runtime.
type LeshyZone struct {
	Name        string `toml:"name" json:"name"`
	Mode        string `toml:"mode" json:"mode"`
	RouteTarget string `toml:"route_target" json:"route_target"`
}

// DefaultLeshyConfigPath is where the generator writes the router configuration.
func DefaultLeshyConfigPath() string {
	if runtime.GOOS == "windows" {
		return `C:\ProgramData\leshy\config.toml`
	}
	return "/etc/kikimora/leshy/config.toml"
}

// ReadLeshyZones returns the zones of the generated configuration. A missing file is not an
// error: a fresh installation has no zones until the router has been configured once.
func ReadLeshyZones(path string) ([]LeshyZone, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read leshy config: %w", err)
	}
	var parsed struct {
		Zones []LeshyZone `toml:"zones"`
	}
	if _, err := toml.Decode(string(data), &parsed); err != nil {
		return nil, fmt.Errorf("parse leshy config: %w", err)
	}
	return parsed.Zones, nil
}
