package routing

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func mustPrefix(t *testing.T, raw string) netip.Prefix {
	t.Helper()
	prefix := netip.MustParsePrefix(raw)
	if !IsCanonicalPrefix(prefix) {
		t.Fatalf("%s is not canonical", raw)
	}
	return prefix
}

func TestReserveCreatesPendingOwnership(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	prefix := mustPrefix(t, "203.0.113.10/32")
	if err := registry.Reserve("primary", "primary", prefix, SelectedDNS, 3, 9, "kk-awg0", 7); err != nil {
		t.Fatal(err)
	}
	snapshot := registry.Snapshot("primary")
	if len(snapshot) != 1 || snapshot[0].State != SelectedPending {
		t.Fatalf("expected one pending entry: %#v", snapshot)
	}
	if snapshot[0].Zone != "primary" || snapshot[0].Source != SelectedDNS ||
		snapshot[0].ToadGeneration != 3 || snapshot[0].RouteGeneration != 9 ||
		snapshot[0].Interface != "kk-awg0" || snapshot[0].IfIndex != 7 {
		t.Fatalf("identity lost: %#v", snapshot[0])
	}
}

func TestDuplicateReserveIsIdempotent(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	prefix := mustPrefix(t, "2001:db8::10/128")
	arguments := []any{"primary", "primary", prefix, SelectedDNS, uint64(3), uint64(9), "kk-awg0", 7}
	for i := 0; i < 3; i++ {
		if err := registry.Reserve(arguments[0].(string), arguments[1].(string), prefix,
			arguments[3].(SelectedRouteSource), arguments[4].(uint64), arguments[5].(uint64),
			arguments[6].(string), arguments[7].(int)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := registry.Snapshot("primary")
	if len(snapshot) != 1 || snapshot[0].State != SelectedPending {
		t.Fatalf("duplicate reserves must be idempotent: %#v", snapshot)
	}
}

func TestReserveWithNewIfindexDemotesActiveToPending(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	prefix := mustPrefix(t, "10.55.0.1/32")
	if err := registry.Reserve("primary", "primary", prefix, SelectedDNS, 3, 9, "kk-awg0", 7); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkActive("primary", prefix); err != nil {
		t.Fatal(err)
	}
	// A replacement Toad reused the interface name but has a new ifindex.
	if err := registry.Reserve("primary", "primary", prefix, SelectedDNS, 4, 10, "kk-awg0", 8); err != nil {
		t.Fatal(err)
	}
	snapshot := registry.Snapshot("primary")
	if snapshot[0].State != SelectedPending || snapshot[0].IfIndex != 8 {
		t.Fatalf("identity change must demote Active to Pending with the new ifindex: %#v", snapshot[0])
	}
}

func TestMarkParkedAndMarkActiveRequireRegistration(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	unregistered := mustPrefix(t, "192.0.2.1/32")
	if err := registry.MarkParked("primary", unregistered); err == nil {
		t.Fatal("an unregistered prefix must not be parkable")
	}
	registered := mustPrefix(t, "192.0.2.2/32")
	if err := registry.Reserve("primary", "primary", registered, SelectedStatic, 1, 1, "kk-awg0", 7); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkParked("primary", registered); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkActive("primary", registered); err != nil {
		t.Fatal(err)
	}
	if got := registry.Snapshot("primary")[0].State; got != SelectedActive {
		t.Fatalf("state after MarkActive: %s", got)
	}
}

func TestSnapshotFamilySplitsV4AndV6(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	v4 := mustPrefix(t, "203.0.113.10/32")
	v6 := mustPrefix(t, "2001:db8::10/128")
	staticWide := mustPrefix(t, "10.0.0.0/24")
	for _, entry := range []struct {
		prefix netip.Prefix
		source SelectedRouteSource
	}{
		{v4, SelectedDNS},
		{v6, SelectedDNS},
		{staticWide, SelectedStatic},
	} {
		if err := registry.Reserve("primary", "primary", entry.prefix, entry.source, 1, 1, "kk-awg0", 7); err != nil {
			t.Fatal(err)
		}
	}
	if got := registry.SnapshotFamily("primary", 4); len(got) != 2 {
		t.Fatalf("family 4 snapshot: %#v", got)
	}
	if got := registry.SnapshotFamily("primary", 6); len(got) != 1 || got[0].Prefix != v6 {
		t.Fatalf("family 6 snapshot: %#v", got)
	}
	if got := registry.Snapshot("other"); len(got) != 0 {
		t.Fatalf("another role must see nothing: %#v", got)
	}
	// Static non-host prefixes own wider ownership records.
	for _, entry := range registry.Snapshot("primary") {
		if entry.Prefix == staticWide && (entry.Source != SelectedStatic || entry.Prefix.Bits() != 24) {
			t.Fatalf("static /24 ownership: %#v", entry)
		}
	}
}

func TestRemoveAndRemoveRole(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	first := mustPrefix(t, "203.0.113.10/32")
	second := mustPrefix(t, "203.0.113.11/32")
	_ = registry.Reserve("primary", "primary", first, SelectedDNS, 1, 1, "kk", 5)
	_ = registry.Reserve("primary", "primary", second, SelectedDNS, 1, 1, "kk", 5)
	_ = registry.Reserve("secondary", "secondary", second, SelectedDNS, 1, 1, "kk", 5)

	registry.Remove("primary", first)
	if got := registry.SnapshotFamily("primary", 4); len(got) != 1 {
		t.Fatalf("Remove left an entry behind: %#v", got)
	}
	registry.RemoveRole("primary")
	if got := registry.Snapshot("primary"); len(got) != 0 {
		t.Fatalf("RemoveRole left entries behind: %#v", got)
	}
	if got := registry.Snapshot("secondary"); len(got) != 1 {
		t.Fatalf("RemoveRole must not touch other roles: %#v", got)
	}
}

func TestNonCanonicalPrefixIsRejected(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	loose := netip.MustParsePrefix("203.0.113.10/24") // host bits set
	if err := registry.Reserve("primary", "primary", loose, SelectedDNS, 1, 1, "kk", 5); err == nil {
		t.Fatal("a non-canonical prefix must be rejected")
	}
	if err := registry.MarkActive("primary", loose); err == nil {
		t.Fatal("a non-canonical prefix must be rejected by mark")
	}
}

func TestSelectedRoutesCheckpointRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state", "selected-routes.json")
	checkpoint := SelectedRoutesCheckpoint{
		Schema:          selectedRoutesCheckpointSchema,
		Role:            "primary",
		RouteGeneration: 9,
		Entries: []SelectedRoute{
			{Role: "primary", Zone: "primary", Prefix: mustPrefix(t, "203.0.113.10/32"), Source: SelectedDNS, ToadGeneration: 3, RouteGeneration: 9, Interface: "kk-awg0", IfIndex: 7, State: SelectedActive},
			{Role: "primary", Zone: "primary", Prefix: mustPrefix(t, "2001:db8::10/128"), Source: SelectedDNS, ToadGeneration: 3, RouteGeneration: 9, Interface: "kk-awg0", IfIndex: 7, State: SelectedParked},
		},
		Parked: []string{"2001:db8::10/128"},
	}
	if err := WriteSelectedRoutesCheckpoint(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadSelectedRoutesCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.RouteGeneration != 9 || len(decoded.Entries) != 2 || decoded.Entries[0].State != SelectedActive {
		t.Fatalf("checkpoint round-trip mismatch: %#v", decoded)
	}
	if len(decoded.Parked) != 1 || decoded.Parked[0] != "2001:db8::10/128" {
		t.Fatalf("parked list lost: %#v", decoded)
	}
}

func TestSelectedRoutesCheckpointSurvivesCrashBetweenReserveAndMarkActive(t *testing.T) {
	// The crash invariant: ownership checkpointed before the kernel
	// mutation. Restoring the checkpoint must yield a Pending entry that
	// recovery can reconcile or park from, never a claimed Active row.
	registry := NewMemorySelectedRegistry()
	prefix := mustPrefix(t, "203.0.113.10/32")
	if err := registry.Reserve("primary", "primary", prefix, SelectedDNS, 3, 9, "kk-awg0", 7); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "selected-routes.json")
	if err := WriteSelectedRoutesCheckpoint(path, SelectedRoutesCheckpoint{
		Schema:          selectedRoutesCheckpointSchema,
		Role:            "primary",
		RouteGeneration: 9,
		Entries:         registry.Snapshot("primary"),
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate the crash: nothing marked Active, kernel row may or may not
	// exist. Restore rebuilds the registry from the checkpoint.
	restored := NewMemorySelectedRegistry()
	decoded, err := ReadSelectedRoutesCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range decoded.Entries {
		if err := restored.Reserve(entry.Role, entry.Zone, entry.Prefix, entry.Source,
			entry.ToadGeneration, entry.RouteGeneration, entry.Interface, entry.IfIndex); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := restored.Snapshot("primary")
	if len(snapshot) != 1 || snapshot[0].State != SelectedPending {
		t.Fatalf("restored ownership must be pending: %#v", snapshot)
	}
}

func TestSelectedRoutesCheckpointRejectsUnknownSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "selected-routes.json")
	if err := os.WriteFile(path, []byte(`{"schema":99,"role":"primary"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSelectedRoutesCheckpoint(path); err == nil {
		t.Fatal("an unknown schema must be rejected, not silently trusted")
	}
}
