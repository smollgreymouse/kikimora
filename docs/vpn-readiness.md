# VPN data-path readiness

Kikimora may start before either VPN client is connected. The normal boot path is:

```text
systemd starts Leshy and the watchers
        |
        v
the user connects the primary VPN
        |
        v
the user connects the secondary VPN
```

A VPN interface can exist, be marked `UP`, and have an IPv4 address before the
tunnel can actually carry traffic. Publishing that interface to Leshy too early
creates a race: routes are installed through a tunnel whose session, policy
rules, or NAT state is not ready yet.

## Readiness gate

`reconcile` now separates link readiness from data-path readiness.

For each VPN it checks:

1. the interface exists and has the `UP` flag;
2. the interface has an IPv4 address;
3. an HTTP probe succeeds while explicitly bound to that interface.

The default probe is:

```text
http://1.1.1.1/
```

The probe uses `curl --interface`, so the check is tied to the VPN data path
instead of the machine's ordinary default route.

A device file is not published to Leshy until two consecutive probes succeed:

```text
interface appears
        |
        v
probe 1 succeeds
        |
        v
probe 2 succeeds
        |
        v
primary.dev or secondary.dev is published
```

A published device is retained across two transient failures and withdrawn on
the third consecutive failure. A hard link-down or address loss removes the
device immediately.

## Leshy route-state recovery

Leshy 0.4 records an IPv4 route in its in-memory aggregator before the kernel
route operation has succeeded. When a DNS query arrives before the corresponding
VPN device file exists, the kernel route add fails but the route may remain
marked as installed inside Leshy. Later DNS queries for the same address then
produce no new route operation.

The reproduced sequence was:

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
subsequent DNS queries do not recreate the missing route
```

`leshy-route-watch` therefore performs one Leshy restart when a device changes
from unavailable to ready. The watcher keeps its own active-device state outside
the route-lifecycle cleanup directory, so this restart does not retrigger itself
in a loop. After the restart it flushes the system DNS cache so affected names
are resolved again against a clean Leshy route state.

A transient probe failure that does not withdraw the published device does not
restart Leshy. A restart is only triggered after a real unpublished-to-published
transition.

## Configuration

The following variables may be added to
`/etc/kikimora/leshy/vpn.conf`:

```bash
PRIMARY_PROBE_URL="http://1.1.1.1/"
SECONDARY_PROBE_URL="http://1.1.1.1/"

VPN_PROBE_TIMEOUT=4
VPN_READY_SUCCESSES=2
VPN_DOWN_FAILURES=3
```

An empty probe URL disables the data-path probe for that role and restores the
old link-and-address-only readiness behavior:

```bash
PRIMARY_PROBE_URL=""
```

A private or corporate VPN may use a more appropriate endpoint that is known to
be reachable only through that tunnel:

```bash
SECONDARY_PROBE_URL="http://172.25.41.151/"
```

The endpoint only needs to complete an HTTP request. Any HTTP status is accepted;
the check is about transport reachability, not page content.

## Runtime state

Readiness counters are kept under:

```text
/run/kikimora/leshy/vpn/readiness/
```

The route watcher keeps the last published device set under:

```text
/run/kikimora/leshy/route-watch/active.devices
```

These files are runtime-only and disappear on reboot. Leshy device files remain:

```text
/run/kikimora/leshy/vpn/primary.dev
/run/kikimora/leshy/vpn/secondary.dev
```

Their presence now means that the corresponding VPN passed the data-path gate,
not merely that the kernel interface exists.

## Manual validation

After boot, with both VPNs disconnected:

```bash
sudo systemctl start leshy.service
ls -la /run/kikimora/leshy/vpn/
```

Connect the primary VPN, wait a few seconds, and verify:

```bash
cat /run/kikimora/leshy/vpn/primary.dev
curl --interface amn0 --max-time 4 --head http://1.1.1.1/
```

Then connect the secondary VPN:

```bash
cat /run/kikimora/leshy/vpn/secondary.dev
curl --interface vpn0 --max-time 4 --head http://1.1.1.1/
```

The journal should contain one recovery restart for each actual readiness
transition:

```bash
sudo journalctl -u leshy-route-watch.service -u leshy.service --since boot
```

Expected watcher messages include:

```text
Interface ready: vpn0
VPN became ready; restarting Leshy to discard failed route state
Leshy restarted after VPN readiness transition
systemd-resolved DNS cache flushed after VPN readiness change
```

The device file must not appear while the bound probe fails. Disconnecting and
reconnecting a VPN must withdraw and republish only that VPN's device file. When
it is republished, the watcher automatically restarts Leshy once so routes that
failed before readiness are rebuilt.
