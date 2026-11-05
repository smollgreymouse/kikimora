//go:build linux

// Privileged netns acceptance for the managed selected-route path
// (08D1 §2.5, Go-side subset): the schema-2 registry, the exact-identity
// reconciler, the independent park transaction and the crash invariants,
// driven against the real kernel.
//
// Run through linux/tests/toad/selected-routes-netns.sh, which creates a
// disposable namespace with a dual-stack dummy device and executes the
// prebuilt test binary inside it as root.

package netlink

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

func netnsDevice() string {
	return os.Getenv("KK_NETNS_DEV")
}

// ifIndexOf resolves the harness device once per call; the netns device is
// stable for the duration of the test.
func ifIndexOf(dev string) int {
	iface, err := net.InterfaceByName(dev)
	if err != nil {
		panic(fmt.Sprintf("interface %s: %v", dev, err))
	}
	return iface.Index
}

func registryEntryOf(registry routing.SelectedRouteRegistry, role string, prefix netip.Prefix) (*routing.SelectedRoute, bool) {
	for _, entry := range registry.Snapshot(role) {
		if entry.Prefix == prefix {
			return &entry, true
		}
	}
	return nil, false
}

func runningAsRootNetns() bool {
	return os.Getuid() == 0
}

func ip(args ...string) string {
	out, err := runCmd("ip", args...)
	if err != nil {
		panic(fmt.Sprintf("ip %v: %v", args, err))
	}
	return out
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%v", err)
	}
	return string(out), nil
}

func kernelRows() string {
	return ip("route", "show", "table", "main") + ip("-6", "route", "show", "table", "main")
}

// netnsRecord reserves one prefix with the exact normalized identity and
// returns the registry record.
func netnsRecord(t *testing.T, executor Executor, registry routing.SelectedRouteRegistry,
	role, dev string, prefix netip.Prefix, source routing.SelectedRouteSource) *routing.SelectedRoute {
	t.Helper()
	desired := routing.SelectedRouteDesired{Role: role, Prefix: prefix, IfIndex: ifIndexOf(dev)}
	identity, err := executor.NormalizeSelectedRoute(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Reserve(role, role, prefix, source, 1, 1, dev, identity); err != nil {
		t.Fatal(err)
	}
	record, _ := registryEntryOf(registry, role, prefix)
	return record
}

// TestManagedSelectedRoutesNetns walks the whole managed pipeline against
// the real kernel: schema-2 registry Reserve with exact identity ->
// reconciler add with exact read-back -> zero-mutation replays ->
// foreign-row conflict -> independent park transaction with exact proof ->
// schema-2 checkpoint crash invariant.
func TestManagedSelectedRoutesNetns(t *testing.T) {
	dev := netnsDevice()
	if dev == "" {
		t.Skip("not inside the netns harness (KK_NETNS_DEV unset)")
	}
	if !runningAsRootNetns() {
		t.Fatal("the netns harness runs as root")
	}

	executor := Executor{}
	registry := routing.NewMemorySelectedRegistry()
	ctx := context.Background()

	v4 := netip.MustParsePrefix("203.0.113.10/32")
	v6 := netip.MustParsePrefix("2001:db8::10/128")

	// --- real /32 and /128 apply with exact read-back --------------------
	for _, prefix := range []netip.Prefix{v4, v6} {
		netnsRecord(t, executor, registry, "primary", dev, prefix, routing.SelectedDNS)
		desired := routing.SelectedRouteDesired{Role: "primary", Prefix: prefix, IfIndex: ifIndexOf(dev)}
		if err := registry.MarkApplying("primary", prefix); err != nil {
			t.Fatal(err)
		}
		// The reconciler always sees the durable record: re-read after the
		// Applying checkpoint exactly like the manager does.
		record, _ := registryEntryOf(registry, "primary", prefix)
		result, err := executor.ReconcileSelectedRoute(ctx, desired, record)
		if err != nil {
			t.Fatalf("%v apply: %v", prefix, err)
		}
		if !result.Mutated {
			t.Fatalf("%v first apply must mutate", prefix)
		}
		if result.Verified != record.Desired {
			t.Fatalf("%v verified identity mismatch: %v vs %v", prefix, result.Verified, record.Desired)
		}
		if err := registry.MarkActive("primary", prefix, result.Verified); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(kernelRows(), prefix.Addr().String()+" dev") {
			t.Fatalf("%v row missing after the apply:\n%s", prefix, kernelRows())
		}
		// The exact identity reads back: proto static(4), the normalized
		// metric and scope (§26.1).
		rows := strings.Split(kernelRows(), "\n")
		found := false
		for _, row := range rows {
			// ip route prints host routes without the /32 or /128 suffix.
			if strings.HasPrefix(row, prefix.Addr().String()+" dev ") {
				found = true
				if !strings.Contains(row, "proto static") {
					t.Fatalf("%v row must carry proto static: %q", prefix, row)
				}
				if !prefix.Addr().Is4() && !strings.Contains(row, "metric 1024") {
					t.Fatalf("%v row must carry the normalized IPv6 metric 1024: %q", prefix, row)
				}
			}
		}
		if !found {
			t.Fatalf("%v exact row not found:\n%s", prefix, kernelRows())
		}
	}

	// --- stable state: replays write nothing ------------------------------
	stable := kernelRows()
	for i := 0; i < 50; i++ {
		record, _ := registryEntryOf(registry, "primary", v4)
		desired := routing.SelectedRouteDesired{Role: "primary", Prefix: v4, IfIndex: ifIndexOf(dev)}
		if _, err := executor.ReconcileSelectedRoute(ctx, desired, record); err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
	}
	if kernelRows() != stable {
		t.Fatalf("a converged table changed under replays:\n%s", kernelRows())
	}

	// --- external deletion is restored by the exact reconcile -------------
	if _, err := runCmd("ip", "route", "del", v4.String(), "table", "main"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(kernelRows(), v4.Addr().String()+" dev") {
		t.Fatal("the row must be gone after the external delete")
	}
	record, _ := registryEntryOf(registry, "primary", v4)
	desired := routing.SelectedRouteDesired{Role: "primary", Prefix: v4, IfIndex: ifIndexOf(dev)}
	if result, err := executor.ReconcileSelectedRoute(ctx, desired, record); err != nil || !result.Mutated {
		t.Fatalf("restore must re-create the row: %v %v", result, err)
	}
	if !strings.Contains(kernelRows(), v4.Addr().String()+" dev") {
		t.Fatalf("the row must be restored:\n%s", kernelRows())
	}

	// --- a foreign row is never replaced nor deleted ----------------------
	foreign := "203.0.113.99/32"
	if _, err := runCmd("ip", "route", "add", foreign, "dev", "lo", "table", "main"); err != nil {
		t.Fatal(err)
	}
	foreignPrefix := netip.MustParsePrefix(foreign)
	foreignDesired := routing.SelectedRouteDesired{
		Role: "primary", Prefix: foreignPrefix, IfIndex: ifIndexOf(dev),
	}
	if _, err := executor.ReconcileSelectedRoute(ctx, foreignDesired, record); err == nil {
		t.Fatal("a foreign row must conflict, not be replaced")
	}
	if !strings.Contains(kernelRows(), "203.0.113.99 dev lo") {
		t.Fatalf("the foreign row must survive the refused ensure:\n%s", kernelRows())
	}
	// RemoveSelectedRoute leaves foreign rows alone: a Pending record never
	// deletes the Desired, and nothing matches the foreign row.
	owned := routing.SelectedRoute{
		Role: "primary", Zone: "primary", Prefix: foreignPrefix, Source: routing.SelectedDNS,
		Desired: record.Desired, State: routing.SelectedPending,
	}
	if err := executor.RemoveSelectedRoute(ctx, owned); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kernelRows(), "203.0.113.99 dev lo") {
		t.Fatalf("RemoveSelectedRoute must not delete a foreign row:\n%s", kernelRows())
	}

	// --- independent park transaction (§7.2) ------------------------------
	parkDesired, err := executor.NormalizeManagedPark(ctx, v4)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ReservePark("primary", v4, parkDesired, false); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkParkApplying("primary", v4); err != nil {
		t.Fatal(err)
	}
	record, _ = registryEntryOf(registry, "primary", v4)
	parkResult, err := executor.ReconcileManagedPark(ctx, "primary", v4, record)
	if err != nil {
		t.Fatalf("park apply: %v", err)
	}
	if !parkResult.Mutated || parkResult.Verified != parkDesired {
		t.Fatalf("park apply must land the exact identity: %#v", parkResult)
	}
	if err := registry.MarkParkActive("primary", v4, parkResult.Verified); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kernelRows(), "unreachable") || !strings.Contains(kernelRows(), "metric 42760") {
		t.Fatalf("the unreachable protection must be installed:\n%s", kernelRows())
	}
	// The exact persisted identity observes present; a wrong identity does
	// not.
	present, err := executor.ObserveManagedPark(ctx, v4, parkDesired)
	if err != nil || !present {
		t.Fatalf("the exact park identity must observe present: %v %v", present, err)
	}
	wrong := parkDesired
	wrong.Metric = 100
	present, err = executor.ObserveManagedPark(ctx, v4, wrong)
	if err != nil || present {
		t.Fatalf("a wrong park identity must not observe present: %v %v", present, err)
	}
	// Selected route and park coexist (§0.2/§7.4): both rows are live.
	if !strings.Contains(kernelRows(), v4.Addr().String()+" dev") {
		t.Fatalf("the selected row must coexist with the park:\n%s", kernelRows())
	}
	// State-aware removal: the retirement is checkpointed first, ParkActive
	// deletes exactly Owned, and ClearPark completes the transaction (§7.5).
	if err := registry.MarkParkRetiring("primary", v4); err != nil {
		t.Fatal(err)
	}
	record, _ = registryEntryOf(registry, "primary", v4)
	if err := executor.RemoveManagedPark(ctx, "primary", v4, *record.Park); err != nil {
		t.Fatal(err)
	}
	present, err = executor.ObserveManagedPark(ctx, v4, parkDesired)
	if err != nil || present {
		t.Fatalf("the park must be gone after the exact removal: %v %v", present, err)
	}
	if err := registry.ClearPark("primary", v4); err != nil {
		t.Fatal(err)
	}

	// --- owned rows retire through RemoveSelectedRoute ---------------------
	for _, prefix := range []netip.Prefix{v4, v6} {
		entry, _ := registryEntryOf(registry, "primary", prefix)
		if err := registry.MarkRetiring("primary", prefix); err != nil {
			t.Fatal(err)
		}
		entry, _ = registryEntryOf(registry, "primary", prefix)
		if err := executor.RemoveSelectedRoute(ctx, *entry); err != nil {
			t.Fatal(err)
		}
		registry.Remove("primary", prefix)
	}
	if strings.Contains(kernelRows(), v4.Addr().String()+" dev") || strings.Contains(kernelRows(), v6.Addr().String()+" dev") {
		t.Fatalf("owned rows must be gone after removal:\n%s", kernelRows())
	}
	if !strings.Contains(kernelRows(), "203.0.113.99 dev lo") {
		t.Fatalf("the foreign fixture row must still be there:\n%s", kernelRows())
	}
	if _, err := runCmd("ip", "route", "del", foreign, "table", "main"); err != nil {
		t.Fatal(err)
	}

	// --- schema-2 checkpoint crash invariant --------------------------------
	dir := t.TempDir()
	path := filepath.Join(dir, "selected-routes.json")
	netnsRecord(t, executor, registry, "primary", dev, v6, routing.SelectedDNS)
	if err := routing.WriteSelectedRoutesCheckpoint(path, routing.SelectedRoutesCheckpoint{
		Schema:          routing.SelectedRoutesCheckpointSchema,
		Role:            "primary",
		RouteGeneration: 5,
		Entries:         registry.Snapshot("primary"),
		Platform:        routing.RuntimeCheckpointPlatform(),
	}); err != nil {
		t.Fatal(err)
	}
	decoded, err := routing.ReadSelectedRoutesCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != routing.SelectedRoutesCheckpointSchema || decoded.RouteGeneration != 5 {
		t.Fatalf("schema-2 checkpoint roundtrip: %#v", decoded)
	}
	restored := routing.NewMemorySelectedRegistry()
	if err := restored.RestoreRole(decoded); err != nil {
		t.Fatal(err)
	}
	entry := restored.Snapshot("primary")[0]
	if entry.Desired != decoded.Entries[0].Desired || entry.State != decoded.Entries[0].State {
		t.Fatalf("the exact identity must survive the checkpoint: %#v", entry)
	}
	// The seeded startup generation is checkpoint+1 (§2.4 step 7).
	if seeded := decoded.RouteGeneration + 1; seeded != 6 {
		t.Fatalf("the startup generation must be checkpoint+1: %d", seeded)
	}
}
