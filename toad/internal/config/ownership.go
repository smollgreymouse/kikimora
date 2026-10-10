package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Ownership is the installation-level single-writer cutover state. It is
// intentionally global so roles cannot drift into mixed ownership.
type Ownership struct {
	RoutingOwner  string `toml:"routing_owner"`
	TunnelOwner   string `toml:"tunnel_owner"`
	EndpointOwner string `toml:"endpoint_owner"`
}

func (o Ownership) Valid() error {
	o = o.Normalized()
	if !oneOf(o.RoutingOwner, "legacy", "go") || !oneOf(o.EndpointOwner, "legacy", "go") || !oneOf(o.TunnelOwner, "external", "go") {
		return fmt.Errorf("invalid orchestration ownership values")
	}
	if o.RoutingOwner == "legacy" && o.TunnelOwner == "go" {
		return fmt.Errorf("legacy routing owner cannot run with Go-owned Toad tunnels")
	}
	return nil
}

// Normalized returns the compatibility defaults that are implied by an older
// ownership file. Keeping this explicit lets callers use one file across the
// legacy and Go service generations without carrying zero-value fields.
func (o Ownership) Normalized() Ownership {
	if o.RoutingOwner == "" {
		o.RoutingOwner = "legacy"
	}
	if o.TunnelOwner == "" {
		o.TunnelOwner = "external"
	}
	if o.EndpointOwner == "" {
		o.EndpointOwner = o.RoutingOwner
	}
	return o
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func LoadOwnership(path string) (Ownership, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Ownership{}, fmt.Errorf("read ownership config: %w", err)
	}
	var ownership Ownership
	if _, err := toml.Decode(string(data), &ownership); err != nil {
		return Ownership{}, fmt.Errorf("decode ownership config: %w", err)
	}
	ownership = ownership.Normalized()
	if err := ownership.Valid(); err != nil {
		return Ownership{}, err
	}
	return ownership, nil
}
