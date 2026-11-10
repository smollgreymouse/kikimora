//go:build kikimora_test_failpoints

package control

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// The D10 unit crash matrix (packet §10.2, companion §12): for every
// failpoint boundary a real child process runs the real transaction and dies
// with exit code 86 at that exact durable boundary (no deferred cleanup).
// The parent then asserts the pre-restart durable state — journal phase,
// config content, marker presence/content, committed generation, checkpoint
// files — and restarts through the production startup path, asserting final
// convergence.

var d10Boundaries = []string{
	"before_journal",
	"after_prepared",
	"after_config_write",
	"after_config_written",
	"after_marker_operation",
	"after_marker_written",
	"after_apply",
	"after_generation_commit",
	"before_journal_delete",
}

// d10ChildDirs rebuilds the fixed directory layout the parent staged: the
// config lives under base, the state under base/state.
func d10ChildSetup(t *testing.T, base string) *Manager {
	t.Helper()
	configPath := writeConfig(t, base, "primary", "openconnect")
	manager, err := NewManagerWithConfigDir(base, []string{configPath}, &fakeLauncher{}, "", "", "")
	if err != nil {
		t.Fatalf("child manager: %v", err)
	}
	manager.monitorCancel()
	t.Cleanup(func() { _ = manager.Close() })
	stateDir := filepath.Join(base, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manager.SetStateDir(stateDir)
	manager.writerLockPath = filepath.Join(base, "leshy-writer.lock")
	manager.leshyConfigPath = filepath.Join(base, "leshy", "config.toml")
	manager.routingDir = filepath.Join(base, "leshy", "domains")
	manager.bypassConfigPath = filepath.Join(base, "leshy", "bypass.json")
	// Production creates the credential at the first startup; the crash
	// helper mirrors that so the parent restart can converge on a managed
	// install. The canonical bytes are written directly: the hardened-DACL
	// writer locks the file to SYSTEM/Administrators, which this
	// non-elevated test process cannot read back (the VM matrices cover the
	// real DACL path).
	raw := make([]byte, privilegedAuthKeyBytes)
	for i := range raw {
		raw[i] = byte(i + 5)
	}
	encoded := strings.ToLower(hex.EncodeToString(raw)) + "\n"
	if err := os.WriteFile(PrivilegedAuthKeyPath(stateDir), []byte(encoded), 0o600); err != nil {
		t.Fatalf("stage the credential: %v", err)
	}
	return manager
}

// TestD10CrashStageHelper is the crash child: it runs ONE real transaction
// under an active failpoint and never returns normally — the failpoint exits
// 86 at the boundary, and reaching the end means the boundary never fired.
func TestD10CrashStageHelper(t *testing.T) {
	direction := os.Getenv("KK_D10_CRASH_STAGE")
	if direction == "" {
		t.Skip("crash helper: drive through TestD10UnitCrashMatrix")
	}
	base := os.Getenv("KK_D10_STATE")
	boundary := os.Getenv("KK_D10_BOUNDARY")
	manager := d10ChildSetup(t, base)

	switch direction {
	case "enable":
		os.Setenv("KK_MODE_FAILPOINT", "enable/"+boundary)
		if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err != nil {
			// A failpoint-free failure is a staging bug, not a crash result.
			t.Fatalf("enable failed before any boundary: %v", err)
		}
	case "disable":
		if _, err := manager.EnableManagedLeshy("/run/kk-test.sock", "", testCapabilities()); err != nil {
			t.Fatalf("pre-stage enable: %v", err)
		}
		marker, found, err := readManagedMarker(filepath.Join(base, "leshy-managed.json"))
		if err != nil || !found {
			t.Fatalf("pre-stage marker: found=%v err=%v", found, err)
		}
		// An empty current-activation checkpoint: the Disable drain owns it
		// from the marker-operation boundary on.
		roleStateDir := filepath.Join(base, "primary")
		if err := os.MkdirAll(roleStateDir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := routing.WriteSelectedRoutesCheckpoint(filepath.Join(roleStateDir, "selected-routes.json"),
			routing.SelectedRoutesCheckpoint{
				Schema:          routing.SelectedRoutesCheckpointSchema,
				Role:            "primary",
				RouteGeneration: 3,
				ActivationID:    marker.ActivationID,
				ModeGeneration:  marker.ModeGeneration,
				Entries:         []routing.SelectedRoute{},
				Platform:        routing.RuntimeCheckpointPlatform(),
			}); err != nil {
			t.Fatal(err)
		}
		os.Setenv("KK_MODE_FAILPOINT", "disable/"+boundary)
		if _, err := manager.DisableManagedLeshy(""); err != nil {
			t.Fatalf("disable failed before any boundary: %v", err)
		}
	default:
		t.Fatalf("unknown direction %q", direction)
	}
	// The failpoint must have fired inside the transaction.
	t.Logf("boundary %s/%s never fired", direction, boundary)
	os.Exit(87)
}

// d10Expectation is the per-boundary durable state and convergence contract.
type d10Expectation struct {
	journalPhase  string // "" = no journal
	configManaged bool
	markerPresent bool
	generation    uint64
	checkpoint    bool
	// convergence after the production restart:
	finalManaged    bool
	finalGeneration uint64
}

// d10JournalPhase maps each boundary to the durable journal phase it leaves
// behind ("": no journal). The raw config write and the raw marker operation
// happen BEFORE the journal advance, so their boundaries still carry the
// previous phase.
func d10JournalPhase(boundary string) string {
	switch boundary {
	case "after_prepared", "after_config_write":
		return string(ManagedTransitionPrepared)
	case "after_config_written", "after_marker_operation":
		return string(ManagedTransitionConfigWritten)
	case "after_marker_written", "after_apply", "after_generation_commit", "before_journal_delete":
		return string(ManagedTransitionMarkerWritten)
	}
	return "" // before_journal
}

func d10Expectations(direction, boundary string) d10Expectation {
	position := map[string]int{}
	for i, name := range d10Boundaries {
		position[name] = i
	}
	atOrAfter := func(b string) bool { return position[boundary] >= position[b] }
	afterConfigWrite := atOrAfter("after_config_write")
	afterMarkerOp := atOrAfter("after_marker_operation")
	afterGeneration := atOrAfter("after_generation_commit")
	if direction == "enable" {
		if boundary == "before_journal" {
			return d10Expectation{finalManaged: false, finalGeneration: 0}
		}
		return d10Expectation{
			journalPhase:    d10JournalPhase(boundary),
			configManaged:   afterConfigWrite,
			markerPresent:   afterMarkerOp,
			generation:      map[bool]uint64{true: 1, false: 0}[afterGeneration],
			finalManaged:    true,
			finalGeneration: 1,
		}
	}
	if boundary == "before_journal" {
		return d10Expectation{
			configManaged: true, markerPresent: true, generation: 1, checkpoint: true,
			finalManaged: true, finalGeneration: 1,
		}
	}
	return d10Expectation{
		journalPhase:    d10JournalPhase(boundary),
		configManaged:   !afterConfigWrite,
		markerPresent:   !afterMarkerOp,
		generation:      map[bool]uint64{true: 2, false: 1}[afterGeneration],
		checkpoint:      !afterMarkerOp,
		finalManaged:    false,
		finalGeneration: 2,
	}
}

func TestD10UnitCrashMatrix(t *testing.T) {
	for _, direction := range []string{"enable", "disable"} {
		for _, boundary := range d10Boundaries {
			t.Run(direction+"/"+boundary, func(t *testing.T) {
				base := t.TempDir()
				// Stage the crash through a real child process.
				cmd := exec.Command(os.Args[0], "-test.run", "^TestD10CrashStageHelper$", "-test.timeout", "120s")
				cmd.Env = append(os.Environ(),
					"KK_D10_CRASH_STAGE="+direction,
					"KK_D10_STATE="+base,
					"KK_D10_BOUNDARY="+boundary,
				)
				output, err := cmd.CombinedOutput()
				if err != nil {
					exitErr, ok := err.(*exec.ExitError)
					if !ok || exitErr.ExitCode() != 86 {
						t.Fatalf("the crash child must exit 86 at the boundary, got %v:\n%s", err, output)
					}
				} else {
					t.Fatalf("the crash child returned cleanly:\n%s", output)
				}

				want := d10Expectations(direction, boundary)

				// Pre-restart durable state (§10.2).
				_, journalFound, err := ReadTransitionJournal(filepath.Join(base, "state"))
				if err != nil {
					t.Fatal(err)
				}
				if journalFound != (want.journalPhase != "") {
					t.Fatalf("journal presence %v does not match the %s boundary (want phase %q)",
						journalFound, boundary, want.journalPhase)
				}
				if want.journalPhase != "" {
					transition, _, err := ReadTransitionJournal(filepath.Join(base, "state"))
					if err != nil {
						t.Fatal(err)
					}
					if string(transition.Phase) != want.journalPhase {
						t.Fatalf("journal phase %q does not match the %s boundary (want %q)",
							transition.Phase, boundary, want.journalPhase)
					}
				}
				configRaw, err := os.ReadFile(filepath.Join(base, "leshy", "config.toml"))
				configExists := err == nil
				if configExists && want.configManaged != strings.Contains(string(configRaw), "routing_backend = \"kikimora\"") {
					t.Fatalf("config managed-ness does not match the %s boundary:\n%s", boundary, configRaw)
				}
				_, markerFound, err := readManagedMarker(filepath.Join(base, "leshy-managed.json"))
				if err != nil {
					t.Fatal(err)
				}
				if markerFound != want.markerPresent {
					t.Fatalf("marker presence %v does not match the %s boundary", markerFound, boundary)
				}
				generation, err := ReadCommittedModeGeneration(filepath.Join(base, "state"))
				if err != nil {
					t.Fatal(err)
				}
				if generation != want.generation {
					t.Fatalf("committed generation %d does not match the %s boundary (want %d)",
						generation, boundary, want.generation)
				}
				_, checkpointErr := os.Stat(filepath.Join(base, "primary", "selected-routes.json"))
				checkpointExists := checkpointErr == nil
				if checkpointExists != want.checkpoint {
					t.Fatalf("checkpoint presence %v does not match the %s boundary", checkpointExists, boundary)
				}

				// Restart through the production startup path: a fresh
				// deferred manager over the crashed state.
				restart, err := NewManagerWithConfigDir(base, []string{filepath.Join(base, "primary.toml")}, &fakeLauncher{}, "", "", "")
				if err != nil {
					t.Fatalf("restart manager: %v", err)
				}
				restart.monitorCancel()
				defer restart.Close()
				restart.leshyConfigPath = filepath.Join(base, "leshy", "config.toml")
				restart.routingDir = filepath.Join(base, "leshy", "domains")
				restart.bypassConfigPath = filepath.Join(base, "leshy", "bypass.json")
				state, err := restart.StartupManagedAuthority(filepath.Join(base, "state"))
				if err != nil {
					t.Fatalf("the restart must converge: %v", err)
				}
				if state.Managed != want.finalManaged || state.Generation != want.finalGeneration {
					t.Fatalf("converged descriptor %#v does not match the %s/%s contract",
						state, direction, boundary)
				}
				if restart.modeTransitionGateActive() {
					t.Fatal("a converged restart must not carry a transition gate")
				}
				if _, found, _ := ReadTransitionJournal(filepath.Join(base, "state")); found {
					t.Fatal("the converged state must have no journal")
				}
				snapshot := restart.Snapshot()
				if snapshot.ManagedMode != want.finalManaged {
					t.Fatalf("snapshot mode does not match the convergence: %#v", snapshot)
				}
			})
		}
	}
}
