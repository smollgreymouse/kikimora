package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

// ListRoutingFiles returns the role lists that exist in a routing directory, sorted. The global
// bypass list is not a role and is therefore never a zone.
func ListRoutingFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read routing directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".txt" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".txt")
		if name == BypassList || !ValidRoutingList(name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// BuildLeshyZones turns the per-role lists into zones. A role whose list has no entries produces
// no zone: Leshy refuses a zone without a domain, a pattern or a static route.
func BuildLeshyZones(dir, vpnDir, bypassMode string) ([]LeshyZoneSpec, error) {
	names, err := ListRoutingFiles(dir)
	if err != nil {
		return nil, err
	}
	zones := make([]LeshyZoneSpec, 0, len(names)+1)
	for _, name := range names {
		content, err := ReadRoutingList(dir, name)
		if err != nil {
			return nil, err
		}
		entries, err := ValidateRoutingContent(content)
		if err != nil {
			return nil, fmt.Errorf("%s list: %w", name, err)
		}
		zones = append(zones, LeshyZoneSpec{
			Name:        name,
			RouteTarget: RoleZoneTarget(vpnDir, name),
			Domains:     entries,
		})
	}
	// The bypass mode decides what happens to traffic no zone claimed: straight out, or through
	// one role's tunnel as the default. Leshy matches patterns as regular expressions, so the
	// catch-all is ".*", not "*".
	if bypassMode != "" && bypassMode != BypassDirect && ValidRoutingList(bypassMode) {
		zones = append(zones, LeshyZoneSpec{
			Name:        BypassList,
			RouteTarget: RoleZoneTarget(vpnDir, bypassMode),
			Patterns:    []string{".*"},
		})
	}
	return zones, nil
}

// roleIPv6Settings is the persisted per-role AAAA enable policy for the
// generated Leshy zones. A missing entry means the product default.
type roleIPv6Settings struct {
	Schema int             `json:"schema"`
	Roles  map[string]bool `json:"roles"`
}

const roleIPv6SettingsSchema = 1

func roleIPv6SettingsPath(dir string) string {
	return filepath.Join(dir, "role-ipv6.json")
}

// RoleIPv6SettingsPath exposes the per-role IPv6 policy file location so the
// controller can snapshot and restore its exact bytes in the fail-closed live
// policy transaction (AUD2B, 2026-11-16 post-e7ec9c2 audit §3.5).
func RoleIPv6SettingsPath(dir string) string {
	return roleIPv6SettingsPath(dir)
}

// ReadRoleIPv6Settings loads the per-role IPv6 switches. Roles without an
// entry read as config.DefaultLeshyIPv6. Permissive by design: it backs the
// hot snapshot path; authoritative startup truth must use
// ReadRoleIPv6SettingsStrict (PC8A §1.3).
func ReadRoleIPv6Settings(dir string) map[string]bool {
	out, _ := ReadRoleIPv6SettingsStrict(dir)
	return out
}

// ReadRoleIPv6SettingsStrict is the authoritative IPv6 policy reader
// (PC8A §1.3): a missing file is an empty policy, an existing file must
// parse with the current schema or the caller fails closed instead of
// silently re-enabling IPv6 for every role.
func ReadRoleIPv6SettingsStrict(dir string) (map[string]bool, error) {
	out := make(map[string]bool)
	data, err := os.ReadFile(roleIPv6SettingsPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the per-role IPv6 policy: %w", err)
	}
	var parsed roleIPv6Settings
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse the per-role IPv6 policy: %w", err)
	}
	if parsed.Schema != roleIPv6SettingsSchema {
		return nil, fmt.Errorf("per-role IPv6 policy schema %d, want %d", parsed.Schema, roleIPv6SettingsSchema)
	}
	for role, enabled := range parsed.Roles {
		out[role] = enabled
	}
	return out, nil
}

// WriteRoleIPv6Setting persists one role's IPv6 switch through the shared
// durable JSON primitive (PC8A §1.8) and returns the whole settings map. A
// corrupt existing policy is never silently repaired by a one-entry map: the
// caller must have strict-read the policy first (PC8A §1.3).
func WriteRoleIPv6Setting(dir, role string, enabled bool) (map[string]bool, error) {
	settings, err := ReadRoleIPv6SettingsStrict(dir)
	if err != nil {
		return nil, err
	}
	settings[role] = enabled
	if err := fsutil.WriteAtomicJSON(roleIPv6SettingsPath(dir), roleIPv6Settings{
		Schema: roleIPv6SettingsSchema,
		Roles:  settings,
	}, 0o600); err != nil {
		return nil, err
	}
	return settings, nil
}
