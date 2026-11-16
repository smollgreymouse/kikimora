package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// normalizeMembership validates a role/profile-membership edit the same way for
// the single-role helper and the multi-role transaction: the role name and every
// profile name must be valid, duplicates collapse, and the result must keep at
// least one profile so the role never vanishes from every profile view.
func normalizeMembership(name string, profiles []string) (string, []string, error) {
	name = strings.TrimSpace(name)
	if !instanceNameRE.MatchString(name) {
		return "", nil, fmt.Errorf("invalid tunnel name %q", name)
	}

	membership := make([]string, 0, len(profiles))
	seen := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		profile = strings.TrimSpace(profile)
		if profile == "" {
			return "", nil, fmt.Errorf("profile name must not be empty")
		}
		if !instanceNameRE.MatchString(profile) {
			return "", nil, fmt.Errorf("invalid profile name %q", profile)
		}
		if seen[profile] {
			continue
		}
		seen[profile] = true
		membership = append(membership, profile)
	}
	if len(membership) == 0 {
		return "", nil, fmt.Errorf("tunnel %q needs at least one profile", name)
	}
	return name, membership, nil
}

// SetTunnelProfiles replaces the profile membership of an existing tunnel. This is what the
// UI uses to move a role between profiles: the toad configuration carries the list, so moving
// a tunnel is a rewrite of that list rather than a new tunnel.
//
// An empty list is rejected: config.Load would silently fall back to "default", and a tunnel
// that belongs to no profile would vanish from every profile view.
//
// The rewrite goes through the same profile-composition transaction as the
// multi-role edits: there is one atomicity implementation (AUD1, 2026-11-16
// post-e7ec9c2 audit §2.4).
func SetTunnelProfiles(dir, name string, profiles []string) (string, error) {
	tx, err := PrepareProfileComposition(dir, ProfileCompositionPlan{
		Memberships: []ProfileMembership{{Role: name, Profiles: profiles}},
	})
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return filepath.Join(dir, strings.TrimSpace(name)+".toml"), nil
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

// ProfileCompositionPlan describes one atomic profile edit (AUD1, 2026-11-16
// post-e7ec9c2 audit §2.4): every affected role TOML and, when requested, the
// declared-profiles registry commit together or not at all.
type ProfileCompositionPlan struct {
	Memberships []ProfileMembership
	// DeclaredProfiles nil leaves profiles.list untouched. A non-nil pointer
	// names the complete registry content the commit writes; an empty slice is
	// valid and clears the registry.
	DeclaredProfiles *[]string
}

// profileFileSnapshot is the exact-byte image of one affected file: original
// bytes, original mode and whether the file existed, so a rollback restores
// byte-for-byte state including files the transaction created.
type profileFileSnapshot struct {
	path    string
	existed bool
	data    []byte
	mode    os.FileMode
	next    []byte // exact bytes the commit writes; nil when the content is unchanged
}

// ProfileCompositionTxn is a prepared, not yet applied profile edit.
type ProfileCompositionTxn struct {
	roleFiles []profileFileSnapshot
	registry  *profileFileSnapshot
	committed bool
}

// PrepareProfileComposition reads and validates every file the plan touches
// without mutating anything: all memberships must reference existing role
// configurations, every rewritten tunnel must still encode to a usable TOML,
// and every declared-profile name must be valid. A Prepare error therefore
// guarantees the on-disk state was never touched.
func PrepareProfileComposition(dir string, plan ProfileCompositionPlan) (*ProfileCompositionTxn, error) {
	if dir == "" {
		return nil, errors.New("profile composition needs a configuration directory")
	}
	tx := &ProfileCompositionTxn{}
	for _, membership := range plan.Memberships {
		snap, err := prepareMembershipFile(dir, membership)
		if err != nil {
			return nil, err
		}
		tx.roleFiles = append(tx.roleFiles, snap)
	}
	if plan.DeclaredProfiles != nil {
		snap, err := prepareRegistryFile(dir, *plan.DeclaredProfiles)
		if err != nil {
			return nil, err
		}
		tx.registry = &snap
	}
	return tx, nil
}

// prepareMembershipFile snapshots one role TOML and encodes the exact bytes the
// commit will write.
func prepareMembershipFile(dir string, membership ProfileMembership) (profileFileSnapshot, error) {
	name, normalized, err := normalizeMembership(membership.Role, membership.Profiles)
	if err != nil {
		return profileFileSnapshot{}, err
	}
	path := filepath.Join(dir, name+".toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return profileFileSnapshot{}, fmt.Errorf("snapshot tunnel %q: %w", name, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return profileFileSnapshot{}, fmt.Errorf("snapshot tunnel %q: %w", name, err)
	}
	cfg, err := Load(path)
	if err != nil {
		return profileFileSnapshot{}, fmt.Errorf("load tunnel %q: %w", name, err)
	}
	cfg.Profiles = normalized

	var buffer bytes.Buffer
	if err := toml.NewEncoder(&buffer).Encode(cfg); err != nil {
		return profileFileSnapshot{}, fmt.Errorf("encode tunnel %q: %w", name, err)
	}
	return profileFileSnapshot{
		path:    path,
		existed: true,
		data:    data,
		mode:    info.Mode().Perm(),
		next:    buffer.Bytes(),
	}, nil
}

// prepareRegistryFile snapshots profiles.list and renders the exact bytes the
// commit will write. Content identical to the current registry produces a nil
// next, which the commit skips.
func prepareRegistryFile(dir string, declared []string) (profileFileSnapshot, error) {
	names := make([]string, 0, len(declared))
	seen := make(map[string]bool, len(declared))
	for _, name := range declared {
		name = strings.TrimSpace(name)
		if name == "" {
			return profileFileSnapshot{}, fmt.Errorf("profile name must not be empty")
		}
		if !instanceNameRE.MatchString(name) {
			return profileFileSnapshot{}, fmt.Errorf("invalid profile name %q", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	slices.Sort(names)

	path := declaredProfilesPath(dir)
	snap := profileFileSnapshot{path: path, mode: 0o600}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		snap.existed = false
	case err != nil:
		return profileFileSnapshot{}, fmt.Errorf("snapshot declared profiles: %w", err)
	default:
		snap.existed = true
		snap.data = data
		if info, statErr := os.Stat(path); statErr == nil {
			snap.mode = info.Mode().Perm()
		}
	}
	next := renderDeclaredProfiles(names)
	if !bytes.Equal(next, snap.data) {
		snap.next = next
	}
	return snap, nil
}

// renderDeclaredProfiles serializes the registry in the same sorted
// one-name-per-line format AddDeclaredProfile writes.
func renderDeclaredProfiles(names []string) []byte {
	var buffer strings.Builder
	for _, entry := range names {
		buffer.WriteString(entry)
		buffer.WriteByte('\n')
	}
	return []byte(buffer.String())
}

// Commit writes every prepared file. Each write is an atomic-per-file
// replacement; any failure (including a rewritten tunnel that no longer loads)
// restores the exact original state of everything this transaction touched
// before returning.
func (tx *ProfileCompositionTxn) Commit() error {
	if tx == nil {
		return errors.New("profile transaction is nil")
	}
	if tx.committed {
		return errors.New("profile transaction already committed")
	}
	for i := range tx.roleFiles {
		if err := tx.roleFiles[i].write(); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	if tx.registry != nil {
		if err := tx.registry.write(); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	// The membership move must leave every rewritten tunnel usable: a rejected
	// TOML would break the next core start. This mirrors the single-file
	// verification the old SetTunnelProfiles performed after its write.
	for i := range tx.roleFiles {
		if _, err := Load(tx.roleFiles[i].path); err != nil {
			err = fmt.Errorf("tunnel %q is not usable after the move: %w", strings.TrimSuffix(filepath.Base(tx.roleFiles[i].path), ".toml"), err)
			return errors.Join(err, tx.Rollback())
		}
	}
	tx.committed = true
	return nil
}

// Rollback restores the exact original bytes, mode and existence of every file
// the transaction touches. It works after a successful Commit: that is the path
// a later desired-state persistence failure uses to keep disk, memory and
// desired state consistent (AUD1 §2.4).
func (tx *ProfileCompositionTxn) Rollback() error {
	if tx == nil {
		return errors.New("profile transaction is nil")
	}
	var errs []error
	for i := range tx.roleFiles {
		errs = append(errs, tx.roleFiles[i].restore())
	}
	if tx.registry != nil {
		errs = append(errs, tx.registry.restore())
	}
	return errors.Join(errs...)
}

// write replaces the file with the prepared bytes through a temporary file in
// the same directory, preserving the original mode. A nil next means the
// content is unchanged and the write is skipped.
func (s *profileFileSnapshot) write() error {
	if s.next == nil {
		return nil
	}
	mode := s.mode
	if mode == 0 {
		mode = 0o600
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), "."+filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", s.path, err)
	}
	tempPath := temp.Name()
	if _, err := temp.Write(s.next); err != nil {
		temp.Close()
		os.Remove(tempPath)
		return fmt.Errorf("write %s: %w", s.path, err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("write %s: %w", s.path, err)
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("write %s: %w", s.path, err)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("replace %s: %w", s.path, err)
	}
	return nil
}

// restore returns the file to its exact pre-transaction state: original bytes
// and mode when it existed, removal when the transaction created it.
func (s *profileFileSnapshot) restore() error {
	if !s.existed {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", s.path, err)
		}
		return nil
	}
	if err := os.WriteFile(s.path, s.data, s.mode); err != nil {
		return fmt.Errorf("restore %s: %w", s.path, err)
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
