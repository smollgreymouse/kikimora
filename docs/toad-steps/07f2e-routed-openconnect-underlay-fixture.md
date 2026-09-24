# Toad step 07F.2E — routed OpenConnect endpoint under synthetic underlay

Status: **FIXTURE FIX IMPLEMENTED; PRIVILEGED VERIFICATION REQUIRED**.

Privileged baseline exposing this packet:
`db20949df277400735669e349ab1fed575b53d22`.

Fixture implementation commit:
`6dbe86a` — `test(toad): route OpenConnect endpoint through synthetic underlay`.

Purpose: fix the orchestration fixture topology so a real OpenConnect replacement
can reach its real ocserv endpoint through whichever synthetic physical underlay
the production endpoint-policy layer currently selects.

## Fresh privileged evidence at db20949

The operator reran:

```bash
bash run-privileged-gates.sh
```

Fresh artifacts were written at approximately 16:29-16:31 +03:00 on
2026-09-24.

Results:

- route-parking: PASS;
- multi-toad: PASS;
- xray-interop: PASS;
- orchestration Phase 3: PASS;
- Phase A: PASS;
- Phase 6 AWG endpoint non-recursion: PASS;
- Phase B AWG outage remains fail-closed and recoverable exactly as required;
- the 07F.2D duplicate recovery replay is gone;
- OpenConnect remains in `Starting` with:
  - `recovery.step=start-transport`;
  - `recovery.attempt=1`;
  - `last_error=Toad restart started; awaiting replacement generation`;
- no second Quiesce/Restart transaction appears;
- AWG itself recovers to Ready/current epoch after primary restoration;
- orchestration still times out before Phase C because the OpenConnect
  replacement never creates a ready managed TUN/control plane.

This verifies the 07F.2D state-machine fix. The remaining failure is a separate
fixture reachability defect.

## Root cause

Production endpoint policy intentionally routes each VPN transport endpoint
through the current canonical physical underlay.

The orchestration fixture previously created three independent point-to-point
links:

```text
client <-> AWG server namespace   192.0.2.0/30
client <-> Xray server namespace  198.51.100.0/30
client <-> OC server namespace    203.0.113.0/30
```

and then reused the AWG and Xray protocol-server links as synthetic primary and
backup physical underlays.

That was sufficient for initial process startup because the OpenConnect endpoint
`203.0.113.2` is directly connected before endpoint-policy recovery matters.

After a material underlay transition, however, production `ApplyEndpoint`
correctly installs the OpenConnect transport endpoint through the canonical
underlay gateway. The replacement therefore attempts to reach
`203.0.113.2` via either:

```text
192.0.2.2  (primary synthetic underlay)
198.51.100.2 (backup synthetic underlay)
```

Neither gateway namespace previously had any route to the OpenConnect server
namespace. The replacement process therefore remained in legitimate bounded
startup waiting for `kk-oc0`, while the test waited for Ready.

This is a fixture topology defect, not a reason to weaken production endpoint
binding.

## Fix

The shared orchestration fixture now creates an isolated transit namespace with
three extra point-to-point links:

```text
AWG gateway ns  198.18.0.1/30
      |
      | 198.18.0.2/30
   transit ns
      | 198.18.0.6/30
Xray gateway ns 198.18.0.5/30

   transit ns
      | 198.18.0.10/30
OC server ns    198.18.0.9/30
```

Forwarding and static routes are deliberately narrow:

- AWG gateway may route only the OpenConnect endpoint network
  `203.0.113.0/30` into the transit;
- Xray gateway may route only the OpenConnect endpoint network into the transit;
- the OC server has return routes to the AWG/Xray client-underlay source
  networks through transit;
- the transit namespace knows all three directly attached fixture networks.

Critically, AWG and Xray endpoint networks are **not** cross-routed through the
other gateway.

Therefore Phase B keeps its existing safety meaning:

```text
AWG physical link down
    -> AWG endpoint still unreachable through backup Xray
    -> stable AWG TUN remains fail-closed
```

while OpenConnect replacement gains the reachability that production endpoint
policy requires:

```text
OC endpoint via primary AWG synthetic underlay  -> reachable
OC endpoint via backup Xray synthetic underlay  -> reachable
```

No NAT and no public network are introduced.

## New preflight proof

Before core startup, the orchestration gate now temporarily installs an explicit
`203.0.113.2/32` route through each synthetic underlay in turn and pings the
real OpenConnect server endpoint.

The directly connected OC fixture route therefore cannot hide a broken transit
topology.

Expected marker:

```text
OpenConnect endpoint routed-underlay fixture: primary+backup PASS
```

The temporary host route is removed before core startup.

## Non-privileged verification

PASS:

```text
bash -n linux/tests/toad/go-orchestration-acceptance.sh        linux/tests/toad/lib/multi-protocol-fixture.sh

shellcheck -S warning        linux/tests/toad/go-orchestration-acceptance.sh        linux/tests/toad/lib/multi-protocol-fixture.sh

git diff --check
bash linux/tests/toad/service-cutover.sh
bash linux/tests/toad/orchestration-cutover.sh
bash linux/tests/toad/run-rootless.sh model
```

Production Go code is unchanged by this packet.

## Required privileged verification

Run:

```bash
bash run-privileged-gates.sh
```

Required evidence:

- routed OpenConnect endpoint preflight primary+backup PASS;
- route-parking PASS;
- multi-toad PASS;
- orchestration Phase A-F PASS;
- 07F.2C AWG fail-closed Phase B semantics remain unchanged;
- OpenConnect replacement reaches Ready/current epoch after canonical underlay
  transition;
- xray-interop PASS;
- final cleanup PASS;
- host-state before/after PASS.

Do not start 08A until the entire 07F.2 privileged suite is green.
