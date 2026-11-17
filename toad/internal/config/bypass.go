package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

// BypassDirect sends everything that no role claims straight out, without a tunnel.
const BypassDirect = "direct"

// DefaultBypassPath is where the global bypass decision lives. It is not a role list: it answers
// one question, what happens to traffic that no zone matched.
func DefaultBypassPath() string {
	if runtime.GOOS == "windows" {
		return `C:\ProgramData\Kikimora\leshy\bypass.conf`
	}
	return "/etc/kikimora/leshy/bypass.conf"
}

// ReadBypassMode returns the stored mode: "direct" or the name of a role that should carry the
// rest through its tunnel. A missing file means the direct mode.
//
// Permissive by design: it backs the hot DNS path and the synthetic-zone
// resolver. Authoritative startup policy truth must use ReadBypassModeStrict,
// which fails closed on a corrupt or unreadable file (PC8A §1.3).
func ReadBypassMode(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return BypassDirect
	}
	mode := strings.TrimSpace(string(data))
	if mode == "" {
		return BypassDirect
	}
	return mode
}

// ReadBypassModeStrict is the authoritative bypass reader (PC8A §1.3): a
// missing file means direct, an existing file must parse or the caller fails
// closed instead of silently treating corruption as a direct default route.
func ReadBypassModeStrict(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return BypassDirect, nil
	}
	if err != nil {
		return "", fmt.Errorf("read the bypass mode: %w", err)
	}
	mode := strings.TrimSpace(string(data))
	if mode == "" {
		return BypassDirect, nil
	}
	if mode != BypassDirect && !instanceNameRE.MatchString(mode) {
		return "", fmt.Errorf("invalid bypass mode %q", mode)
	}
	return mode, nil
}

// WriteBypassMode stores the mode through the durable cross-platform
// primitive (PC8A §1.8): same-directory temp + payload sync + atomic replace
// with Windows MOVEFILE_WRITE_THROUGH. bypass.conf is the single durable
// desired owner recovered by PC8B handover, so an acknowledged write must be
// crash-durable.
func WriteBypassMode(path, mode string) error {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = BypassDirect
	}
	if mode != BypassDirect && !instanceNameRE.MatchString(mode) {
		return fmt.Errorf("invalid bypass mode %q", mode)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create bypass directory: %w", err)
	}
	return fsutil.WriteAtomicFile(path, []byte(mode+"\n"), 0o644)
}
