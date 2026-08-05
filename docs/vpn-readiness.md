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

When a role changes from unpublished to published, `leshy-route-watch` therefore:

1. records the newly ready device in the route lifecycle snapshot;
2. restarts Leshy once to discard failed in-memory route state;
3. flushes the systemd-resolved cache so affected names are queried again.

The watcher stores its own active-device set under:

```text
/run/kikimora/leshy/route-watch/active.devices
```

That state is separate from the route-lifecycle cleanup directory. Restarting
Leshy therefore does not make the watcher rediscover the same device and enter a
restart loop.

Restarting the watcher itself while a device is already published also does not
restart Leshy again. A restart is triggered only by a real unpublished-to-
published transition.

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

Expected output for a newly ready interface includes one recovery cycle:

```text
secondary vpn0 validating -> stable 1/3
secondary vpn0 validating -> stable 2/3
secondary vpn0 ready
Interface ready: vpn0
VPN became ready; restarting Leshy to discard failed route state
Leshy restarted after VPN readiness transition
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
new Leshy restart.
