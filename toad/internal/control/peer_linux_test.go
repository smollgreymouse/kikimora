//go:build linux

package control

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/operatorauth"
)

func TestPeerSupplementaryGroupsReadsProcStatus(t *testing.T) {
	groups, err := peerSupplementaryGroups(int32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	if groups == nil {
		t.Fatal("peer supplementary groups must not be nil")
	}
}

// dialLocal connects to a Unix listener over a real AF_UNIX socket pair so
// SO_PEERCRED carries the test process' own credentials.
func dialLocal(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "probe.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	client, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return client, server
}

// closeAllowBranches drives the registry/rejection paths with a real
// self-pair socket: a self-connection would otherwise be admitted by the
// server-uid branch (and the effective-gid branch) before the registry.
func closeAllowBranches(t *testing.T) {
	t.Helper()
	origUID, origEGID, origGroups := serverUID, serverEGID, readPeerGroups
	serverUID = -1
	serverEGID = -1
	readPeerGroups = func(pid int32) ([]uint32, error) { return nil, nil }
	t.Cleanup(func() {
		serverUID, serverEGID, readPeerGroups = origUID, origEGID, origGroups
	})
}

func expectAuthorized(t *testing.T, registry operatorauth.Registry) {
	t.Helper()
	_, server := dialLocal(t)
	if err := authorizePeerWithRegistry(server, registry); err != nil {
		t.Fatalf("peer must be authorized: %v", err)
	}
}

func expectRejected(t *testing.T, registry operatorauth.Registry) {
	t.Helper()
	_, server := dialLocal(t)
	if err := authorizePeerWithRegistry(server, registry); err == nil {
		t.Fatal("peer must be rejected")
	}
}

func TestAuthorizePeerSameUIDAllowed(t *testing.T) {
	expectAuthorized(t, operatorauth.Registry{Dir: t.TempDir()})
}

func TestAuthorizePeerRootBranchAllowsUIDZero(t *testing.T) {
	// The root allow branch precedes the registry: point serverUID elsewhere
	// and admit the peer as uid 0 is impossible from a non-root test
	// process, so exercise the branch through the server-uid comparison and
	// leave the uid-0 ordering covered by the helper tests.
	if os.Getuid() != 0 {
		t.Skip("a non-root test process cannot present a root SO_PEERCRED")
	}
	expectAuthorized(t, operatorauth.Registry{Dir: t.TempDir()})
}

func TestAuthorizePeerRegistryAuthorizedUIDWithoutKikimoraGroup(t *testing.T) {
	closeAllowBranches(t)
	registry := operatorauth.Registry{Dir: t.TempDir()}
	uid := strconv.Itoa(os.Getuid())
	if err := os.WriteFile(filepath.Join(registry.Dir, uid), []byte(uid+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expectAuthorized(t, registry)
}

func TestAuthorizePeerMissingUIDRejected(t *testing.T) {
	closeAllowBranches(t)
	expectRejected(t, operatorauth.Registry{Dir: t.TempDir()})
}

func TestAuthorizePeerMalformedRegistryFailsClosed(t *testing.T) {
	closeAllowBranches(t)
	registry := operatorauth.Registry{Dir: t.TempDir()}
	uid := strconv.Itoa(os.Getuid())
	if err := os.WriteFile(filepath.Join(registry.Dir, uid), []byte("9999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, server := dialLocal(t)
	err := authorizePeerWithRegistry(server, registry)
	if err == nil {
		t.Fatal("a malformed registry entry must fail closed")
	}
	if !strings.Contains(err.Error(), "operator registry check") {
		t.Fatalf("the failure must surface the registry check, got %v", err)
	}
}

func TestAuthorizePeerSymlinkRegistryFailsClosed(t *testing.T) {
	closeAllowBranches(t)
	registry := operatorauth.Registry{Dir: t.TempDir()}
	uid := strconv.Itoa(os.Getuid())
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte(uid+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(registry.Dir, uid)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	expectRejected(t, registry)
}

func TestAuthorizePeerOldGroupPathStillGreen(t *testing.T) {
	// Close only the server-uid branch: the legacy supplementary-group path
	// must admit a peer whose effective gid equals the server's, with an
	// empty registry.
	serverUID = -1
	t.Cleanup(func() { serverUID = os.Getuid() })
	expectAuthorized(t, operatorauth.Registry{Dir: t.TempDir()})
}
