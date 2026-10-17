# Kikimora desktop UI prototype

This directory is the first runnable implementation of the desktop UI roadmap.
It intentionally does **not** connect a real VPN yet.

The purpose of this prototype is to freeze the frontend/backend boundary early and make
the Amnezia-inspired interaction model testable before the Go networking core exists.

## What is implemented

- Qt 6 / Qt Quick desktop application;
- one large global `CONNECT` / `DISCONNECT` circle;
- compact collapsed rows for each VPN role/toad;
- role detail drawer whose primary/secondary actions are taken from the
  core-reported `available_actions` list (never guessed from QML state), with
  underlay, endpoint, Leshy publication, parking and recovery data;
- functional (prototype) Routing/Leshy page (underlay summary/epoch and a
  per-role publication card) and Settings/Diagnostics page;
- a visible `SIMULATED` marker on the Home header and Settings page so the UI
  never implies the host is protected;
- a small subset of the `K*` design-system controls (`KButton`, `KCard`,
  `KStatusDot`, `KTag`, `KSectionHeader`, `KInfoRow`, `KPageTitle`);
- clickable Home / Profiles / Routing / Settings navigation;
- `CoreBackend` UI-facing contract (including `simulated` and
  `roleActionByName`);
- `FakeCoreBackend` implementing that contract;
- fake asynchronous Connect All / Disconnect All transitions;
- ten deterministic demo fixtures covering recovery, lost physical underlay,
  per-role failure and partial protection;
- Windows uses the same FakeCore path, forces `simulated == true` and reports
  no Leshy support.

> The full `K*` control library, the complete drawer field surface and the
> `DesktopShell` tray contract remain unimplemented; see
> `docs/desktop-ui-implementation-plan.md`.

No shell commands, route mutations, systemd/launchd calls, VPN processes or Leshy changes
are made by this prototype.

## Build

Requirements:

- CMake 3.21+
- C++20 compiler
- Qt 6.5+ with Core, Gui, Qml, Quick and QuickControls2

```bash
cmake -S desktop -B build/desktop
cmake --build build/desktop -j
./build/desktop/kikimora-ui
```

On macOS the executable may be emitted as an app bundle depending on the generator.

## Native Linux packages

Build the Debian and portable archives, including the Qt UI, Go core and Toad runtime:

```bash
bash desktop/packaging/build-release.sh
```

Artifacts are written to `desktop/dist/`. Existing system configuration is not included
in or overwritten by the portable bundle.

## FakeCore controls

The large circle drives the same future high-level intent as the real core API:

```text
ConnectAll
DisconnectAll
```

Click a role row to open its detail drawer. The drawer's action buttons come
from the role's core-reported `available_actions`, so the same code path works
for both `FakeCoreBackend` and a future `LocalIpcBackend`.

`Next demo state` advances `(index + 1) % 10` through the ten fixtures
(`FakeCoreBackend::fixtureCount`). Starting from the stopped baseline, the first
ten clicks therefore visit:

1. Primary Ready + Secondary Recovering with parked routes;
2. physical underlay unavailable for both roles (WaitingForUnderlay);
3. Primary Ready + Secondary Failed;
4. both Ready;
5. Primary Recovering + Secondary Ready;
6. Primary Stopped + Secondary Ready;
7. both Connecting;
8. Primary Recovering (awaiting validation) + Secondary WaitingForUnderlay;
9. Primary Failed + Secondary Stopped;
10. healthy/stopped baseline (scenario 0).

The fake state is deliberately shaped like future core snapshots rather than like QML-only
booleans.

## Future replacement boundary

`CoreBackend` is the seam to preserve.

The intended production path is:

```text
QML
  -> Qt presentation models
  -> CoreBackend
       -> FakeCoreBackend       (prototype / Windows for now)
       -> LocalIpcBackend       (future Linux/macOS)
            -> versioned local IPC
            -> Go kikimora-core
```

The future IPC implementation should populate the same role model from canonical core
snapshot/event DTOs and implement the same high-level commands. QML must not learn about
Unix sockets, NetworkManager, rtnetlink, route tables or VPN process details.
