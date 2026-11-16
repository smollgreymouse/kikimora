package config

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func seedTunnel(t *testing.T, dir, name, profile string) {
	t.Helper()
	if _, err := CreateTunnel(dir, TunnelSpec{
		Name:         name,
		Protocol:     "openconnect",
		PasswordFile: filepath.Join(dir, "oc.password"),
		Gateway:      "https://vpn.example",
		Username:     "lab",
		Profile:      profile,
	}); err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
}

func TestSetTunnelProfilesMovesARole(t *testing.T) {
	dir := t.TempDir()
	seedTunnel(t, dir, "lab", "one")

	path, err := SetTunnelProfiles(dir, "lab", []string{"two", "three", "two"})
	if err != nil {
		t.Fatalf("SetTunnelProfiles: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Profiles) != 2 || cfg.Profiles[0] != "two" || cfg.Profiles[1] != "three" {
		t.Fatalf("profiles = %v, want [two three]", cfg.Profiles)
	}

	if _, err := SetTunnelProfile(dir, "lab", "one"); err != nil {
		t.Fatalf("SetTunnelProfile: %v", err)
	}
	moved, err := Load(filepath.Join(dir, "lab.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(moved.Profiles) != 1 || moved.Profiles[0] != "one" {
		t.Fatalf("profiles = %v, want [one]", moved.Profiles)
	}
}

func TestSetTunnelProfilesRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	seedTunnel(t, dir, "lab", "one")

	cases := []struct {
		name     string
		tunnel   string
		profiles []string
	}{
		{"empty membership", "lab", nil},
		{"empty name in list", "lab", []string{""}},
		{"invalid profile", "lab", []string{"bad name"}},
		{"unknown tunnel", "missing", []string{"one"}},
		{"invalid tunnel name", "bad name", []string{"one"}},
	}
	for _, testCase := range cases {
		if _, err := SetTunnelProfiles(dir, testCase.tunnel, testCase.profiles); err == nil {
			t.Fatalf("%s: accepted", testCase.name)
		}
	}

	unchanged, err := Load(filepath.Join(dir, "lab.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.Profiles) != 1 || unchanged.Profiles[0] != "one" {
		t.Fatalf("rejected calls changed the file: %v", unchanged.Profiles)
	}
}

func TestCredentialErrorRejectsTunnelsThatCannotAuthenticate(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "oc.password")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{"no section", &Config{Protocol: ProtocolOpenConnect}, true},
		{"no username", &Config{Protocol: ProtocolOpenConnect, OpenConnect: &OpenConnectConfig{PasswordFile: secret}}, true},
		{"no secret source", &Config{Protocol: ProtocolOpenConnect, OpenConnect: &OpenConnectConfig{Username: "lab"}}, true},
		{"missing file", &Config{Protocol: ProtocolOpenConnect, OpenConnect: &OpenConnectConfig{Username: "lab", PasswordFile: filepath.Join(dir, "absent")}}, true},
		{"complete", &Config{Protocol: ProtocolOpenConnect, OpenConnect: &OpenConnectConfig{Username: "lab", PasswordFile: secret}}, false},
		{"other protocol", &Config{Protocol: ProtocolAWG2}, false},
	}
	for _, testCase := range cases {
		err := testCase.cfg.CredentialError()
		if testCase.wantErr && err == nil {
			t.Fatalf("%s: accepted", testCase.name)
		}
		if !testCase.wantErr && err != nil {
			t.Fatalf("%s: rejected: %v", testCase.name, err)
		}
	}
}

func TestRemoveTunnelDeletesTheConfiguration(t *testing.T) {
	dir := t.TempDir()
	seedTunnel(t, dir, "lab", "one")

	if err := RemoveTunnel(dir, "lab"); err != nil {
		t.Fatalf("RemoveTunnel: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "lab.toml")); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
	if err := RemoveTunnel(dir, "lab"); err == nil {
		t.Fatal("second removal accepted")
	}
	if err := RemoveTunnel(dir, "bad name"); err == nil {
		t.Fatal("invalid name accepted")
	}
}

// The AUD1 tests below pin the profile-composition transaction contract
// (2026-11-16 post-e7ec9c2 audit §2.4): Prepare validates everything before any
// mutation, Commit is an all-or-nothing multi-file replacement, and Rollback
// restores exact bytes, modes and existence even after a successful Commit.

func fileState(t *testing.T, path string) ([]byte, os.FileMode, bool) {
	t.Helper()
	info, statErr := os.Stat(path)
	if os.IsNotExist(statErr) {
		return nil, 0, false
	}
	if statErr != nil {
		t.Fatalf("stat %s: %v", path, statErr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data, info.Mode().Perm(), true
}

func writeDeclaredProfilesFixture(t *testing.T, dir string, names ...string) {
	t.Helper()
	var buffer bytes.Buffer
	for _, name := range names {
		buffer.WriteString(name)
		buffer.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles.list"), buffer.Bytes(), 0o600); err != nil {
		t.Fatalf("seed profiles.list: %v", err)
	}
}

func TestProfileCompositionCommitAppliesMembershipsAndRegistry(t *testing.T) {
	dir := t.TempDir()
	seedTunnel(t, dir, "lab", "one")
	seedTunnel(t, dir, "gamma", "two")
	writeDeclaredProfilesFixture(t, dir, "alpha")

	declared := []string{"alpha", "beta"}
	tx, err := PrepareProfileComposition(dir, ProfileCompositionPlan{
		Memberships: []ProfileMembership{
			{Role: "lab", Profiles: []string{"beta", "beta", "one"}},
			{Role: "gamma", Profiles: []string{"beta"}},
		},
		DeclaredProfiles: &declared,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	lab, err := Load(filepath.Join(dir, "lab.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(lab.Profiles, []string{"beta", "one"}) {
		t.Fatalf("lab profiles = %v, want [beta one]", lab.Profiles)
	}
	gamma, err := Load(filepath.Join(dir, "gamma.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(gamma.Profiles, []string{"beta"}) {
		t.Fatalf("gamma profiles = %v, want [beta]", gamma.Profiles)
	}
	names, err := ReadDeclaredProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"alpha", "beta"}) {
		t.Fatalf("registry = %v, want [alpha beta]", names)
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("double Commit accepted")
	}
}

func TestProfileCompositionPrepareRejectsUnknownRoleWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	seedTunnel(t, dir, "lab", "one")
	writeDeclaredProfilesFixture(t, dir, "alpha")
	before, beforeMode, _ := fileState(t, filepath.Join(dir, "lab.toml"))
	registryBefore, registryMode, _ := fileState(t, filepath.Join(dir, "profiles.list"))

	declared := []string{"beta"}
	if _, err := PrepareProfileComposition(dir, ProfileCompositionPlan{
		Memberships:      []ProfileMembership{{Role: "missing", Profiles: []string{"beta"}}},
		DeclaredProfiles: &declared,
	}); err == nil {
		t.Fatal("Prepare accepted an unknown role")
	}

	after, afterMode, _ := fileState(t, filepath.Join(dir, "lab.toml"))
	if !bytes.Equal(before, after) || beforeMode != afterMode {
		t.Fatal("role TOML changed during a failed Prepare")
	}
	registryAfter, registryAfterMode, _ := fileState(t, filepath.Join(dir, "profiles.list"))
	if !bytes.Equal(registryBefore, registryAfter) || registryMode != registryAfterMode {
		t.Fatal("registry changed during a failed Prepare")
	}
}

func TestProfileCompositionMidWriteFailureRestoresEverything(t *testing.T) {
	dir := t.TempDir()
	seedTunnel(t, dir, "lab", "one")
	seedTunnel(t, dir, "gamma", "two")
	writeDeclaredProfilesFixture(t, dir, "alpha")

	labBytes, labMode, _ := fileState(t, filepath.Join(dir, "lab.toml"))
	registryBytes, registryMode, _ := fileState(t, filepath.Join(dir, "profiles.list"))

	// Changes are prepared in slice order (lab, aux): making the second file
	// read-only forces the failure after lab.toml was already replaced.
	// On Windows the read-only second file fails the rename after lab.toml
	// was already replaced; on POSIX the read-only directory fails the
	// temporary-file creation - both prove the same all-or-nothing rollback.
	if runtime.GOOS == "windows" {
		gammaPath := filepath.Join(dir, "gamma.toml")
		if err := os.Chmod(gammaPath, 0o400); err != nil {
			t.Skipf("cannot make %s read-only: %v", gammaPath, err)
		}
		defer os.Chmod(gammaPath, 0o600)
	} else {
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Skipf("cannot make %s read-only: %v", dir, err)
		}
		defer os.Chmod(dir, 0o750)
	}

	declared := []string{"beta"}
	tx, err := PrepareProfileComposition(dir, ProfileCompositionPlan{
		Memberships: []ProfileMembership{
			{Role: "lab", Profiles: []string{"beta"}},
			{Role: "gamma", Profiles: []string{"beta"}},
		},
		DeclaredProfiles: &declared,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("Commit succeeded despite an unwritable role config")
	}

	after, afterMode, _ := fileState(t, filepath.Join(dir, "lab.toml"))
	if !bytes.Equal(labBytes, after) || labMode != afterMode {
		t.Fatalf("lab.toml was not restored byte-exactly (mode %v -> %v)", labMode, afterMode)
	}
	registryAfter, registryAfterMode, _ := fileState(t, filepath.Join(dir, "profiles.list"))
	if !bytes.Equal(registryBytes, registryAfter) || registryMode != registryAfterMode {
		t.Fatal("profiles.list was not restored byte-exactly")
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".tmp" {
				t.Fatalf("temporary file left behind: %s", entry.Name())
			}
		}
	}
}

func TestProfileCompositionRollbackAfterCommitRestoresExactState(t *testing.T) {
	dir := t.TempDir()
	seedTunnel(t, dir, "lab", "one")
	writeDeclaredProfilesFixture(t, dir, "alpha")

	labBytes, labMode, _ := fileState(t, filepath.Join(dir, "lab.toml"))
	registryBytes, registryMode, _ := fileState(t, filepath.Join(dir, "profiles.list"))

	declared := []string{"alpha", "beta"}
	tx, err := PrepareProfileComposition(dir, ProfileCompositionPlan{
		Memberships:      []ProfileMembership{{Role: "lab", Profiles: []string{"beta"}}},
		DeclaredProfiles: &declared,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback after Commit: %v", err)
	}

	after, afterMode, _ := fileState(t, filepath.Join(dir, "lab.toml"))
	if !bytes.Equal(labBytes, after) || labMode != afterMode {
		t.Fatalf("lab.toml did not return to its exact original bytes (mode %v -> %v)", labMode, afterMode)
	}
	registryAfter, registryAfterMode, _ := fileState(t, filepath.Join(dir, "profiles.list"))
	if !bytes.Equal(registryBytes, registryAfter) || registryMode != registryAfterMode {
		t.Fatal("profiles.list did not return to its exact original bytes")
	}
}

func TestProfileCompositionRollbackRemovesCreatedRegistry(t *testing.T) {
	dir := t.TempDir()
	seedTunnel(t, dir, "lab", "one")

	labBytes, labMode, _ := fileState(t, filepath.Join(dir, "lab.toml"))
	registryPath := filepath.Join(dir, "profiles.list")
	if _, _, existed := fileState(t, registryPath); existed {
		t.Fatal("registry fixture must not pre-exist")
	}

	declared := []string{"fresh"}
	tx, err := PrepareProfileComposition(dir, ProfileCompositionPlan{
		Memberships:      []ProfileMembership{{Role: "lab", Profiles: []string{"fresh"}}},
		DeclaredProfiles: &declared,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, _, existed := fileState(t, registryPath); !existed {
		t.Fatal("Commit did not create the registry")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, _, existed := fileState(t, registryPath); existed {
		t.Fatal("Rollback left the created registry behind")
	}
	after, afterMode, _ := fileState(t, filepath.Join(dir, "lab.toml"))
	if !bytes.Equal(labBytes, after) || labMode != afterMode {
		t.Fatal("lab.toml did not return to its exact original bytes")
	}
}

func TestProfileCompositionEmptyDeclaredListClearsRegistry(t *testing.T) {
	dir := t.TempDir()
	writeDeclaredProfilesFixture(t, dir, "alpha", "beta")

	empty := []string{}
	tx, err := PrepareProfileComposition(dir, ProfileCompositionPlan{
		DeclaredProfiles: &empty,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	data, _, existed := fileState(t, filepath.Join(dir, "profiles.list"))
	if !existed || len(data) != 0 {
		t.Fatalf("registry = %q (existed %v), want an empty file", data, existed)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	names, err := ReadDeclaredProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"alpha", "beta"}) {
		t.Fatalf("rollback lost the original registry: %v", names)
	}
}

func TestProfileCompositionNilRegistryLeavesProfilesListUntouched(t *testing.T) {
	dir := t.TempDir()
	seedTunnel(t, dir, "lab", "one")
	writeDeclaredProfilesFixture(t, dir, "alpha")

	tx, err := PrepareProfileComposition(dir, ProfileCompositionPlan{
		Memberships: []ProfileMembership{{Role: "lab", Profiles: []string{"one", "alpha"}}},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if tx.registry != nil {
		t.Fatal("a nil DeclaredProfiles pointer must not touch the registry")
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	names, err := ReadDeclaredProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"alpha"}) {
		t.Fatalf("registry changed without a DeclaredProfiles plan: %v", names)
	}
}

func TestProfileCompositionCommitVerificationFailureRestores(t *testing.T) {
	dir := t.TempDir()
	seedTunnel(t, dir, "lab", "one")
	before, beforeMode, _ := fileState(t, filepath.Join(dir, "lab.toml"))

	tx, err := PrepareProfileComposition(dir, ProfileCompositionPlan{
		Memberships: []ProfileMembership{{Role: "lab", Profiles: []string{"two"}}},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// Corrupt the prepared payload: the write succeeds but the rewritten
	// tunnel no longer loads, which must fail the commit and restore the file.
	tx.roleFiles[0].next = []byte("name = \"lab\"\nthis is not valid TOML [[[")
	if err := tx.Commit(); err == nil {
		t.Fatal("Commit accepted a payload that no longer loads")
	} else if !strings.Contains(err.Error(), "not usable") {
		t.Fatalf("commit error does not report the unusable tunnel: %v", err)
	}
	after, afterMode, _ := fileState(t, filepath.Join(dir, "lab.toml"))
	if !bytes.Equal(before, after) || beforeMode != afterMode {
		t.Fatal("commit verification failure did not restore the original file")
	}
}
