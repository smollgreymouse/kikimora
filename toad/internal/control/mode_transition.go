package control

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

// The Leshy mode transaction (08D1 §31.1): the generated Leshy config and
// the managed marker participate in one crash-safe transaction recorded in a
// durable core-owned journal. Recovery always rolls the recorded transaction
// forward; it never guesses from whichever of config/marker happens to exist.

type ManagedLeshyMode string

const (
	ManagedLeshyLocal   ManagedLeshyMode = "local"
	ManagedLeshyManaged ManagedLeshyMode = "managed"
)

type ManagedLeshyTransitionPhase string

const (
	ManagedTransitionPrepared      ManagedLeshyTransitionPhase = "prepared"
	ManagedTransitionConfigWritten ManagedLeshyTransitionPhase = "config_written"
	ManagedTransitionMarkerWritten ManagedLeshyTransitionPhase = "marker_written"
)

const managedLeshyTransitionSchema = 1

// ManagedLeshyTransition is the durable journal record (§31.1). The
// deterministic-field rules are §31.23: ID is a fresh 128-bit crypto/rand
// lowercase-hex transaction identifier distinct from ActivationID, zones are
// canonical sorted unique names, the digest covers the exact generated
// config bytes including the final newline, and
// ToGeneration == FromGeneration+1 exactly.
type ManagedLeshyTransition struct {
	Schema           int                         `json:"schema"`
	ID               string                      `json:"id"`
	From             ManagedLeshyMode            `json:"from"`
	To               ManagedLeshyMode            `json:"to"`
	Phase            ManagedLeshyTransitionPhase `json:"phase"`
	Socket           string                      `json:"socket,omitempty"`
	FromActivationID string                      `json:"from_activation_id,omitempty"`
	ToActivationID   string                      `json:"to_activation_id,omitempty"`
	FromGeneration   uint64                      `json:"from_generation"`
	ToGeneration     uint64                      `json:"to_generation"`
	Zones            []string                    `json:"zones"`
	ConfigSHA256     string                      `json:"config_sha256"`
}

// TransitionJournalPath is the journal location inside the hardened core
// state directory, not beside the generated Leshy config.
func TransitionJournalPath(stateDir string) string {
	return filepath.Join(stateDir, "leshy-mode-transition.json")
}

// ModeGenerationPath is the independently persisted monotonic counter
// (§31.4). It is a counter, not a second mode authority.
func ModeGenerationPath(stateDir string) string {
	return filepath.Join(stateDir, "leshy-mode-generation.json")
}

type modeGenerationState struct {
	Schema                  int    `json:"schema"`
	LastCommittedGeneration uint64 `json:"last_committed_generation"`
}

// NewActivationID generates a fresh 128-bit lowercase-hex activation
// identifier (§31.4): one per Local -> Managed activation, never reused.
func NewActivationID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate activation id: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// newTransitionID generates the journal transaction identifier (§31.23):
// fresh 128-bit crypto/rand lowercase hex, distinct from ActivationID.
func newTransitionID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate transition id: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// canonicalTransitionZones sorts and deduplicates the zone set (§31.23).
func canonicalTransitionZones(zones []string) []string {
	out := make([]string, 0, len(zones))
	seen := make(map[string]bool, len(zones))
	for _, zone := range zones {
		if zone == "" || seen[zone] {
			continue
		}
		seen[zone] = true
		out = append(out, zone)
	}
	sort.Strings(out)
	return out
}

// NewManagedLeshyTransition builds a validated journal record for one
// local <-> managed transaction.
func NewManagedLeshyTransition(from, to ManagedLeshyMode, socket string,
	fromActivationID, toActivationID string, fromGeneration uint64,
	zones []string, configSHA256 string) (*ManagedLeshyTransition, error) {
	if fromGeneration == ^uint64(0) {
		return nil, fmt.Errorf("mode generation overflow: from generation is MaxUint64")
	}
	id, err := newTransitionID()
	if err != nil {
		return nil, err
	}
	transition := &ManagedLeshyTransition{
		Schema:           managedLeshyTransitionSchema,
		ID:               id,
		From:             from,
		To:               to,
		Phase:            ManagedTransitionPrepared,
		Socket:           socket,
		FromActivationID: fromActivationID,
		ToActivationID:   toActivationID,
		FromGeneration:   fromGeneration,
		ToGeneration:     fromGeneration + 1,
		Zones:            canonicalTransitionZones(zones),
		ConfigSHA256:     configSHA256,
	}
	if err := transition.Validate(); err != nil {
		return nil, err
	}
	return transition, nil
}

var (
	lowerHex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)
	lowerHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Validate enforces the §31.23 deterministic-field rules.
func (t *ManagedLeshyTransition) Validate() error {
	if t.Schema != managedLeshyTransitionSchema {
		return fmt.Errorf("unknown transition schema %d", t.Schema)
	}
	if !lowerHex32.MatchString(t.ID) {
		return fmt.Errorf("transition id must be 32 lowercase hex characters")
	}
	if t.From == t.To {
		return fmt.Errorf("transition with from == to %q is invalid", t.From)
	}
	switch t.From {
	case ManagedLeshyLocal, ManagedLeshyManaged:
	default:
		return fmt.Errorf("unknown transition from mode %q", t.From)
	}
	switch t.To {
	case ManagedLeshyLocal, ManagedLeshyManaged:
	default:
		return fmt.Errorf("unknown transition to mode %q", t.To)
	}
	switch t.Phase {
	case ManagedTransitionPrepared, ManagedTransitionConfigWritten, ManagedTransitionMarkerWritten:
	default:
		return fmt.Errorf("unknown transition phase %q", t.Phase)
	}
	if t.ToGeneration != t.FromGeneration+1 {
		return fmt.Errorf("to generation %d must be from generation %d plus one", t.ToGeneration, t.FromGeneration)
	}
	// FromActivationID is empty exactly when From == local and otherwise
	// equals the active schema2 marker ActivationID (§31.23).
	if t.From == ManagedLeshyLocal && t.FromActivationID != "" {
		return fmt.Errorf("from activation id must be empty when transitioning from local mode")
	}
	if t.From == ManagedLeshyManaged && t.FromActivationID == "" {
		return fmt.Errorf("from activation id is required when transitioning from managed mode")
	}
	// ToActivationID is nonempty exactly when To == managed (§31.23).
	if t.To == ManagedLeshyManaged {
		if t.ToActivationID == "" {
			return fmt.Errorf("to activation id is required when transitioning to managed mode")
		}
		if t.Socket == "" {
			return fmt.Errorf("transition to managed mode requires the target socket")
		}
	}
	if t.To == ManagedLeshyLocal && t.ToActivationID != "" {
		return fmt.Errorf("to activation id must be empty when transitioning to local mode")
	}
	previous := ""
	for i, zone := range t.Zones {
		if zone == "" {
			return fmt.Errorf("zone %d is empty", i)
		}
		if i > 0 && zone <= previous {
			return fmt.Errorf("zones must be canonical sorted unique names")
		}
		previous = zone
	}
	if !lowerHex64.MatchString(t.ConfigSHA256) {
		return fmt.Errorf("config sha256 must be 64 lowercase hex characters")
	}
	return nil
}

// WriteTransitionJournal durably writes the journal record.
func WriteTransitionJournal(stateDir string, transition ManagedLeshyTransition) error {
	if err := transition.Validate(); err != nil {
		return fmt.Errorf("refusing to write an invalid transition: %w", err)
	}
	return fsutil.WriteAtomicJSON(TransitionJournalPath(stateDir), transition, 0o600)
}

// ReadTransitionJournal loads the journal. Missing is (nil, false, nil).
func ReadTransitionJournal(stateDir string) (*ManagedLeshyTransition, bool, error) {
	raw, err := os.ReadFile(TransitionJournalPath(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read transition journal: %w", err)
	}
	var transition ManagedLeshyTransition
	if err := json.Unmarshal(raw, &transition); err != nil {
		return nil, true, fmt.Errorf("decode transition journal: %w", err)
	}
	if err := transition.Validate(); err != nil {
		return nil, true, fmt.Errorf("transition journal is invalid: %w", err)
	}
	return &transition, true, nil
}

// RemoveTransitionJournal durably deletes the journal; missing is success.
func RemoveTransitionJournal(stateDir string) error {
	return fsutil.RemoveDurable(TransitionJournalPath(stateDir))
}

// ReadCommittedModeGeneration loads the monotonic committed counter.
// A missing counter is generation 0: the first completed transition commits
// generation 1.
func ReadCommittedModeGeneration(stateDir string) (uint64, error) {
	raw, err := os.ReadFile(ModeGenerationPath(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read mode generation: %w", err)
	}
	var state modeGenerationState
	if err := json.Unmarshal(raw, &state); err != nil {
		return 0, fmt.Errorf("decode mode generation: %w", err)
	}
	if state.Schema != 1 {
		return 0, fmt.Errorf("mode generation schema %d is not supported", state.Schema)
	}
	return state.LastCommittedGeneration, nil
}

// WriteCommittedModeGeneration durably commits the counter.
func WriteCommittedModeGeneration(stateDir string, generation uint64) error {
	return fsutil.WriteAtomicJSON(ModeGenerationPath(stateDir),
		modeGenerationState{Schema: 1, LastCommittedGeneration: generation}, 0o600)
}

// transitionRecoveryDecision is what startup must do with a found journal
// (§31.4): compare the committed counter with the journal generations.
type transitionRecoveryDecision int

const (
	// transitionRecoveryRollForward: committed == FromGeneration, the
	// transaction is in flight and must be rolled forward to the target.
	transitionRecoveryRollForward transitionRecoveryDecision = iota
	// transitionRecoveryVerifyAndClean: committed == ToGeneration, the
	// generation commit already happened and only target verification plus
	// journal removal remain.
	transitionRecoveryVerifyAndClean
)

// planTransitionRecovery implements the §31.4 comparison rule. Anything else
// than the two legal relations is stale/corrupt and fails startup closed.
func planTransitionRecovery(committed uint64, transition *ManagedLeshyTransition) (transitionRecoveryDecision, error) {
	switch {
	case committed == transition.FromGeneration:
		return transitionRecoveryRollForward, nil
	case committed == transition.ToGeneration:
		return transitionRecoveryVerifyAndClean, nil
	default:
		return 0, fmt.Errorf(
			"stale transition journal: committed generation %d matches neither from %d nor to %d",
			committed, transition.FromGeneration, transition.ToGeneration)
	}
}

// activationIDFingerprint renders the diagnostics-safe activation identity:
// the first 12 hex characters of SHA-256(ActivationID) (§31.13). Never the
// identifier itself, never the bearer token.
func activationIDFingerprint(activationID string) string {
	if activationID == "" {
		return ""
	}
	sum := sha256Hex([]byte(activationID))
	return sum[:12]
}

// sha256Hex is the lowercase hex SHA-256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// configDigest computes the §31.23 digest over the exact generated config
// bytes including the final newline.
func configDigest(configBytes []byte) string {
	return sha256Hex(configBytes)
}

// ensureTerminalNewline guarantees the deterministic config bytes end with
// exactly one newline (§31.23 line-ending policy follows the generator).
func ensureTerminalNewline(data []byte) []byte {
	text := strings.TrimRight(string(data), "\n")
	return []byte(text + "\n")
}
