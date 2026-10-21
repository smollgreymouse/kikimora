package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// WriteBypassMode stores the mode atomically.
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
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(mode+"\n"), 0o644); err != nil {
		return fmt.Errorf("write bypass mode: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace bypass mode: %w", err)
	}
	return nil
}
