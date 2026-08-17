# VPN endpoint underlay routing

Kikimora forces the control/data endpoints used to establish the primary and
secondary VPN tunnels through the physical network instead of through either
managed VPN.

Endpoint discovery is pluggable. Each role of each VPN profile selects an
endpoint provider. The built-in providers are `static`, `happ` and `command`;
see [`endpoint-providers.md`](endpoint-providers.md) for the complete provider
API and extension guide.

## Static endpoints

The default provider is `static`. The installation package contains separate
endpoint templates:

```text
linux/files/endpoints/primary.txt
linux/files/endpoints/secondary.txt
```

The installer creates the corresponding user configuration files and preserves
existing ones on reinstall/upgrade:

```text
/etc/kikimora/leshy/endpoints/primary.txt
/etc/kikimora/leshy/endpoints/secondary.txt
```

The route watcher also recreates an empty template if an endpoint file is
unexpectedly missing.

Each non-comment line is one exact hostname or IP address. Matching is
intentionally exact: wildcards, CIDRs and parent-domain matching are not
supported.

Example:

```text
# /etc/kikimora/leshy/endpoints/secondary.txt
ve.ad-tech.ru
```

This remains exact even when normal domain routing contains a parent domain such
as:

```text
# /etc/kikimora/leshy/domains/secondary.txt
ad-tech.ru
```

In that case `ve.ad-tech.ru` is resolved and its IP addresses are forced through
the physical Wi-Fi/Ethernet underlay, while other hosts below `ad-tech.ru`
continue to use the secondary VPN.

The static endpoint files are shared by role, but provider **selection is part of
the profile**. An ordinary profile can remain `static/static`, while another
profile can assign `happ` only to the `tun0` role. Dynamically discovered Happ
endpoints do not need to be copied into the static endpoint files.

## Routing policy

On Linux Kikimora installs high-priority destination rules into routing table
`51890`. Primary and secondary endpoint rules use separate priorities, so each
role can be transitioned independently. The underlay route excludes both
currently managed VPN interfaces and uses the best remaining physical default
route. An unreachable fallback prevents a protected endpoint from falling
through into normal VPN routing while a physical path is temporarily
unavailable.

IPv4-mapped IPv6 resolver results such as `::ffff:46.243.227.103` are ignored.
They are synthetic representations of IPv4 answers rather than real AAAA
endpoint addresses and therefore do not create IPv6 rules or routes.

Providers only report endpoint specifications. They do not own `ip rule`, table
`51890`, pending state or route cleanup; those remain centralized in
`route-watch`.

## Lifecycle

Endpoint routing is active only while Kikimora is running.

`sudo kk start` applies or reconciles endpoint DIRECT policy before starting
Leshy and route-watch. `sudo kk enable --now` follows the same order. This
prevents a VPN started afterwards from accidentally building its transport
through the other VPN.

`sudo kk restart` keeps endpoint policy in place while restarting Kikimora
services. It does not remove and recreate endpoint routes underneath
already-connected VPN clients.

`sudo kk stop` removes endpoint policy after stopping Kikimora, but refuses
before changing anything if protected policy is present and either managed VPN
interface is still UP. Disconnect the VPN interfaces first. `sudo kk stop
--force` explicitly overrides this protection and removes the policy anyway.
`sudo kk disable --now` has the same safety check; `--force` is accepted only
together with `--now`.

A plain `sudo kk enable` only enables autostart. It does not start Kikimora and
therefore does not activate endpoint policy until `kk start`, `kk enable --now`,
or the next service start.

## Starting or reconfiguring while a VPN is already connected

For exact/static endpoint sets Kikimora never rewrites a conflicting endpoint
route underneath a live VPN merely to make the policy correct.

If a role's VPN interface is already UP and the desired endpoint path differs
from the currently applied exact policy, that role becomes `underlay-pending`.
If the role had already passed readiness and its runtime device file still
identifies the same live interface, Kikimora keeps that device file published so
Leshy can continue routing through the established tunnel until the safe
disconnect. A role that had not reached ready before the pending transition
remains unpublished.

Disconnect that VPN once. Route-watch sees the safe down transition, removes
normal readiness, installs the pending exact endpoint policy, clears pending,
and the next connection goes through the physical underlay. Normal link
readiness then starts again from zero.

Unchanged pending state is idempotent: route-watch does not rewrite the same
pending marker or repeat the transition log on every periodic reconcile.

The logic is symmetric and is independent of OpenConnect, WireGuard, Amnezia or
another fixed-endpoint client.

## Dynamic additive endpoints

Some VPN clients rotate transport servers without dropping the virtual
interface. The provider protocol therefore has a deliberately narrow
`dynamic-additive` capability.

A dynamic-additive provider promises that each newly emitted endpoint has
**already been observed as a live VPN transport on the selected physical
underlay**. While the role interface is UP, core may add protection for such a
new endpoint, but it will not remove old endpoint rules and will not rewrite
existing endpoints onto another physical default:

```text
live protected set := old protected set UNION newly observed endpoints
```

A physical-underlay change still becomes pending. Exact removal of stale dynamic
endpoints happens only when the managed VPN interface is down, which is the safe
boundary where removing the old transport protection cannot cut a live tunnel.

The built-in Happ provider uses this capability. It does not trust a subscription
list as proof of the active server. Instead it intersects Xray remote sockets
originating from the Happ TUN address with sing-box remote sockets originating
from the physical underlay. Only endpoints visible on both sides are emitted.
This lets Happ switch servers automatically while avoiding unrelated sing-box
DNS/direct sockets.

A generic `command` provider can also return `dynamic-additive`, but authors must
satisfy the same proof requirement. Merely having frequently changing
configuration is not sufficient.

## Provider failure

Provider discovery is fail-safe. A non-zero provider exit keeps the previous
endpoint policy rather than replacing it with partial or guessed data. For an
active role, Kikimora marks endpoint state pending so an unready replacement
interface cannot be published without endpoint protection.

## Shared-IP limitation

Routing happens by IP after DNS resolution. If a protected VPN endpoint hostname
shares an IP address with another service, all traffic to that IP is forced
through the physical underlay. This is an inherent limitation of IP routing and
should be considered when choosing static endpoint hostnames.
