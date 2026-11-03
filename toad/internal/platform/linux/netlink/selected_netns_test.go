//go:build linux

// Privileged netns acceptance for the managed selected-route path
// (08D §16, Go-side subset): the explicit registry, the platform
// reconciler and the registered parking driven against the real kernel.
//
// Run through linux/tests/toad/selected-routes-netns.sh, which creates a
// disposable namespace with a dual-stack dummy device and executes the
// prebuilt test binary inside it as root.

package netlink

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

func netnsDevice() string {
	return os.Getenv("KK_NETNS_DEV")
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

// TestManagedSelectedRoutesNetns walks the whole managed pipeline against
// the real kernel: registry Reserve -> reconciler add with exact read-back
// -> zero-mutation replays -> foreign-row conflict -> registered parking
// with exact restoration proof -> checkpoint crash invariant.
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
		if err := registry.Reserve("primary", "primary", prefix, routing.SelectedDNS, 1, 1, dev, ifIndexOf(dev)); err != nil {
			t.Fatal(err)
		}
		desired := routing.SelectedRouteDesired{
			Role: "primary", Prefix: prefix, IfIndex: ifIndexOf(dev), Protocol: 4,
		}
		record, _ := registryEntryOf(registry, "primary", prefix)
		if err := executor.ReconcileSelectedRoute(ctx, desired, record); err != nil {
			t.Fatalf("%v apply: %v", prefix, err)
		}
		if !strings.Contains(kernelRows(), prefix.Addr().String()+" dev") {
			t.Fatalf("%v row missing after the apply:\n%s", prefix, kernelRows())
		}
	}

	// --- stable state: replays write nothing ------------------------------
	stable := kernelRows()
	for i := 0; i < 50; i++ {
		desired := routing.SelectedRouteDesired{
			Role: "primary", Prefix: v4, IfIndex: ifIndexOf(dev), Protocol: 4,
		}
		record, _ := registryEntryOf(registry, "primary", v4)
		if err := executor.ReconcileSelectedRoute(ctx, desired, record); err != nil {
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
	desired := routing.SelectedRouteDesired{Role: "primary", Prefix: v4, IfIndex: ifIndexOf(dev), Protocol: 4}
	record, _ := registryEntryOf(registry, "primary", v4)
	if err := executor.ReconcileSelectedRoute(ctx, desired, record); err != nil {
		t.Fatalf("restore: %v", err)
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
		Role: "primary", Prefix: foreignPrefix, IfIndex: ifIndexOf(dev), Protocol: 4,
	}
	if err := executor.ReconcileSelectedRoute(ctx, foreignDesired, record); err == nil {
		t.Fatal("a foreign row must conflict, not be replaced")
	}
	if !strings.Contains(kernelRows(), "203.0.113.99 dev lo") {
		t.Fatalf("the foreign row must survive the refused ensure:\n%s", kernelRows())
	}
	// RemoveSelectedRoute leaves foreign rows alone.
	owned := routing.SelectedRoute{Role: "primary", Zone: "primary", Prefix: foreignPrefix, Source: routing.SelectedDNS, IfIndex: ifIndexOf(dev)}
	if err := executor.RemoveSelectedRoute(ctx, owned); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kernelRows(), "203.0.113.99 dev lo") {
		t.Fatalf("RemoveSelectedRoute must not delete a foreign row:\n%s", kernelRows())
	}

	// --- registered parking with exact restoration proof -------------------
	parks := parking.NewManager(executor)
	owned4 := routing.SelectedRoute{
		Role: "primary", Zone: "primary", Prefix: v4, Source: routing.SelectedDNS,
		Interface: dev, IfIndex: ifIndexOf(dev), State: routing.SelectedActive,
	}
	parked, err := parks.PrepareRegisteredWithdrawal(ctx, "primary", []routing.SelectedRoute{owned4})
	if err != nil {
		t.Fatal(err)
	}
	if len(parked) != 1 {
		t.Fatalf("the registered destination must park: %#v", parked)
	}
	if !strings.Contains(kernelRows(), "unreachable") || !strings.Contains(kernelRows(), "metric 42760") {
		t.Fatalf("the unreachable protection must be installed:\n%s", kernelRows())
	}
	// A wrong-interface lower-metric row does not release the park.
	if _, err := runCmd("ip", "route", "replace", v4.String(), "dev", "lo", "table", "main"); err != nil {
		t.Fatal(err)
	}
	released, err := parks.RegisteredRestoration(ctx, "primary", []routing.SelectedRoute{owned4})
	if err != nil || released != 0 {
		t.Fatalf("a physical-interface row must never release a park: %d %v", released, err)
	}
	// The exact expected row on the recorded interface releases it.
	if _, err := runCmd("ip", "route", "replace", v4.String(), "dev", dev, "table", "main"); err != nil {
		t.Fatal(err)
	}
	released, err = parks.RegisteredRestoration(ctx, "primary", []routing.SelectedRoute{owned4})
	if err != nil || released != 1 {
		t.Fatalf("the exact expected row releases the park: %d %v", released, err)
	}

	// --- owned rows retire through RemoveSelectedRoute ---------------------
	for _, prefix := range []netip.Prefix{v4, v6} {
		owned := routing.SelectedRoute{Role: "primary", Zone: "primary", Prefix: prefix, Source: routing.SelectedDNS, IfIndex: ifIndexOf(dev)}
		if err := executor.RemoveSelectedRoute(ctx, owned); err != nil {
			t.Fatal(err)
		}
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

	// --- checkpoint crash invariant ----------------------------------------
	dir := t.TempDir()
	path := filepath.Join(dir, "selected-routes.json")
	if err := registry.Reserve("primary", "primary", v6, routing.SelectedDNS, 1, 1, dev, ifIndexOf(dev)); err != nil {
		t.Fatal(err)
	}
	if err := routing.WriteSelectedRoutesCheckpoint(path, routing.SelectedRoutesCheckpoint{
		Schema: 1, Role: "primary", RouteGeneration: 1,
		Entries: registry.Snapshot("primary"),
	}); err != nil {
		t.Fatal(err)
	}
	decoded, err := routing.ReadSelectedRoutesCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	restored := routing.NewMemorySelectedRegistry()
	for _, entry := range decoded.Entries {
		if err := restored.Reserve(entry.Role, entry.Zone, entry.Prefix, entry.Source,
			entry.ToadGeneration, entry.RouteGeneration, entry.Interface, entry.IfIndex); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := restored.Snapshot("primary")
	for _, entry := range snapshot {
		if entry.State != routing.SelectedPending {
			t.Fatalf("restored ownership must be pending, never claimed active: %#v", snapshot)
		}
	}
}

func ifIndexOf(dev string) int {
	out, err := runCmd("ip", "link", "show", "dev", dev)
	if err != nil {
		panic(err)
	}
	// "3: dummy0: ..." -> 3
	for i := 0; i < len(out); i++ {
		if out[i] == ':' {
			index := 0
			for _, c := range out[:i] {
				if c < '0' || c > '9' {
					continue
				}
				index = index*10 + int(c-'0')
			}
			if index > 0 {
				return index
			}
		}
	}
	panic("no ifindex for " + dev)
}

func registryEntryOf(registry routing.SelectedRouteRegistry, role string, prefix netip.Prefix) (*routing.SelectedRoute, bool) {
	for _, entry := range registry.Snapshot(role) {
		if entry.Prefix == prefix {
			return &entry, true
		}
	}
	return nil, false
}
