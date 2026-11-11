package diagnostics

import (
	"context"
	"path/filepath"
	"testing"
)

// U5 (docs/roadmap.md §12.4): hostSources=false must make one collection pass
// touch no ambient host source — no journal, no event log, no product-log
// globs, no network-state commands — while the collector itself still works.

func newHermeticCollector(t *testing.T) *Collector {
	t.Helper()
	collector, err := New(filepath.Join(t.TempDir(), "diagnostics"), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	return collector
}

func TestDiagnosticsHostSourcesDefaultToTrue(t *testing.T) {
	collector := newHermeticCollector(t)
	var globReads int
	collector.logGlobsFn = func() []string {
		globReads++
		return nil
	}
	if err := collector.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := collector.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if globReads == 0 {
		t.Fatal("default collector never expands the platform log globs")
	}
}

func TestDiagnosticsHostSourcesFalseSkipsAmbientCollectors(t *testing.T) {
	collector := newHermeticCollector(t)
	collector.SetHostSources(false)

	var globReads, networkReads int
	collector.logGlobsFn = func() []string {
		globReads++
		return []string{"/etc/kikimora/should-never-be-read/*.log"}
	}
	collector.networkStateFn = func() string {
		networkReads++
		return "ip route show"
	}
	if err := collector.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}

	if err := collector.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if globReads != 0 {
		t.Fatalf("hostSources=false still expanded log globs %d times", globReads)
	}
	if networkReads != 0 {
		t.Fatalf("hostSources=false still ran network state %d times", networkReads)
	}

	// The collector stays functional: its own snapshot spool is still written
	// for this pass.
	entries, err := filepath.Glob(filepath.Join(collector.Directory(), "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("hermetic collection produced no spool output at all")
	}
}

func TestDiagnosticsHostSourcesTogglePerCollection(t *testing.T) {
	collector := newHermeticCollector(t)
	var globReads int
	collector.logGlobsFn = func() []string {
		globReads++
		return nil
	}
	if err := collector.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := collector.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := globReads
	if before == 0 {
		t.Fatal("host pass never read the globs")
	}
	collector.SetHostSources(false)
	if err := collector.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if globReads != before {
		t.Fatalf("hostSources=false changed glob reads: %d -> %d", before, globReads)
	}
}
