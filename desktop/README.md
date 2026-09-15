# Kikimora desktop UI prototype

This directory is the first runnable implementation of the desktop UI roadmap.
It intentionally does **not** connect a real VPN yet.

The purpose of this prototype is to freeze the frontend/backend boundary early and make
the Amnezia-inspired interaction model testable before the Go networking core exists.

## What is implemented

- Qt 6 / Qt Quick desktop application;
- one large global `CONNECT` / `DISCONNECT` circle;
- compact collapsed rows for each VPN role/toad;
- role detail drawer with underlay, endpoint, Leshy publication, parking and recovery data;
- clickable Home / Profiles / Routing / Settings navigation;
- `CoreBackend` UI-facing contract;
- `FakeCoreBackend` implementing that contract;
- fake asynchronous Connect All / Disconnect All transitions;
- demo scenarios for recovery, lost physical underlay and per-role failure;
- Windows uses the same FakeCore path and reports no Leshy support.

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

## FakeCore controls

The large circle drives the same future high-level intent as the real core API:

```text
ConnectAll
DisconnectAll
```

Click a role row to open its detail drawer and test a per-role action.

`Next demo state` cycles through:

1. healthy/stopped baseline;
2. Primary Ready + Secondary Recovering with parked routes;
3. physical underlay unavailable for both roles;
4. Primary Ready + Secondary Failed.

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
