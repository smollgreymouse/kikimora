# VPN link readiness and Leshy route recovery

Kikimora may start before either VPN client is connected. The normal workstation
sequence is:

```text
systemd starts Leshy and the watchers
        |
        v
the user connects the primary VPN
        |
        v
the user connects the secondary VPN
```

Two independent startup races matter here:

1. a VPN interface may appear and receive an IPv4 address while its client is
   still finishing setup;
2. Leshy 0.4 can remember a route as processed even when the kernel route add
   failed because the corresponding device file did not exist yet.

## Link stabilization

`reconcile` checks only structural tunnel state:

1. the interface exists;
2. the interface has the `UP` flag;
3. the interface has an IPv4 address.

The state must remain valid for several consecutive reconcile cycles before the
role device file is published. The default is three cycles:

```bash
VPN_LINK_READY_SUCCESSES=3
```

With the default one-second route-watch interval, publication normally occurs
after roughly three seconds of stable link state.

A published device is removed immediately when the interface disappears, loses
the `UP` flag, or loses its IPv4 address. When it returns, it must pass the
stabilization window again.

## DNS upstream routing

Leshy upstream DNS packets are not bound to `amn0` or `vpn0`. They follow normal
kernel route selection, matching the working Kikimora 1.1.0 behavior.

This is separate from the routes that Leshy creates for resolved application
addresses. For example, a query may leave through the kernel-selected network,
while the resulting GitLab address receives a `/32` route through `vpn0`.

## No mandatory URL polling

Kikimora does not use a public HTTP endpoint as a mandatory readiness check and
does not continuously poll a URL in the background.

A corporate VPN may correctly provide access only to private networks while
blocking public destinations such as `1.1.1.1:80`. Treating that public endpoint
as mandatory would incorrectly withdraw a working VPN. Domain lists also cannot
safely provide an automatic HTTP probe: a listed suffix does not guarantee that
its root name serves HTTP, supports `HEAD`, or is continuously available.

After publication, background monitoring remains limited to interface and IPv4
state. This is intentionally not a full application-level or tunnel-throughput
health monitor.

Kikimora also does not signal or reconnect third-party VPN clients. Recovery
signals such as OpenConnect `SIGUSR2` remain an operator action, not an automatic
Kikimora behavior.

## Leshy route-state recovery

Leshy 0.4 records an IPv4 route in its in-memory aggregator before the kernel
operation has succeeded. The reproduced sequence was:

```text
Leshy starts without VPNs
        |
        v
a secondary domain resolves
        |
        v
route add fails because secondary.dev is absent
        |
        v
vpn0 becomes ready
        |
        v
later DNS queries do not recreate the missing route
```

When a role changes from unpublished to published, `leshy-route-watch` therefore
restarts the Leshy process once to discard failed in-memory route state.

This recovery restart is deliberately different from an ordinary administrator
restart. Before requesting it, the watcher creates:

```text
/run/kikimora/leshy/recovery-restart
```

While that marker exists, the `leshy.service` drop-in:

1. keeps the existing route-lifecycle baseline;
2. skips route cleanup on stop;
3. keeps the current `systemd-resolved` integration active;
4. starts the new Leshy process;
5. removes the marker after successful start hooks.

This avoids tearing down DNS integration and deleting large sets of host routes
at the same moment that a VPN client is completing its own connection setup.

An ordinary `kk stop`, `kk restart`, or direct systemd stop/restart does not
create the marker and retains the normal DNS suspension, route cleanup, snapshot,
and resume behavior.

After the process-only recovery restart, the watcher flushes the
`systemd-resolved` cache so affected names are queried again.

The watcher stores its own active-device set under:

```text
/run/kikimora/leshy/route-watch/active.devices
```

That state is separate from the route-lifecycle cleanup directory. Restarting
the watcher itself while a device is already published does not initiate another
recovery restart.

## Runtime state

Link-stability counters are stored under:

```text
/run/kikimora/leshy/vpn/readiness/
```

Device files remain:

```text
/run/kikimora/leshy/vpn/primary.dev
/run/kikimora/leshy/vpn/secondary.dev
```

Their presence means that the interface remained structurally ready throughout
the configured stabilization window.

## Manual validation

Boot with both VPNs disconnected and start Kikimora. Then connect the primary
VPN and the secondary VPN manually.

Watch transitions:

```bash
sudo journalctl -f \
  -u leshy-route-watch.service \
  -u leshy.service
```

Expected output for a newly ready interface includes one process-only recovery
cycle:

```text
secondary vpn0 validating -> stable 1/3
secondary vpn0 validating -> stable 2/3
secondary vpn0 ready
Interface ready: vpn0
VPN became ready; restarting only the Leshy process to discard failed route state
leshy-recovery: DNS suspend skipped for process-only restart
leshy-recovery: route cleanup skipped for process-only restart
leshy-recovery: preserving existing route baseline
leshy-recovery: keeping system DNS integration active
leshy-recovery: process-only restart completed
systemd-resolved DNS cache flushed after VPN readiness change
```

Verify the published device and a secondary-domain route:

```bash
cat /run/kikimora/leshy/vpn/secondary.dev

D='gitlab.sca.ad-tech.ru'
IP="$(dig @127.0.0.1 -p 53053 "$D" A +short |
      grep -m1 -E '^[0-9.]+$')"

ip route get "$IP"
curl -4I --connect-timeout 10 --max-time 20 "https://$D/"
```

Disconnecting `vpn0` must remove `secondary.dev` immediately. Reconnecting it
must publish the file after the stabilization window and produce exactly one
new process-only Leshy recovery restart.
