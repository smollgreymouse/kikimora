//go:build linux

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/operatorauth"
)

type capturedCommand struct {
	name string
	args []string
}

// stubExec replaces the exec and privilege seams and records every argv.
func stubExec(t *testing.T) *[]capturedCommand {
	t.Helper()
	commands := &[]capturedCommand{}
	origRun, origRoot := runCommand, requireRoot
	runCommand = func(_ context.Context, name string, args ...string) error {
		*commands = append(*commands, capturedCommand{name: name, args: args})
		return nil
	}
	requireRoot = func() error { return nil }
	t.Cleanup(func() { runCommand, requireRoot = origRun, origRoot })
	return commands
}

// registryWithOperators returns a temp registry with root-owned entries for
// the given uids; a non-root run skips (the root run owns the ownership
// path, mirroring the operatorauth suite).
func registryWithOperators(t *testing.T, uids ...uint32) operatorauth.Registry {
	t.Helper()
	registry := operatorauth.Registry{Dir: t.TempDir()}
	for _, uid := range uids {
		name := strconv.FormatUint(uint64(uid), 10)
		path := filepath.Join(registry.Dir, name)
		if err := os.WriteFile(path, []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(path, 0, 0); err != nil {
			t.Skipf("cannot create root-owned registry entries without privileges: %v", err)
		}
	}
	return registry
}

func TestAuthorizeCurrentUserRequiresRoot(t *testing.T) {
	origRoot := requireRoot
	requireRoot = func() error { return errors.New("not root") }
	t.Cleanup(func() { requireRoot = origRoot })
	t.Setenv("PKEXEC_UID", "1000")
	if err := authorizeCurrentUser(context.Background(),
		operatorauth.Registry{Dir: t.TempDir()}); err == nil {
		t.Fatal("authorize-current-user must require root")
	}
}

func TestAuthorizeCurrentUserRejectsMissingPKExecUID(t *testing.T) {
	stubExec(t)
	os.Unsetenv("PKEXEC_UID")
	err := authorizeCurrentUser(context.Background(), operatorauth.Registry{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "PKEXEC_UID is not set") {
		t.Fatalf("missing PKEXEC_UID must be rejected, got %v", err)
	}
}

func TestAuthorizeCurrentUserRejectsUIDZero(t *testing.T) {
	stubExec(t)
	t.Setenv("PKEXEC_UID", "0")
	err := authorizeCurrentUser(context.Background(), operatorauth.Registry{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("uid 0 must be refused, got %v", err)
	}
}

func TestAuthorizeCurrentUserRejectsMalformedUID(t *testing.T) {
	stubExec(t)
	for _, raw := range []string{"12a3", " 1000", "-1000", "0x10", "01000", "99999999999"} {
		t.Setenv("PKEXEC_UID", raw)
		err := authorizeCurrentUser(context.Background(), operatorauth.Registry{Dir: t.TempDir()})
		if err == nil {
			t.Fatalf("PKEXEC_UID %q must be rejected", raw)
		}
	}
}

func TestAuthorizeCurrentUserRequiresKnownUser(t *testing.T) {
	stubExec(t)
	t.Setenv("PKEXEC_UID", "59999")
	err := authorizeCurrentUser(context.Background(), operatorauth.Registry{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "no user account") {
		t.Fatalf("an unknown uid must be rejected, got %v", err)
	}
}

func TestAuthorizeCurrentUserWritesRegistryAndRunsExactArgv(t *testing.T) {
	// The helper runs under pkexec as root and the registry write requires
	// effective root, so the end-to-end case is a root-run case. The target
	// identity must be a NON-root existing account: uid 0 is refused.
	if os.Geteuid() != 0 {
		t.Skip("the registry write and the root-helper flow need euid 0")
	}
	commands := stubExec(t)
	operatorUID := uint32(65534) // nobody: exists, never root
	registry := operatorauth.Registry{Dir: t.TempDir()}
	// The registry entry is written BY the helper under test; the ACL phase
	// then needs the listing to see it root-owned.
	t.Setenv("PKEXEC_UID", strconv.FormatUint(uint64(operatorUID), 10))
	targets := aclTargets{dir: t.TempDir(), socket: filepath.Join(t.TempDir(), "core.sock")}
	// Provide the runtime paths so the wait phase succeeds.
	newSocket(t, targets.socket)

	origTargets := runtimeACLTargets
	runtimeACLTargets = targets
	t.Cleanup(func() { runtimeACLTargets = origTargets })

	if err := authorizeCurrentUser(context.Background(), registry); err != nil {
		t.Fatalf("authorize-current-user: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(registry.Dir, strconv.FormatUint(uint64(operatorUID), 10)))
	if err != nil {
		t.Fatalf("the registry entry was not written: %v", err)
	}
	if string(content) != strconv.FormatUint(uint64(operatorUID), 10)+"\n" {
		t.Fatalf("registry content %q, want the uid + newline", content)
	}

	// Exactly one systemctl activation with exactly this argv, no shell.
	var activations [][]string
	for _, command := range *commands {
		if command.name == "systemctl" {
			activations = append(activations, command.args)
		}
	}
	if len(activations) != 1 {
		t.Fatalf("exactly one systemctl activation expected, got %v", activations)
	}
	want := []string{"enable", "--now", "kikimora-core.service"}
	if strings.Join(activations[0], " ") != strings.Join(want, " ") {
		t.Fatalf("systemctl argv %v, want %v", activations[0], want)
	}
}

func TestApplyRuntimeACLEmitsExactCommandsForMultipleUIDs(t *testing.T) {
	commands := stubExec(t)
	registry := registryWithOperators(t, 1000, 1001)
	targets := aclTargets{dir: t.TempDir(), socket: filepath.Join(t.TempDir(), "core.sock")}
	newSocket(t, targets.socket)

	if err := applyRuntimeACL(context.Background(), registry, time.Second, targets); err != nil {
		t.Fatalf("apply-runtime-acl: %v", err)
	}

	var got []string
	for _, command := range *commands {
		got = append(got, command.name+" "+strings.Join(command.args, " "))
	}
	want := []string{
		"setfacl -m u:1000:--x " + targets.dir,
		"setfacl -m u:1000:rw- " + targets.socket,
		"setfacl -m u:1001:--x " + targets.dir,
		"setfacl -m u:1001:rw- " + targets.socket,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ACL commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestApplyRuntimeACLNeverTouchesRestrictedPaths(t *testing.T) {
	commands := stubExec(t)
	registry := registryWithOperators(t, 1000)
	targets := aclTargets{dir: t.TempDir(), socket: filepath.Join(t.TempDir(), "core.sock")}
	newSocket(t, targets.socket)

	if err := applyRuntimeACL(context.Background(), registry, time.Second, targets); err != nil {
		t.Fatalf("apply-runtime-acl: %v", err)
	}
	for _, command := range *commands {
		joined := command.name + " " + strings.Join(command.args, " ")
		for _, forbidden := range []string{"toads", "secrets", "leshy", "-d ", "default"} {
			if strings.Contains(joined, forbidden) {
				t.Fatalf("ACL command %q must not reference or default-extend %q", joined, forbidden)
			}
		}
	}
}

func TestApplyRuntimeACLWaitTimeoutIsHardError(t *testing.T) {
	commands := stubExec(t)
	registry := registryWithOperators(t, 1000)
	// The runtime dir exists but the socket never appears: the wait must
	// time out and no ACL command may run.
	targets := aclTargets{dir: t.TempDir(), socket: filepath.Join(t.TempDir(), "core.sock")}
	err := applyRuntimeACL(context.Background(), registry, 300*time.Millisecond, targets)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("the socket wait timeout must be a hard error, got %v", err)
	}
	if len(*commands) != 0 {
		t.Fatalf("no ACL command may run after a timeout, got %v", *commands)
	}
}

func TestProductionACLTargetsAreTheFixedRuntimePaths(t *testing.T) {
	if runtimeACLTargets.dir != "/run/kikimora" || runtimeACLTargets.socket != "/run/kikimora/core.sock" {
		t.Fatalf("production ACL targets moved: %+v", runtimeACLTargets)
	}
}

// newSocket binds a real listening Unix socket at path so the wait phase
// sees a connectable control socket.
func newSocket(t *testing.T, path string) *net.UnixListener {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener.(*net.UnixListener)
}
