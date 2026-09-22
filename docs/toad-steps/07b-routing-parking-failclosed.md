# Toad step 07B — route ownership, endpoint reconciliation and IPv4/IPv6 fail-closed parking

Status: **implemented/audited; current HEAD tests pass (handoff Phase 0 green)**. Do not redesign. Re-run formal acceptance from this packet if 07B-specific regression is suspected.

Reviewed implementation baseline: `2c0fa833177c49c60cd0c58291490e1a28a16f79`.

This packet makes Go routing ownership safe enough for a later cutover. It fixes the currently additive endpoint writer, broad parking ownership, IPv4-only parking and “Ready while still parked” recovery ordering.

## Goal

After this packet:

- every selected host route has explicit ownership;
- both IPv4 /32 and IPv6 /128 selected routes fail closed;
- endpoint policy is reconciled as a complete desired set, not only appended;
- stale endpoint rules cannot survive a generation change;
- recovery applies the new endpoint physical path before transport reset/rebind;
- a role cannot be product-Ready while its destinations remain parked;
- the legacy Leshy file bridge uses the configured zone, not an accidental role-name equality.

## 1. Add one canonical host-prefix predicate

Create in `toad/internal/routing/prefix.go`:

```go
func IsHostPrefix(prefix netip.Prefix) bool {
    return prefix.IsValid() &&
        prefix.Addr().IsValid() &&
        prefix.Bits() == prefix.Addr().BitLen()
}
```

Use it from parking and routing tests instead of hardcoded 32.

Modify:

- `toad/internal/parking/manager.go:64-70`;
- `toad/internal/parking/manager.go:101-110`;
- `toad/internal/parking/ownership.go:12-13`.

Required diff shape:

```diff
-if !prefix.IsValid() || prefix.Bits() != 32 {
+if !routing.IsHostPrefix(prefix) {
     continue
 }
```

and:

```diff
-route.Prefix.Bits() == 32
+routing.IsHostPrefix(route.Prefix)
```

Tests must cover IPv4 /32, IPv6 /128, and reject /24,/64/default.

## 2. Preserve route protocol ownership in Linux writes

File: `toad/internal/platform/linux/netlink/routes.go:126`.

Current route creation drops `Operation.Protocol`.

Change:

```diff
-route := netlink.Route{Table: op.Table, LinkIndex: op.IfIndex, Priority: int(op.Metric)}
+route := netlink.Route{
+    Table: op.Table,
+    LinkIndex: op.IfIndex,
+    Priority: int(op.Metric),
+    Protocol: netlink.RouteProtocol(op.Protocol),
+}
```

For operations with Protocol 0, leave kernel/default semantics as today by setting the field only when nonzero if the netlink library distinguishes zero. Write a unit-level operation conversion helper so this can be tested without mutating the host.

Refactor `applyOperation` into:

```go
func routeForOperation(op routing.Operation) (netlink.Route, error)
func ruleForOperation(op routing.Operation) (*netlink.Rule, error)
```

and keep syscalls in a thin layer.

## 3. Fix endpoint IPv4-mapped IPv6 filtering

File: `toad/internal/endpoint/resolver.go`.

Replace:

```go
if (a.Is4() && !a.Is4In6()) || a.Is6() {
    ...
}
```

with:

```go
if a.Is4In6() {
    continue
}
if a.Is4() || a.Is6() {
    out = append(out, netip.AddrPortFrom(a, port))
}
```

Add exact test using `::ffff:192.0.2.10`; it must not enter the returned candidate set.

## 3A. Normalize transport endpoints once and preserve hostname ports

Current endpoint parsing is inconsistent:

- `config.ConfiguredTransportEndpoints()` uses line-oriented `endpoint.ParseSpecs`;
- AWG2/Xray backend reporters hand-parse with `netip.ParseAddrPort`;
- OpenConnect reports the full gateway URL as a hostname.

Do not maintain three parsers.

### Add one config-level normalization function

File: `toad/internal/config/config.go`.

Add:

```go
func (c *Config) TransportEndpointSpecs() ([]endpoint.EndpointSpec, error)
```

It returns a complete normalized configured transport set before DNS resolution.

AWG2/VLESS:

- accept numeric `IP:port`;
- accept `hostname:port`;
- use protocol default only when port omitted;
- preserve `Network`, hostname and port separately.

OpenConnect:

Add a helper:

```go
func parseOpenConnectGateway(raw string) (host string, port uint16, err error)
```

Supported forms:

```text
ve.example
ve.example:4443
https://ve.example
https://ve.example:4443
https://ve.example:4443/optional/path
```

For URL form use `net/url`:

- scheme must be empty or `https`;
- userinfo is rejected;
- hostname must be non-empty;
- port must be 1..65535 when present;
- default port = 443;
- path/query may remain part of the original OpenConnect gateway passed to the official client, but are **not** part of the routing hostname.

For a bare endpoint use the same host/port parser as AWG2/Xray.

Do not rewrite `OpenConnectConfig.Gateway`; the official client still receives the original validated gateway string.

### Remove swallowed parse failure

Current `ConfiguredTransportEndpoints()` returns nil when parsing fails.

Change call sites to use the error-returning normalizer. Invalid configured transport targets must fail config/core startup or endpoint reconciliation explicitly; they must not become “provider returned no endpoints”.

If `ConfiguredTransportEndpoints()` must remain temporarily for API compatibility, make it a thin wrapper used only where an error cannot be returned and add a comment; all safety-critical paths use `TransportEndpointSpecs()`.

### Preserve hostname port in Toad live endpoint DTO

Files:

- `toad/internal/backend/backend.go`;
- `toad/internal/toadctl/protocol.go`.

Add:

```go
Port uint16 `json:"port,omitempty"`
```

to both `backend.TransportEndpoint` and `toadctl.TransportEndpoint`.

Update `toadruntime.refreshEndpoints` to copy Port.

AWG2/Xray/OpenConnect `TransportEndpoints()` must use the shared normalized spec instead of hand-parsing raw strings.

Mapping rule:

```go
if spec.Address.IsValid() {
    Address = spec.Address
    Port = uint16(spec.Address.Port())
} else {
    Hostname = spec.Hostname
    Port = spec.Port
}
```

Never place `host:port` or a URL in `Hostname`.

### Recovery config path

`recoveryDriver.ApplyEndpoint` must consume the same normalized specs/provider result. For the default configured source, a valid OpenConnect URL must resolve to its hostname and exact port.

### Required tests

Add config/backend tests for:

- AWG numeric `192.0.2.1:51820`;
- AWG hostname `awg.example:51820`;
- Xray hostname `xray.example:443`;
- OpenConnect bare hostname;
- OpenConnect hostname:4443;
- OpenConnect `https://host`;
- OpenConnect `https://host:4443/path`;
- URL userinfo rejected;
- invalid port rejected;
- reported Hostname never contains scheme or port;
- Port survives backend -> Toad snapshot conversion;
- OpenConnect default endpoint policy no longer returns an empty set.

## 4. Make endpoint rule ownership complete and idempotent

The current `ApplyEndpointPolicy` at `platform/linux/netlink/routes.go:71-93` only adds/replaces routes/rules. When a hostname/provider changes its candidate set, old rules can remain indefinitely.

The ownership discriminator already exists: every role must have a unique `EndpointPolicy.RulePriority` (validated in `config.ValidateEndpointPolicies`). Use that priority as the role-owned rule identity.

### Change platform contract

File: `toad/internal/platform/contracts.go`.

Replace:

```go
ApplyEndpointPolicy(context.Context, endpoint.Policy) error
```

with:

```go
ReconcileEndpointPolicy(context.Context, endpoint.Policy) error
RemoveEndpointPolicy(context.Context, endpoint.Policy) error
```

Do not keep both names after call sites migrate.

### Linux reconcile algorithm

In `platform/linux/netlink/routes.go`, implement under the existing kernel mutex:

1. validate desired policy;
2. snapshot current rules in IPv4 and IPv6;
3. find rules where:
   - `rule.Priority == p.Priority`;
   - `rule.Table == 51890`;
4. create desired host-prefix set from `p.Routes`;
5. install/refresh table 51890 unreachable defaults first;
6. install/replace every desired endpoint route in table 51890;
7. add/replace desired rules for each host prefix;
8. delete current owned rules with that priority whose prefix is no longer desired;
9. delete a table-51890 endpoint route only when no remaining endpoint rule from any Kikimora endpoint priority references that prefix.

Step 9 is intentionally global: two roles may resolve to the same endpoint IP. Do not delete a shared table route while another role rule still needs it.

### Ownership for route cleanup

The table route itself has no role identity, so do not guess role from metric.

Add a helper that snapshots **all** rules pointing at table 51890 and returns the referenced endpoint prefix set. Route deletion is allowed only for:

- non-default host routes in table 51890;
- not referenced by any table-51890 host rule after desired rule changes.

Do not delete unrelated non-host routes or the default unreachable sentinels.

### RemoveEndpointPolicy

Delete all rules with the role’s priority/table, then garbage-collect now-unreferenced host routes as above. Keep the table default unreachable sentinels while any Go endpoint owner is active; full product stop may remove them in 07D shutdown cleanup.

### Tests

Use a fake netlink boundary or extracted pure desired/current diff helper. Required cases:

- candidate {A,B} -> {B,C}: add C, delete A rule, retain B;
- shared A referenced by two priorities: removing one role retains route A;
- role removal deletes only its rules;
- both IPv4 and IPv6;
- no rule ever points to a table that lacks its unreachable default during transition;
- repeat same desired policy -> zero mutation.

## 5. Apply endpoint policy before every stale-underlay transport action

File: `toad/internal/core/recovery_runner.go:34-51`.

Current:

```go
ActionRebind:
  ObserveRoutes, Park, Withdraw, Quiesce, Rebind, ...
ActionRestartTransport:
  ObserveRoutes, Park, Withdraw, Quiesce, StartTransport, ...
```

Required:

```go
ActionRebind:
  ObserveRoutes
  Park
  Withdraw
  Quiesce
  ApplyEndpoint
  Rebind
  Validate
  Publish
  ResyncLeshy
  ObserveRestore

ActionRestartTransport:
  ObserveRoutes
  Park
  Withdraw
  Quiesce
  ApplyEndpoint
  StartTransport
  Validate
  Publish
  ResyncLeshy
  ObserveRestore
```

Do not put `ApplyEndpoint` after transport start.

For `ActionValidate`, do not apply endpoint policy if the underlay epoch is already the validated one.

Unit-test exact sequence arrays.

## 6. Recovery cannot finish Ready while parking is active

Current automatic callers use `includeRestoration=false`:

- `control.go:641`;
- `control.go:731`.

The current Engine then unconditionally sets RoleReady.

Remove the boolean escape hatch from the product recovery path.

Refactor:

```go
func RecoverySequenceForAction(action RecoveryAction) []RecoveryStep
func (e Engine) Recover(ctx context.Context, role string, operation, epoch uint64) error
```

Every action that invokes `Park` must end with `ObserveRestoration`.

`ActionValidate` can remain validate-only because it did not park.

In `recoveryDriver.ObserveRestoration`:

1. call `ObserveRestorationFromKernel`;
2. get parking state;
3. if `state.Active`, return a typed retryable error, e.g. `ErrRoutesStillParked`;
4. update product Parking resource state after every observation.

Do not silently return success while parks remain.

### Retry behavior

Parking restoration may require Leshy to reinstall its routes asynchronously.

Engine recovery should not spin internally.

At Manager level, when recovery returns `ErrRoutesStillParked`:

- keep RoleRecovering;
- bounded backoff;
- schedule another reconcile/recovery attempt;
- do not full-restart the Toad solely for this error.

Use existing supervisor backoff infrastructure or a dedicated role recovery timer; never sleep while holding manager/controller mutexes.

Add test:

```text
Park -> Publish -> Leshy resync -> no real route yet
=> RoleRecovering, Parking.Active=true, not Ready
later real route appears
=> ObserveRestore removes park -> RoleReady
```

## 7. Make parking ownership explicit instead of “all static host routes via ifindex”

Current `PrepareWithdrawalFromKernel` at `parking/manager.go:89-113` captures every RTPROT_STATIC host route via the Toad ifindex.

That can capture user routes.

### Add an owned-route registry

Create:

`toad/internal/routing/ownership.go`

```go
type SelectedRouteOwner struct {
    Role      string
    Interface string
    IfIndex   int
    Prefix    netip.Prefix
}

type OwnershipRegistry interface {
    Snapshot(role string) []SelectedRouteOwner
    Replace(role string, []SelectedRouteOwner)
    Remove(role string)
}
```

Implement an in-memory registry first, owned by the Go routing controller. Persist it in the parking checkpoint only for crash recovery; kernel discovery alone is not ownership proof.

Every future Leshy-selected route installation callback/API must update this registry. During current file-bridge migration, build the registry from a **baseline + observed delta**:

- capture baseline before role publication;
- after Leshy has installed selected routes, only routes newly appearing relative to baseline become observed-owned candidates;
- persist `Baseline/Observed/Parked` in the existing `parking.Checkpoint`.

Wire the existing checkpoint fields instead of leaving them decorative.

### Change parking entry point

Prefer:

```go
PrepareOwnedWithdrawal(ctx, role string, owned []routing.SelectedRouteOwner)
```

over discovering ownership by ifindex inside parking.Manager.

Keep a kernel read-back before parking to verify each owned route still exists with expected ifindex/prefix/protocol. Missing routes are skipped.

**STOP/DESIGN:** there is one legitimate architectural choice here: whether Leshy is changed in this PR to report selected-route ownership directly over an IPC/API, or whether Stage-1 migration continues with baseline+delta observation. The current code only has the file bridge, so this packet specifies baseline+delta as the implementable path. If executor finds an already-landed Leshy ownership API newer than this reviewed baseline, stop and write `07b-leshy-route-ownership-api.md` rather than maintaining two sources of truth.

## 8. Make Leshy compatibility publication zone-correct

Current `leshy.FileBridge` has a `Zone` in `RolePublication` but writes by `Role`:

```go
func (b FileBridge) path(role string) string { ... role+".dev" }
```

This only works for role IDs literally named primary/secondary.

Change Bridge contract to make zone explicit for all file operations:

```go
type Bridge interface {
    Publish(context.Context, RolePublication) error
    Withdraw(context.Context, zone string) error
    Resync(context.Context, zone string) error
}
```

File path:

```go
func (b FileBridge) path(zone string) string
```

`Publish` requires `Zone != ""` and writes `<zone>.dev`.

Recovery driver:

- `Withdraw`: derive `zone := r.cfg.EffectiveEndpointPolicy().Zone`;
- `Publish`: already has zone; keep product Publication state;
- `ResyncLeshy`: call by zone.

Tests:

- role ID `corp-vpn`, zone `secondary` writes `secondary.dev`, never `corp-vpn.dev`;
- primary/secondary compatibility unchanged;
- empty zone rejected.

This does not make Leshy N-role itself; it makes the compatibility bridge honest about its two legacy zones.

## 9. IPv6 fail-closed privileged test

Extend/add namespace gate, preferably:

`linux/tests/toad/route-parking-netns.sh`.

Add an IPv6 physical default and an IPv6 host selected route through a fake/managed TUN.

Required phase:

```text
selected 2001:db8:100::10/128 -> Toad
Toad route withdrawn
parking manager installs unreachable /128
physical IPv6 default remains available
curl/ping/route-get must not escape physical default
selected route restored
park removed only after winning selected route is observed
```

Also keep existing IPv4 test.

The test must inspect `ip -6 route get` and fail if physical underlay is selected.

## 10. Transaction failure safety test

Even with serialized reconciliation, inject failure after the first/second route operation in a fake Executor.

Required property:

- at no observable committed step does a selected destination lose both its selected route and its park/unreachable protection;
- endpoint table always retains unreachable default before endpoint rules are activated;
- retry of same transaction converges idempotently.

Do not require a magical kernel-wide atomic transaction; require a safe operation order and idempotent reconciliation.

## Acceptance commands

```bash
cd toad
go test ./internal/routing ./internal/parking ./internal/endpoint ./internal/leshy ./internal/control ./internal/core
go test -race ./internal/routing ./internal/parking ./internal/leshy ./internal/control
go test ./...
cd ..

sudo bash linux/tests/toad/route-parking-netns.sh
./linux/tests/toad/run-isolated.sh multi-toad
```

Then full PR CI.

## Completion report

Return:

1. commits;
2. endpoint desired/current reconciliation examples;
3. IPv4 and IPv6 parking evidence;
4. proof a user baseline route was not captured;
5. endpoint rebind order evidence;
6. proof RoleReady is impossible while parking Active;
7. zone mapping test;
8. multi-Toad regression result;
9. CI run IDs;
10. any STOP/DESIGN packet.
