# Leshy integration contract — cross-OS

Status: **LINUX IMPLEMENTED IN THE CANONICAL PACKAGE; WINDOWS/MACOS PENDING (see toad-roadmap.md)**.

This document fixes the architectural contract that makes traffic steering work
identically on every supported OS. The Linux package is the reference
implementation; Windows and macOS installers must satisfy the same contract.

## Mechanism

The Go control plane and Leshy split the work; neither substitutes the other:

```text
kikimora-core (control plane, owns tunnel lifecycle + endpoint policy + parking)
   |
   | per role: publish managed interface as "<leshy_zone>.dev" file
   |           (FileBridge, toad/internal/leshy/bridge.go)
   v
run/.../leshy/vpn/<zone>.dev          <-- file contains the interface name
   |
   v
leshя (DNS-driven split-tunnel router)
   - static config.toml: zones with route_target = "<publication>/<zone>.dev"
   - resolves domains from the zone domain lists
   - installs a kernel route for every resolved IP into the published interface
   - serves DNS on 127.0.0.1:53053, forwards non-zone queries to default upstreams
```

Consequences:

- A tunnel being Ready is necessary but not sufficient: without a running Leshy
  with a valid config.toml, zero user traffic enters the tunnels.
- Zone names are defined by the toad configs (`leshy_zone` in
  `/etc/kikimora/toads/*.toml`); the config generator must resolve them from
  there, never hardcode publication stems. Role ordering (primary/secondary)
  follows the endpoint policy priority (lower `rule_priority` = primary).
- Single writer: the legacy bash route-watch must not run next to the Go
  runtime. It is not shipped by the package and exits on its own when
  ownership is go/go/go.

Network note for networks that intercept plain DNS:53 (common on RU ISPs):
with the default `UPSTREAM_ZONE=direct` Leshy forwards its upstream lookups
directly, so zone domains get poisoned answers (NXDomain) even though the
tunnels are Ready. Set `UPSTREAM_ZONE=primary` in
`/etc/kikimora/leshy/routing.conf` — Leshy then installs host routes for the
upstream DNS servers into the primary tunnel and resolves over it. Regenerate
the config with `/usr/local/libexec/kikimora/leshy/build-config-go` and
restart `leshy.service` after changing routing.conf.

## Per-OS layout

| Piece | Linux | Windows | macOS |
|---|---|---|---|
| Publication dir | `/run/kikimora/leshy/vpn` | `%ProgramData%\leshy\vpn` (already written by `service_windows.go`) | `/var/run/kikimora/leshy/vpn` |
| Leshy config | `/etc/kikimora/leshy/config.toml` | `%ProgramData%\leshy\config.toml` | `/etc/kikimora/leshy/config.toml` |
| Leshy binary | `/usr/local/bin/leshy` | installer-owned path on `PATH` of the service | `/usr/local/bin/leshy` |
| DNS listener | `127.0.0.1:53053` | same | same |
| DNS integration | `systemd-resolved` via `leshy-dns` helper (enable/resume/suspend/check), non-fatal unit hooks | NRPT rules or per-adapter DNS set by a Windows service helper | `/etc/resolver/<domain>` files or scoped resolver setup |
| Routing backend | leshy 0.4.x netlink backend | leshy windows port (`windows-support` branch, route via interface alias) | leshy 0.4.x `src/routing/macos.rs` (route `-ifscope`) |
| Supervisor | `systemd` `leshy.service`, pulled in by `kikimora-core.service` via `Wants=` | Windows service (own SCM service or child of the core service host) | `launchd` plist pulled in by the core job |
| Config generation | `build-config-go` (zones from toad configs) | port of the same generator, same toml inputs | port of the same generator |

## What the canonical Linux deb ships

- `/usr/local/bin/leshy` — built from ftelnov/leshy v0.4.0 at package build
  time (`KIKIMORA_LESHY_SRC`, default sibling checkout; `KIKIMORA_LESHY_BIN`
  prebuilt override; the build fails loudly when neither is available).
- `/usr/local/libexec/kikimora/leshy/build-config-go` — config.toml generator
  for the Go runtime (zone resolution from toad configs, publication-dir
  targets). `check-config` validates the result against the binary.
- `/usr/local/sbin/leshy-dns` — DNS integration helper (imperative, not a
  service).
- `/usr/lib/systemd/system/leshy.service` +
  `leshy.service.d/kikimora-dns-hooks.conf` — unit with the documented
  non-fatal cold-boot DNS hooks.
- Config seeds under `/usr/share/kikimora/leshy/` (domain lists, endpoints,
  routing.conf) and the static-route preseed next to the generator.
- postinst seeds `/etc/kikimora/leshy` and generates `config.toml` only when
  missing (admin edits survive upgrades); generation failure is non-fatal.
- Deliberately NOT shipped: route-watch, health-watch, reconcile,
  route-lifecycle, their units and the route-cleanup drop-in — the legacy bash
  watcher stack.

## Windows/macOS implementation checklist

1. Build Leshy for the target OS (Windows: `windows-support` branch of the
   smollgreymouse/leshy fork, 0.5.x series; macOS: upstream 0.4.x).
2. Ship the binary, config seeds and a config generator honoring the same
   toad-config zone resolution and the per-OS publication dir.
3. Service supervision equivalent to `leshy.service`, started together with
   the core service.
4. DNS integration per the table above, with suspend/resume around service
   restarts so the resolver never points at a dead listener.
5. Acceptance: a configured domain resolves through Leshy and its traffic
   enters the interface published by the core; publications survive core
   restart and underlay changes.
