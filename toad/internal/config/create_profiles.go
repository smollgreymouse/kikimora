package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// SetTunnelProfiles replaces the profile membership of an existing tunnel. This is what the
// UI uses to move a role between profiles: the toad configuration carries the list, so moving
// a tunnel is a rewrite of that list rather than a new tunnel.
//
// An empty list is rejected: config.Load would silently fall back to "default", and a tunnel
// that belongs to no profile would vanish from every profile view.
func SetTunnelProfiles(dir, name string, profiles []string) (string, error) {
	name = strings.TrimSpace(name)
	if !instanceNameRE.MatchString(name) {
		return "", fmt.Errorf("invalid tunnel name %q", name)
	}

	membership := make([]string, 0, len(profiles))
	seen := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		profile = strings.TrimSpace(profile)
		if profile == "" {
			return "", fmt.Errorf("profile name must not be empty")
		}
		if !instanceNameRE.MatchString(profile) {
			return "", fmt.Errorf("invalid profile name %q", profile)
		}
		if seen[profile] {
			continue
		}
		seen[profile] = true
		membership = append(membership, profile)
	}
	if len(membership) == 0 {
		return "", fmt.Errorf("tunnel %q needs at least one profile", name)
	}

	path := filepath.Join(dir, name+".toml")
	cfg, err := Load(path)
	if err != nil {
		return "", fmt.Errorf("load tunnel %q: %w", name, err)
	}
	cfg.Profiles = membership

	var buffer bytes.Buffer
	if err := toml.NewEncoder(&buffer).Encode(cfg); err != nil {
		return "", fmt.Errorf("encode tunnel %q: %w", name, err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := Load(path); err != nil {
		return "", fmt.Errorf("tunnel %q is not usable after the move: %w", name, err)
	}
	return path, nil
}

// SetTunnelProfile moves a tunnel into exactly one profile.
func SetTunnelProfile(dir, name, profile string) (string, error) {
	return SetTunnelProfiles(dir, name, []string{profile})
}

// ProfileMembership is one role's new profile list in a multi-role membership
// transaction.
type ProfileMembership struct {
	Role     string
	Profiles []string
}

// SetProfileMemberships rewrites several role configurations as one transaction.
// The caller's order is preserved. If any write fails, every file that was
// already replaced (including the failed one) is restored from a byte snapshot
// taken before the transaction, so the on-disk membership never mixes the old
// and the new role set (PROD1, 2026-11-16 audit §2.1).
func SetProfileMemberships(dir string, changes []ProfileMembership) error {
	type snapshot struct {
		path string
		data []byte
		mode os.FileMode
	}
	originals := make([]snapshot, 0, len(changes))
	restore := func() error {
		var errs []error
		for _, original := range originals {
			if err := os.WriteFile(original.path, original.data, original.mode); err != nil {
				errs = append(errs, fmt.Errorf("restore %s: %w", original.path, err))
			}
		}
		return errors.Join(errs...)
	}
	for _, change := range changes {
		path := filepath.Join(dir, strings.TrimSpace(change.Role)+".toml")
		data, err := os.ReadFile(path)
		if err != nil {
			return errors.Join(fmt.Errorf("snapshot tunnel %q: %w", change.Role, err), restore())
		}
		info, err := os.Stat(path)
		if err != nil {
			return errors.Join(fmt.Errorf("snapshot tunnel %q: %w", change.Role, err), restore())
		}
		originals = append(originals, snapshot{path: path, data: data, mode: info.Mode().Perm()})
		if _, err := SetTunnelProfiles(dir, change.Role, change.Profiles); err != nil {
			return errors.Join(err, restore())
		}
	}
	return nil
}

// CredentialError reports why a tunnel cannot authenticate. The core refuses to start
// such a role: without credentials OpenConnect never reaches its data phase, and the
// role would sit in Recovering for ever instead of failing with a reason.
func (c *Config) CredentialError() error {
	if c == nil || c.Protocol != ProtocolOpenConnect {
		return nil
	}
	if c.OpenConnect == nil {
		return errors.New("openconnect section is missing")
	}
	if strings.TrimSpace(c.OpenConnect.Username) == "" {
		return errors.New("openconnect needs username")
	}
	password := strings.TrimSpace(c.OpenConnect.PasswordFile)
	token := strings.TrimSpace(c.OpenConnect.TokenSecretFile)
	if password == "" && token == "" {
		return errors.New("openconnect needs password_file or token_secret_file")
	}
	for _, path := range []string{password, token} {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("openconnect secret file %s: %w", path, err)
		}
	}
	return nil
}

// RemoveTunnel deletes a toad configuration the core wrote earlier. It is the counterpart
// of CreateTunnel: a tunnel created through the API must be removable through it too, or
// leftovers accumulate in the configuration directory.
func RemoveTunnel(dir, name string) error {
	name = strings.TrimSpace(name)
	if !instanceNameRE.MatchString(name) {
		return fmt.Errorf("invalid tunnel name %q", name)
	}
	path := filepath.Join(dir, name+".toml")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("tunnel %q is not configured: %w", name, err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
