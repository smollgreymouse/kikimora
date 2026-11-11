# 09D — desktop UI / UI↔core execution log

Status: **ACTIVE — stages U1…U7 per docs/roadmap.md §5.**

Authority chain:

~~~text
docs/roadmap.md
docs/desktop-ui-core-audit-2026-11-10.md          (findings/evidence)
docs/desktop-ui-executor-coverage-plan-2026-11-11.md (stage packets)
this file                                          (executor status)
~~~

Execution host note (standing constraint, 2026-11-11): this workstation runs
the REAL product Leshy (`leshy.service`, active since 2026-11-11 10:08 MSK).
Layer-C suites on this host must never trigger `systemctl restart
leshy.service` or write outside their temp root. A cancelled matrix run on
2026-11-11 ~22:15 did NOT touch the host service (verified via `systemctl
show` / journal) — no restart, no config write outside the injected root.

---

## U1 — current core truth in the desktop backend — DONE

~~~text
commit:
    f4ecdbd fix(desktop): map current core routing authority and family truth
~~~

- RealCoreClient: API v3, Handshake min/max validation with an explicit
  Incompatible state, D9 snapshot mapping (startup_ready, managed_mode,
  mode_generation, activation/mode-transition fingerprints,
  route_authority_ready/error).
- Backend-owned fail-closed policy: commandChannelReady / safeToMutate /
  safeToDisconnect / diagnosticsAvailable / diagnosticsMutable; every command
  class is refused before any IPC frame (including the optimistic profile and
  membership caches); disconnect stays available during authority loss;
  command-scoped error lifecycle (clear on new command, transport errors
  clear on the post-handshake authoritative snapshot only).
- RoleSnapshot/RoleListModel: independent ipv4/ipv6 family fields, the
  selected_routes map, and the leshyIpv6 key RoleListModel::get() used to
  drop (role drawer defaulted a core-reported false to enabled).
- Layer A tests: +20 real-core-client cases, +4 role-list-model cases.
- The role-list-model invalid-type QWARN (type id 4097 = QPixmap) is fixed at
  its cause: QAbstractItemModelTester probes GUI roles through legacy
  QMetaType ids, so the model suite keeps QtGui loaded (`--no-as-needed`).

## U2 — FakeCore schema identity — DONE

~~~text
commit:
    54a0c68 test(desktop): align FakeCore with the real snapshot schema
~~~

- FakeCore implements the same policy rules as RealCoreClient plus the D9
  truth fields and deterministic scenario controls (managed authority, mode
  transition, API compatibility, startup readiness, role selected-route
  counters).
- Schema fixes: managed_interfaces as the real nested {present, managed}
  shape; leshySupported no longer reports Windows unsupported; setRoleIpv6
  actually flips the role policy; family defaults follow the role policy.

## U3 — fail-closed presentation — DONE

~~~text
commit:
    fb118dd fix(desktop): surface route authority and gate unsafe UI mutations
~~~

- Never-Protected rule at the backend: a managed core with
  route_authority_ready=false renders "Routing authority lost" everywhere
  (header, tray, connect control), never "Protected".
- routeAuthorityBanner (stable objectName) with the fixed explanation
  priority: API incompatibility → mode transition → authority loss; no
  fingerprints outside diagnostics.
- Gating by safety class: connect ring (DISCONNECT stays available during
  authority loss), profile selector/creation, membership save, tunnel
  add/remove, routing-list save, bypass/default route, role IPv6 switch,
  role action button, tray aggregate action.
- Diagnostics: managed-authority card (diagStartupReady … diagCommandChannel
  rows), per-family readiness rows, managed selected-route counters in the
  role drawer, nested managed_interfaces rendered as explicit fields.
- Layer B: +16 UiBehavior cases, +4 TrayMenu cases.

## U4 — production QML bootstrap + warning gate — DONE

~~~text
commit:
    a2c97ae test(desktop): share the production QML bootstrap and fail on runtime warnings
~~~

- tests/support/UiTestFixture: Basic style pinned once per process,
  Core/Desktop/ThemeManager/SystemIconProvider context identical to
  production main.cpp; used by ui-behavior, real-core-ui, real-core-matrix.
- tests/support/QmlWarningGuard: fails the test function on unexpected QML
  messages (ReferenceError/TypeError/binding loops/engine failures/assignment
  errors/style-ordering), chained after QtTest's handler.
- UiBehaviorTest 38/38 with zero QWARN (baseline was 19 style warnings).
- Stale live-suite expectations repaired: two bottom tabs (Settings at
  index 1), current status text, routing-list test no longer hard-codes a
  developer host name.

## U5a — core runtime-path injection — DONE

~~~text
commit:
    81545ae test(control): make desktop live-core filesystem paths injectable
~~~

- control.RuntimePaths + SetRuntimePaths (closed once StartupManagedAuthority
  begins); buildLeshyZones and GetRoutingLists resolve through the Manager
  instead of config.Default* (the exact P1-UI10 leak).
- serve flags --leshy-config/--routing-dir/--bypass-config/
  --diagnostics-host-sources (nil ⇒ true for the packaged service).
- diagnostics Collector: hostSources=false skips product-log glob expansion
  (previously only journal/event-log/network state were gated) with
  logGlobsFn/networkStateFn seams.
- Go tests: 9 runtime-path cases, 3 host-sources cases, 3 serve hermetic
  cases (flags reach the Manager before startup authority; a fully
  overridden serve creates nothing under /etc, /var/lib, /run kikimora
  roots). All green locally.

## U5b — hermetic LiveCoreFixture + Layer C on an owned core — DONE

~~~text
commits:
    eff89b9 fix(control): notify subscribers on routing writes and make the Leshy reload injectable
    4d647e1 test(desktop): run live UI suites against a self-owned core
~~~

- tests/support/LiveCoreFixture owns the exact core process + fake-toad
  helper + temp root with the §12.6 serve line (config/state/publication/
  leshy config+routing+bypass/providers/socket, KIKIMORA_RESTART_MARKER
  under the root, --diagnostics-host-sources=false, --leshy-reload=false);
  deterministic alpha/beta roles, default/home/work profiles, alpha/beta
  routing lists; generated route_target files asserted under the temp
  publication root. Missing binaries are a harness failure, never a skip.
- The reload blocker from the previous entry is RESOLVED: --leshy-reload=false
  installs a no-op Manager reload hook, and three writers
  (SetRoleIPv6/SetBypassMode/SetRoutingList) now bump the revision so
  subscribers actually observe the writes (a real core bug the matrix
  round trips caught: a UI IPv6 switch visibly reverted without it).
- Client fix: GetDiagnosticsSnapshot answers with a full Snapshot at the
  current revision, which the revision gate dropped — the diagnostics
  payload is now mapped before the gate.
- Layer-C local evidence on the owned core (this host, 2026-11-11):

~~~text
real-core-e2e-test      4 passed, 0 failed, 0 skipped
real-core-ui-test       6 passed, 0 failed, 0 skipped
real-core-matrix-test  10 passed, 0 failed, 0 skipped
tray-live-test          4 passed, 0 failed, 0 skipped
~~~

- The host leshy.service was NOT restarted: ActiveEnterTimestamp stayed
  2026-11-11 10:08:29 MSK through every Layer-C run.
- Layer-C local-mode scope per roadmap §12.7: authority fields assert the
  actual local-mode values; managed authority loss/selected-route truth
  stays with Layer A+B (fake/protocol) and Layer D (installed managed VM).

## U6 — zero-skip/zero-warning suite wrappers — DONE

~~~text
commit:
    6e0f415 test(desktop): fail required suites on skips and runtime warnings
~~~

- desktop/tests/run-required-suites.sh/.ps1: every required binary runs
  directly; the gate fails on nonzero exit, mandatory QSKIP, unexpected QML
  warning patterns, or missing Layer-C binaries (harness failure). The .sh
  wrapper builds the Go core + fake-toad helpers into <build>/fixtures and
  exports their paths, so Layer C can never skip.
- Full local wrapper run: Layer A/B/C all green
  (29+11+5+38+8+4+6+10+4 passed); the wrapper exits 1 on exactly one
  remaining gate item — the tray-menu translation QSKIP, closed by
  installing qt6-l10n-tools locally (needs sudo) and pre-installed in the
  U7 CI lane.

## U7 — Desktop UI GitHub workflow — DONE (code; CI evidence pending push)

~~~text
commit:
    dbaed73 ci(desktop): gate the UI-core contract on every supported OS
~~~

- Triggers: pull_request + push to master over desktop/**, toad/**, VERSION,
  .github/managed-routing-companion.env, the workflow file; workflow_dispatch.
- The historical ftelnov/leshy v0.4.0 checkout is removed; the companion pin
  is resolved/verified like the package workflows (40-hex SHA).
- Go fixture binaries + Go contract tests on all three OSes; Layer A/B/C via
  the U6 wrappers (zero mandatory skips, zero unexpected warnings); per-OS
  logs uploaded. The release-package lanes moved out — build-packages.yml
  stays the sole release-artifact owner.
- CI evidence (green Linux/macOS/Windows runs on the exact SHA) requires a
  push and is the remaining open item of U7; not producible from this host.

## Skipped / deferred by environment or stage order

~~~text
U8e Ubuntu installed-UI acceptance      — VM gate (after Phase J), not this host
U9e Windows installed-UI acceptance     — VM gate (after Phase J)
U8i/U9i runners, U10 accessibility/visual, U11 coverage, U12 evidence
                                        — later roadmap stages, not started
tray-menu translation case              — QSKIP locally: qt6-l10n-tools needs
                                          sudo (apt install qt6-l10n-tools closes
                                          it); U7 installs LinguistTools in CI
Desktop UI CI evidence (green run on exact SHA) — requires U7 + push
~~~

## Gate status at this log entry (U1…U7 complete locally)

~~~text
Layer A: real-core-client 29/29 · role-list-model 11/11 · system-theme 5/5
         0 failed, 0 skipped, 0 QWARN
Layer B: ui-behavior 38/38 (0 QWARN) · tray-menu 8/8 (+1 documented
         translation QSKIP: LinguistTools needs sudo locally)
Layer C: e2e 4/4 · real-core-ui 6/6 · real-core-matrix 10/10 ·
         tray-live 4/4 — all on the self-owned hermetic core, 0 skipped
U6 gate: wrapper exit 1 ONLY on the translation QSKIP above
Core:    go test control/config/diagnostics/cmd — ok
Host safety: leshy.service untouched (ActiveEnterTimestamp unchanged)
git HEAD of this entry: dbaed73 (U1..U7 complete)
~~~

## Remaining open items (not closable on this host)

~~~text
tray-menu translation skip    sudo apt install qt6-l10n-tools, rerun wrapper
U7 CI evidence                push + green Linux/macOS/Windows runs on the
                              exact SHA (workflow_dispatch available)
U8e/U9e installed VM gates    after Phase J candidate artifacts
U8i/U9i runners, U10 accessibility/geometry/screenshots, U11 coverage,
U12 evidence closure          next roadmap stages
~~~
