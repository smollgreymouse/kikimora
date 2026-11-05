package control

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The privileged control credential (08D1 §31.6): 32 random bytes, stored as
// exactly 64 lowercase hexadecimal characters plus one trailing newline. The
// in-memory comparison uses the decoded 32 bytes. The file is never logged,
// never returned by Handshake/Snapshot/diagnostics and never stored in
// leshy-managed.json.
const (
	privilegedAuthKeyBytes  = 32
	privilegedAuthKeyHexLen = 2 * privilegedAuthKeyBytes
)

// PrivilegedAuthKeyPath is the credential file under the hardened core state
// directory (08D1 §31.6).
func PrivilegedAuthKeyPath(stateDir string) string {
	return filepath.Join(stateDir, "control-route-auth.key")
}

// ensurePrivilegedAuthKey loads the existing credential or creates one. On a
// fresh/local installation a missing or malformed key is generated before the
// control socket opens; an already-managed installation fails closed on a
// missing or malformed key instead of silently rotating a credential a live
// Leshy process may still hold.
func EnsurePrivilegedAuthKey(stateDir string, managed bool) ([]byte, error) {
	path := PrivilegedAuthKeyPath(stateDir)
	if raw, err := os.ReadFile(path); err == nil {
		key, parseErr := parsePrivilegedAuthKey(raw)
		if parseErr != nil {
			if managed {
				return nil, fmt.Errorf("privileged auth key %s is malformed: %w", path, parseErr)
			}
			// Local install: regenerate rather than serving with a key no
			// client can ever match.
			return createPrivilegedAuthKey(path)
		}
		return key, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read privileged auth key: %w", err)
	}
	if managed {
		return nil, fmt.Errorf("managed installation is missing its privileged auth key %s", path)
	}
	return createPrivilegedAuthKey(path)
}

// createPrivilegedAuthKey writes a fresh credential through the durable
// primitive: temporary file inside the already-hardened state directory,
// final mode/DACL applied before rename, durable sync, atomic replacement.
func createPrivilegedAuthKey(path string) ([]byte, error) {
	raw := make([]byte, privilegedAuthKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate privileged auth key: %w", err)
	}
	encoded := hex.EncodeToString(raw) + "\n"
	if err := writePrivilegedAuthFile(path, []byte(encoded)); err != nil {
		return nil, err
	}
	return raw, nil
}

// parsePrivilegedAuthKey decodes the on-disk representation into the 32
// in-memory bytes: exactly 64 lowercase hex characters plus one newline.
func parsePrivilegedAuthKey(raw []byte) ([]byte, error) {
	text := string(raw)
	if !strings.HasSuffix(text, "\n") {
		return nil, fmt.Errorf("credential must end with one newline")
	}
	text = strings.TrimSuffix(text, "\n")
	if len(text) != privilegedAuthKeyHexLen {
		return nil, fmt.Errorf("credential must be exactly %d hex characters, got %d", privilegedAuthKeyHexLen, len(text))
	}
	key, err := hex.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("credential is not hexadecimal: %w", err)
	}
	return key, nil
}

// encodePrivilegedAuthKey renders the wire form: 64 lowercase hex characters
// without the trailing newline.
func encodePrivilegedAuthKey(key []byte) string {
	return hex.EncodeToString(key)
}

// verifyPrivilegedAuth compares the presented token with the credential in
// constant time.
func verifyPrivilegedAuth(key []byte, presented string) bool {
	expected := encodePrivilegedAuthKey(key)
	return len(presented) == len(expected) &&
		subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) == 1
}
