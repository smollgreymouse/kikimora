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
  "profiles": { "active": "office" },
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

`kk endpoints --json` is deliberately provider-agnostic. The CLI does not know or
parse provider-specific caches, process names, client implementation details or
provider diagnostics.

Each role reports:

- `interface` — the managed VPN interface;
- `provider` and `provider_args` — opaque provider configuration;
- `state` and `pending` — generic Kikimora runtime state;
- `configured` — endpoint specs from Kikimora's role endpoint list;
- `installed` — endpoint addresses currently installed in Kikimora's endpoint
  policy rules;
- `actions` — generic capabilities exposed by the endpoint API.

Example:

```json
{
  "schema_version": 1,
  "roles": {
    "secondary": {
      "interface": "tun0",
      "provider": "command",
      "provider_args": "/usr/local/libexec/provider",
      "state": "ready",
      "pending": false,
      "configured": [],
      "installed": ["198.51.100.40", "198.51.100.41"],
      "actions": {
        "rediscover": true,
        "invalidate": false
      }
    }
  }
}
```

Refreshing the JSON endpoint state never executes the provider. `rediscover` is a
generic control operation that asks the route watcher to reconcile again:

```bash
sudo kk endpoints rediscover secondary
```

Provider-private cache invalidation is not part of the generic API today. If a
future provider needs such an operation, it should be added through a provider
capability/control protocol rather than by teaching the CLI or TUI that provider's
name or cache format.

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
`kk profiles`, `kk endpoints`, `kk dns`, or `kk logs`. Human output is free to
evolve independently; the JSON schema is the machine interface.
