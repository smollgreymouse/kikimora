# Toad step 07C — underlay convergence, TUN drift repair, NetworkManager ownership and suspend/resume

Status: **automated implementation complete (handoff Phases 1-4 green)**. Real workstation suspend/resume remains an explicit manual/operator gate.

Reviewed implementation baseline: `2c0fa833177c49c60cd0c58291490e1a28a16f79`.

This packet implements the host-network convergence model motivated by the legacy Amnezia suspend incident. Host events are invalidations only; they do not directly mean “restart VPN”.

## Goal

After this packet:

- underlay identity includes the kernel-selected source address;
- bursty netlink changes settle into one canonical underlay generation;
- a periodic consistency audit runs even when netlink subscription is healthy;
- read-only API snapshots have no kernel/recovery side effects;
- a managed TUN with missing address/MTU is detected and repaired without recreating its ifindex when the protocol permits;
- NetworkManager is prevented from owning `kk-*` IP configuration;
- suspend/resume observation survives D-Bus reconnects;
- a resume causes convergence against current underlay state, not a one-shot `CONNECTED_GLOBAL -> restart`.

## 1. Refactor Linux netlink access behind a testable source

Current `platform/linux/netlink/snapshot.go` calls package-global netlink functions directly.

Introduce an internal interface in that package:

```go
type source interface {
    RouteListFiltered(family int, filter *netlink.Route, mask uint64) ([]netlink.Route, error)
    RouteGetWithOptions(destination net.IP, options *netlink.RouteGetOptions) ([]netlink.Route, error)
    LinkByIndex(index int) (netlink.Link, error)
    LinkByName(name string) (netlink.Link, error)
    AddrList(link netlink.Link, family int) ([]netlink.Addr, error)
}
```

Production implementation is a zero-size wrapper around vishvananda/netlink.

Change:

```go
type Snapshotter struct{}
```

to:

```go
type Snapshotter struct{ source source }
```

with a constructor/default helper choosing production source when nil.

Do not export the low-level interface outside the Linux package.

This is required so gateway/source ranking is unit-testable without host route mutation.

## 2. Derive PreferredSrc even when the default route has no Src

Current `snapshot.go:75-79` only copies `winner.route.Src`.

Add:

```go
func preferredSource(src source, route netlink.Route, link netlink.Link, family int) netip.Addr
```

Exact precedence:

1. if `route.Src` is valid for the family, use it;
2. if gateway is present, call `RouteGetWithOptions(gateway, &netlink.RouteGetOptions{OifIndex: route.LinkIndex})`;
   - use a returned route matching the winner LinkIndex;
   - if its Src is valid, use it;
3. list addresses on the winning link;
   - IPv4: choose first stable-sorted global-unicast IPv4;
   - IPv6: prefer global-unicast non-link-local; use link-local only if the route/gateway itself is link-local and no global address exists;
4. otherwise return invalid address.

Normalize/sort address candidates by prefix/address bytes so enumeration order does not create epoch churn.

Unit tests must cover:

- DHCP-style default `via gw dev wlan0` with empty route.Src;
- source changes while ifindex/gateway remain equal;
- IPv6 global vs link-local choice;
- no usable source.

## 3. Use netstate.Compare as the only underlay epoch authority

Current `control.Manager.refreshUnderlay` at lines 595-618 duplicates epoch logic and always sends `ChangeInterface`.

Replace it with a method that receives a freshly observed snapshot:

```go
func (m *Manager) acceptUnderlay(next netstate.Snapshot) (netstate.ChangeReason, bool) {
    m.mu.Lock()
    merged, reason, changed := netstate.Compare(m.underlay, next)
    if changed {
        m.underlay = merged
        m.bumpLocked()
    }
    m.mu.Unlock()

    if changed {
        m.product.SetUnderlay(merged, reason)
    }
    return reason, changed
}
```

Do not let both Manager and core independently invent different epochs. Pass the already-normalized snapshot into Controller.

Adjust `core.SetUnderlay` so if `next.Epoch` is nonzero it accepts that epoch and does not bump it again. The controller should validate monotonicity, not re-number an already canonical Manager snapshot.

Add tests for ChangeInitial, Interface, Gateway, PreferredSource, Availability.

## 4. Wire the existing coalescer instead of rescanning every raw event

Current `watchUnderlay` lines 648-672 calls `refreshUnderlay()` on every invalidation; the existing `netstate.Coalescer` is not used.

Refactor `netstate.Coalescer` first.

Current `Changed chan Snapshot` loses change reason and uses non-blocking send. Replace with a callback:

```go
type ChangeHandler func(Snapshot, ChangeReason) error

type Coalescer struct {
    Settle  time.Duration
    Maximum time.Duration
    Build   func(context.Context) (Snapshot, error)
    Changed ChangeHandler
}
```

In `publish`:

```go
merged, reason, changed := Compare(*current, next)
if !changed { return nil }
*current = merged
return c.Changed(merged, reason)
```

No lossy output channel.

Manager `watchUnderlay`:

- start platform watcher into `invalidations chan Invalidation`;
- start Coalescer with:
  - Settle = 250ms;
  - Maximum = 2s;
  - Build = `underlay.DefaultSnapshot`;
  - Changed = `m.acceptUnderlay` adapter;
- independently run a 30-second audit ticker that sends one synthetic invalidation `Source:"periodic-audit"`;
- if watcher exits, restart it with bounded exponential backoff; keep periodic audit alive meanwhile.

Dropping raw invalidations at the netlink watcher is acceptable after this because the audit closes the “last event dropped” hole.

Do not use one goroutine per event.

## 5. Make Snapshot() read-only

Current `control.Manager.Snapshot()`:

```go
m.refreshUnderlay()
m.refreshStates()
...
```

Remove both side effects.

New Snapshot:

```go
func (m *Manager) Snapshot() Snapshot {
    m.mu.Lock()
    defer m.mu.Unlock()
    return m.snapshotLocked()
}
```

Compatibility `state.json` polling, if still needed, belongs in its own monitor goroutine started once from `NewManager`, not inside API reads.

Add a test with fake underlay source/recovery driver:

- call Snapshot 100 times;
- zero underlay scans;
- zero recovery calls;
- revision unchanged.

This prevents UI polling from becoming a recovery trigger.

## 6. Add platform interface reconciler for local TUN drift

Step 07A makes addresses observable. This step repairs them.

Extend `toad/internal/platform/tun.go` with an optional interface:

```go
type InterfaceReconciler interface {
    Reconcile(TunnelSpec) error
}
```

Implement it on Linux `linuxTunnel`.

Reconcile semantics:

- look up by the Toad-owned `t.name`;
- verify ifindex is exactly `t.index`; if name exists with different ifindex, return a typed fatal ownership error;
- restore configured MTU;
- add any missing expected addresses;
- set link UP;
- do not delete extra addresses in this packet unless they are known Kikimora-owned;
- never close/reopen the TUN fd.

For the Toad-owned AWG tunnel this is the authoritative repair path.

### Xray

Xray’s TUN is created by the official Xray core, not `platform.Tunnel`.

Create a Linux platform helper:

```go
type ManagedInterfaceReconciler interface {
    ReconcileExisting(name string, ifindex int, mtu int, expected []netip.Prefix) error
}
```

Use it only after confirming the same ifindex. Restore MTU/expected configured gateway addresses and link UP.

### OpenConnect

OpenConnect address is negotiated, not static. Do not invent it.

If the previously observed negotiated address disappears:

- `RouteReady=false`;
- trigger Toad transport recovery/restart through capabilities;
- preserve interface identity if official OpenConnect can restore it;
- do not netlink-add a stale negotiated address.

**STOP/DESIGN:** if the OpenConnect child cannot restore its existing TUN address without recreating the interface during this drift scenario, capture exact behavior and write `07c-openconnect-address-recovery.md`. Do not copy a stale server-assigned address back manually.

## 7. Reconcile drift from the Toad health loop with bounded rate

Current health loop runs every 250ms.

Do not execute netlink repair every tick.

Add runtime fields:

```go
lastRepairAttempt time.Time
repairInterval    time.Duration // default 1s
```

After reading interface:

- compute structural readiness using 07A helper;
- if false and platform reconciler exists and repair interval elapsed:
  - attempt repair;
  - re-read interface;
- publish new state only after re-read.

On successful repair, ifindex must remain unchanged.

On failure, state becomes degraded/not route-ready with explicit reason; core will keep/enter fail-closed policy through 07B.

Tests with fake InterfaceReader/Reconciler:

- delete expected address -> one repair;
- repeated bad samples within interval -> no repair storm;
- repair restores route ready;
- changed ifindex -> fatal ownership mismatch, no blind mutation.

## 8. Replace shell dbus-monitor sleep source with native D-Bus

Current `platform/linux/logind/sleep.go` spawns `dbus-monitor`, and `Manager.watchSleep` returns forever if it exits.

Use native system D-Bus.

Add Go dependency:

`github.com/godbus/dbus/v5`

Use a version resolved once when implementing this packet and commit the resulting `go.mod/go.sum`; executor must not switch D-Bus libraries.

Implement `logind.Source.Watch`:

- connect to system bus;
- add match for:
  - sender `org.freedesktop.login1`;
  - interface `org.freedesktop.login1.Manager`;
  - member `PrepareForSleep`;
- subscribe to signals;
- parse exactly one boolean body item;
- emit `platform.SleepEvent{Preparing: value}`;
- on bus disconnect return a typed retryable error.

Manager `watchSleep` must supervise the source:

- reconnect with bounded backoff;
- context cancellation stops it;
- one watcher failure never permanently disables sleep detection.

Record watcher degraded/reconnected counters in diagnostics if a diagnostics struct is already available; do not introduce a logging framework solely for this.

## 9. Implement NetworkManager ownership exclusion for managed kk-* interfaces

Current `platform/linux/networkmanager/watcher.go` is a no-op stub.

Use the same godbus system connection.

Create a small manager, e.g.:

```go
type Manager struct {
    Interfaces func() []string
}
func (m Manager) EnsureUnmanaged(ctx context.Context) error
func (m Manager) Watch(ctx context.Context, invalidations chan<- string) error
```

Install a persistent NetworkManager rule with the Linux package:

```ini
# /etc/NetworkManager/conf.d/90-kikimora-unmanaged.conf
[keyfile]
unmanaged-devices=interface-name:kk-*
```

Package/install tests must verify that exact file is shipped. The `kk-*` prefix is reserved for Kikimora-managed interfaces.

For every configured managed Toad interface that exists:

1. call NetworkManager `GetDeviceByIpIface(name)`;
2. read `org.freedesktop.NetworkManager.Device.Managed`;
3. require false;
4. if it is true, call `org.freedesktop.NetworkManager.Device.SetManaged(0, flags)`.

Prefer the modern `SetManaged` method when the target NetworkManager supports it. Use runtime-only flags for immediate correction because the package config is the persistent authority. If the method is unavailable on an older target, fall back to the read/write `Managed` property, then read it back. Do not silently assume success.

Subscribe/re-run EnsureUnmanaged on:

- NetworkManager `DeviceAdded`;
- D-Bus `NameOwnerChanged` for `org.freedesktop.NetworkManager`;
- Toad interface appearance.

If NetworkManager is not installed/running, treat this as unsupported/not-required, not fatal.

If NetworkManager is running and refuses to relinquish a managed `kk-*` device, expose an explicit degraded ownership condition and block privileged production cutover in 07D.

Do not use `CONNECTED_GLOBAL` to trigger protocol recovery.

Do not manage external `vpn0`.

### Behavioral test

Where NetworkManager is available on a real Linux dev host:

```text
create/start kk-awg0
verify NM Managed=false
restart NetworkManager
verify Managed=false is re-applied
delete AWG address externally
verify Toad repairs address with same ifindex
```

Hermetic unit tests use a D-Bus facade/fake, not a real host NM.

## 10. Resume sequence

Refactor `Manager.watchSleep` around the architecture contract.

On `Preparing=true`:

- set suspended flag;
- do not stop any Toad;
- do not clear desired state;
- do not unpublish/remove stable TUN solely because suspend starts.

On `Preparing=false`:

1. clear suspended;
2. enqueue one underlay invalidation `resume`;
3. wait for coalesced canonical snapshot or bounded 5s underlay availability;
4. invalidate product validated epochs using `RequestResumeValidation`;
5. ensure NetworkManager ownership exclusion;
6. allow 07A scheduler to structurally validate each Toad;
7. if local TUN drift exists, repair it;
8. if protocol transport is stale, 07B recovery runs endpoint apply -> transport action -> validate -> publish -> restore parking;
9. repeated netlink/NM/sleep events only cause another reconcile pass.

Delete `validateEnabledRolesAfterResume` as a separate one-off pipeline once the unified scheduler/reconciler covers the same path.

The key acceptance condition is no state where a later useful event is ignored because the role is “already reconnecting”.

## 11. Tests

Add deterministic tests:

- netlink event burst -> one changed snapshot after settle;
- last raw invalidation dropped -> periodic audit still notices state change;
- source-address-only change -> new epoch/reason `source-address-changed`;
- Snapshot API read has no observation side effect;
- sleep D-Bus watcher reconnects after bus/source error;
- resume while recovery already in flight coalesces and eventually validates current epoch;
- NetworkManager re-owner attempt is corrected and package config contains the persistent `kk-*` unmanaged rule;
- AWG address removal repaired with same ifindex;
- Xray expected gateway-address removal repaired with same ifindex;
- OpenConnect missing negotiated address becomes not route-ready and requests transport recovery rather than static address injection.

## Real suspend/resume manual gate

Do not run this until all unit/netns gates pass.

Capture before suspend:

```text
core revision
underlay epoch/interface/gateway/source
each Toad PID/generation
each TUN ifindex/addresses/MTU
endpoint policies
parking state
Leshy publication
```

Suspend at least several minutes, resume, then require:

- same TUN ifindex for ordinary recoverable protocols;
- expected local addresses restored;
- endpoint underlay uses current physical interface/gateway/source;
- no `CONNECTED_GLOBAL -> unconditional stop/start`;
- current underlay epoch is the only validated epoch;
- selected traffic never escapes physical IPv4 or IPv6 while recovery incomplete;
- eventual session traffic recovers.

Archive diagnostics.

## Acceptance commands

```bash
cd toad
go test ./internal/netstate ./internal/platform/... ./internal/underlay ./internal/toadruntime ./internal/control ./internal/core
go test -race ./internal/netstate ./internal/toadruntime ./internal/control ./internal/core
go test ./...
cd ..
./linux/tests/toad/run-isolated.sh multi-toad
sudo bash linux/tests/toad/route-parking-netns.sh
```

Then the manual suspend/resume gate and full CI.

## Executor report

Return:

1. commits;
2. canonical underlay before/after examples;
3. source-address derivation tests;
4. coalescing/audit evidence;
5. no-side-effect Snapshot evidence;
6. AWG/Xray drift repair evidence with unchanged ifindex;
7. OpenConnect behavior;
8. NetworkManager Managed=false evidence;
9. sleep watcher reconnect evidence;
10. real suspend/resume diagnostic archive/result;
11. CI run IDs;
12. any STOP/DESIGN packet.
