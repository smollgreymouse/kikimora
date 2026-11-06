package control

import (
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestEnsurePrivilegedAuthKeyCreatesAndReuses(t *testing.T) {
	stateDir := t.TempDir()
	key, err := EnsurePrivilegedAuthKey(stateDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("in-memory key must be 32 bytes, got %d", len(key))
	}
	path := PrivilegedAuthKeyPath(stateDir)
	if !processIsElevated() {
		// The SYSTEM+Administrators-only DACL must exclude this unelevated
		// token (08D1 §31.6): the roundtrip is proven by the denial itself.
		if readableAfterDACL(path) {
			t.Fatal("the credential DACL must deny an unelevated read-back")
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.HasSuffix(text, "\n") || !regexp.MustCompile(`^[0-9a-f]{64}\n$`).MatchString(text) {
		t.Fatalf("on-disk key must be 64 lowercase hex plus one newline, got %q", text)
	}
	// Ordinary restart reuses the valid existing key.
	again, err := EnsurePrivilegedAuthKey(stateDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(key) {
		t.Fatal("valid existing key must be reused, not rotated")
	}
}

func TestEnsurePrivilegedAuthKeyMalformed(t *testing.T) {
	dir := t.TempDir()
	path := PrivilegedAuthKeyPath(dir)
	// Malformed key on a local install: regenerate.
	if err := os.WriteFile(path, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := EnsurePrivilegedAuthKey(dir, false)
	if err != nil {
		t.Fatalf("local install must regenerate a malformed key: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("regenerated key must be 32 bytes, got %d", len(key))
	}
	if !processIsElevated() {
		// Overwriting the hardened file needs the elevated token; the
		// managed fail-closed branches below are exercised in the elevated
		// and POSIX runs.
		return
	}
	// Malformed key on a managed install: fail closed.
	if err := os.WriteFile(path, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsurePrivilegedAuthKey(dir, true); err == nil {
		t.Fatal("managed install must fail closed on a malformed key")
	}
	// Missing key on a managed install: fail closed.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsurePrivilegedAuthKey(dir, true); err == nil {
		t.Fatal("managed install must fail closed on a missing key")
	}
}

func TestCheckPrivilegedAuthMatrix(t *testing.T) {
	dir := t.TempDir()
	key, err := EnsurePrivilegedAuthKey(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager([]string{writeConfig(t, t.TempDir(), "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	// No credential configured: legacy peer policy is the only authority.
	if apiErr := manager.checkPrivilegedAuth("EnsureLeshyRoute", ""); apiErr != nil {
		t.Fatalf("unconfigured install must keep the legacy branch: %#v", apiErr)
	}

	manager.SetPrivilegedAuth(key)
	// Correct token succeeds.
	if apiErr := manager.checkPrivilegedAuth("EnsureLeshyRoute", encodePrivilegedAuthKey(key)); apiErr != nil {
		t.Fatalf("correct token must pass: %#v", apiErr)
	}
	// Wrong or garbage token: zero-mutation non-retryable denial.
	for _, bad := range []string{"wrong", strings.Repeat("0", 64), strings.ToUpper(encodePrivilegedAuthKey(key))} {
		apiErr := manager.checkPrivilegedAuth("EnsureLeshyRoute", bad)
		if apiErr == nil || apiErr.Code != "privileged_auth_required" {
			t.Fatalf("wrong token %q must be denied with privileged_auth_required, got %#v", bad, apiErr)
		}
		if apiErr.Retryable {
			t.Fatal("auth denial must be non-retryable")
		}
	}
	// Empty token while schema2 is not active: isolated legacy branch, counted.
	if apiErr := manager.checkPrivilegedAuth("EnsureLeshyRoute", ""); apiErr != nil {
		t.Fatalf("legacy v1 branch must tolerate the empty token pre-schema2: %#v", apiErr)
	}
	manager.mu.Lock()
	legacy := manager.legacyAuthRequests
	manager.mu.Unlock()
	if legacy != 1 {
		t.Fatalf("legacy branch must be counted, got %d", legacy)
	}
}

func TestPrivilegedAuthKeyFileIsSecret(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DACL contract is a Windows package check")
	}
	dir := t.TempDir()
	if _, err := EnsurePrivilegedAuthKey(dir, false); err != nil {
		t.Fatal(err)
	}
	if !processIsElevated() {
		if readableAfterDACL(PrivilegedAuthKeyPath(dir)) {
			t.Fatal("the credential DACL must deny an unelevated read-back")
		}
		return
	}
	// Elevated: the SYSTEM+Administrators-only descriptor is readable. The
	// Go FileMode on Windows carries no POSIX bits (a 0600 file stats as
	// -rw-rw-rw-), so on Windows the DACL is the contract; the POSIX mode
	// assertion is POSIX-only.
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(PrivilegedAuthKeyPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("credential file must not be group/other readable, got %v", info.Mode().Perm())
	}
}
