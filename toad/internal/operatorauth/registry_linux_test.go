//go:build linux

package operatorauth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func tempRegistry(t *testing.T) Registry {
	t.Helper()
	return Registry{Dir: t.TempDir()}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestIsAuthorizedMissingEntry(t *testing.T) {
	registry := tempRegistry(t)
	authorized, err := registry.IsAuthorized(1000)
	if err != nil {
		t.Fatalf("missing entry must be (false, nil), got %v", err)
	}
	if authorized {
		t.Fatal("a missing entry must not authorize")
	}
}

func TestIsAuthorizedValidEntry(t *testing.T) {
	registry := tempRegistry(t)
	path := filepath.Join(registry.Dir, "1000")
	writeFile(t, path, "1000\n", 0o644)
	authorized, err := registry.IsAuthorized(1000)
	if err != nil {
		t.Fatalf("valid entry: %v", err)
	}
	if !authorized {
		t.Fatal("a valid entry must authorize")
	}
}

func TestIsAuthorizedRejectsUIDZero(t *testing.T) {
	registry := tempRegistry(t)
	if _, err := registry.IsAuthorized(0); !errors.Is(err, ErrRootUID) {
		t.Fatalf("uid 0 query must fail with ErrRootUID, got %v", err)
	}
}

func TestIsAuthorizedMalformedContent(t *testing.T) {
	cases := map[string]string{
		"content not the filename uid": "1001\n",
		"multiple lines":               "1000\n1000\n",
		"non-decimal content":          "one thousand\n",
		"empty content":                "",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			registry := tempRegistry(t)
			writeFile(t, filepath.Join(registry.Dir, "1000"), content, 0o644)
			authorized, err := registry.IsAuthorized(1000)
			if err == nil {
				t.Fatalf("malformed entry must be an error, got authorized=%v", authorized)
			}
			if authorized {
				t.Fatal("a malformed entry must never authorize")
			}
		})
	}
}

func TestIsAuthorizedRejectsSymlink(t *testing.T) {
	registry := tempRegistry(t)
	target := filepath.Join(t.TempDir(), "target")
	writeFile(t, target, "1000\n", 0o644)
	if err := os.Symlink(target, filepath.Join(registry.Dir, "1000")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := registry.IsAuthorized(1000); err == nil {
		t.Fatal("a symlinked registry entry must be rejected")
	}
}

func TestIsAuthorizedRejectsGroupWritable(t *testing.T) {
	registry := tempRegistry(t)
	writeFile(t, filepath.Join(registry.Dir, "1000"), "1000\n", 0o664)
	if _, err := registry.IsAuthorized(1000); err == nil {
		t.Fatal("a group-writable registry entry must be rejected")
	}
}

func TestIsAuthorizedRejectsNonRootOwned(t *testing.T) {
	// Chowning to another owner needs privileges; where they are absent the
	// root-ownership rule is still covered by TestValidateEntryRejectsForeignOwner.
	entry := filepath.Join(t.TempDir(), "1000")
	writeFile(t, entry, "1000\n", 0o644)
	if err := os.Chown(entry, 65534, 65534); err != nil {
		t.Skipf("chown unavailable without privileges: %v", err)
	}
	registry := Registry{Dir: t.TempDir()}
	if err := os.Rename(entry, filepath.Join(registry.Dir, "1000")); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.IsAuthorized(1000); err == nil {
		t.Fatal("a non-root-owned registry entry must be rejected")
	}
}

func TestValidateEntryRejectsForeignOwner(t *testing.T) {
	registry := tempRegistry(t)
	path := filepath.Join(registry.Dir, "1000")
	writeFile(t, path, "1000\n", 0o644)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	err = validateEntry(info, 1000, []byte("1000\n"))
	if os.Geteuid() == 0 {
		if err != nil {
			t.Fatalf("as root the freshly written entry is root-owned and must validate: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatal("a non-root-owned entry must be rejected by the validation helper")
	}
}

func TestAuthorizeRejectsUIDZero(t *testing.T) {
	registry := tempRegistry(t)
	if err := registry.Authorize(0); !errors.Is(err, ErrRootUID) {
		t.Fatalf("authorizing uid 0 must fail with ErrRootUID, got %v", err)
	}
}

func TestAuthorizeRequiresRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: the non-root rejection cannot be exercised")
	}
	registry := tempRegistry(t)
	if err := registry.Authorize(1000); err == nil {
		t.Fatal("Authorize must require effective root")
	}
}

func TestAuthorizeWritesEntry(t *testing.T) {
	registry := tempRegistry(t)
	origRequireRoot := requireRoot
	requireRoot = func() error { return nil }
	t.Cleanup(func() { requireRoot = origRequireRoot })

	if err := registry.Authorize(1000); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	path := filepath.Join(registry.Dir, "1000")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("registry entry missing after Authorize: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatal("registry entry must be a regular file")
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("registry entry mode is %v, want 0644", info.Mode().Perm())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "1000\n" {
		t.Fatalf("registry entry content %q, want \"1000\\n\"", content)
	}
	dirInfo, err := os.Stat(registry.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o755 {
		t.Fatalf("registry dir mode is %v, want 0755", dirInfo.Mode().Perm())
	}

	authorized, err := registry.IsAuthorized(1000)
	if err != nil || !authorized {
		t.Fatalf("the written entry must validate: authorized=%v err=%v", authorized, err)
	}
}

func TestAuthorizedUIDsListsValidEntries(t *testing.T) {
	registry := tempRegistry(t)
	origRequireRoot := requireRoot
	requireRoot = func() error { return nil }
	t.Cleanup(func() { requireRoot = origRequireRoot })

	for _, uid := range []uint32{1000, 1001} {
		if err := registry.Authorize(uid); err != nil {
			t.Fatalf("Authorize(%d): %v", uid, err)
		}
	}
	uids, err := registry.AuthorizedUIDs()
	if err != nil {
		t.Fatalf("AuthorizedUIDs: %v", err)
	}
	if len(uids) != 2 {
		t.Fatalf("AuthorizedUIDs returned %v, want [1000 1001]", uids)
	}
}

func TestAuthorizedUIDsFailsClosedOnMalformed(t *testing.T) {
	registry := tempRegistry(t)
	writeFile(t, filepath.Join(registry.Dir, "1000"), "1000\n", 0o644)
	writeFile(t, filepath.Join(registry.Dir, "1001"), "9999\n", 0o644)
	if _, err := registry.AuthorizedUIDs(); err == nil {
		t.Fatal("one malformed entry must fail the whole listing (fail closed)")
	}
}

func TestAuthorizedUIDsRejectsNonCanonicalName(t *testing.T) {
	registry := tempRegistry(t)
	writeFile(t, filepath.Join(registry.Dir, "01000"), "1000\n", 0o644)
	if _, err := registry.AuthorizedUIDs(); err == nil {
		t.Fatal("a non-canonical decimal filename must be rejected")
	}
}
