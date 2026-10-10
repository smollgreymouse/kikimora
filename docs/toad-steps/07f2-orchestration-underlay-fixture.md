# Toad step 07F.2 — orchestration underlay fixture closure

Status: **COMPLETE — ALL FOUR PRIVILEGED GATES GREEN ON 846335d; KERNEL ACCEPTANCE REMAINS OPERATOR-ONLY**.

Reviewed implementation HEAD: `578852a471f5938a76818b0c9fb99a2130f475bf`.

Purpose: close the last local blocker before 08A without weakening AWG health or replacing real protocol fixtures with mocks.

Implementation note: the synthetic primary/backup underlay helpers, Phase B transition logic, non-recursive endpoint assertions, Leshy test zones, and service-cutover assertion repair landed in `d7f2cce8ce27a5fbc5c2f87d23cdee932e845592` **before** the required rootless harness existed.

07F.2A is closed as a capability split: deterministic/model work stays rootless, while the current executor cannot create mapped user namespaces. Real kernel/network evidence therefore comes only from the operator privileged runner. Fix only failures demonstrated by those fresh artifacts.
All executor gates are local. Do not query or wait for GitHub Actions.

Prerequisite: `07f2a-rootless-hermetic-test-runner.md` final split must be respected. Executor commands must not use sudo; operator kernel acceptance uses repository-root `run-privileged-gates.sh`.

---

## Root-cause finding from code audit

The current `go-orchestration-acceptance.sh` failure is **not proven to be a veth keepalive artifact**.

The core underlay contract explains the observed failure:

- Linux `underlay.DefaultSnapshot()` delegates to `netlink.Snapshotter`;
- `Snapshotter.defaultPath()` only accepts a main-table default route (`0.0.0.0/0` or `::/0`);
- the shared multi-protocol netns fixture creates only connected `/30` routes and no default route;
- `Manager.scheduleValidation()` explicitly refuses validation when `underlay.Epoch == 0` or both IPv4/IPv6 paths are nil;
- standalone `multi-toad` does not require core underlay authority, so it can pass in the same route-free topology;
- core-managed orchestration can therefore remain `Starting` until the initially observed AWG handshake becomes stale after 30 s.

The pre-core command currently added to `go-orchestration-acceptance.sh`:

```bash
ip netns exec "$MPF_CLIENT_NS" ping -c 1 ... "$MPF_AWG_SERVER_IP"
```

cannot refresh an AWG handshake because the AWG client/Toad has not been started yet; it only pings the physical veth endpoint.

Do not change `recentHandshakeWindow`, AWG health semantics, or production core logic to hide this fixture defect.

---

# Phase 1 — add an isolated synthetic default-underlay for core tests

File:

`linux/tests/toad/lib/multi-protocol-fixture.sh`

Add helpers used only by core-managed orchestration acceptance. Do not change the standalone `multi-toad` no-default-route contract.

Suggested helpers:

```bash
mpf_enable_core_underlay()
mpf_switch_core_underlay_to_backup()
mpf_restore_core_underlay_primary()
mpf_assert_core_underlay_routes()
```

## 1.1 Initial synthetic underlay

For `go-orchestration-acceptance` only, install two isolated defaults in the client namespace after the veth topology exists:

```text
primary: via AWG server underlay IP on AWG client veth, metric 100
backup:  via Xray server underlay IP on Xray client veth, metric 200
```

Example shape:

```bash
ip -n "$MPF_CLIENT_NS" route replace default \
  via "$MPF_AWG_SERVER_IP" dev "$MPF_AWG_CLIENT_VETH" metric 100
ip -n "$MPF_CLIENT_NS" route replace default \
  via "$MPF_XR_SERVER_IP" dev "$MPF_XR_CLIENT_VETH" metric 200
```

These routes are **not Internet access**:

- both gateways terminate in isolated test namespaces;
- there is no NAT/public route;
- protocol endpoints remain covered by their more-specific connected `/30` routes.

Do not add forwarding/NAT to the server namespaces.

## 1.2 Preserve standalone isolation

`multi-toad-interop.sh`, `xray-interop.sh`, AWG standalone gates, etc. must retain their existing no-default-route isolation unless they explicitly need core underlay authority.

Do not globally add a default route in `mpf_setup_namespaces()`.

Only the core orchestration fixture calls `mpf_enable_core_underlay()`.

---

# Phase 2 — remove the invalid AWG ping workaround

File:

`linux/tests/toad/go-orchestration-acceptance.sh`

Delete:

- the comment claiming veth stalls AWG rekey;
- the pre-core ping to `$MPF_AWG_SERVER_IP`;
- the fixed `sleep 2` workaround.

Replace them with:

1. `mpf_enable_core_underlay`;
2. explicit route assertions;
3. core startup;
4. wait for a canonical underlay snapshot before starting roles.

---

# Phase 3 — prove core sees the intended underlay before ConnectAll

After core socket appears but before `mpf_core_connect_all`, wait for the core snapshot to satisfy:

- `underlay.epoch > 0`;
- IPv4 path is non-null;
- interface equals `$MPF_AWG_CLIENT_VETH`;
- gateway equals `$MPF_AWG_SERVER_IP` when gateway is exposed in JSON;
- preferred source equals the client address on that veth when exposed.

Also record/assert kernel route selection:

```bash
ip -n "$MPF_CLIENT_NS" route get "$MPF_AWG_SERVER_IP"
ip -n "$MPF_CLIENT_NS" route get "$MPF_XR_SERVER_IP"
ip -n "$MPF_CLIENT_NS" route get "$MPF_OC_SERVER_IP"
```

Every protocol endpoint must resolve to its physical veth/connected path, never to `kk-awg0`, `kk-xray0` or `kk-oc0`.

If the core underlay is still empty, stop and dump:

- main table routes;
- links/addresses;
- core status JSON.

Do not continue to AWG health debugging until underlay is proven valid.

---

# Phase 4 — Phase A must reach Ready before handshake staleness

Run normal `mpf_core_connect_all` only after Phase 3 succeeds.

Require all desired roles to reach `Ready` at the current nonzero underlay epoch.

For AWG specifically require:

- `route_ready=true`;
- recent handshake / connected session;
- validation completed before the 30-second handshake window can expire;
- endpoint route to `$MPF_AWG_SERVER_IP` still resolves through `$MPF_AWG_CLIENT_VETH`;
- no production health threshold was widened.

If AWG is still not Ready with a proven nonzero underlay:

capture before changing production code:

- full redacted core status;
- `ip -4 rule show`;
- `ip -4 route show table all`;
- `ip route get $MPF_AWG_SERVER_IP`;
- AWG client health fields from Toad/core snapshots;
- redacted reference-server UAPI;
- timestamps of first handshake and each health transition.

Then determine whether endpoint routing changes after validation/publication. Do not label it a veth artifact without this evidence.

---

# Phase 5 — make Phase B mutate canonical underlay explicitly

The current Phase B only brings the AWG veth down. With a default route present, link state alone is not a sufficient deterministic semantic change because a route entry may remain visible.

Change Phase B to:

1. record initial epoch, PIDs/generations and TUN ifindices;
2. remove the primary default route;
3. bring the AWG underlay veth down;
4. require core underlay to converge to the backup Xray veth/default route and epoch to advance;
5. require AWG selected traffic fail-closed while its physical endpoint is unavailable;
6. Xray/OpenConnect may transiently revalidate/recover for the new epoch, but their process/TUN identities must not be recreated merely because the canonical underlay changed;
7. restore AWG veth;
8. restore primary metric-100 default;
9. require another canonical convergence and eventual Ready/current-epoch state.

Do not require unrelated roles to remain literally `Ready` at every instant if the architecture correctly revalidates them on epoch change. Require:

- no Failed terminal state;
- no unnecessary process/TUN recreation;
- eventual Ready at current epoch.

---

# Phase 6 — keep full-tunnel AWG coverage

The shared fixture currently uses:

```toml
allowed_ips = ["0.0.0.0/0", "::/0"]
```

Do not immediately shrink this to a payload `/32`; full-tunnel configuration is valuable for endpoint-exception/routing acceptance.

After Phase A reaches Ready, assert again:

```bash
ip -n "$MPF_CLIENT_NS" route get "$MPF_AWG_SERVER_IP"
```

The AWG transport endpoint must still resolve through the physical underlay veth, never recursively through `kk-awg0`.

If this assertion fails, that is a real endpoint-routing defect. Fix endpoint policy/routing; do not weaken the fixture.

---

# Phase 7 — repair the stale service-cutover packaging assertions

File:

`linux/tests/toad/service-cutover.sh`

Current test still greps implementation details from `linux/package.sh`, but `linux/package.sh` is now intentionally a thin wrapper around canonical `packaging/linux/*`.

Update the test contract:

- assert `linux/package.sh` delegates to canonical `packaging/linux/build-release.sh`;
- assert `desktop/packaging/build-release.sh` delegates to the same canonical builder;
- move payload-content assertions to either:
  - `packaging/linux/stage-release.sh` static contract, or preferably
  - existing `desktop/tests/test_packaging.sh` artifact-content checks;
- do not require wrapper scripts to contain literal `kikimora-core.service`, ownership config, provider install loops, or NetworkManager paths.

`service-cutover.sh` should test service/ownership/cutover semantics, not duplicate the package payload implementation.

Run it locally and require PASS.

---

# Phase 8 — local acceptance

Run locally:

```bash
cd toad
test -z "$(gofmt -l .)"
go test ./...
go test -race ./...
go vet ./...
cd ..

bash linux/tests/toad/service-cutover.sh
bash linux/tests/toad/orchestration-cutover.sh
bash desktop/tests/test_packaging.sh

bash linux/tests/toad/run-rootless.sh model
bash linux/tests/toad/run-rootless.sh probe   # diagnostic only; exit 77 is acceptable in restricted executor
```

The authoritative real kernel/network gates are run together by the operator:

```bash
bash run-privileged-gates.sh
```

That suite contains route-parking, multi-toad, orchestration-acceptance and the existing hermetic official-Xray interop. No external Xray server is required.

Update `test-report.txt` with the **actual final HEAD**, not the previous implementation SHA.

Record:

- core underlay snapshot before ConnectAll;
- default-route primary/backup identities;
- endpoint `route get` results;
- Phase A-F orchestration acceptance result;
- service-cutover result;
- package artifact/SHA from 07F.1.

---

# Completion

07F.2 is complete only when:

1. service-cutover is green;
2. core sees nonzero canonical underlay in orchestration fixture before role startup;
3. orchestration-acceptance phases A-F pass;
4. AWG endpoint route is proven non-recursive under full-tunnel allowed-IPs;
5. route-parking, multi-toad and hermetic Xray remain green;
6. no AWG health semantics were weakened;
7. `test-report.txt` contains final local evidence.

Then:

- close remaining 07E privileged evidence;
- mark 07F-Linux complete;
- Linux 08A may become the next operator-gated packet for real AmneziaWG + OpenConnect.

Do not run 08A in this packet.

Do not run real suspend/resume.

Do not run legacy retirement.

---

# Executor report

Put full evidence in `test-report.txt`; chat response may be short:

```text
HEAD before:
HEAD after:

fixture underlay:
- initial core underlay:
- backup transition:
- endpoint route non-recursive:

local gates:
- service-cutover:
- route-parking:
- multi-toad:
- xray-interop:
- orchestration-acceptance A-F:

07F Linux artifact:
- filename:
- SHA256:

Full evidence: test-report.txt

Unresolved:
- none OR exact failing phase + diagnostics
```
