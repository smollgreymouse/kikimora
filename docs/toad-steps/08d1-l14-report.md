# 08D1 L14 — AuthorityOnDemand + targeted reload (in progress)

Status: **REJECTED BY INDEPENDENT AUDIT / RESCUE REQUIRED.**

The implementation was pushed to Leshy master before its own exit gates were
green. At audited Leshy SHA
`6b70c07ba609d8b6d11f8c1749b56cfb1c0b6db3`, src/routing/mod.rs does not
parse because required reload tests were partially commented out. Required
GitHub CI is red.

Do not continue L15 from this report. Execute:

`docs/toad-steps/08d1-post-l11-rescue-plan.md`

starting at R1. L14 may become accepted only after rescue R1-R7 and the exact
required GitHub gates are green.

## Implemented

- `RouteInvalidation::AuthorityLost` added; `Full` is documented as a
  local-kernel-only signal that the managed backend never emits.
- `RouteRegistry::mark_authority_lost()` dirties every desired managed entry
  while preserving desired intent/ownership and without touching `full_epoch`
  or calling the backend.
- `RouteManager` dispatch now follows `ReconcileMode`:
  - `KernelSnapshot`: existing mark-dirty + snapshot reconcile + audit;
  - `AuthorityOnDemand`: `Zone(z)` and `AuthorityLost` only mark local state
    dirty, zero snapshot/RPC; other kinds increment a test-visible invariant
    counter and are logged, never snapshot;
  - `RegistryOnly`: unchanged (no invented audit).
- Subscription pump emits `AuthorityLost` on socket loss/malformed frames/
  failed reconnects, keeps 1s→30s backoff, and now carries the per-role
  generation map across reconnects so an unchanged controller dirties nothing.
- Snapshot invalidations no longer emit `Full`; unattributable generation
  changes emit `AuthorityLost`.
- Managed `snapshot()` has a test-visible invocation counter; the invariant
  test asserts it stays zero.
- Reload: `routing_backend`, `kikimora_socket`, `route_aggregation_prefix`
  and `max_dynamic_routes` are restart-only with the spec error text.
- Reload invalidation is targeted: `reconcile_routes_after_reload(&[String])`
  applies one `Zone(name)` per sorted/deduped Changed/Added/Removed zone and
  never emits `Full`.
- Registered Add intents now use the backend-resolved target, so the first
  aggregate plan and later covered-address hits produce the same intent (this
  also makes the managed synthetic-controller target consistent).

## New tests

- 1000 Verified managed intents + `AuthorityLost`: 1000 Dirty, zero Ensure,
  zero snapshot.
- One demand → one Ensure; repeated demand → zero extra RPC.
- 32 concurrent same-key demands → one Ensure.
- Zone invalidation dirties only that zone; unrelated zone stays zero-RPC.
- Managed `Full` is an invariant: no snapshot, no dirtying, no RPC.
- Managed `for_server` selects `AuthorityOnDemand`, never `PlatformBackend`.
- Reconnect with unchanged generations emits no invalidation.

## Remaining for L14

- Confirm the routing/concurrency tests green.
- Add/verify reload boundary tests (restart-only rejection + two-zone
  targeted reload counters), including the managed zero-controller-call case.
- Run fmt, clippy `-D warnings`, full `cargo test --all-features`.
- Commit/push Leshy and update this report.
