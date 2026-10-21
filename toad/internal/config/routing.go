package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// BypassList is the one global list. Every other list belongs to exactly one role and is named
// after it: a role is its tunnel plus one list, so there is nothing to choose and nothing to
// assign.
const BypassList = "bypass"

// DefaultRoutingDir is where Leshy keeps the domain and network lists. Both platforms use the
// same layout so the core can read and write it without a per-platform branch at the call site.
func DefaultRoutingDir() string {
	if runtime.GOOS == "windows" {
		return `C:\ProgramData\Kikimora\leshy\domains`
	}
	return "/etc/kikimora/leshy/domains"
}

var routingDomainRE = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// ValidRoutingList reports whether a list may exist: the global bypass list, or the list of a
// configured role.
func ValidRoutingList(name string) bool {
	if name == BypassList {
		return true
	}
	return instanceNameRE.MatchString(name)
}

// ValidateRoutingEntry accepts a domain with an optional wildcard prefix, an IP address or a
// CIDR block. Everything else is rejected with the offending line, because a typo in this file
// silently sends the wrong traffic into a tunnel.
func ValidateRoutingEntry(entry string) error {
	value := strings.ToLower(strings.TrimSpace(entry))
	if value == "" {
		return fmt.Errorf("empty entry")
	}
	if _, _, err := net.ParseCIDR(value); err == nil {
		return nil
	}
	if net.ParseIP(value) != nil {
		return nil
	}
	if routingDomainRE.MatchString(value) {
		return nil
	}
	return fmt.Errorf("%q is neither a domain, an IP address nor a CIDR block", entry)
}

// ValidateRoutingContent checks a whole list and returns its entries without comments.
func ValidateRoutingContent(content string) ([]string, error) {
	entries := make([]string, 0, 16)
	for index, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := ValidateRoutingEntry(line); err != nil {
			return nil, fmt.Errorf("line %d: %w", index+1, err)
		}
		entries = append(entries, strings.ToLower(line))
	}
	return entries, nil
}

// routingBypassAll is the marker that sends everything not matched by another list around the
// tunnel. It only makes sense in the bypass list, and it is a single line by definition.
const routingBypassAll = "-"

// ReadRoutingList returns the current content of a list. A missing file is not an error: a role
// that never had a list simply starts empty.
func ReadRoutingList(dir, name string) (string, error) {
	if !ValidRoutingList(name) {
		return "", fmt.Errorf("unknown routing list %q", name)
	}
	data, err := os.ReadFile(filepath.Join(dir, name+".txt"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s list: %w", name, err)
	}
	return string(data), nil
}

// WriteRoutingList validates and replaces a list atomically, keeping the operator's comments.
func WriteRoutingList(dir, name, content string) error {
	if !ValidRoutingList(name) {
		return fmt.Errorf("unknown routing list %q", name)
	}
	lines, err := normalizeRoutingContent(name, content)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create routing directory: %w", err)
	}

	var buffer strings.Builder
	for _, line := range lines {
		buffer.WriteString(line)
		buffer.WriteString("\n")
	}

	path := filepath.Join(dir, name+".txt")
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(buffer.String()), 0o644); err != nil {
		return fmt.Errorf("write %s list: %w", name, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace %s list: %w", name, err)
	}
	return nil
}

// normalizeRoutingContent validates the list and returns it ready to write: entries are lowered
// and trimmed, while comment lines are kept verbatim so the explanations a package or an operator
// put in the file survive every save from the UI.
func normalizeRoutingContent(name, content string) ([]string, error) {
	trimmed := strings.TrimSpace(content)
	if name == BypassList && trimmed == routingBypassAll {
		// The marker replaces the whole list; there is nothing to validate.
		return []string{routingBypassAll}, nil
	}

	lines := make([]string, 0, 32)
	for index, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			lines = append(lines, line)
			continue
		}
		if line == routingBypassAll {
			return nil, fmt.Errorf("%s is only allowed as the whole bypass list", routingBypassAll)
		}
		if err := ValidateRoutingEntry(line); err != nil {
			return nil, fmt.Errorf("line %d: %w", index+1, err)
		}
		lines = append(lines, strings.ToLower(line))
	}
	return lines, nil
}
