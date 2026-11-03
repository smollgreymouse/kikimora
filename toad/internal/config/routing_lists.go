package config

import (
	"encoding/json"
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

// ReadRoleIPv6Settings loads the per-role IPv6 switches. Roles without an
// entry read as config.DefaultLeshyIPv6.
func ReadRoleIPv6Settings(dir string) map[string]bool {
	out := make(map[string]bool)
	data, err := os.ReadFile(roleIPv6SettingsPath(dir))
	if err != nil {
		return out
	}
	var parsed roleIPv6Settings
	if json.Unmarshal(data, &parsed) != nil || parsed.Schema != roleIPv6SettingsSchema {
		return out
	}
	for role, enabled := range parsed.Roles {
		out[role] = enabled
	}
	return out
}

// WriteRoleIPv6Setting persists one role's IPv6 switch and returns the whole
// settings map.
func WriteRoleIPv6Setting(dir, role string, enabled bool) (map[string]bool, error) {
	settings := ReadRoleIPv6Settings(dir)
	settings[role] = enabled
	if err := writeAtomicJSONFile(roleIPv6SettingsPath(dir), roleIPv6Settings{
		Schema: roleIPv6SettingsSchema,
		Roles:  settings,
	}); err != nil {
		return nil, err
	}
	return settings, nil
}

// writeAtomicJSONFile stores small control-plane records with the same
// temp-file + rename + directory-sync discipline as the checkpoints.
func writeAtomicJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ipv6-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err = tmp.Write(append(b, 0x0a)); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	return fsutil.SyncDir(filepath.Dir(path))
}
