# Kikimora Documentation

## 1. Introduction

Kikimora is a Linux routing and DNS orchestration project designed for systems
where more than one VPN must be active at the same time.

Its main use case is domain-aware split routing:

- selected domains use a primary VPN;
- all remaining traffic uses a secondary VPN;
- DNS remains local and is handled by Leshy;
- system DNS integration is repaired automatically after VPN reconnects.

Kikimora does not replace the VPN clients. It coordinates routing and DNS around
the interfaces created by those clients.

## 2. Reference deployment

The reference deployment uses the following interface names:

```text
Primary VPN:     amn0
Secondary VPN:   vpn0
DNS interface:   leshy-dns0
```

The reference DNS endpoint is:

```text
127.0.0.1:53053
```

The default routing zone is:

```text
secondary
```

The main configuration directory is:

```text
/etc/kikimora/leshy/
```

These values may differ in another installation. The project scripts and
configuration must agree on the selected names and paths.

## 3. Design goals

Kikimora is designed around the following goals:

1. Keep DNS classification and Linux routing separate.
2. Make DNS integration observable and repairable.
3. Do not trust stale marker files as proof of runtime health.
4. Recover from VPN disconnects and reconnects automatically.
5. Fail safely when Leshy becomes unavailable.
6. Keep system-specific resolver knowledge in one component.
7. Provide clear systemd logs for every recovery action.
8. Avoid requiring a full service restart for recoverable runtime damage.

## 4. Non-goals

Kikimora is not:

- a VPN implementation;
- a firewall;
- a replacement for nftables;
- a general-purpose network namespace manager;
- a DNS privacy product by itself;
- a guarantee against leaks caused by unrelated privileged software;
- compatible with VPN kill switches that reject policy-routed traffic.

## 5. High-level architecture

```text
+--------------------------+
| User applications        |
+------------+-------------+
             |
             | DNS lookup
             v
+--------------------------+
| systemd-resolved         |
+------------+-------------+
             |
             | routing domain ~.
             v
+--------------------------+
| leshy-dns0               |
| DNS: 127.0.0.1:53053     |
+------------+-------------+
             |
             v
+--------------------------+
| Leshy                    |
| domain classification    |
+------------+-------------+
             |
      +------+------+
      |             |
      v             v
 primary zone    secondary zone
      |             |
      v             v
    amn0           vpn0
```

The resolver sends DNS queries to Leshy. Leshy classifies the queried domain.
The routing layer ensures that traffic for the resolved destination uses the
correct VPN.

## 6. Component model

### 6.1 Leshy

Leshy is the local DNS service.

Responsibilities:

- listen on the configured local DNS address and port;
- classify domains into routing zones;
- provide DNS responses used by applications;
- remain independent from `systemd-resolved` lifecycle details.

Leshy does not own the global resolver configuration. That responsibility
belongs to `leshy-dns`.

### 6.2 `leshy-dns`

`leshy-dns` is the authoritative owner of runtime DNS integration.

Responsibilities:

- create or validate `leshy-dns0`;
- assign the local Leshy DNS endpoint;
- assign routing domain `~.`;
- configure resolver default-route behavior;
- remove conflicting routing domains from other links when repairing;
- expose a stable command-line interface;
- restore or suspend DNS integration safely.

Public commands:

```text
status
check
enable
disable
resume
suspend
```

The separation is intentional: other components must not duplicate
`systemd-resolved` logic.

### 6.3 Route Watch

Route Watch monitors the availability and state of the VPN interfaces.

Responsibilities:

- detect when `amn0` or `vpn0` appears;
- detect when an interface disappears;
- trigger route reconstruction;
- keep route state synchronized with available VPNs.

Route Watch does not decide whether DNS integration is valid.

### 6.4 Health Watch

Health Watch coordinates service recovery.

Responsibilities:

- check whether Leshy answers locally;
- call `leshy-dns check`;
- call `leshy-dns resume` when resolver integration is lost;
- call `leshy-dns suspend` when Leshy is unavailable;
- log all recovery actions.

Health Watch treats `leshy-dns` as a public API.

### 6.5 `kk`

`kk` is the operator-facing command.

Typical commands:

```bash
kk status
kk start
kk stop
kk restart
```

Its exact behavior depends on the installed script, but it should provide a
single entry point for day-to-day operation.

## 7. DNS integration model

### 7.1 Why use a dedicated interface

Kikimora uses `leshy-dns0` instead of attaching all resolver state directly to
the loopback interface.

Benefits:

- clear ownership in `resolvectl`;
- isolated lifecycle;
- explicit routing-domain assignment;
- easier health validation;
- reduced risk of interfering with unrelated loopback services;
- deterministic repair behavior.

### 7.2 The routing domain `~.`

In `systemd-resolved`, `~.` is the catch-all routing domain.

Assigning it to `leshy-dns0` means that DNS queries are routed through the Leshy
link unless a more specific resolver route exists.

Healthy state:

```text
Link (...) (leshy-dns0)
    DNS Server: 127.0.0.1:53053
    DNS Domain: ~.
```

### 7.3 Resolver ownership conflict

A VPN client may reconnect and assign:

```text
DNS Domain: ~.
Default Route: yes
```

to its own interface.

When this happens, Leshy may still be running and answering on localhost, but
normal applications bypass it because `systemd-resolved` selects the VPN link.

This is why a direct `dig` health check is not enough.

## 8. Runtime health model

Kikimora distinguishes two states:

### 8.1 Service health

Question:

```text
Does Leshy answer on its local DNS socket?
```

Typical check:

```bash
dig @127.0.0.1 -p 53053 . NS
```

### 8.2 Integration health

Question:

```text
Does the operating system still route DNS through Leshy?
```

Public check:

```bash
sudo /usr/local/sbin/leshy-dns check
```

Expected exit status:

```text
0 = healthy
1 = broken
```

The check verifies runtime state rather than persistent marker files.

## 9. Runtime validation

A valid resolver state requires all of the following:

1. `leshy-dns0` exists.
2. The expected DNS server is configured.
3. Routing domain `~.` is assigned.
4. The expected default-route property is configured.

If any of these conditions is false, the integration is considered broken.

This is critical because an interface can exist while its resolver properties
have been partially removed by another program.

## 10. Runtime repair

When the interface exists but the resolver configuration is incomplete,
`leshy-dns resume` performs a repair.

Conceptual sequence:

```text
validate interface
      |
      v
clear conflicting resolver domains
      |
      v
assign Leshy DNS server
      |
      v
assign ~. to leshy-dns0
      |
      v
restore default-route policy
      |
      v
validate final state
```

The repair path avoids unnecessary interface recreation.

## 11. Why marker files are insufficient

A marker file only records that a previous command completed.

It cannot prove that:

- the interface still exists;
- another VPN client did not remove `~.`;
- DNS server assignment still exists;
- `systemd-resolved` still considers the link active;
- the default-route property is still correct.

Therefore:

```text
marker exists != runtime is healthy
```

Kikimora uses runtime inspection as the source of truth.

## 12. Routing model

### 12.1 Primary zone

Domains listed in the primary domain set use the primary VPN interface.

Reference interface:

```text
amn0
```

Typical examples:

```text
openai.com
chatgpt.com
github.com
githubusercontent.com
```

### 12.2 Secondary zone

The secondary zone uses:

```text
vpn0
```

With:

```text
DEFAULT_ZONE=secondary
```

all traffic not matched by a more specific rule uses the secondary VPN.

### 12.3 Bypass zone

A bypass list may be used for destinations that should avoid both VPNs.

Exact bypass behavior depends on the local route-generation logic.

## 13. Configuration layout

```text
/etc/kikimora/
└── leshy/
    ├── config.yml
    ├── routing.conf
    ├── vpn.conf
    ├── primary.txt
    ├── secondary.txt
    └── bypass.txt
```

### 13.1 `config.yml`

Stores the main service and routing configuration.

The exact schema belongs to the installed Leshy version and Kikimora scripts.

### 13.2 `primary.txt`

One domain per line for the primary VPN.

Recommended format:

```text
openai.com
chatgpt.com
github.com
api.github.com
```

Do not write:

```text
https://github.com/path
```

### 13.3 `secondary.txt`

Explicit secondary domain assignments.

When the default zone is secondary, this file may contain only exceptions that
must be documented separately or may be unused, depending on implementation.

### 13.4 `bypass.txt`

Domains excluded from normal VPN routing.

Use carefully because bypass routes may expose traffic to the ordinary network.

### 13.5 `routing.conf`

Controls the default routing zone for traffic not matched by any domain list.

```text
DEFAULT_ZONE=secondary
```

Valid values: `secondary`, `primary`, `direct`, `none`.

### 13.6 `vpn.conf`

Maps each VPN role to a physical network interface. Created during installation
and read by all Kikimora components at runtime.

```bash
PRIMARY_INTERFACE="tun0"
SECONDARY_INTERFACE="tun1"
```

The values are set via `--primary-interface` and `--secondary-interface` flags
during installation:

```bash
sudo ./install.sh --primary-interface tun0 --secondary-interface tun1
```

After installation, you can change them by editing this file directly or by
re-running `kk install` with the desired flags.

## 14. Startup lifecycle

Typical boot sequence:

```text
systemd starts VPN services or clients
                |
                v
VPN interfaces become available
                |
                v
Route Watch builds routes
                |
                v
Leshy starts listening
                |
                v
leshy-dns enables resolver integration
                |
                v
Health Watch begins supervision
```

The actual order should be expressed through systemd dependencies whenever
possible instead of relying only on sleep delays.

## 15. VPN disconnect lifecycle

Example: primary VPN disconnects.

```text
amn0 disappears
       |
       v
Route Watch detects missing interface
       |
       v
routes using amn0 are withdrawn or rebuilt
       |
       v
status reports primary VPN as missing
```

Expected status:

```text
Primary     amn0         missing
Secondary   vpn0         ready
DNS         leshy-dns0   active
```

DNS may remain active because Leshy and the secondary VPN are still available.

## 16. VPN reconnect lifecycle

```text
amn0 appears again
       |
       v
Route Watch detects interface
       |
       v
primary routes are rebuilt
       |
       v
VPN client may overwrite DNS
       |
       v
Health Watch detects integration loss
       |
       v
leshy-dns resume repairs resolver state
```

No Leshy restart is required.

## 17. Leshy failure lifecycle

```text
Leshy stops answering
       |
       v
Health Watch fails local DNS probe
       |
       v
leshy-dns suspend
       |
       v
system resolver is released
       |
       v
normal DNS fallback becomes possible
```

This avoids leaving applications permanently pointed at an unavailable local
DNS service.

## 18. Recovery after AmneziaVPN reconnect

Observed failure mode:

1. AmneziaVPN disconnects.
2. AmneziaVPN reconnects.
3. `amn0` receives DNS servers.
4. `amn0` receives `DNS Domain: ~.`.
5. `leshy-dns0` remains up but loses `~.`.
6. DNS queries bypass Leshy.

Observed broken resolver state:

```text
Link (amn0)
    DNS Servers: 1.1.1.1 1.0.0.1
    DNS Domain: ~.
    Default Route: yes

Link (leshy-dns0)
    DNS Server: 127.0.0.1:53053
```

Health Watch recovery:

```text
Leshy answers locally
        |
        v
leshy-dns check fails
        |
        v
log: system DNS integration is lost
        |
        v
leshy-dns resume
        |
        v
clear routing domains from competing links
        |
        v
restore ~. on leshy-dns0
```

Final expected state:

```text
Link (leshy-dns0): ~.
Link (amn0):
```

## 19. AmneziaVPN Kill Switch incompatibility

AmneziaVPN Kill Switch may install nftables rules that reject traffic outside
its own expected routing model.

This produces a misleading symptom:

- routes are correct;
- the kernel selects the expected VPN interface;
- DNS works;
- HTTPS or other traffic still fails.

The blocking layer is nftables, not route selection.

Operational requirement:

```text
Disable AmneziaVPN Kill Switch.
```

Kikimora performs routing itself and cannot override an unrelated privileged
firewall rule that rejects the resulting packets.

## 20. Commands reference

### 20.1 `leshy-dns status`

Displays the current integration state.

Use for human inspection.

### 20.2 `leshy-dns check`

Silent health check for automation.

Example:

```bash
if /usr/local/sbin/leshy-dns check; then
    logger -t kikimora "DNS integration healthy"
else
    logger -t kikimora "DNS integration broken"
fi
```

### 20.3 `leshy-dns enable`

Creates the integration for the first time.

Typical actions:

- ensure interface exists;
- assign DNS endpoint;
- set routing domain;
- set default-route behavior;
- create persistent marker if used.

### 20.4 `leshy-dns disable`

Removes the integration.

Use when uninstalling or permanently disabling Kikimora DNS management.

### 20.5 `leshy-dns resume`

Restores runtime integration.

Use after:

- VPN reconnect;
- resolver configuration change;
- manual domain removal;
- transient `systemd-resolved` restart.

### 20.6 `leshy-dns suspend`

Temporarily releases resolver integration without necessarily deleting all
persistent project state.

Use when Leshy cannot answer.

## 21. Manual verification procedure

### Step 1: Verify interfaces

```bash
ip link show amn0
ip link show vpn0
ip link show leshy-dns0
```

### Step 2: Verify Leshy service

```bash
systemctl status leshy.service
```

### Step 3: Verify Leshy directly

```bash
dig @127.0.0.1 -p 53053 example.com
```

### Step 4: Verify resolver state

```bash
resolvectl status leshy-dns0
resolvectl domain leshy-dns0
```

Expected routing domain:

```text
~.
```

### Step 5: Verify public health API

```bash
sudo /usr/local/sbin/leshy-dns check
echo $?
```

Expected:

```text
0
```

### Step 6: Verify application lookup path

```bash
resolvectl query example.com
```

Expected resolver link:

```text
leshy-dns0
```

### Step 7: Verify routes

Use the actual resolved destination address:

```bash
ip route get <destination-ip>
```

Confirm that the expected interface is selected.

### Step 8: Verify firewall behavior

```bash
sudo nft list ruleset
```

Look for kill-switch or reject chains if routes are correct but traffic fails.

## 22. Recovery test procedure

The following procedure intentionally breaks resolver integration.

### Break the routing domain

```bash
sudo resolvectl domain leshy-dns0 ''
```

### Confirm failure

```bash
sudo /usr/local/sbin/leshy-dns check
echo $?
```

Expected:

```text
1
```

### Trigger supervision

```bash
sudo kk restart
```

### Verify automatic repair

```bash
sudo /usr/local/sbin/leshy-dns check
resolvectl domain leshy-dns0
```

Expected:

```text
0
Link (...) (leshy-dns0): ~.
```

### Inspect logs

```bash
journalctl -u leshy-health-watch.service -n 50
```

Expected sequence:

```text
Leshy responds, but system DNS integration is lost
DNS integration recovery requested
DNS integration restored
```

Exact wording depends on the installed localization.

## 23. Logging

Recommended service logs:

```bash
journalctl -u leshy.service
journalctl -u leshy-route-watch.service
journalctl -u leshy-health-watch.service
```

Follow all related logs:

```bash
journalctl \
  -u leshy.service \
  -u leshy-route-watch.service \
  -u leshy-health-watch.service \
  -f
```

Logs should distinguish:

- service failure;
- resolver integration failure;
- repair attempt;
- successful repair;
- suspension;
- route rebuild.

## 24. Troubleshooting matrix

| Symptom | Likely cause | Verification | Recovery |
|---|---|---|---|
| `leshy-dns0` missing | DNS integration not enabled | `ip link show leshy-dns0` | `leshy-dns enable` |
| `leshy-dns check` returns 1 | Resolver state incomplete | `resolvectl status` | `leshy-dns resume` |
| DNS goes through `amn0` | VPN owns `~.` | `resolvectl query` | `leshy-dns resume` |
| Leshy direct query fails | Leshy service unavailable | `dig @127.0.0.1 -p 53053` | restart Leshy |
| Correct route, blocked HTTPS | VPN kill switch/nftables | `nft list ruleset` | disable conflicting kill switch |
| Primary interface missing | VPN disconnected | `ip link show amn0` | reconnect primary VPN |
| Routes not restored | Route Watch inactive | `systemctl status ...route-watch...` | restart Route Watch |
| DNS repair not automatic | Health Watch inactive | `systemctl status ...health-watch...` | restart/enable Health Watch |

## 25. Systemd integration guidance

Service units should use:

- explicit `After=` dependencies for required services;
- `Wants=` or `Requires=` where appropriate;
- automatic restart for persistent daemons;
- reasonable restart delays;
- journald logging;
- restricted privileges where compatible with network operations.

Avoid relying entirely on fixed sleep durations because VPN startup time varies.

## 26. Security considerations

Kikimora modifies:

- routes;
- resolver state;
- systemd services;
- privileged network interfaces.

Protect the following locations from unprivileged writes:

```text
/usr/local/sbin/
/etc/kikimora/
/etc/systemd/system/
```

Domain lists are security-sensitive because they decide which tunnel receives
traffic.

Before accepting configuration changes:

- inspect added domains;
- confirm whether subdomains are included;
- verify bypass entries;
- test both DNS and route selection;
- inspect logs after reconnect tests.

## 27. Privacy considerations

Kikimora controls where DNS and application traffic are routed, but it does not
guarantee privacy by itself.

Privacy depends on:

- VPN provider behavior;
- DNS behavior inside Leshy;
- bypass configuration;
- browser features such as DNS-over-HTTPS;
- application-level proxy settings;
- firewall configuration;
- IPv6 routing;
- VPN kill-switch rules.

Applications using their own encrypted DNS may bypass `systemd-resolved`.

## 28. IPv6 considerations

The reference troubleshooting described here focuses primarily on IPv4.

If IPv6 is enabled, verify:

- IPv6 routes for each VPN;
- DNS AAAA responses;
- policy routing for IPv6;
- firewall behavior;
- leak behavior when one VPN lacks IPv6 support.

Do not assume that correct IPv4 routing implies correct IPv6 routing.

## 29. Browser DNS-over-HTTPS

A browser using built-in DNS-over-HTTPS may bypass `systemd-resolved` and Leshy.

For deterministic domain routing, disable application-level secure DNS or
configure it to use the system resolver.

## 30. Upgrade procedure

Recommended upgrade sequence:

1. Review `CHANGELOG.md`.
2. Back up `/etc/kikimora/`.
3. Stop Kikimora services.
4. Replace scripts and unit files.
5. Run `systemctl daemon-reload`.
6. Start Leshy.
7. Start Route Watch.
8. Start Health Watch.
9. Run `kk status`.
10. Run `leshy-dns check`.
11. Test a primary and secondary domain.
12. Perform a VPN reconnect test.

## 31. Uninstall procedure

A safe uninstall should:

1. Suspend or disable Leshy DNS integration.
2. Stop and disable Kikimora services.
3. remove Kikimora systemd unit files;
4. run `systemctl daemon-reload`;
5. remove installed scripts;
6. optionally remove `/etc/kikimora/`;
7. verify `resolvectl status`;
8. verify normal network access.

Example conceptual sequence:

```bash
sudo /usr/local/sbin/leshy-dns disable
sudo kk disable --now
# kk uninstall also handles disable --now and daemon-reload automatically
```

Adapt names to the actual installed units.

## 32. Development principles

Contributors should preserve these architectural boundaries:

- `leshy-dns` owns resolver integration knowledge.
- Health Watch orchestrates public commands only.
- Route Watch owns interface-driven route reconstruction.
- Marker files are not runtime truth.
- Recovery must be idempotent.
- Commands should return meaningful exit codes.
- Logging should describe cause, action, and result.
- Failure paths should preserve usable system DNS whenever possible.

## 33. Release 1.0.0 guarantees

Version 1.0.0 represents the first stable project release.

The release includes:

- domain-based multi-VPN routing;
- primary and secondary VPN support;
- DNS integration through `leshy-dns0`;
- runtime DNS health checks;
- automatic repair after AmneziaVPN reconnect;
- safe DNS suspension;
- Route Watch;
- Health Watch;
- public `leshy-dns check` API;
- documented incompatibility with AmneziaVPN Kill Switch.

## 34. Further reading

- [README.md](README.md) — project overview and quick start.
- [CONTRIBUTING.md](CONTRIBUTING.md) — development workflow.
- [SECURITY.md](SECURITY.md) — vulnerability reporting.
- [CHANGELOG.md](CHANGELOG.md) — release history.
- [LICENSE](LICENSE) — MIT License.
