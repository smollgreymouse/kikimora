package control

import (
	"context"
	"path/filepath"
	"testing"
)

// The capture switch is wired through the same API the desktop toggle uses:
// a missing value is rejected, and a fresh core reports the spool as disabled
// rather than pretending collection is running.
func TestDiagnosticsCaptureFailsClosedWithoutAValue(t *testing.T) {
	manager, err := NewManager([]string{writeConfig(t, t.TempDir(), "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := manager.SetDiagnosticsDir(filepath.Join(t.TempDir(), "diagnostics")); err != nil {
		t.Fatalf("SetDiagnosticsDir: %v", err)
	}

	response := manager.Handle(context.Background(), Request{Version: APIVersion, Method: "SetDiagnosticsCapture"})
	if response.OK || response.Error == "" {
		t.Fatalf("missing enabled was accepted: %+v", response)
	}

	diagnostics := manager.DiagnosticSnapshot().Diagnostics
	if diagnostics == nil || diagnostics.Capture == nil {
		t.Fatalf("diagnostics snapshot carries no capture status: %+v", diagnostics)
	}
	if diagnostics.Capture.Enabled {
		t.Fatalf("a fresh capture must be disabled: %+v", diagnostics.Capture)
	}
	if diagnostics.Capture.Directory == "" {
		t.Fatal("capture directory is empty")
	}
}

func TestDiagnosticsCaptureSwitchIsExposedInTheSnapshot(t *testing.T) {
	manager, err := NewManager([]string{writeConfig(t, t.TempDir(), "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := manager.SetDiagnosticsDir(filepath.Join(t.TempDir(), "diagnostics")); err != nil {
		t.Fatalf("SetDiagnosticsDir: %v", err)
	}
	enabled := false
	response := manager.Handle(context.Background(), Request{
		Version: APIVersion,
		Method:  "SetDiagnosticsCapture",
		Enabled: &enabled,
	})
	if !response.OK {
		t.Fatalf("SetDiagnosticsCapture: %+v", response)
	}
	diagnostics := manager.DiagnosticSnapshot().Diagnostics
	if diagnostics == nil || diagnostics.Capture == nil || diagnostics.Capture.Enabled {
		t.Fatalf("capture status did not follow the switch: %+v", diagnostics)
	}
}
