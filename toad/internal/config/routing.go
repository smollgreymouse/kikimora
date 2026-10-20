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

// RoutingList names the three Leshy lists the UI edits.
type RoutingList string

const (
	RoutingPrimary   RoutingList = "primary"
	RoutingSecondary RoutingList = "secondary"
	RoutingBypass    RoutingList = "bypass"
)

// DefaultRoutingDir is where Leshy keeps the domain and network lists. Both platforms use the
// same layout so the core can read and write it without a per-platform branch at the call site.
func DefaultRoutingDir() string {
	if runtime.GOOS == "windows" {
		return `C:\ProgramData\Kikimora\leshy\domains`
	}
	return "/etc/kikimora/leshy/domains"
}

var routingDomainRE = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

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

// ReadRoutingList returns the current content of a list. A missing file is not an error: a list
// that was never written simply starts empty.
func ReadRoutingList(dir string, list RoutingList) (string, error) {
	if !list.Valid() {
		return "", fmt.Errorf("unknown routing list %q", list)
	}
	data, err := os.ReadFile(filepath.Join(dir, string(list)+".txt"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s list: %w", list, err)
	}
	return string(data), nil
}

// WriteRoutingList validates and replaces a list atomically, so Leshy never reads a half-written
// file. The write is allowed to fail on permissions - the caller reports that to the operator.
// routingBypassAll is the marker that sends everything not matched by another list around the
// tunnel. It only makes sense in the bypass list, and it is a single line by definition.
const routingBypassAll = "-"

func WriteRoutingList(dir string, list RoutingList, content string) error {
	if !list.Valid() {
		return fmt.Errorf("unknown routing list %q", list)
	}

	var entries []string
	if list == RoutingBypass && strings.TrimSpace(content) == routingBypassAll {
		// The marker replaces the whole list; there is nothing to validate.
		entries = []string{routingBypassAll}
	} else {
		for _, line := range strings.Split(content, "\n") {
			if strings.TrimSpace(line) == routingBypassAll {
				return fmt.Errorf("the %q marker is only allowed as the whole bypass list", routingBypassAll)
			}
		}
		validated, err := ValidateRoutingContent(content)
		if err != nil {
			return err
		}
		entries = validated
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create routing directory: %w", err)
	}

	var buffer strings.Builder
	for _, entry := range entries {
		buffer.WriteString(entry)
		buffer.WriteString("\n")
	}

	path := filepath.Join(dir, string(list)+".txt")
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(buffer.String()), 0o644); err != nil {
		return fmt.Errorf("write %s list: %w", list, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace %s list: %w", list, err)
	}
	return nil
}

// Valid reports whether the list name is one of the three Leshy lists.
func (l RoutingList) Valid() bool {
	switch l {
	case RoutingPrimary, RoutingSecondary, RoutingBypass:
		return true
	default:
		return false
	}
}
