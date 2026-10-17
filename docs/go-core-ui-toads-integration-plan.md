# Go core — UI + CLI + Toad control-plane integration plan

Status: **implemented historical integration plan; do not execute from stage 0 again**.

The core/UI/three-Toad integration described here has largely landed. Its original hard constraints were intentionally exceeded by the later orchestration-v2 work (for example, the core now owns endpoint/routing/parking recovery code and Toad IPC is no longer state.json-only).

For current work use `docs/toad-roadmap.md`, `docs/toad-post-push-audit.md` and the ordered 06A/06/07A-07D packets. Keep this document as the historical integration contract and as a regression reference for API/UI behavior.

## Relationship to existing documents

- `docs/toad-roadmap.md`, `docs/toad-stage0.md`, `docs/toad-naming.md` — canonical
  Toad runtime/state/naming; do not modify Toad protocol code.
- `docs/go-multi-vpn-architecture.md` / `docs/go-multi-vpn-implementation-plan.md`
  — target Go control-plane architecture (stages G1–G10). This plan implements the
  narrow slice G1 (daemon + N-role model) plus a real local control API and CLI,
  without yet moving routing/endpoint/parking ownership out of the Bash runtime.
- `docs/desktop-ui-architecture.md` / `docs/desktop-ui-implementation-plan.md` —
  target UI (stages U1–U7). This plan implements U7's real local control API client
  on top of the already-present `FakeCoreBackend` skeleton.
- `docs/cli-json-api.md`, `docs/profiles.md` — machine-readable CLI conventions and
  the legacy profile model the CLI must remain compatible with.

## Hard constraints (do not violate)

1. **Do not modify Toad protocol code.** `toad/internal/backend/{awg2,xray,openconnect}`,
   `toad/internal/platform/*`, `toad/internal/config/*`, `toad/internal/state/*`,
   `toad/internal/profileimport/*`, and `toad/cmd/kikimora-toad/*` are the Toad
   runtime and are out of scope for edits. The core only *launches* `kikimora-toad`
   and reads its `state.json` snapshot.
2. **The three Toad protocol types are:** `amneziawg2` (AWG2), `vless-reality`
   (Xray/VLESS-REALITY), `openconnect`. The core must load and manage configs of all
   three types uniformly; it must not add a fourth protocol or special-case one of
   them outside the shared role model.
3. **The core owns desired state and process supervision only.** It spawns/stops
   `kikimora-toad run -config <path>` processes and reports observed state. It must
   not install routes, mutate table `51890`, write `/run/kikimora/leshy/vpn/*.dev`,
   or touch Leshy. Those remain owned by the legacy Bash runtime in this plan.
4. **UI must not shell out or parse human CLI text.** QML communicates only through
   the `CoreBackend` interface. A real backend speaks the versioned Unix-socket IPC.
5. **Secrets never enter snapshots, logs, argv, or tests.** Configs may reference
   secret files; the core must not copy secret material into its own state output.

## Branch policy

All implementation work happens on the current branch
`feat/native-core-vpn-clients` after the UI branch `feature/desktop-ui-fake-core`
is merged into it. `master` receives only the plan/roadmap documentation updates,
not the networking implementation, until the real-core gates below are green.

---

## Stage 0 — Baseline: merge the UI branch and record the starting state

### Goal

Bring the desktop UI code into the working branch and capture a green baseline
against which every later stage is measured.

### Work

1. Ensure the local tracking branch exists:
   ```bash
   git fetch origin feature/desktop-ui-fake-core
   git branch --track feature/desktop-ui-fake-core origin/feature/desktop-ui-fake-core
   ```
2. Merge the UI branch into `feat/native-core-vpn-clients`:
   ```bash
   git checkout feat/native-core-vpn-clients
   git merge feature/desktop-ui-fake-core
   ```
   Conflicts, if any, must resolve toward keeping `desktop/` plus the workflow
   `.github/workflows/desktop-ui.yml` (which is present on the UI branch but absent
   from the current branch's `.github/workflows/`).
3. Do **not** delete the untracked core prototype `toad/cmd/kikimora-core/` and
   `toad/internal/control/`. It is the starting seed for Stages 1–2.

### Acceptance

```bash
cd toad && go test ./...
cmake -S desktop -B build/desktop -DCMAKE_BUILD_TYPE=Release
cmake --build build/desktop --parallel
```

### Exit gate

- `go test ./...` in `toad/` is green.
- The desktop UI builds on Linux.
- Both `desktop/` and `.github/workflows/desktop-ui.yml` are present in the branch.

---

## Stage 1 — Core control-plane model for the three Toad types

### Goal

Extend the seed `toad/internal/control` into a correct N-role model that can load
and supervise all three protocol types and expose desired/observed state.

### Work

1. Keep the `Manager` role map keyed by config `name`. Confirm `config.Load` already
   accepts all three protocols (`amneziawg2`, `vless-reality`, `openconnect`); add
   a unit test that loads one config of each type and asserts `Protocol` is surfaced
   into `RoleSnapshot.Protocol`.
2. Separate **desired** state from **observed** state:
   - desired: `enabled` (bool) per role, default `false` (stopped);
   - observed: the last `state.Snapshot` read from `<state_dir>/state.json`.
   `Snapshot().Roles[].State` must reflect observed state when running, `Stopped`
   when not, and `Failed` only when a start/read error was recorded.
3. Add per-role `available_actions` derived from desired/observed state:
   - stopped/disabled → `connect`;
   - running → `disconnect`, `retry`;
   - failed → `retry`.
   This is what the UI later uses to enable/disable role-drawer buttons; it must
   never be guessed in QML.
4. Add `SetActiveProfile` support in the model. In this plan the profile is the set
   of configured roles loaded at startup; `SetActiveProfile` must validate the
   requested profile name against the loaded role set and report an error for an
   unknown name. No filesystem profile store is required yet.
5. Add `RediscoverEndpoints(role)` as a semantic no-op that re-reads the role's
   observed state snapshot and bumps the revision; document it as a placeholder for
   the later endpoint-provider migration (G4).

### Files

- `toad/internal/control/control.go`
- `toad/internal/control/control_test.go`

### Acceptance

```bash
cd toad && go test ./internal/control/ -run . -v
```

### Exit gate

- Unit tests cover: three-protocol config load; desired vs observed state;
  per-role available actions; `SetActiveProfile` validation; revision monotonicity.

---

## Stage 2 — Versioned local control API (Unix socket)

### Goal

Finalize the IPC contract the C++ UI will consume: version handshake, complete
snapshot, revisioned events, and high-level semantic commands.

### Transport

Unix-domain socket, length-prefixed UTF-8 JSON, single-frame request/response plus
a subscription stream. Reuse the existing framing in `api.go` (4-byte big-endian
length, 1 MiB frame cap). Socket path default `/run/kikimora/core.sock`.

### Contract

Request:

```json
{ "version": 1, "id": "…", "method": "…", "role": "…", "profile": "…" }
```

Response:

```json
{ "version": 1, "id": "…", "ok": true, "error": "", "capabilities": ["…"], "snapshot": { … } }
```

Methods (must all be implemented and covered by `api` tests):

```text
Handshake
GetSnapshot
Subscribe
ConnectAll
DisconnectAll
ConnectRole
DisconnectRole
RetryRole
SetActiveProfile
RediscoverEndpoints
GetDiagnosticsSnapshot
```

Semantics:

- `Handshake` returns `capabilities` listing all methods; no state mutation.
- `GetSnapshot` returns the current `Snapshot` with `revision`.
- `Subscribe` returns the current `Snapshot` then streams a new `Snapshot` frame on
  every revision bump until the connection closes.
- `ConnectAll`/`DisconnectAll`/`ConnectRole`/`DisconnectRole`/`RetryRole` mutate
  desired state and immediately return the resulting `Snapshot`.
- `GetDiagnosticsSnapshot` returns the same `Snapshot` plus `backend_kind = "real"`
  and a `diagnostics` object (core version, protocol version, platform, socket
  path). It must not expose secret material.

### Snapshot schema (extend existing, keep additive)

```text
schema, revision, core_state, aggregate_state, roles[]
role: id, label, protocol, state, reason, route_ready,
      interface{name,ifindex,mtu}, session{connected,rx_bytes,tx_bytes,endpoint},
      available_actions[]
```

### Security

The real core authorizes the local peer and exposes only the high-level operations
above. No arbitrary shell/root execution is exposed through the socket.

### Files

- `toad/internal/control/api.go`
- `toad/internal/control/control.go` (if snapshot fields change)

### Acceptance

```bash
cd toad && go test ./internal/control/ -run . -v
```

### Exit gate

- A Go test dials a `net.Pipe`/Unix socket, performs `Handshake`, `GetSnapshot`,
  `ConnectAll`, `DisconnectAll`, and a `Subscribe` stream, and asserts revision
  ordering and no stale revision delivery.

---

## Stage 3 — Core CLI (inherit legacy Bash Kikimora commands)

### Goal

Provide a CLI on the core binary whose command surface mirrors the legacy
`linux/kikimora` Bash CLI for the VPN-role lifecycle it now owns, so operators can
keep using the same verbs.

### Legacy command list to inherit (source: `linux/files/kikimora-cli/help.sh`)

The Bash CLI exposes: `install, upgrade, uninstall, verify, doctor, debuglog, diag,
start, stop, restart, enable, disable, status, interfaces, logs, profiles, dns,
config, endpoints, domains, routes, backup, restore, completion, version, help`.

The **core** implements and maps to its API the VPN-lifecycle subset:

```text
start      -> ConnectAll
stop       -> DisconnectAll
restart    -> DisconnectAll then ConnectAll
status     -> GetSnapshot (human) / GetSnapshot (--json)
interfaces -> per-role interface summary from Snapshot
profiles   -> list configured roles; use -> SetActiveProfile
version    -> core version + backend_kind
help       -> usage text
```

Commands that remain owned by the legacy Bash/Leshy/installer and are **explicitly
out of scope** for the core in this plan: `install, upgrade, uninstall, verify,
doctor, debuglog, diag, logs, dns, config, endpoints, domains, routes, backup,
restore, completion`.

### CLI surface

Extend `toad/cmd/kikimora-core/main.go`:

```text
kikimora-core serve [--socket PATH] [--toad-binary PATH] --config FILE [--config FILE ...]
kikimora-core status [--socket PATH] [--json]
kikimora-core start|stop|restart [--socket PATH]
kikimora-core connect|disconnect|retry --role NAME [--socket PATH]
kikimora-core interfaces [--socket PATH]
kikimora-core profiles [--socket PATH] [--json]
kikimora-core version
kikimora-core help
```

`--json` must emit the machine-readable snapshot (schema `schema_version: 1`) and
never mix human text. Human output is free-form; the JSON schema is the contract,
consistent with `docs/cli-json-api.md`.

The legacy `kk` name may be added later as a thin alias; do not implement a second
command tree in this stage.

### Files

- `toad/cmd/kikimora-core/main.go`

### Acceptance

```bash
cd toad && go build -o /tmp/kikimora-core ./cmd/kikimora-core
go test ./...
```

### Exit gate

- Each command in the list parses its flags, rejects a missing required `--role`,
  and either talks to a running `serve` socket or prints a clear error.
- `version` and `help` print and exit 0 without touching the socket.

---

## Stage 4 — C++ real backend (`RealCoreClient`) for the Qt6 UI

### Goal

Implement a real `CoreBackend` that speaks the Stage 2 IPC, replacing FakeCore on
Linux/macOS while leaving FakeCore for Windows and for deterministic UI tests.

### Work

1. Add `desktop/src/core/RealCoreClient.{h,cpp}` implementing `CoreBackend` with the
   same properties as `FakeCoreBackend` (`backendKind`, `platformName`, `coreState`,
   `activeProfile`, `underlaySummary`, `aggregateState`, `aggregateStateText`,
   `aggregateActionText`, `revision`, `leshySupported`, `roles`).
2. Implement a `LocalIpcTransport` using `QLocalSocket` (Qt's Unix-socket
   abstraction) with the same length-prefixed JSON framing as `api.go`.
3. Map core methods to UI actions:
   - `toggleAll()` → `ConnectAll` when aggregate is stopped, else `DisconnectAll`;
   - `connectAll()` → `ConnectAll`;
   - `disconnectAll()` → `DisconnectAll`;
   - `roleAction(row)` → `ConnectRole`/`DisconnectRole` based on role state;
   - `nextDemoScenario()` → no-op on real backend (UI hides the demo control).
4. On connection: send `Handshake`, then `GetSnapshot`, then `Subscribe`; replace
   `RoleListModel` contents and bump `revision` from streamed snapshots. On gap or
   reconnect, request a fresh `GetSnapshot` instead of trusting stale revisions.
5. `main.cpp`: select backend at startup — real client on Linux/macOS when the
   socket is available, FakeCore on Windows or when `--fake` is passed. Keep the
   `CoreBackend` QML-facing contract unchanged; desktop integration may add
   small shell/status bindings around it.
6. Add Qt desktop integration around the unchanged QML contract: a system-tray
   controller with show/connect-all/disconnect-all/quit actions, close-to-tray
   behavior, and a bundled application icon.
7. Keep the core stream visible in the shell: show connection state and revision
   in the window header, and hide fake-only demo controls for the real backend.
8. Respect the desktop platform palette and select the matching Qt Quick control
   style on macOS, Windows and Linux. Allow `KIKIMORA_QT_STYLE` as a diagnostic
   override without hard-coding a dark palette in QML.

### Files

- `desktop/src/core/RealCoreClient.h`
- `desktop/src/core/RealCoreClient.cpp`
- `desktop/src/DesktopIntegration.h`
- `desktop/src/DesktopIntegration.cpp`
- `desktop/src/main.cpp`
- `desktop/CMakeLists.txt` (add sources; `Qt6::Network` only if the transport needs it)

### Acceptance

```bash
cmake -S desktop -B build/desktop -DCMAKE_BUILD_TYPE=Release
cmake --build build/desktop --parallel
```

### Exit gate

- The UI builds and runs with FakeCore (no socket) and does not crash when the real
  socket is absent; it reports an unavailable backend.
- `CoreBackend` QML contract remains unchanged; desktop shell/status bindings do
  not add protocol-specific logic to QML.

---

## Stage 5 — End-to-end wiring: UI ↔ core ↔ Toads

### Goal

Prove the full bundle: the C++ UI talks to the Go core over IPC, and the Go core
spawns real `kikimora-toad` processes for each configured role and reports their
state back to the UI.

### Work

1. Write three fixture Toad configs (one per protocol) under a temp dir, using
   secret-file references for OpenConnect and dummy-but-valid placeholders for
   AWG2/Xray. Do not commit real secrets; keep them local-only.
2. Start the core:
   ```bash
   kikimora-core serve --socket /tmp/core.sock --toad-binary ./kikimora-toad \
     --config awg.toml --config xray.toml --config oc.toml
   ```
3. Verify via CLI that `status`, `start`, `stop`, `interfaces`, `profiles` work
   against the running core.
4. Launch the UI pointed at the socket and verify the role list renders three roles
   with correct protocols; `ConnectAll` transitions each role to a running state and
   `DisconnectAll` stops them.
5. Confirm the core reports each Toad's `state.json` (read from `state_dir`), and
   that killing a Toad process surfaces as that role leaving its running state
   without affecting the other two roles.

The real-server acceptance layer is an opt-in local smoke gate, separate from
the hermetic fixture test. With the existing `0600` credential files in
`linux/tests/toad/`, run `sudo -v` followed by
`linux/tests/toad/run-isolated.sh real-vps`; this reuses the isolated-network
AWG2, VLESS/REALITY and OpenConnect diagnostics against the real VPN servers.
The regular `all` mode and hosted CI jobs do not run this gate.

### Files

- `toad/internal/control/control_test.go` (add an integration test using a fake
  launcher that writes `state.json` and exits, for all three protocols)
- any test fixture configs under `toad/internal/control/testdata/`

### Acceptance

```bash
cd toad && go test ./... && go test -race ./...
cmake --build build/desktop --parallel
```

### Exit gate

- UI ↔ core ↔ Toad bundle is exercised end to end in the test above and by the
  manual smoke described in work item 4.
- `go test -race ./...` is green.

---

## Stage 6 — CI and final acceptance

### Goal

Make the bundle reproducible in CI and define the completion criteria.

### Work

1. Ensure `.github/workflows/toad.yml` still passes; extend it (or add
   `go-core.yml`) to also build `./cmd/kikimora-core` and run `go test ./...` on
   Linux/macOS/Windows.
2. Ensure `.github/workflows/desktop-ui.yml` (brought over in Stage 0) builds the
   UI on all three OSes and keeps the offscreen QML smoke test.
3. Add a C++ unit test for `RealCoreClient`'s framing/snapshot mapping using a
   local `QLocalServer` fixture, and wire it into the desktop CMake test target.

### Acceptance

```bash
cd toad && go test ./... && go vet ./... && go test -race ./...
cmake --build build/desktop --parallel && ctest --test-dir build/desktop
```

### Final completion criteria

1. Go core `kikimora-core` loads and manages all three Toad protocol types without
   modifying Toad protocol code.
2. The core exposes the versioned Unix-socket API with `Handshake`, `GetSnapshot`,
   `Subscribe`, and the semantic commands listed in Stage 2.
3. The core CLI mirrors the legacy Bash VPN-lifecycle commands (`start`, `stop`,
   `restart`, `status`, `interfaces`, `profiles`, `version`, `help`) with `--json`.
4. The C++ Qt6 UI builds and runs on Linux and communicates with the Go core through
   `RealCoreClient`; FakeCore remains for Windows and deterministic tests.
5. `go test ./...`, `go vet ./...`, `go test -race ./...` are green.
6. The C++ build, QML smoke test, and C++ unit tests are green.
7. No secret material enters snapshots, logs, or committed test fixtures.
8. The opt-in local real-VPS smoke suite is available through
   `linux/tests/toad/run-isolated.sh real-vps`; hosted CI does not run it without
   an explicitly provisioned credential source.
