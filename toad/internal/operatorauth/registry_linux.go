//go:build linux

// Package operatorauth owns the root-owned operator registry that decides
// which desktop users may reach the core control socket (09G-W1.1). The
// registry is the authorization authority; filesystem ACLs are only the
// kernel reachability gate applied on top of it.
package operatorauth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

// DefaultRegistryDir is the root-owned operator registry directory.
const DefaultRegistryDir = "/etc/kikimora/operators.d"

// Registry reads and writes the per-uid operator authorization entries.
// One file per uid: the file name is the decimal uid and the content is the
// same decimal uid followed by one newline (owner root:root, mode 0644).
type Registry struct {
	Dir string
}

// requireRoot is a seam so tests can exercise the write path without real
// privileges; production code always uses the real effective-uid check.
var requireRoot = func() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("operator registry writes require root (euid %d)", os.Geteuid())
	}
	return nil
}

// ErrRootUID marks an invalid registry query or entry for uid 0: root is
// authorized separately and must never carry a registry entry.
var ErrRootUID = errors.New("uid 0 is never a registry entry (root is authorized separately)")

// entryPath builds the registry filename strictly from the decimal uid.
func entryPath(dir string, uid uint32) string {
	return filepath.Join(dir, strconv.FormatUint(uint64(uid), 10))
}

// validateEntry enforces the on-disk security contract for one registry
// entry: regular non-symlink file owned by root, not group/other writable,
// with exactly one decimal line equal to the filename uid.
func validateEntry(info os.FileInfo, uid uint32, content []byte) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("registry entry for uid %d is a symlink", uid)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("registry entry for uid %d is not a regular file", uid)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid != 0 {
		return fmt.Errorf("registry entry for uid %d is not root-owned", uid)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("registry entry for uid %d is group/other writable", uid)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 1 {
		return fmt.Errorf("registry entry for uid %d must hold exactly one line", uid)
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(lines[0]), 10, 32)
	if err != nil {
		return fmt.Errorf("registry entry for uid %d is not a decimal uid: %w", uid, err)
	}
	if uint32(parsed) != uid {
		return fmt.Errorf("registry entry content %d does not match its filename uid %d", parsed, uid)
	}
	return nil
}

// IsAuthorized reports whether uid carries a valid registry entry. A missing
// entry is (false, nil); a malformed or insecure entry is an error — callers
// must fail closed on it.
func (r Registry) IsAuthorized(uid uint32) (bool, error) {
	if uid == 0 {
		return false, ErrRootUID
	}
	path := entryPath(r.Dir, uid)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat registry entry %s: %w", path, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read registry entry %s: %w", path, err)
	}
	if err := validateEntry(info, uid, content); err != nil {
		return false, err
	}
	return true, nil
}

// Authorize writes the registry entry for uid. It requires effective root,
// creates the registry directory 0755 when missing, and stores exactly
// "<uid>\n" through fsutil.WriteAtomicFile (same-directory temp + fsync +
// rename + parent-directory fsync; final mode 0644).
func (r Registry) Authorize(uid uint32) error {
	if uid == 0 {
		return ErrRootUID
	}
	if err := requireRoot(); err != nil {
		return err
	}
	if err := os.MkdirAll(r.Dir, 0o755); err != nil {
		return fmt.Errorf("create registry dir %s: %w", r.Dir, err)
	}
	// The directory must be exactly 0755 even if it already existed with a
	// wider mode (WriteAtomicFile's MkdirAll is a no-op on existing dirs).
	if err := os.Chmod(r.Dir, 0o755); err != nil {
		return fmt.Errorf("chmod registry dir %s: %w", r.Dir, err)
	}
	payload := []byte(strconv.FormatUint(uint64(uid), 10) + "\n")
	if err := fsutil.WriteAtomicFile(entryPath(r.Dir, uid), payload, 0o644); err != nil {
		return fmt.Errorf("write registry entry for uid %d: %w", uid, err)
	}
	return nil
}

// AuthorizedUIDs lists the uids that carry valid registry entries. A
// malformed or insecure entry fails the whole listing (fail closed): the
// ACL reapplication must not silently skip an operator it cannot validate.
func (r Registry) AuthorizedUIDs() ([]uint32, error) {
	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read registry dir %s: %w", r.Dir, err)
	}
	uids := make([]uint32, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat registry entry %s: %w", entry.Name(), err)
		}
		uid, err := parseEntryName(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("registry dir %s: %w", r.Dir, err)
		}
		content, err := os.ReadFile(filepath.Join(r.Dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read registry entry %s: %w", entry.Name(), err)
		}
		if err := validateEntry(info, uid, content); err != nil {
			return nil, fmt.Errorf("registry dir %s: %w", r.Dir, err)
		}
		uids = append(uids, uid)
	}
	return uids, nil
}

// parseEntryName accepts only a pure decimal uid filename; uid 0 is refused
// as a registry entry.
func parseEntryName(name string) (uint32, error) {
	parsed, err := strconv.ParseUint(name, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("registry entry %q is not a decimal uid", name)
	}
	if parsed == 0 {
		return 0, fmt.Errorf("registry entry %q: %w", name, ErrRootUID)
	}
	// The name must be the canonical decimal form of the uid (no leading
	// zeros, no signs), so filename and content identity stays exact.
	if strconv.FormatUint(parsed, 10) != name {
		return 0, fmt.Errorf("registry entry %q is not the canonical decimal uid", name)
	}
	return uint32(parsed), nil
}
