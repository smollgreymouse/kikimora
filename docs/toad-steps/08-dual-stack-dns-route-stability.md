# Chapter 08 cross-platform regression — dual-stack, DNS ownership and route stability

Status: **MANDATORY PRODUCT CONTRACT / FIELD-REGRESSION INPUT**.

This packet records a real failure mode observed on the legacy Kikimora stack and turns it into acceptance criteria for the new Go/Toad runtime. It is not proof that the new runtime fails in the same way, and the legacy host measurements below are not new-runtime acceptance evidence.

The contract applies to:

- deferred Linux developer-workstation cutover in 08A.1 and observation in 08B;
- Windows native networking implementation in 08A.4a;
- Windows VM production-networking acceptance in 08A.4b;
- any future platform that claims real Kikimora routing/DNS ownership.

## Field regression that must not reappear

On the legacy workstation the physical link and ordinary IPv4 transport were healthy, but user-visible networking still stalled.

Observed together:

- Wi-Fi quality was healthy enough for the workload; router RTT was low and packet loss was not the primary fault;
- TCP retransmissions were low enough that they did not explain multi-second stalls;
- the legacy VPN/routing stack installed IPv6 split defaults `::/1` and `8000::/1` through a VPN interface with a ULA source;
- effective IPv6 traffic through that path was dead;
- DNS still returned AAAA answers;
- applications that did not recover quickly with Happy Eyeballs could wait for IPv6 connect timeouts;
- several competing default routes and several competing default-scope DNS sources were present;
- the legacy stack generated continuous route activity: roughly 70 host-route additions per minute in the observed sample, hundreds of IPv4/IPv6 host routes and hundreds of unreachable placeholders;
- legacy DNS integration had a history of periodic repair activity;
- moderate bufferbloat existed under heavy download, but it was a separate secondary effect and must not be used to explain away address-family or routing defects.

The product regression is therefore not merely "IPv6 ping fails". It is the combination of false family readiness, ambiguous DNS ownership and route churn on an otherwise usable physical network.

## 1. Address-family readiness is independent

IPv4 Ready never implies IPv6 Ready, and vice versa.

For each address family the platform/runtime must be able to answer separately:

- is a physical underlay path available;
- is the transport endpoint safely pinned through that underlay;
- does the selected Toad support that family for user traffic;
- is the managed interface configured with an appropriate address;
- is the selected route usable;
- is DNS allowed to expose answers that will use that family.

A role or aggregate state must not imply usable IPv6 merely because the interface is UP or because IPv4 validation succeeded.

## 2. No silent split-default capture by an unusable family

A protocol core or managed Toad must not install uncontrolled default or split-default routes.

In particular, `::/1 + 8000::/1` (and the IPv4 equivalent `0.0.0.0/1 + 128.0.0.0/1`) must never appear as an accidental side effect of starting a managed role.

If a future explicit full-tunnel feature intentionally owns a whole address family, it must have a separate family capability/readiness gate and real data-plane acceptance before those routes become active.

A family that is unsupported, degraded or waiting for underlay must not remain captured by a route that silently times out.

## 3. DNS A/AAAA answers must agree with routing safety

Returning an AAAA answer while the only effective IPv6 path is a silent dead tunnel is forbidden.

For a Kikimora-managed destination, before an A/AAAA answer is considered safely usable, one of these must already be true for that exact family:

1. a working selected route exists; or
2. a **fast-fail** deny exists; or
3. the Kikimora-owned DNS policy suppresses that family for the destination.

For this regression, a silent blackhole is not equivalent to fast-fail because it reproduces the application-timeout symptom. Linux currently uses an unreachable route for the fail-closed sentinel; Windows must provide equivalent prompt failure semantics.

The test must cover at least one application/client path that does not rely on Happy Eyeballs to mask the defect.

## 4. Physical endpoint routing must use a sane source

For every resolved VPN transport endpoint family, capture the effective kernel route and source address before starting/restarting the transport.

Require:

- endpoint route resolves through the intended physical underlay;
- the source belongs to that underlay and is valid for the family;
- a managed Toad interface is never selected recursively as its own endpoint underlay;
- an external VPN default/split-default cannot steal the managed endpoint because the endpoint exception policy wins first.

For IPv6, record whether the source is global, ULA or link-local and justify it. A ULA source sent toward an Internet endpoint through a physical ISP path is a failure.

## 5. DNS ownership must be deterministic and observable

The product must expose enough diagnostics to identify the effective DNS owner.

Acceptance must record:

- system/default DNS owner;
- per-interface DNS state;
- split-DNS/search-domain state where applicable;
- whether a legacy compatibility watcher is active;
- which component is permitted to repair DNS.

Multiple DNS servers are not inherently wrong, but multiple competing default scopes with no deterministic ownership contract are a failure.

On Linux, while `leshy-health-watch.service` remains as a compatibility component, workstation cutover must prove it does not fight NetworkManager/systemd-resolved or a Go owner and does not enter periodic repair loops.

On Windows, 08A.4a must define the owner explicitly (interface DNS, system DNS, NRPT or another native mechanism) and cleanup/recovery must be idempotent.

## 6. Stable desired state must converge to zero mutation

Repeated reconciliation of an unchanged desired state must perform zero kernel mutations after convergence.

Required deterministic regression:

1. establish a fixed underlay, fixed Toad state and fixed A/AAAA classification set;
2. reconcile once and record mutations;
3. replay the same observations/classifications repeatedly;
4. second and later passes must add/delete/replace **zero** routes/rules/DNS state;
5. change one input and prove only the minimal affected state changes;
6. replay that new state and again converge to zero mutation.

Runtime observation must separately flag:

- repeated add/delete of the same host route;
- duplicate endpoint rules;
- duplicate parking entries;
- route counts that grow without new unique classified destinations;
- periodic DNS repair with no observed drift;
- recovery/reconcile loops that wake continuously while producing no state change.

The legacy observation of about 70 host-route additions per minute on a stable host is a regression signature, not an acceptable steady state.

## 7. Required diagnostics counters

The new runtime should make churn diagnosable without parsing free-form logs.

Expose or record during acceptance, per platform where applicable:

- reconcile wakeups;
- reconcile passes with zero diff;
- route/rule adds, replaces and deletes by reason;
- DNS apply/repair count;
- underlay epoch changes and their reason;
- recovery actions by role;
- current owned route/rule count per family;
- current parking count per family.

These counters may live only in diagnostics rather than the stable public API, but they must be available to the acceptance harness.

## 8. Cross-platform acceptance probes

At every real-network acceptance baseline and after lifecycle recovery:

1. capture IPv4 and IPv6 default/effective paths separately;
2. resolve a public dual-stack hostname and record A and AAAA answers;
3. verify effective route/source for one A and one AAAA address;
4. verify the expected behavior of both families:
   - working family carries a real application probe;
   - intentionally unsupported/blocked family fails promptly and does not hang through a dead managed route;
5. capture managed routes/rules before and after repeated identical reconcile passes;
6. capture DNS ownership before and after recovery;
7. require no unexplained growth after a bounded soak.

If the test environment has no usable public IPv6, that is not a PASS for IPv6 transport. The expected product behavior in that environment must instead prove that Kikimora does not falsely publish/capture IPv6 and does not create long application timeouts from AAAA answers.

## 9. Linux workstation-specific gate

The new implementation/test work package is `08a3r-linux-dual-stack-dns-route-regression.md`. Its four required slices cover rootless Go model tests, privileged hermetic Linux netns, combined Leshy/DNS integration (including health-watch), and installed VM regression probes. This work is a prerequisite to any operator-authorized cutover, not an instruction to mutate the developer workstation.


The accepted Linux VM/package result remains valid, but it does not close this real-host regression because the developer workstation still runs the legacy routing/DNS environment.

Before any Linux workstation ownership cutover:

- capture the legacy host's current IPv4/IPv6 routes, rules and DNS scopes;
- record the old split-default/dead-family behavior if still present;
- install the side-by-side candidate without changing ownership;
- prove candidate inactivity does not mutate those routes/DNS;
- at the later explicit cutover, repeat this packet and require the legacy regression to disappear rather than merely preserving application connectivity.

Do not retire legacy rollback assets until 08B observation proves stable route/DNS ownership and no churn.

## 10. Windows-specific gate

08A.4a implementation must include family-aware route/DNS model tests for this packet.

08A.4b VM acceptance must include real dual-stack evidence when the lab has IPv6, or explicit no-false-IPv6 evidence when it does not.

The Windows release gate is not satisfied by IPv4-only `curl -4` probes plus a nominal IPv6 route table.

## Completion

This regression is closed on a platform only when:

- no dead managed family is advertised as usable;
- no accidental full/split default captures that family;
- DNS answers and fail-closed behavior cannot create silent multi-second dead-family stalls;
- DNS ownership is deterministic;
- unchanged desired state converges to zero route/DNS mutation;
- lifecycle recovery preserves those properties;
- evidence is recorded from the real target environment.
