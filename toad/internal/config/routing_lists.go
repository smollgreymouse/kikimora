package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
