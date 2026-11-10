package routing

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testIdentity(ifindex int) SelectedRouteIdentity {
	return SelectedRouteIdentity{
		Table:    ManagedSelectedRouteTable,
		IfIndex:  ifindex,
		Protocol: 4, // Linux RTPROT_STATIC; platform-native per §31.26
		Metric:   0,
		Scope:    ScopeLink,
	}
}

func testPrefix(t *testing.T, text string) netip.Prefix {
	t.Helper()
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		t.Fatal(err)
	}
	return prefix
}

func reserveEntry(t *testing.T, registry SelectedRouteRegistry, role, zone, prefixText string, ifindex int) netip.Prefix {
	t.Helper()
	prefix := testPrefix(t, prefixText)
	if err := registry.Reserve(role, zone, prefix, SelectedDNS, 1, 1, "kk0", testIdentity(ifindex)); err != nil {
		t.Fatal(err)
	}
	return prefix
}

func TestSelectedTransitionsPendingApplyingActive(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	prefix := reserveEntry(t, registry, "primary", "corp", "203.0.113.10/32", 5)

	entry := registry.Snapshot("primary")[0]
	if entry.State != SelectedPending {
		t.Fatalf("reserve must start pending, got %q", entry.State)
	}
	if err := registry.MarkActive("primary", prefix, testIdentity(5)); err == nil {
		t.Fatal("MarkActive from pending must be rejected")
	}
	if err := registry.MarkApplying("primary", prefix); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkActive("primary", prefix, testIdentity(6)); err == nil {
		t.Fatal("observed must equal desired")
	}
	if err := registry.MarkActive("primary", prefix, testIdentity(5)); err != nil {
		t.Fatal(err)
	}
	entry = registry.Snapshot("primary")[0]
	if entry.State != SelectedActive || entry.Owned == nil || *entry.Owned != testIdentity(5) {
		t.Fatalf("active entry mismatch: %#v", entry)
	}
}

func TestSelectedRetireDesiredSemantics(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	pending := reserveEntry(t, registry, "primary", "corp", "203.0.113.10/32", 5)
	if err := registry.MarkRetiring("primary", pending); err != nil {
		t.Fatal(err)
	}
	entry := registry.Snapshot("primary")[0]
	if entry.RetireDesired {
		t.Fatal("retiring from pending must not set retire_desired: no mutation was authorized")
	}
	if entry.Desired.IfIndex != 5 || entry.State != SelectedRetiring {
		t.Fatalf("retiring evidence must survive: %#v", entry)
	}

	registry2 := NewMemorySelectedRegistry()
	applying := reserveEntry(t, registry2, "primary", "corp", "203.0.113.11/32", 5)
	if err := registry2.MarkApplying("primary", applying); err != nil {
		t.Fatal(err)
	}
	if err := registry2.MarkRetiring("primary", applying); err != nil {
		t.Fatal(err)
	}
	entry = registry2.Snapshot("primary")[0]
	if !entry.RetireDesired {
		t.Fatal("retiring from applying must set retire_desired: the syscall may have landed")
	}
}

func TestParkTransitionsNilThroughNil(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	prefix := reserveEntry(t, registry, "primary", "corp", "203.0.113.10/32", 5)
	park := SelectedParkIdentity{
		Kind:     SelectedParkUnreachable,
		Table:    ManagedSelectedRouteTable,
		Protocol: 4,
		Metric:   ManagedParkMetric,
	}
	if err := registry.MarkParkRetiring("primary", prefix); err == nil {
		t.Fatal("park transitions require a park transaction")
	}
	if err := registry.ReservePark("primary", prefix, park, false); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkParkActive("primary", prefix, park); err == nil {
		t.Fatal("park active from pending must be rejected")
	}
	if err := registry.MarkParkApplying("primary", prefix); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkParkActive("primary", prefix, park); err != nil {
		t.Fatal(err)
	}
	entry := registry.Snapshot("primary")[0]
	if entry.Park == nil || entry.Park.Owned == nil || *entry.Park.Owned != park {
		t.Fatalf("park active must record owned: %#v", entry.Park)
	}
	if err := registry.ClearPark("primary", prefix); err == nil {
		t.Fatal("ClearPark is illegal from ParkActive")
	}
	if err := registry.MarkParkRetiring("primary", prefix); err != nil {
		t.Fatal(err)
	}
	if err := registry.ClearPark("primary", prefix); err != nil {
		t.Fatal(err)
	}
	if entry := registry.Snapshot("primary")[0]; entry.Park != nil {
		t.Fatalf("park must be nil after ClearPark: %#v", entry.Park)
	}
}

func TestParkRetireDesiredSemantics(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	prefix := reserveEntry(t, registry, "primary", "corp", "203.0.113.10/32", 5)
	park := SelectedParkIdentity{
		Kind:     SelectedParkUnreachable,
		Table:    ManagedSelectedRouteTable,
		Protocol: 4,
		Metric:   ManagedParkMetric,
	}
	if err := registry.ReservePark("primary", prefix, park, false); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkParkRetiring("primary", prefix); err == nil {
		t.Fatal("MarkParkRetiring is illegal from ParkPending")
	}
	// From Pending the unstarted park state is cleared directly.
	if err := registry.ClearPark("primary", prefix); err != nil {
		t.Fatal(err)
	}

	if err := registry.ReservePark("primary", prefix, park, false); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkParkApplying("primary", prefix); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkParkRetiring("primary", prefix); err != nil {
		t.Fatal(err)
	}
	entry := registry.Snapshot("primary")[0]
	if !entry.Park.RetireDesired {
		t.Fatal("park retiring from applying must set retire_desired")
	}

	registry2 := NewMemorySelectedRegistry()
	prefix2 := reserveEntry(t, registry2, "primary", "corp", "203.0.113.12/32", 5)
	if err := registry2.ReservePark("primary", prefix2, park, false); err != nil {
		t.Fatal(err)
	}
	if err := registry2.MarkParkApplying("primary", prefix2); err != nil {
		t.Fatal(err)
	}
	if err := registry2.MarkParkActive("primary", prefix2, park); err != nil {
		t.Fatal(err)
	}
	if err := registry2.MarkParkRetiring("primary", prefix2); err != nil {
		t.Fatal(err)
	}
	if entry := registry2.Snapshot("primary")[0]; entry.Park.RetireDesired {
		t.Fatal("park retiring from active must not set retire_desired")
	}
}

func TestReserveParkIdentityChangeKeepsOwnedPending(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	prefix := reserveEntry(t, registry, "primary", "corp", "203.0.113.10/32", 5)
	park := SelectedParkIdentity{
		Kind:     SelectedParkLoopback,
		Table:    ManagedSelectedRouteTable,
		IfIndex:  1,
		NextHop:  "127.0.0.1",
		Protocol: 3, // Windows NetMgmt
		Metric:   ManagedParkMetric,
	}
	if err := registry.ReservePark("primary", prefix, park, false); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkParkApplying("primary", prefix); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkParkActive("primary", prefix, park); err != nil {
		t.Fatal(err)
	}
	// The loopback ifindex changed: a new Desired with forcePending keeps the
	// previous exact ownership while re-opening the transaction.
	changed := park
	changed.IfIndex = 9
	if err := registry.ReservePark("primary", prefix, changed, true); err != nil {
		t.Fatal(err)
	}
	entry := registry.Snapshot("primary")[0]
	if entry.Park.State != SelectedParkPending {
		t.Fatalf("identity change must reopen pending, got %q", entry.Park.State)
	}
	if entry.Park.Owned == nil || *entry.Park.Owned != park {
		t.Fatalf("previous ownership must be preserved: %#v", entry.Park)
	}
	if entry.Park.Desired != changed {
		t.Fatalf("desired must be updated: %#v", entry.Park)
	}
}

func TestInvalidSelectedTransitionsRejected(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	prefix := reserveEntry(t, registry, "primary", "corp", "203.0.113.10/32", 5)
	if err := registry.MarkRetiring("primary", prefix); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkApplying("primary", prefix); err == nil {
		t.Fatal("MarkApplying from retiring must be rejected")
	}
	if err := registry.MarkActive("primary", prefix, testIdentity(5)); err == nil {
		t.Fatal("MarkActive from retiring must be rejected")
	}
	if err := registry.MarkApplying("unknown", prefix); err == nil {
		t.Fatal("transitions on unregistered routes must be rejected")
	}
}

func TestIdentityValidation(t *testing.T) {
	prefix := testPrefix(t, "203.0.113.10/32")
	if err := (SelectedRouteIdentity{Table: 999, IfIndex: 5, Protocol: 4}).Validate(prefix); err == nil {
		t.Fatal("table must be 254")
	}
	if err := (SelectedRouteIdentity{Table: ManagedSelectedRouteTable, IfIndex: 0, Protocol: 4}).Validate(prefix); err == nil {
		t.Fatal("ifindex must be positive")
	}
	if err := (SelectedRouteIdentity{Table: ManagedSelectedRouteTable, IfIndex: 5, Protocol: 0}).Validate(prefix); err == nil {
		t.Fatal("protocol must be positive")
	}
	if err := (SelectedRouteIdentity{Table: ManagedSelectedRouteTable, IfIndex: 5, Protocol: 4, Scope: 7}).Validate(prefix); err == nil {
		t.Fatal("scope must be a valid Linux route scope")
	}
	if err := (SelectedRouteIdentity{Table: ManagedSelectedRouteTable, IfIndex: 5, Protocol: 4, NextHop: "not-an-ip"}).Validate(prefix); err == nil {
		t.Fatal("next hop must parse")
	}
	if err := (SelectedRouteIdentity{Table: ManagedSelectedRouteTable, IfIndex: 5, Protocol: 4, NextHop: "2001:db8::1"}).Validate(prefix); err == nil {
		t.Fatal("next hop must match the prefix family")
	}
	good := SelectedRouteIdentity{Table: ManagedSelectedRouteTable, IfIndex: 5, Protocol: 4, Scope: ScopeUniverse}
	if err := good.Validate(prefix); err != nil {
		t.Fatalf("valid identity rejected: %v", err)
	}
	other := good
	other.Metric = 1024
	if good.Equal(other) {
		t.Fatal("metric is part of exact identity")
	}
	other = good
	other.InterfaceLuid = 777
	if good.Equal(other) {
		t.Fatal("interface LUID is part of exact identity (§31.19)")
	}
}

func TestParkIdentityValidation(t *testing.T) {
	prefix := testPrefix(t, "203.0.113.10/32")
	unreachable := SelectedParkIdentity{
		Kind:     SelectedParkUnreachable,
		Table:    ManagedSelectedRouteTable,
		Protocol: 4,
		Metric:   ManagedParkMetric,
	}
	if err := unreachable.Validate(prefix); err != nil {
		t.Fatalf("valid unreachable park rejected: %v", err)
	}
	wrong := unreachable
	wrong.Metric = 100
	if err := wrong.Validate(prefix); err == nil {
		t.Fatal("park metric must be exact")
	}
	wrong = unreachable
	wrong.IfIndex = 3
	if err := wrong.Validate(prefix); err == nil {
		t.Fatal("unreachable park must have no interface")
	}
	loopback := SelectedParkIdentity{
		Kind:     SelectedParkLoopback,
		Table:    ManagedSelectedRouteTable,
		IfIndex:  1,
		NextHop:  "127.0.0.1",
		Protocol: 3,
		Metric:   ManagedParkMetric,
	}
	if err := loopback.Validate(prefix); err != nil {
		t.Fatalf("valid loopback park rejected: %v", err)
	}
	v6Prefix := testPrefix(t, "2001:db8:dead::10/128")
	if err := loopback.Validate(v6Prefix); err == nil {
		t.Fatal("v4 loopback park cannot protect a v6 prefix")
	}
	v6Loopback := loopback
	v6Loopback.NextHop = "::1"
	if err := v6Loopback.Validate(v6Prefix); err != nil {
		t.Fatalf("v6 loopback park rejected: %v", err)
	}
}

func TestSchema2CheckpointRoundTrip(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	prefix := reserveEntry(t, registry, "primary", "corp", "203.0.113.10/32", 5)
	if err := registry.MarkApplying("primary", prefix); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkActive("primary", prefix, testIdentity(5)); err != nil {
		t.Fatal(err)
	}
	park := SelectedParkIdentity{
		Kind:     SelectedParkUnreachable,
		Table:    ManagedSelectedRouteTable,
		Protocol: 4,
		Metric:   ManagedParkMetric,
	}
	if err := registry.ReservePark("primary", prefix, park, false); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkParkApplying("primary", prefix); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkParkActive("primary", prefix, park); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "selected-routes.json")
	checkpoint := SelectedRoutesCheckpoint{
		Schema:          selectedRoutesCheckpointSchema,
		Role:            "primary",
		RouteGeneration: 9,
		Entries:         registry.Snapshot("primary"),
		ActivationID:    "0123456789abcdef0123456789abcdef",
		ModeGeneration:  3,
		Platform:        CheckpointPlatformLinux,
	}
	if err := WriteSelectedRoutesCheckpoint(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadSelectedRoutesCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Entries) != 1 {
		t.Fatalf("roundtrip entry count: %d", len(loaded.Entries))
	}
	entry := loaded.Entries[0]
	if entry.Owned == nil || *entry.Owned != testIdentity(5) {
		t.Fatalf("owned identity must roundtrip: %#v", entry)
	}
	if entry.Park == nil || entry.Park.Owned == nil || *entry.Park.Owned != park {
		t.Fatalf("park transaction must roundtrip: %#v", entry.Park)
	}
	if loaded.ActivationID != checkpoint.ActivationID || loaded.ModeGeneration != 3 || loaded.Platform != CheckpointPlatformLinux {
		t.Fatalf("fencing fields must roundtrip: %#v", loaded)
	}
}

func TestSchema2RejectsCorruptAndSchema1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "selected-routes.json")

	if err := os.WriteFile(path, []byte("{\"schema\":2,\"role\":\"primary\""), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSelectedRoutesCheckpoint(path); err == nil {
		t.Fatal("truncated JSON must be rejected")
	}
	if err := os.WriteFile(path, []byte("{\"schema\":1,\"role\":\"primary\",\"entries\":[]}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadSelectedRoutesCheckpoint(path)
	if err == nil || !strings.Contains(err.Error(), "schema 1 is not supported") {
		t.Fatalf("schema 1 must decode only to an explicit unsupported error, got %v", err)
	}
	// A schema-2 checkpoint whose park claims Active without owned must fail
	// validation.
	park := SelectedParkIdentity{Kind: SelectedParkUnreachable, Table: 254, Protocol: 4, Metric: 100}
	broken := SelectedRoutesCheckpoint{
		Schema: 2, Role: "primary", RouteGeneration: 1,
		ActivationID:   "0123456789abcdef0123456789abcdef",
		ModeGeneration: 1,
		Entries: []SelectedRoute{{
			Role: "primary", Zone: "corp", Prefix: testPrefix(t, "203.0.113.10/32"),
			Source: SelectedDNS, ToadGeneration: 1, RouteGeneration: 1,
			Desired: testIdentity(5), State: SelectedPending,
			Park: &SelectedPark{State: SelectedParkActive, Desired: park},
		}},
		Platform: CheckpointPlatformLinux,
	}
	blob, _ := json.Marshal(broken)
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSelectedRoutesCheckpoint(path); err == nil {
		t.Fatal("invalid park/owned combination must fail validation")
	}
}

func TestValidateCheckpointRoleZoneAndPrefixRules(t *testing.T) {
	prefix := testPrefix(t, "203.0.113.10/32")
	entry := SelectedRoute{
		Role: "primary", Zone: "corp", Prefix: prefix,
		Source: SelectedDNS, ToadGeneration: 1, RouteGeneration: 2,
		Desired: testIdentity(5), State: SelectedPending,
	}
	checkpoint := SelectedRoutesCheckpoint{
		Schema: 2, Role: "primary", RouteGeneration: 2,
		ActivationID:   "0123456789abcdef0123456789abcdef",
		ModeGeneration: 1,
		Entries:        []SelectedRoute{entry},
		Platform:       CheckpointPlatformLinux,
	}
	if err := ValidateSelectedRoutesCheckpoint(checkpoint, "primary", "corp"); err != nil {
		t.Fatalf("valid checkpoint rejected: %v", err)
	}
	// The schema2 activation fence is mandatory (D7R §6.1): every valid
	// fixture carries a real 32-hex activation id and a positive generation.
	emptyActivation := checkpoint
	emptyActivation.ActivationID = ""
	if err := ValidateSelectedRoutesCheckpoint(emptyActivation, "primary", "corp"); err == nil {
		t.Fatal("an empty activation id must be rejected")
	}
	malformedActivation := checkpoint
	malformedActivation.ActivationID = "0123456789ABCDEF0123456789ABCDEF"
	if err := ValidateSelectedRoutesCheckpoint(malformedActivation, "primary", "corp"); err == nil {
		t.Fatal("an uppercase activation id must be rejected")
	}
	zeroGeneration := checkpoint
	zeroGeneration.ModeGeneration = 0
	if err := ValidateSelectedRoutesCheckpoint(zeroGeneration, "primary", "corp"); err == nil {
		t.Fatal("a zero mode generation must be rejected")
	}
	if err := ValidateSelectedRoutesCheckpoint(checkpoint, "other", "corp"); err == nil {
		t.Fatal("role mismatch must be rejected")
	}
	if err := ValidateSelectedRoutesCheckpoint(checkpoint, "primary", "other"); err == nil {
		t.Fatal("zone mismatch must be rejected")
	}
	if err := ValidateSelectedRoutesCheckpoint(checkpoint, "primary", ""); err != nil {
		t.Fatalf("empty expected zone skips the zone check: %v", err)
	}

	// DNS intents are host prefixes only; static may be wider.
	dns := checkpoint
	dns.Entries[0].Prefix = testPrefix(t, "203.0.113.0/24")
	if err := ValidateSelectedRoutesCheckpoint(dns, "primary", "corp"); err == nil {
		t.Fatal("dns entries must be host prefixes")
	}
	static := checkpoint
	static.Entries[0].Source = SelectedStatic
	static.Entries[0].Prefix = testPrefix(t, "203.0.113.0/24")
	if err := ValidateSelectedRoutesCheckpoint(static, "primary", "corp"); err != nil {
		t.Fatalf("static /24 must be accepted: %v", err)
	}
	static6 := static
	static6.Entries[0].Prefix = testPrefix(t, "2001:db8:ab00::/64")
	static6.Entries[0].Desired = SelectedRouteIdentity{Table: ManagedSelectedRouteTable, IfIndex: 5, Protocol: 4, Metric: 1024}
	if err := ValidateSelectedRoutesCheckpoint(static6, "primary", "corp"); err != nil {
		t.Fatalf("static /64 must be accepted: %v", err)
	}
}

func TestValidateCheckpointGenerationsAndActiveOwned(t *testing.T) {
	prefix := testPrefix(t, "203.0.113.10/32")
	mk := func(toadGen uint64) SelectedRoutesCheckpoint {
		return SelectedRoutesCheckpoint{
			Schema: 2, Role: "primary", RouteGeneration: 2,
			ActivationID:   "0123456789abcdef0123456789abcdef",
			ModeGeneration: 1,
			Entries: []SelectedRoute{{
				Role: "primary", Zone: "corp", Prefix: prefix,
				Source: SelectedDNS, ToadGeneration: toadGen, RouteGeneration: 2,
				Desired: testIdentity(5), State: SelectedActive, Owned: idPtr(testIdentity(5)),
			}},
			Platform: CheckpointPlatformLinux,
		}
	}
	if err := ValidateSelectedRoutesCheckpoint(mk(1), "primary", "corp"); err != nil {
		t.Fatalf("valid active entry rejected: %v", err)
	}
	if err := ValidateSelectedRoutesCheckpoint(mk(0), "primary", "corp"); err == nil {
		t.Fatal("toad generation must be positive for schema2 records")
	}
	missingOwned := mk(1)
	missingOwned.Entries[0].Owned = nil
	if err := ValidateSelectedRoutesCheckpoint(missingOwned, "primary", "corp"); err == nil {
		t.Fatal("active requires owned")
	}
	mismatched := mk(1)
	other := testIdentity(6)
	mismatched.Entries[0].Owned = &other
	if err := ValidateSelectedRoutesCheckpoint(mismatched, "primary", "corp"); err == nil {
		t.Fatal("active requires owned == desired")
	}
	nonRetiring := mk(1)
	nonRetiring.Entries[0].RetireDesired = true
	if err := ValidateSelectedRoutesCheckpoint(nonRetiring, "primary", "corp"); err == nil {
		t.Fatal("retire_desired must be false for non-retiring records")
	}
	dup := mk(1)
	dup.Entries = append(dup.Entries, dup.Entries[0])
	if err := ValidateSelectedRoutesCheckpoint(dup, "primary", "corp"); err == nil {
		t.Fatal("duplicate prefix must be rejected")
	}
}

func idPtr(identity SelectedRouteIdentity) *SelectedRouteIdentity {
	return &identity
}

func parkIdPtr(identity SelectedParkIdentity) *SelectedParkIdentity {
	return &identity
}

func TestRegistryGenerationChangeDemotesActiveKeepsOwned(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	prefix := reserveEntry(t, registry, "primary", "corp", "203.0.113.10/32", 5)
	if err := registry.MarkApplying("primary", prefix); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkActive("primary", prefix, testIdentity(5)); err != nil {
		t.Fatal(err)
	}
	// A target change reserves a new Desired: Active -> Pending, Owned kept.
	newDesired := testIdentity(9)
	if err := registry.Reserve("primary", "corp", prefix, SelectedDNS, 1, 2, "kk0", newDesired); err != nil {
		t.Fatal(err)
	}
	entry := registry.Snapshot("primary")[0]
	if entry.State != SelectedPending {
		t.Fatalf("generation change must demote to pending, got %q", entry.State)
	}
	if entry.Owned == nil || *entry.Owned != testIdentity(5) {
		t.Fatalf("old owned must be preserved: %#v", entry)
	}
	if entry.Desired != newDesired {
		t.Fatalf("desired must be updated: %#v", entry)
	}
}

func TestRestoreRoleAllOrNothing(t *testing.T) {
	registry := NewMemorySelectedRegistry()
	goodPrefix := reserveEntry(t, registry, "primary", "corp", "203.0.113.10/32", 5)
	if err := registry.MarkApplying("primary", goodPrefix); err != nil {
		t.Fatal(err)
	}
	before := registry.Snapshot("primary")

	bad := SelectedRoutesCheckpoint{
		Schema: 2, Role: "primary", RouteGeneration: 5,
		ActivationID:   "0123456789abcdef0123456789abcdef",
		ModeGeneration: 1,
		Entries: []SelectedRoute{
			before[0],
			{
				Role: "primary", Zone: "corp", Prefix: testPrefix(t, "203.0.113.11/32"),
				Source: SelectedDNS, ToadGeneration: 1, RouteGeneration: 5,
				Desired: SelectedRouteIdentity{Table: 999, IfIndex: 5, Protocol: 4},
				State:   SelectedPending,
			},
		},
		Platform: CheckpointPlatformLinux,
	}
	if err := registry.RestoreRole(bad); err == nil {
		t.Fatal("restore must reject an invalid entry")
	}
	after := registry.Snapshot("primary")
	if len(after) != 1 || after[0].Prefix != before[0].Prefix || after[0].RouteGeneration != before[0].RouteGeneration {
		t.Fatalf("failed restore must not mutate the map: %#v", after)
	}

	good := SelectedRoutesCheckpoint{
		Schema: 2, Role: "primary", RouteGeneration: 5,
		ActivationID:   "0123456789abcdef0123456789abcdef",
		ModeGeneration: 1,
		Entries:        []SelectedRoute{before[0]},
		Platform:       CheckpointPlatformLinux,
	}
	if err := registry.RestoreRole(good); err != nil {
		t.Fatal(err)
	}
	entry := registry.Snapshot("primary")[0]
	if entry.RouteGeneration != before[0].RouteGeneration {
		t.Fatalf("restored entry keeps its recorded generation: %#v", entry)
	}
}

func TestCheckpointPlatformValuesAreExact(t *testing.T) {
	// §31.26: checkpoints are not portable between OSes.
	entry := SelectedRoute{
		Role: "primary", Zone: "corp", Prefix: testPrefix(t, "203.0.113.10/32"),
		Source: SelectedDNS, ToadGeneration: 1, RouteGeneration: 1,
		Desired: testIdentity(5), State: SelectedPending,
	}
	for _, platform := range []string{"", "darwin", "freebsd"} {
		checkpoint := SelectedRoutesCheckpoint{
			Schema: 2, Role: "primary", RouteGeneration: 1,
			ActivationID:   "0123456789abcdef0123456789abcdef",
			ModeGeneration: 1,
			Entries:        []SelectedRoute{entry}, Platform: platform,
		}
		if err := ValidateSelectedRoutesCheckpoint(checkpoint, "primary", "corp"); err == nil {
			t.Fatalf("platform %q must be rejected", platform)
		}
	}
}
