package control

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func validTransition(t *testing.T) *ManagedLeshyTransition {
	t.Helper()
	transition, err := NewManagedLeshyTransition(
		ManagedLeshyLocal, ManagedLeshyManaged, "/run/kikimora/core.sock",
		"", "b4c0ffee00000000000000000000abcd", 7,
		[]string{"zeta", "alpha", "zeta"}, configDigest([]byte("config\n")))
	if err != nil {
		t.Fatal(err)
	}
	return transition
}

func TestTransitionJournalRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	transition := validTransition(t)

	// Zones are canonicalized sorted unique (§31.23).
	if got := strings.Join(transition.Zones, ","); got != "alpha,zeta" {
		t.Fatalf("zones must be canonical sorted unique, got %q", got)
	}
	if transition.ToGeneration != 8 {
		t.Fatalf("to generation must be from+1, got %d", transition.ToGeneration)
	}
	if transition.ID == transition.ToActivationID {
		t.Fatal("transition id and activation id are distinct identifiers")
	}

	if err := WriteTransitionJournal(stateDir, *transition); err != nil {
		t.Fatal(err)
	}
	loaded, exists, err := ReadTransitionJournal(stateDir)
	if err != nil || !exists {
		t.Fatalf("journal must exist: exists=%v err=%v", exists, err)
	}
	if loaded.ID != transition.ID || loaded.Phase != transition.Phase ||
		loaded.ConfigSHA256 != transition.ConfigSHA256 {
		t.Fatalf("roundtrip mismatch: %#v vs %#v", loaded, transition)
	}

	if err := RemoveTransitionJournal(stateDir); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := ReadTransitionJournal(stateDir); err != nil || exists {
		t.Fatalf("journal must be gone: exists=%v err=%v", exists, err)
	}
	// Removing again is success.
	if err := RemoveTransitionJournal(stateDir); err != nil {
		t.Fatal(err)
	}
}

func TestTransitionValidationRules(t *testing.T) {
	base := *validTransition(t)
	cases := map[string]func(*ManagedLeshyTransition){
		"from equals to":                func(x *ManagedLeshyTransition) { x.From, x.To = ManagedLeshyLocal, ManagedLeshyLocal },
		"unknown mode":                  func(x *ManagedLeshyTransition) { x.To = "bogus" },
		"unknown phase":                 func(x *ManagedLeshyTransition) { x.Phase = "halfway" },
		"generation not from+1":         func(x *ManagedLeshyTransition) { x.FromGeneration, x.ToGeneration = 5, 9 },
		"local from carries activation": func(x *ManagedLeshyTransition) { x.FromActivationID = "something" },
		"managed to without activation": func(x *ManagedLeshyTransition) { x.ToActivationID = "" },
		"managed to without socket":     func(x *ManagedLeshyTransition) { x.Socket = "" },
		"local to carries activation": func(x *ManagedLeshyTransition) {
			x.From, x.FromActivationID = ManagedLeshyManaged, "old"
			x.To, x.ToActivationID = ManagedLeshyLocal, "new"
		},
		"unsorted zones":  func(x *ManagedLeshyTransition) { x.Zones = []string{"zeta", "alpha"} },
		"duplicate zones": func(x *ManagedLeshyTransition) { x.Zones = []string{"alpha", "alpha"} },
		"bad id":          func(x *ManagedLeshyTransition) { x.ID = "XYZ" },
		"bad digest":      func(x *ManagedLeshyTransition) { x.ConfigSHA256 = "deadbeef" },
		"bad schema":      func(x *ManagedLeshyTransition) { x.Schema = 2 },
	}
	for name, mutate := range cases {
		transition := base
		mutate(&transition)
		if err := transition.Validate(); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func TestTransitionJournalRejectsInvalidWrite(t *testing.T) {
	stateDir := t.TempDir()
	transition := *validTransition(t)
	transition.ToGeneration = transition.FromGeneration
	if err := WriteTransitionJournal(stateDir, transition); err == nil {
		t.Fatal("writing an invalid transition must fail")
	}
	if _, exists, _ := ReadTransitionJournal(stateDir); exists {
		t.Fatal("nothing may be written for an invalid transition")
	}
}

func TestModeGenerationStorage(t *testing.T) {
	stateDir := t.TempDir()
	// Missing counter is generation 0.
	generation, err := ReadCommittedModeGeneration(stateDir)
	if err != nil || generation != 0 {
		t.Fatalf("missing counter must read 0: got %d err %v", generation, err)
	}
	if err := WriteCommittedModeGeneration(stateDir, 42); err != nil {
		t.Fatal(err)
	}
	generation, err = ReadCommittedModeGeneration(stateDir)
	if err != nil || generation != 42 {
		t.Fatalf("committed generation must roundtrip: got %d err %v", generation, err)
	}
}

func TestTransitionRecoveryDecision(t *testing.T) {
	transition := validTransition(t) // from=7, to=8
	decision, err := planTransitionRecovery(7, transition)
	if err != nil || decision != transitionRecoveryRollForward {
		t.Fatalf("committed==from must roll forward: %v %v", decision, err)
	}
	decision, err = planTransitionRecovery(8, transition)
	if err != nil || decision != transitionRecoveryVerifyAndClean {
		t.Fatalf("committed==to must verify and clean: %v %v", decision, err)
	}
	for _, stale := range []uint64{0, 6, 9, 100} {
		if _, err := planTransitionRecovery(stale, transition); err == nil {
			t.Fatalf("committed %d must fail startup closed", stale)
		}
	}
}

func TestActivationFingerprintAndDigest(t *testing.T) {
	activationID := "some-activation-id"
	fingerprint := activationIDFingerprint(activationID)
	sum := sha256.Sum256([]byte(activationID))
	want := hex.EncodeToString(sum[:])[:12]
	if fingerprint != want || len(fingerprint) != 12 {
		t.Fatalf("fingerprint must be the first 12 hex chars of sha256, got %q", fingerprint)
	}
	if activationIDFingerprint("") != "" {
		t.Fatal("empty activation id has no fingerprint")
	}
	if configDigest([]byte("x\n")) != configDigest([]byte("x\n")) {
		t.Fatal("digest must be deterministic")
	}
	if got := string(ensureTerminalNewline([]byte("x"))); got != "x\n" {
		t.Fatalf("terminal newline policy violated: %q", got)
	}
	if got := string(ensureTerminalNewline([]byte("x\n\n"))); got != "x\n" {
		t.Fatalf("exactly one terminal newline: %q", got)
	}
}

func TestNewActivationIDShape(t *testing.T) {
	first, err := NewActivationID()
	if err != nil {
		t.Fatal(err)
	}
	if !lowerHex32.MatchString(first) {
		t.Fatalf("activation id must be 32 lowercase hex characters, got %q", first)
	}
	second, err := NewActivationID()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("activation ids must never repeat")
	}
}
