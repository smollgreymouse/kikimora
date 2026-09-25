# Toad step 07F.2L — validate embedded orchestration predicates before sudo

Status: **HARNESS FIX IMPLEMENTED; FINAL PRIVILEGED CLOSURE RUN REQUIRED**.

Privileged baseline exposing this packet:
`6b43e9188a9c4d2df90ff86f81265426c8925ce8`.

Harness implementation commit:
`8b67a2d` — `test(toad): validate orchestration snapshot predicates`.

Purpose: prevent shell-embedded Python predicate mistakes from being discovered only after the full privileged kernel/protocol suite.

## Fresh privileged evidence at 6b43e91

The operator ran the authoritative suite on 2026-09-25 at approximately 09:30-09:32 +03:00.

Fresh same-run results:

- build-only PASS;
- route-parking PASS;
- multi-toad PASS;
- xray-interop PASS;
- both routed-endpoint preflights PASS;
- Phase A PASS;
- complete Phase B PASS;
- Phase C PASS;
- Phase D PASS;
- Phase E PASS;
- final privileged-runner cleanup completed;
- host state unchanged PASS.

Phase F printed a timeout failure, but the final core snapshot itself already satisfies the intended Phase F product contract:

- core aggregate state is Ready;
- AWG desired=true, state=Ready, route_ready=true, validated to current epoch;
- Xray desired=true, state=Ready, route_ready=true, validated to current epoch;
- OpenConnect desired=false and state=Stopped;
- no stale parking checkpoint recovery error remains.

This verifies the 07F.2K parking-checkpoint production behavior.

## Actual Phase F failure

The acceptance predicate contained:

```python
awg_ok=false
xray_ok=false
oc_stopped=false
```

inside a `python3 -c` program.

Python requires `False`, not `false`. Each polling attempt therefore failed with `NameError` before evaluating the snapshot.

Earlier harness hardening intentionally suppressed intermediate polling traceback spam, so the typo appeared externally as a normal Phase F timeout even though the final product snapshot was correct.

## Fix

The Phase F predicate now uses proper Python booleans:

```python
awg_ok=False
xray_ok=False
oc_stopped=False
```

## New deterministic harness regression

`linux/tests/toad/check-orchestration-predicates.py` now scans every `mpf_wait_snapshot` embedded Python block in `go-orchestration-acceptance.sh` before any privileged run.

It:

- discovers all embedded snapshot predicates;
- models shell-variable expansion enough to syntax-check the resulting Python;
- compiles every predicate;
- tokenizes every predicate and rejects lowercase Python pseudo-constants such as `true`, `false`, and `none` when they occur as Python identifiers rather than text inside strings.

Current result:

```text
orchestration snapshot predicates: PASS (12 blocks)
```

The checker is part of `linux/tests/toad/privileged-regressions-model.sh`, which is itself run by `run-rootless.sh model`.

Therefore this exact class of acceptance-harness defect can no longer reach the privileged runner while the local model gate is green.

## Non-privileged verification at 8b67a2d

PASS:

- orchestration embedded-predicate checker;
- privileged-regressions-model.sh;
- `run-rootless.sh model`;
- bash syntax;
- shellcheck;
- `git diff --check`.

No production Go behavior changed in this packet.

## Required final privileged closure

Run:

`bash run-privileged-gates.sh`

The expected result is now:

- route-parking PASS;
- multi-toad PASS;
- orchestration phases A-F PASS;
- `=== ALL PHASES PASSED ===`;
- xray-interop PASS;
- cleanup PASS;
- host state unchanged PASS.

If that run is green, 07F.2 can be closed and only then should the roadmap transition to evaluating 08A.
