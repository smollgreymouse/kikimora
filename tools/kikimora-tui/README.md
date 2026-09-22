# Kikimora TUI

Terminal UI for Kikimora built on `ratatui`, `crossterm`, `serde_json` and `tokio`.
The binary name is `kk-tui`.

## Architecture

```text
kikimora-core
    |
    +-- backend/linux.rs     -> kk ... --json + Linux control operations
    +-- backend/macos.rs     -> platform slot (stub for now)
    +-- backend/windows.rs   -> platform slot (stub for now)
    |
    +-- shared API models

kikimora-tui
    +-- app state/actions
    +-- ratatui rendering
    +-- crossterm keyboard + mouse input
```

The Linux backend never parses Kikimora's human-readable status output. State is
loaded from:

```text
kk status --json
kk profiles --json
kk endpoints --json
kk dns --json
kk logs --json
```

The TUI and `kikimora-core` do not know implementation details of individual VPN
clients or endpoint providers. Provider names and arguments are opaque data from
the CLI API. Provider-specific caches, processes and discovery algorithms stay
behind Kikimora's backend/provider boundary.

Mutating operations use Kikimora control commands (`kk start`, `kk profiles use`,
`kk dns enable`, `kk endpoints rediscover`, etc.) and therefore require the same
system permissions as those commands.

## Build / install

From the repository root:

```bash
cargo run --manifest-path tools/kikimora-tui/Cargo.toml -p kikimora-tui
```

To install the command into Cargo's binary directory:

```bash
cargo install --path tools/kikimora-tui/crates/kikimora-tui
kk-tui
```

Set `KIKIMORA_CLI` to override the `kk` executable used by the Linux backend.

## UI

Tabs:

```text
[Status] [Profiles] [Endpoints] [DNS] [Logs] [Settings]
```

The Status screen provides profile selection, interface/service state and
Start/Stop/Restart controls. Profiles can be selected with the mouse or
Up/Down + Enter.

The Endpoints screen switches between primary/secondary roles and shows only
generic Kikimora endpoint state:

- provider name and opaque provider args;
- managed interface and role state;
- configured endpoint specs;
- endpoint addresses currently installed in Kikimora policy rules;
- generic actions advertised by the API.

`Rediscover` is available through the generic endpoint control API. `Invalidate`
is shown only when the endpoint API advertises that capability; the TUI never
deletes a provider-private cache directly.

Settings provides:

- `Manage VPN endpoints` — a persisted TUI safety switch controlling whether this
  UI may execute endpoint mutations. It does not disable Kikimora's route watcher
  itself.
- `Default profile` — cycles/selects the currently active Kikimora profile.
- `DNS` — switches between Leshy and system DNS through `kk dns`.
- `Start service` — toggles Kikimora/Leshy autostart through `kk enable/disable`.

The local TUI safety setting is stored in `$XDG_CONFIG_HOME/kikimora/tui.json` or
`~/.config/kikimora/tui.json`.

## Controls

- mouse: tabs, profiles and enabled buttons
- `1`..`6`: select tab
- `Tab` / arrows: navigate tabs or selection
- `Enter`: apply selected profile
- `F5`: refresh all JSON state
- Status: `s` start, `x` stop, `r` restart
- Endpoints: `d` rediscover, `i` invalidate when supported
- Settings: `m` endpoint safety switch, `p` profile, `d` DNS, `a` autostart
- Logs: PageUp/PageDown or mouse wheel
- `q` / Esc: quit

## Platform work

Linux is the first implemented backend. macOS and Windows already have explicit
backend modules implementing the same trait and currently return an unsupported
error. Porting those platforms should therefore replace backend internals rather
than rewrite the TUI.
