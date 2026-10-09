# 08A.3R — Linux dual-stack, DNS and route-stability regression

Status: **PLANNED / MANDATORY BEFORE DEVELOPER-WORKSTATION CUTOVER**. This is a post-acceptance regression supplement, not a cancellation of Linux 08A.2/08A.3 PASS evidence. Work in a disposable Linux VM/netns; do not mutate the developer workstation or activate side-by-side ownership without explicit operator approval.

Parent product contract: `08-dual-stack-dns-route-stability.md`.

## Purpose and actual gap

The accepted Linux VM evidence establishes installed AWG/OpenConnect transport, service/recovery and packaging, but does not prove the absence of legacy-host-like blackholes and mutation loops when IPv4 works, IPv6 does not, DNS serves AAAA, and Leshy keeps reconciling. The Go route manager and the remaining legacy Leshy DNS/domain route-owner must both be covered. A stable Go table alone is not evidence that the combined product is quiet.

## Slice 1 — deterministic Go unit/model tests (no root)

Extend or add tests alongside `toad/internal/platform/linux/netlink/{routes,snapshot}_test.go`, `toad/internal/endpoint/`, `toad/internal/parking/` and orchestration tests as warranted by the actual ownership path. Do not force DNS policy tests into Go modules that do not own DNS.

Acceptance cases:

1. IPv4 readiness cannot imply IPv6 readiness; IPv6-only, IPv4-only, both, neither and underlay transition are distinct cases.
2. A selected role must not accidentally add `::/1` + `8000::/1` (or IPv4 `0.0.0.0/1` + `128.0.0.0/1`), including protocol backend startup and recovery. Intentional full tunnel would require a separate family-specific capability and data-plane gate.
3. Endpoint exceptions resolve through the physical underlay, never through their own Toad or an externally owned VPN. Source selection must test global/ULA/link-local, IPv4-mapped and missing-address cases. Specifically, do not mistake a syntactically valid ULA for a publicly routable Internet source.
4. Unavailable selected family: a valid fast-fail reject/unreachable or controlled family suppression; no silent blackhole. Verify independently of Happy Eyeballs.
5. Instrument the actual route/rule/DNS mutation boundary or a recording adapter. Fixed snapshots + classifications => initial convergence then **zero attempted kernel mutations** on subsequent identical reconciles. A one-input change mutates only affected state, and re-converges. Testing identical final route tables alone is insufficient.
6. Verify endpoint-rule deduplication, parking deduplication, no unintended ownership transfer, and no perpetual recovery wakeups under stable conditions.

Keep the tests deterministic. Avoid timing-based claims of no churn when a call-count/operation log can prove it.

## Slice 2 — privileged hermetic Linux netns integration

Add a dedicated case to the existing `linux/tests/toad/` privileged runner (suggested name `dualstack-dns-routing-netns.sh`) and register it in the appropriate existing test gate; do not create an unrelated runner.

Construct controlled fake uplink(s), managed Toad, optional externally owned `vpn0`, and a dual-stack DNS fixture. Verify:

- working IPv4 and deliberately unroutable IPv6 with an AAAA reply; a non-Happy-Eyeballs application/connect probe must fail promptly, not after a multi-second TCP timeout;
- disabled/unsupported IPv6 does not create an accidental managed split default and cannot be published Ready because IPv4 is healthy;
- `ip -4 route get`, `ip -6 route get`, rules and effective source prove the managed endpoint is pinned to the physical underlay, including when an external VPN has a lower-metric default;
- fail-closed IPv6 unreachable produces prompt failure while leaving unrelated physical and corporate routes untouched;
- after repeated identical reconcile, netlink mutation instrumentation reports zero calls and route/rule state has not grown;
- underlay loss/return, endpoint address-family changes, and selected-role recovery preserve family separation and idempotent cleanup.

Use explicit namespace-local fixtures. No public services, workstation mutations or operator credentials in hermetic tests.

## Slice 3 — combined Leshy/DNS integration

Inspect the actual Linux ownership boundary before coding: Leshy still owns DNS/domain classification and its legacy compatibility watcher may remain active. Add integration assertions for the **combined** Leshy + Go runtime, rather than assuming Go route-manager stability covers Leshy.

- Verify A/AAAA classification and effective route/deny per family; a returned AAAA with dead selected IPv6 may not create an application timeout through a managed blackhole.
- Assert deterministic ownership across Leshy, NetworkManager and systemd-resolved (system/default, split DNS, per-interface scopes).
- With unchanged DNS answers and classification, repeated refresh/health checks cause no route/rule/DNS rewrites or re-addition of the same `/128` destinations.
- Exercise DNS owner change, service restart, underlay loss/resume, and removal/recreation of owned resources; no competing writers, endless repair, or stale DNS scopes.
- Include `leshy-health-watch.service` when installed; separately record whether it is present, enabled and invoked. No unexplained periodic DNS repair.
- Record counters or bounded instrumented mutation events for reconcile wakeups, zero-diff passes, route/rule add/replace/delete, DNS apply/repair, underlay epoch changes, and owned/parked route counts per family.

If the production stack cannot expose those diagnostics, make the minimum observable instrumentation a dependency; do not treat a quiet log as proof.

## Slice 4 — installed Linux VM regression acceptance

Run on disposable Ubuntu VM against the actual candidate installed package and installed Leshy/DNS integration, not arbitrary source-tree binaries. Preserve prior 08A.2/08A.3 PASS records. A packaging lifecycle rerun is necessary only if the fix alters packaging/install contract; relevant functionality and recovery tests must be rerun after fixes.

Capture before/after and post-recovery:

1. `ip -4/-6 route`, `ip -4/-6 rule`, `ip -4/-6 route get <endpoint-or-resolved-address>` and source; managed `/1` and `/128` counts, owner attribution;
2. `resolvectl status`, NetworkManager DNS state, Leshy configuration and watcher status;
3. A/AAAA of a controlled dual-stack fixture, `curl -4/-6` or equivalent and a non-Happy-Eyeballs TCP probe with bounded failure timing;
4. real AWG + OpenConnect application probes and independent per-family Ready/unavailable evidence;
5. unchanged-state repetitive reconcile instrumentation, plus bounded soak and route/rule/DNS mutation counts;
6. recovery after interface link/DHCP change, core/Toad death and at least one real suspend/resume, repeating family, source and DNS checks.

The VM may have no public IPv6. In that case prove **no false IPv6 capture and prompt failure**; do not report IPv6 transport PASS. A separate real IPv6 path is needed before claiming IPv6 traffic works.

## Exit / handoff

- All four slices have evidence, with identified command/test logs and exact tested package/build revision.
- Every family-specific path is either operational, deterministically suppressed by Kikimora-owned policy, or fails promptly; no dead managed tunnel hijacks AAAA traffic.
- Repeat desired-state inputs produce zero kernel/DNS mutation after convergence, including Leshy route classification; counters distinguish unchanged wakeups from actual writes.
- No unauthorized mutation of external VPN or workstation state.
- Only after this packet passes may deferred `08A.1` workstation staging/cutover seek *separate* explicit operator approval; `08B` observation/retirement still remains separate.

Do not call Linux fully deployment-accepted before this regression is closed.
