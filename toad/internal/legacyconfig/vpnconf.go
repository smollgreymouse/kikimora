// Package legacyconfig reads the small, declarative vpn.conf used by the
// original shell Kikimora. It deliberately does not source or execute it.
package legacyconfig

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Role struct {
	Name             string
	Interface        string
	DeviceFile       string
	EndpointProvider string
	ProviderArgs     string
}

type Config struct {
	Roles                 map[string]Role
	VPNLinkReadySuccesses int
}

var assignmentRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open legacy vpn config: %w", err)
	}
	defer file.Close()
	return Parse(file)
}

func Parse(r io.Reader) (Config, error) {
	result := Config{Roles: make(map[string]Role)}
	scanner := bufio.NewScanner(r)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, raw, ok := strings.Cut(text, "=")
		if !ok {
			return Config{}, fmt.Errorf("legacy vpn config line %d: expected assignment", line)
		}
		key = strings.TrimSpace(key)
		if !assignmentRE.MatchString(key) {
			return Config{}, fmt.Errorf("legacy vpn config line %d: invalid key %q", line, key)
		}
		value, err := parseValue(strings.TrimSpace(raw))
		if err != nil {
			return Config{}, fmt.Errorf("legacy vpn config line %d (%s): %w", line, key, err)
		}
		if err := assign(&result, key, value); err != nil {
			return Config{}, fmt.Errorf("legacy vpn config line %d (%s): %w", line, key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return Config{}, fmt.Errorf("read legacy vpn config: %w", err)
	}
	return result, nil
}

func parseValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.ContainsAny(raw, "`\r\n;") || strings.Contains(raw, "$(") || strings.Contains(raw, "${") {
		return "", fmt.Errorf("unsafe shell expression")
	}
	if raw[0] == '"' {
		if len(raw) < 2 || raw[len(raw)-1] != '"' {
			return "", fmt.Errorf("unterminated double-quoted value")
		}
		value, err := strconv.Unquote(raw)
		if err != nil {
			return "", fmt.Errorf("invalid quoted value: %w", err)
		}
		return value, nil
	}
	if raw[0] == '\'' {
		if len(raw) < 2 || raw[len(raw)-1] != '\'' {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		return raw[1 : len(raw)-1], nil
	}
	if strings.ContainsAny(raw, " \t") {
		return "", fmt.Errorf("unquoted whitespace is not allowed")
	}
	return raw, nil
}

func assign(cfg *Config, key, value string) error {
	if key == "VPN_LINK_READY_SUCCESSES" {
		if value == "" {
			return fmt.Errorf("value is empty")
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 100 {
			return fmt.Errorf("invalid ready success count %q", value)
		}
		cfg.VPNLinkReadySuccesses = n
		return nil
	}
	roleName, field, ok := strings.Cut(key, "_")
	if !ok || (roleName != "PRIMARY" && roleName != "SECONDARY") {
		// Unknown legacy assignments are ignored for forward compatibility, but
		// their values were still parsed and checked above.
		return nil
	}
	name := strings.ToLower(roleName)
	role := cfg.Roles[name]
	role.Name = name
	switch field {
	case "INTERFACE":
		role.Interface = value
	case "DEVICE_FILE":
		role.DeviceFile = value
	case "ENDPOINT_PROVIDER":
		if value != "static" && value != "command" && value != "happ" {
			return fmt.Errorf("unsupported endpoint provider %q", value)
		}
		role.EndpointProvider = value
	case "ENDPOINT_PROVIDER_ARGS":
		role.ProviderArgs = value
	default:
		return nil
	}
	cfg.Roles[name] = role
	return nil
}

func (c Config) Role(name string) (Role, bool) {
	role, ok := c.Roles[strings.ToLower(name)]
	return role, ok
}
