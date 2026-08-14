# Fail-closed parking for Leshy routes

Kikimora keeps IPv4 destinations that were routed through a managed VPN from
silently falling back to another default route when that VPN becomes
unavailable.

Without parking, a Leshy-created route such as:

```text
203.0.113.10/32 dev vpn0 proto static
```

would disappear together with `vpn0`. The ordinary main-table default route
could then make the same destination reachable directly or through the other
VPN. Route parking changes that failure mode to fail closed.

## Lifecycle

While a managed VPN role is published as ready, `leshy-route-watch` asks
`route-lifecycle` to observe its current IPv4 `proto static` `/32` routes. The
startup/begin-device baseline is subtracted so only routes that appeared after
Kikimora began managing that device are treated as Leshy-owned.

For example:

```text
VPN ready:
    203.0.113.10/32 dev vpn0 proto static

VPN unavailable:
    unreachable 203.0.113.10/32 proto static metric 42760
```

The high-metric unreachable host route remains more specific than every normal
default route, so traffic cannot leak through Wi-Fi, Ethernet, primary VPN, or
another default path while `vpn0` is unavailable.

A route present in the lifecycle baseline is never parked. This preserves the
existing ownership rule: Kikimora only cleans up or parks routes that appeared
under its Leshy lifecycle rather than pre-existing VPN/user routes.

## Hard interface disappearance

A VPN device may disappear before cleanup runs, and Linux removes its routes at
the same time. Therefore cleanup cannot rely on querying the failed interface.

The watcher records the last observed Leshy-owned route set before each
readiness reconcile. If the device is already gone, `cleanup-device` keeps that
last observation and parks those destinations.

If a route disappears normally while the VPN remains healthy, a later
observation removes it from the ownership set. Such an expired route is not
resurrected as a stale park when the VPN eventually goes down.

## Recovery

Parking is not removed merely because the VPN link becomes ready again. That
would create a short direct/default-route window before Leshy recreated the
real route.

Instead, the unreachable fallback uses metric `42760`. Linux can keep it beside
a normal lower-metric `/32` restored by Leshy. The normal VPN route wins. The
next route observation confirms the real route exists and only then removes the
parked fallback.

This also works across the automatic Leshy restart used to rebuild Leshy's
in-memory route state after VPN readiness returns. `route-lifecycle
prepare-restart` marks that service stop as internal, so `ExecStopPost` removes
live Leshy routes but preserves parked fallbacks. The next lifecycle snapshot
clears the restart marker while keeping the parks until restored routes are
observed.

`sudo kk restart` uses the same preservation handshake. An ordinary `sudo kk
stop` or `sudo kk disable --now` clears parking because Kikimora is no longer
responsible for fail-closed routing.

## Runtime state

The route lifecycle state is stored under:

```text
/run/kikimora/leshy/route-lifecycle/
```

Important files are:

```text
before.routes     pre-existing /32 baseline per managed device
owned.routes      last observed Leshy-owned /32 routes
parked.routes     fail-closed unreachable destinations and their source device
restart.pending   temporary marker used only across an internal restart
```

The default parking metric is `42760` and can be overridden for testing or
special installations with:

```bash
KIKIMORA_ROUTE_PARKING_METRIC=42760
```

## Scope and endpoint underlay

Parking currently applies only to IPv4 host routes (`/32`) with `proto static`
on managed VPN devices, matching the routes already owned by the existing
route-lifecycle cleanup logic.

VPN server endpoint underlay routes are separate. Endpoint destinations in
routing table `51890` intentionally remain reachable through the physical
underlay so a VPN can establish or re-establish its transport. They are not
route-parking candidates.

## Observation interval

Ownership is learned from periodic route observations. With the default
one-second route-watch interval, a Leshy route must be observed at least once
before a hard device disappearance in order to be parked. Routes that existed
before the lifecycle baseline are intentionally excluded.
