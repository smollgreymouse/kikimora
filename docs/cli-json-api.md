# CLI JSON API

Kikimora exposes an explicit machine-readable mode for consumers such as the TUI.
The existing human-readable commands remain unchanged; JSON is selected only when
`--json` is the first argument after the command name.

```bash
kk status --json
kk profiles --json
kk endpoints --json
kk dns --json
kk logs --json
```

Every Kikimora-owned response starts with `"schema_version": 1`. Consumers should
ignore unknown fields so additive schema changes remain compatible.

## Status

`kk status --json` returns service state, the active profile, managed interfaces,
DNS ownership, autostart state and endpoint-underlay migration state.

```json
{
  "schema_version": 1,
  "service": "running",
  "services": {
    "leshy": "running",
    "route_watch": "running",
    "health_watch": "running"
  },
  "profiles": { "active": "happ" },
  "interfaces": {
    "primary": { "name": "amn0", "state": "ready" },
    "secondary": { "name": "tun0", "state": "ready" },
    "dns": { "name": "leshy-dns0", "state": "active" }
  },
  "dns": { "provider": "leshy", "default_zone": "direct" },
  "startup": { "enabled": true },
  "endpoint_underlay_migration_pending": false
}
```

Service values are `running`, `failed` or `stopped`. VPN interface state reuses the
same runtime semantics as the existing CLI: `ready`, `down`, `missing`, or
`underlay-pending`.

## Profiles

`kk profiles --json` returns the selected profile and the complete role state of
every configured profile, including endpoint provider names and provider args.
The top-level `active` value is `null` when the current VPN state is not represented
by a named profile.

## Endpoints

`kk endpoints --json` is read-only. It never invokes an endpoint provider and
therefore never starts a Happ probe merely because a UI refreshed.

For `static`, candidates are read from the role endpoint list. For `happ`, the API
reads the provider's existing runtime proof cache and exposes its endpoints,
owners, candidate process PIDs, cache age, and degraded flag. `current` is the first
currently proven Happ endpoint, or `null` when no proof is cached.

The endpoint API also reports the role's interface state and whether endpoint
underlay reconciliation is pending.

## DNS

`kk dns --json` reports whether system DNS is currently owned by Leshy or by the
system resolver, the `leshy-dns0` interface state, Leshy service state, and the
local Leshy listener address.

## Logs

`kk logs --json` is always a bounded snapshot and never follows the journal. The
default is 100 records from `leshy.service`.

```bash
kk logs --json --lines 250
kk logs --json --all --lines 250
```

`entries` contains the native JSON objects emitted by `journalctl -o json`, so a
client can consume fields such as `MESSAGE`, `_SYSTEMD_UNIT` and timestamps without
parsing formatted journal text. The maximum requested snapshot is 5000 records.

## Compatibility rule

Do not make a TUI or other client scrape the text produced by `kk status`,
`kk profiles`, `kk dns`, or `kk logs`. Human output is free to evolve independently;
the JSON schema is the machine interface.
