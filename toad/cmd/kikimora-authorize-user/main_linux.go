//go:build linux

// kikimora-authorize-user — the privileged first-run helper (09G-W1.2),
// invoked through pkexec at the fixed install path
// /usr/libexec/kikimora/kikimora-authorize-user. It trusts PKEXEC_UID only,
// never a command-line uid or name, and owns exactly two modes:
//
//	--authorize-current-user   authorize the pkexec caller, activate the
//	                           core service and apply the runtime ACL
//	--apply-runtime-acl        reapply the per-uid kernel reachability ACL
//	                           (ExecStartPost uses this mode)
//
// The operator registry is the authorization authority; the ACL is only
// the kernel reachability gate on top of it.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/operatorauth"
)

// The fixed runtime paths the ACL gate covers. Never widened: no default
// ACL on the directory, no toads/secrets/Leshy path is ever touched.
var runtimeACLTargets = aclTargets{
	dir:    "/run/kikimora",
	socket: "/run/kikimora/core.sock",
}

type aclTargets struct {
	dir    string
	socket string
}

// runCommand is the exec seam; production always uses CommandContext with
// fixed argv and no shell. Tests capture argv instead of executing.
var runCommand = func(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

// requireRoot is the privilege seam; production checks the real euid.
var requireRoot = func() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("kikimora-authorize-user requires root (euid %d)", os.Geteuid())
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "--authorize-current-user":
		if len(os.Args) != 2 {
			usage()
			os.Exit(2)
		}
		err = authorizeCurrentUser(ctx, operatorauth.Registry{Dir: operatorauth.DefaultRegistryDir})
	case "--apply-runtime-acl":
		fs := flag.NewFlagSet("--apply-runtime-acl", flag.ContinueOnError)
		waitMS := fs.Int("wait-ms", 10000, "how long to wait for the core socket")
		if flagErr := fs.Parse(os.Args[2:]); flagErr != nil {
			fmt.Fprintln(os.Stderr, flagErr)
			os.Exit(2)
		}
		err = applyRuntimeACL(ctx, operatorauth.Registry{Dir: operatorauth.DefaultRegistryDir},
			time.Duration(*waitMS)*time.Millisecond, runtimeACLTargets)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "kikimora-authorize-user:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: kikimora-authorize-user --authorize-current-user | --apply-runtime-acl [--wait-ms N]")
}

// authorizeCurrentUser runs the pkexec-driven first-run transaction:
// PKEXEC_UID is the only accepted identity, the registry write, the service
// activation and the ACL application must all succeed.
func authorizeCurrentUser(ctx context.Context, registry operatorauth.Registry) error {
	if err := requireRoot(); err != nil {
		return err
	}
	raw := os.Getenv("PKEXEC_UID")
	if raw == "" {
		return fmt.Errorf("PKEXEC_UID is not set: run the helper through pkexec")
	}
	uid, err := parseUID(raw)
	if err != nil {
		return err
	}
	if _, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err != nil {
		return fmt.Errorf("PKEXEC_UID %d has no user account: %w", uid, err)
	}
	if err := registry.Authorize(uid); err != nil {
		return fmt.Errorf("authorize uid %d: %w", uid, err)
	}
	// Exactly this activation; no other service command exists here.
	if err := runCommand(ctx, "systemctl", "enable", "--now", "kikimora-core.service"); err != nil {
		return fmt.Errorf("activate kikimora-core.service: %w", err)
	}
	return applyRuntimeACL(ctx, registry, 10*time.Second, runtimeACLTargets)
}

// parseUID accepts only a canonical decimal uid; uid 0 is refused (root is
// authorized separately and never through pkexec).
func parseUID(raw string) (uint32, error) {
	parsed, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("PKEXEC_UID %q is not a decimal uid: %w", raw, err)
	}
	if strconv.FormatUint(parsed, 10) != raw {
		return 0, fmt.Errorf("PKEXEC_UID %q is not the canonical decimal uid", raw)
	}
	if parsed == 0 {
		return 0, fmt.Errorf("PKEXEC_UID 0 is refused: root is authorized separately")
	}
	return uint32(parsed), nil
}

// applyRuntimeACL re-establishes the per-uid kernel reachability gate:
// traverse on the runtime directory and read/write on the control socket,
// for every authorized uid from the registry. It waits up to wait for the
// directory and the socket (ExecStartPost races the core's socket bind).
func applyRuntimeACL(ctx context.Context, registry operatorauth.Registry, wait time.Duration, targets aclTargets) error {
	if err := requireRoot(); err != nil {
		return err
	}
	uids, err := registry.AuthorizedUIDs()
	if err != nil {
		return fmt.Errorf("list authorized operators: %w", err)
	}
	if err := waitRuntimePaths(ctx, wait, targets); err != nil {
		return err
	}
	for _, uid := range uids {
		// Per-uid named ACL entries only: --x on the runtime directory,
		// rw- on the socket. Never a default ACL on the directory.
		if err := runCommand(ctx, "setfacl", "-m", fmt.Sprintf("u:%d:--x", uid), targets.dir); err != nil {
			return fmt.Errorf("grant traverse on %s for uid %d: %w", targets.dir, uid, err)
		}
		if err := runCommand(ctx, "setfacl", "-m", fmt.Sprintf("u:%d:rw-", uid), targets.socket); err != nil {
			return fmt.Errorf("grant rw on %s for uid %d: %w", targets.socket, uid, err)
		}
	}
	return nil
}

// waitRuntimePaths polls until the runtime directory and the core socket
// both exist (the socket must be a real Unix socket); the wait timing out
// is a hard error.
func waitRuntimePaths(ctx context.Context, wait time.Duration, targets aclTargets) error {
	deadline := time.Now().Add(wait)
	for {
		err := checkRuntimePaths(targets)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %s waiting for %s and %s: %w",
				wait, targets.dir, targets.socket, err)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("context canceled while waiting for the core socket: %w", ctxErr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func checkRuntimePaths(targets aclTargets) error {
	info, err := os.Lstat(targets.dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", targets.dir)
	}
	socketInfo, err := os.Lstat(targets.socket)
	if err != nil {
		return err
	}
	if socketInfo.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s is not a Unix socket", targets.socket)
	}
	if _, err := net.Dial("unix", targets.socket); err != nil {
		return fmt.Errorf("socket %s does not accept connections yet: %w", targets.socket, err)
	}
	return nil
}
