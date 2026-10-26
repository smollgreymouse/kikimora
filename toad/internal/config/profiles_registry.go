package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// declaredProfilesFile is the registry of profiles that exist independently of the role
// configurations. A profile is an explicit set of roles, so without a registry an empty
// profile could not exist at all: every name would disappear together with its last role.
// The file deliberately is not a *.toml - every *.toml in the configuration directory is a
// role.
const declaredProfilesFile = "profiles.list"

func declaredProfilesPath(dir string) string {
	return filepath.Join(dir, declaredProfilesFile)
}

// ReadDeclaredProfiles returns the profile names registered in dir. A missing registry
// simply means that no profile has been created without roles yet.
func ReadDeclaredProfiles(dir string) ([]string, error) {
	data, err := os.ReadFile(declaredProfilesPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read declared profiles: %w", err)
	}
	names := make([]string, 0, 8)
	for _, line := range strings.Split(string(data), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || !instanceNameRE.MatchString(name) || slices.Contains(names, name) {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// AddDeclaredProfile registers name and reports whether it was new. The write is atomic:
// the registry is replaced through a temporary file so a crash cannot leave it half-written.
func AddDeclaredProfile(dir, name string) (bool, error) {
	name = strings.TrimSpace(name)
	if !instanceNameRE.MatchString(name) {
		return false, fmt.Errorf("invalid profile name %q", name)
	}
	names, err := ReadDeclaredProfiles(dir)
	if err != nil {
		return false, err
	}
	if slices.Contains(names, name) {
		return false, nil
	}
	names = append(names, name)
	sort.Strings(names)

	var buffer strings.Builder
	for _, entry := range names {
		buffer.WriteString(entry)
		buffer.WriteByte('\n')
	}

	temp, err := os.CreateTemp(dir, ".profiles-*.tmp")
	if err != nil {
		return false, fmt.Errorf("write profile registry: %w", err)
	}
	tempPath := temp.Name()
	if _, err := temp.WriteString(buffer.String()); err != nil {
		temp.Close()
		os.Remove(tempPath)
		return false, fmt.Errorf("write profile registry: %w", err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempPath)
		return false, fmt.Errorf("write profile registry: %w", err)
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		os.Remove(tempPath)
		return false, fmt.Errorf("write profile registry: %w", err)
	}
	if err := os.Rename(tempPath, declaredProfilesPath(dir)); err != nil {
		os.Remove(tempPath)
		return false, fmt.Errorf("replace profile registry: %w", err)
	}
	return true, nil
}
