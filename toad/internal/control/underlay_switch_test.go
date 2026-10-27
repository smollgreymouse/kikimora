package control

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

// TestMaterialUnderlayChangeRevalidatesReadyRoleOnNewEpoch pins the live-network-switch
// user path (2026-10-26 audit): the operator's Wi-Fi gives way to another network while a
// role is Ready. The material underlay epoch advances, the role revalidates against the
// new epoch through its live control socket and returns to Ready on that epoch — without
// a transport restart (same process identity).
func TestMaterialUnderlayChangeRevalidatesReadyRoleOnNewEpoch(t *testing.T) {
	dir, err := os.MkdirTemp("", "usw")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	manager, err := NewManager([]string{writeConfig(t, dir, "one", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorCancel()
	defer manager.Close()

	// A live Toad control endpoint answering healthy Validate calls, like an OpenConnect
	// client owning its TUN across the switch.
	socket := filepath.Join(dir, "one", "control.sock")
	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()
	go func() {
		_ = (toadctl.Server{Socket: socket, Handler: validateServingToad{generation: 10}}).Serve(serverCtx)
	}()
	deadline := time.Now().Add(testWaitBudget)
	for {
		if conn, err := os.Stat(socket); err == nil && conn.Size() >= 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("toad control socket not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}

	process := &fakeProcess{done: make(chan error, 1)}
	manager.mu.Lock()
	manager.roles["one"].enabled = true
	manager.roles["one"].process = process
	manager.roles["one"].controlSocket = socket
	manager.mu.Unlock()
	if err := manager.product.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}
	manager.applyUnderlayChange(netstate.Change{
		Snapshot: netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}},
		Reason:   netstate.ChangeInitial,
	})

	ready := toadctl.Snapshot{
		Generation:    10,
		Revision:      1,
		State:         "online",
		RouteReady:    true,
		InterfaceName: "kkone",
		IfIndex:       7,
		MTU:           1380,
		Addresses:     []string{"10.80.0.253/24"},
	}
	if live, accepted := manager.observeToadSnapshotForProcess("one", process, ready); !live || !accepted {
		t.Fatalf("ready snapshot rejected: live=%v accepted=%v", live, accepted)
	}
	token, ok := manager.product.BeginValidation("one")
	if !ok {
		t.Fatal("ready role could not begin validation")
	}
	if !manager.product.CompleteValidation(token, toadctl.ValidationResult{Healthy: true, State: "ready"}) {
		t.Fatal("ready role validation did not commit")
	}
	if role, _ := manager.product.Role("one"); role.ValidatedEpoch != 1 {
		t.Fatalf("premise: role validated on epoch %d, want 1", role.ValidatedEpoch)
	}

	// The switch: a material underlay change (new interface identity) bumps the epoch.
	manager.applyUnderlayChange(netstate.Change{
		Snapshot: netstate.Snapshot{
			Epoch: 2,
			IPv4:  &netstate.Path{Family: 4, IfIndex: 9, Interface: "wlan1", Gateway: netip.MustParseAddr("192.168.43.1"), PreferredSrc: netip.MustParseAddr("192.168.43.20")},
		},
		Reason: netstate.ChangeInterface,
	})

	// The pipeline revalidates on epoch 2 and restores Ready without touching the
	// process. The validation answer travels over the real toadctl socket.
	deadline = time.Now().Add(testWaitBudget)
	for time.Now().Before(deadline) {
		role, ok := manager.product.Role("one")
		if ok && role.State == core.RoleReady && role.ValidatedEpoch == 2 {
			manager.mu.Lock()
			sameProcess := manager.roles["one"].process == process
			manager.mu.Unlock()
			if !sameProcess {
				t.Fatal("revalidation restarted the transport process")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	role, _ := manager.product.Role("one")
	t.Fatalf("role did not revalidate on the new epoch: state=%q validated_epoch=%d", role.State, role.ValidatedEpoch)
}
