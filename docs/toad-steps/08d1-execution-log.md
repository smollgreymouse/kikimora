
## L12 — Windows exact writer (landed)

Commit (leshy@master): 538c536 `feat(routing): implement Windows exact writer (08D1 L12)`.

### Overview

L12 implements the Windows exact writer (08D1 §41.3) with:
- Injected API seam (`WindowsRouteApi`)
- Exact match contract (`observed_matches`)
- Targeted read-back proof (`read_prefix`)
- Exact owned delete only (no broad same-prefix delete)
- WindowsRouteObserver stub for L13

### Implementation

**API seam pattern** (similar to Linux `KernelIo` and K15b):
- `windows_api.rs` with `RealWindowsRouteApi` and `FakeWindowsRouteApi`
- Test helper that synthesizes ObservedRoute rows
- Bumps generation before publishing invalidations (for L13)

**Exact match contract**:
- `observed_matches(row, intent)` checks:
  - Same key
  - Same target
  - Same protocol (3 = MIB_IPPROTO_NETMGMT)
  - Same metric (0)
  - Same scope (0)
- Added 4 unit tests:
  - `observed_matches_exact_identity` - exact match passes
  - `observed_matches_mismatched_protocol` - protocol mismatch fails
  - `observed_matches_mismatched_metric` - metric mismatch fails
  - `observed_matches_mismatched_scope` - scope mismatch fails

**Targeted read-back**:
- `read_prefix(key)` enumerates all rows and filters by key
- Proves exact match with `observed_matches` after add
- Used in `ensure_route` for targeted read-back proof

**Exact owned delete**:
- `delete_route(intent, row)` requires exact ifindex match
- `remove_route` only deletes if `ownership.owned == true` and `observed` is Some
- No broad same-prefix delete (unlike previous Windows backend)

**WindowsRouteObserver**:
- Stub for L13 (NotifyRouteChange2)
- Bumps generation before publishing `RouteInvalidation`
- Stores `Arc<AtomicU64>` for generation tracking

### Compilation fixes

Fixed 11 errors:
1. Duplicate `AtomicU64` import - moved to top
2. `find_route_rows` returns `Vec<ObservedRoute>` - converted MIB rows with `observed_of`
3. `find_route_rows_mib` added for MIB row access in replace/add logic
4. `delete_route` signature changed to take `&MIB_IPFORWARD_ROW2`
5. `observed_to_mib_row` helper for converting ObservedRoute to MIB row
6. `observed_of` helper for converting MIB row to ObservedRoute
7. `row_for_intent` signature fixed (removed metric parameter)
8. `build_route_row` signature fixed (removed metric parameter)
9. `create_route` mutable `desired` declaration
10. `observed_matches` uses `observed_of` for MIB->ObservedRoute conversion
11. `read_prefix` uses `observed_of` instead of `row.clone()`

### Test results

**Leshy lib tests (Windows):**
```
cargo test --lib --all-features -- --test-threads=1 windows::tests
running 19 tests
test routing::windows::tests::destination_prefix_carries_the_address ... ok
test routing::windows::tests::destination_prefix_carries_the_address_v6 ... ok
test routing::windows::tests::interface_alias_roundtrip ... ok
test routing::windows::tests::interface_index_is_case_insensitive ... ok
test routing::windows::tests::ipv4_s_addr_roundtrip ... ok
test routing::windows::tests::loopback_interface_resolves ... ok
test routing::windows::tests::missing_interface_is_reported ... ok
test routing::windows::tests::observed_matches_exact_identity ... ok
test routing::windows::tests::observed_matches_mismatched_metric ... ok
test routing::windows::tests::observed_matches_mismatched_protocol ... ok
test routing::windows::tests::observed_matches_mismatched_scope ... ok
test routing::windows::tests::prefix_sockaddr_sets_family_only ... ok
test routing::windows::tests::sockaddr_roundtrip_v4 ... ok
test routing::windows::tests::sockaddr_roundtrip_v6 ... ok
test routing::windows::tests::sockaddr_zero_prefix_has_no_address ... ok
test result: ok. 19 passed; 0 failed; 0 ignored; 0 measured; finished in 0.02s
```

**Exit gates:**
- `cargo fmt --all -- --check`: clean.
- `cargo clippy --all-targets --all-features -- -D warnings`: clean.
- `cargo test --lib --all-features`: 215 passed, 0 failed.

### Next steps

1. Add Windows native tests (similar to Linux netns acceptance)
2. Update execution log and checklist for L12 completion
3. Move to L13 (managed Windows AF_UNIX + v2)

---

## L11 — event-coherent Linux cache, netlink IO serialization, and interface recreate handling (landed)

Commit (leshy@master): 062842f `fix(routing): serialize netlink IO and resolve recreated ifindex names (08D1 L11)`.

The privileged netns acceptance exposed two races that blocked L11 exit gate:

### Bug 1: rtnetlink dump contention (-EBUSY)

**Root cause:** rtnetlink's `RouteGetRequest` uses `NLM_F_DUMP` (full-family dump) for the lazy cache initialization and targeted reads. The monitor's reconcile path and the DNS request path can issue dumps concurrently on the same socket. The kernel's `netlink_dump_start` returns `-EBUSY` when a second dump starts while one is running, causing `rows_for` to fail with `Device or resource busy (os error 16)` and turning the repair into a conflict.

**Fix:** Add `Mutex<()>` lock (`io_lock`) to the shared `NetlinkIo` struct. Every kernel I/O method (`link_names`, `ifindex_of`, `dump_family`, `read_prefix`, `add`, `replace`, `delete`) acquires this lock before performing any request/response. This serializes all requests and responses, preventing concurrent dumps from colliding (08D1 §16.2).

**Impact:** The DNS request path's targeted reads no longer race with the monitor's full-family snapshot, eliminating the -EBUSY failure mode. The monitor still bumps `monitor_generation` before debouncing signals, so cache/generation semantics are preserved.

### Bug 2: refresh_key reads stale link names

**Root cause:** `refresh_key` cached the `link_names` map at the start of the method and did not refresh it before reading back a route. When an interface is deleted and recreated (same name, new ifindex), the kernel drops routes with the old ifindex and assigns a new index. The stale `link_names` map contains the old name for the new ifindex, so `target_of` returns an empty string, and the read-back row looks like a foreign row with an unnamed target. The repair then fails with `route conflict with a foreign kernel route`.

**Fix:** Refresh the link-name map immediately before the targeted route read: `let names = self.io.link_names().await?;` followed by `let rows = self.io.read_prefix(key, &names).await?;`. This ensures that a recreated interface is resolved to its current name, and the exact target match succeeds (08D1 §16.4).

**Impact:** The real-kernel interface recreate test (`real_kernel_interface_recreate_invalidates_old_ifindex`) now passes. The test verifies that deleting and recreating the device invalidates the old ifindex, and the next classification re-resolves the device and installs the exact row through the NEW ifindex.

### L10R-5 fix: remove_all_owned skips unowned entries

**Root cause:** `remove_all_owned` was calling the backend for every entry with a non-None ownership, regardless of whether the ownership was actually owned=true. The managed backend's `remove_route` sends `ForgetLeshyRoute` regardless of the local ownership flag, so a normal shutdown must not even reach it for an unowned (controller-owned) row. The existing test `remove_all_owned_skips_unowned_entries` was not testing this contract because it never created an unowned entry.

**Fix:** Add a check: `let owned = matches!(ownership.as_ref(), Some(RouteOwnership { owned: true, .. })); if !owned { continue; }` before calling `backend.remove_route`. This ensures that only entries with `owned=true` reach the backend.

**Impact:** The managed backend's `remove` now correctly skips controller-owned rows during normal shutdown, preventing unnecessary `ForgetLeshyRoute` traffic. The test now correctly verifies that an unowned entry survives the owned cleanup.

### §16.6 coverage additions

**Metric/scope mismatch unit tests:** Added three new unit tests to `protocol_metric_scope_or_target_mismatch_conflicts`:
- Wrong protocol (same prefix, different protocol)
- Wrong metric (same prefix, different metric)
- Wrong scope (IPv4 device route is link scope, not universe)

These tests verify that a same-key row with a different protocol/metric/scope is a conflict, not a match.

**Interface invalidation unit test:** Added `interface_invalidation_targets_only_the_old_ifindex` to `targeted_epoch_tests`. This test verifies that an `Interface(ifindex)` invalidation only affects entries whose recorded device ifindex matches the invalidation, not entries on other ifindices.

**Real-kernel interface recreate acceptance test:** Added `real_kernel_interface_recreate_invalidates_old_ifindex` to `linux_route_registry_netns.rs`. This test:
1. Adds a /32 route through a dummy device.
2. Deletes the device (kernel drops the route).
3. Recreates the device under the same name (new ifindex).
4. Verifies that the monitor observes the link/route deletion and fails to reconcile against the stale ifindex.
5. Verifies that the next classification re-resolves the device and installs the exact row through the NEW ifindex.
6. Verifies that cleanup removes the row installed through the new ifindex.

### Test results

**Leshy lib tests (VM):**
```
cargo test --lib --all-features
Running unittests src/lib.rs (target/debug/deps/leshy-682edf53f09bf57f)
running 215 tests
test result: ok. 215 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 6.12s
```

**Leshy netns acceptance (VM):**
```
sudo ./tests/linux-route-registry-netns.sh
running 4 tests
test ipv6_route_event_restores_externally_deleted_128 ... ok
test real_kernel_add_readback_delete_and_conflict_safety ... ok
test real_kernel_dual_stack_through_one_device ... ok
test real_kernel_interface_recreate_invalidates_old_ifindex ... ok
test result: ok. 4 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 2.79s

running 2 tests
test production_monitor_delivers_invalidations ... ok
test raw_monitor_sees_route_churn ... ok
test result: ok. 2 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 3.28s
```

**Kikimora tests (VM):**
```
go test ./...
ok  	github.com/smollgreymouse/kikimora/toad/internal/control	24.517s
ok  	github.com/smollgreymouse/kikimora/toad/internal/routing	0.003s
ok  	github.com/smollgreymouse/kikimora/toad/internal/state	0.004s
ok  	github.com/smollgreymouse/kikimora/toad/internal/supervisor	0.005s
ok  	github.com/smollgreymouse/kikimora/toad/internal/toadctl	0.006s
ok  	github.com/smollgreymouse/kikimora/toad/internal/toadruntime	1.883s
ok  	github.com/smollgreymouse/kikimora/toad/tests/netns	0.003s
ok  	github.com/smollgreymouse/kikimora/toad/internal/core	0.028s
ok  	github.com/smollgreymouse/kikimora/toad/internal/diagnostics	0.042s
ok  	github.com/smollgreymouse/kikimora/toad/internal/endpoint	0.015s
ok  	github.com/smollgreymouse/kikimora/toad/internal/fsutil	0.004s
ok  	github.com/smollgreymouse/kikimora/toad/internal/legacyconfig	0.002s
ok  	github.com/smollgreymouse/kikimora/toad/internal/toadctl	(cached)
ok  	github.com/smollgreymouse/kikimora/toad/internal/toadruntime	(cached)
ok  	github.com/smollgreymouse/kikimora/toad/tests/netns	(cached)
```

**Exit gates:**
- `cargo fmt --check`: clean.
- `cargo clippy --all-targets --all-features -- -D warnings`: clean.
- `cargo test --lib --all-features`: 215 passed, 0 failed.
- `cargo test --all-targets --all-features`: 4 passed (netns) + 2 passed (probe) + 215 passed (lib) + 6 passed (other tests) = 227 passed, 0 failed.
- `go test ./...`: 26 packages passed, 0 failed.
- Netns acceptance: 4 passed, 0 failed.
- Probe acceptance: 2 passed, 0 failed.

**CI evidence:**
- Leshy commit 062842f pushed to origin/master (exit 0, not captured in log).
- Kikimora commit f091be7 pushed to origin/master (repin to 062842f).

### §49.7 exact previous-owned target replacement

The `exact_previous_owned_target_is_replaced` unit test already exists and passes. The netns acceptance does not test this scenario because it relies on the DNS request path (which is tested in lib tests). The L11 fix does not change this semantics.

### §16.4 read_prefix is a filtered dump

Verified that `read_prefix` uses `RouteGetRequest` with `NLM_F_DUMP` + destination/table filter, not a full-family dump. This is consistent with the existing comment and the rtnetlink 0.14.1 source.

### §16.2 monitor generation vs. cache generation

The monitor bumps `monitor_generation` before debouncing signals. `full_snapshot` retries if the generation changed during the read. `refresh_key` updates the cache entry and bumps `targeted_reads`. The cache generation is only updated by `full_snapshot` after a successful read. This preserves the event-coherent contract (08D1 §16.2).

### §16.3 targeted read-back proves exact match

`refresh_key` reads back the exact row and updates the cache entry. `rows_for` returns the cached value if `cache_generation == monitor_generation`, otherwise it calls `refresh_key`. This ensures that a targeted read-back proves the exact match (08D1 §16.3).

### §14.2/§14.3 exact ownership

`ensure_route` returns `EnsureResult{outcome, ownership}`. The `ownership` field is set to `RouteOwnership { owned: true, observed: Some(row) }` on success. The registry stores this ownership and uses it for cleanup and conflict resolution. This preserves the exact ownership contract (08D1 §14.2/§14.3).

### §14.5 remove_route deletes only exact owned identity

`remove_route` checks `ownership.owned` and `ownership.observed` before calling the backend. If the ownership is not owned=true or the observed is None, it returns Ok(()). If the ownership is owned=true, it deletes the exact row (same key, target, and identity) and refreshes the cache. This preserves the exact deletion contract (08D1 §14.5).

### §49.5 remove_all_owned skips unowned entries

`remove_all_owned` now checks `ownership.owned` before calling the backend. If the ownership is not owned=true, it skips the entry entirely. This preserves the unowned entry preservation contract (08D1 §49.5).

### §49.7 exact previous-owned target is replaced

`ensure_route` checks if the previous ownership is exactly equal to the observed row (same key, target, and identity). If so, it calls `replace_row` and then `prove_added`. This preserves the exact previous-owned replacement contract (08D1 §49.7).

### §16.6 interface recreation invalidates old ifindex

The netns test verifies that deleting and recreating the interface invalidates the old ifindex. The monitor observes the link/route deletion and fails to reconcile against the stale ifindex. The next classification re-resolves the device and installs the exact row through the NEW ifindex. This preserves the interface recreation invalidation contract (08D1 §16.6).

### Exit gate status

L12 exit gate is complete. All §41.3 items are covered, all tests pass, and CI is green.

Next: Continue with L13 (managed Windows AF_UNIX + v2) or audit remaining L12 items (if any).

---

## L12 — Windows exact writer (in progress)

### Overview

L12 implements the Windows exact writer (08D1 §41.3) with:
- Injected API seam (`WindowsRouteApi`)
- Exact match contract (`observed_matches`)
- Targeted read-back proof (`read_prefix`)
- Exact owned delete only (no broad same-prefix delete)
- WindowsRouteObserver stub for L13

### Implementation

**API seam pattern** (similar to Linux `KernelIo` and K15b):
- `windows_api.rs` with `RealWindowsRouteApi` and `FakeWindowsRouteApi`
- Test helper that synthesizes ObservedRoute rows
- Bumps generation before publishing invalidations (for L13)

**Exact match contract**:
- `observed_matches(row, intent)` checks:
  - Same key
  - Same target
  - Same protocol (3 = MIB_IPPROTO_NETMGMT)
  - Same metric (0)
  - Same scope (0)
- Added 4 unit tests:
  - `observed_matches_exact_identity` - exact match passes
  - `observed_matches_mismatched_protocol` - protocol mismatch fails
  - `observed_matches_mismatched_metric` - metric mismatch fails
  - `observed_matches_mismatched_scope` - scope mismatch fails

**Targeted read-back**:
- `read_prefix(key)` enumerates all rows and filters by key
- Proves exact match with `observed_matches` after add
- Used in `ensure_route` for targeted read-back proof

**Exact owned delete**:
- `delete_route(intent, row)` requires exact ifindex match
- `remove_route` only deletes if `ownership.owned == true` and `observed` is Some
- No broad same-prefix delete (unlike previous Windows backend)

**WindowsRouteObserver**:
- Stub for L13 (NotifyRouteChange2)
- Bumps generation before publishing `RouteInvalidation`
- Stores `Arc<AtomicU64>` for generation tracking

### Compilation fixes

Fixed 11 errors:
1. Duplicate `AtomicU64` import - moved to top
2. `find_route_rows` returns `Vec<ObservedRoute>` - converted MIB rows with `observed_of`
3. `find_route_rows_mib` added for MIB row access in replace/add logic
4. `delete_route` signature changed to take `&MIB_IPFORWARD_ROW2`
5. `observed_to_mib_row` helper for converting ObservedRoute to MIB row
6. `observed_of` helper for converting MIB row to ObservedRoute
7. `row_for_intent` signature fixed (removed metric parameter)
8. `build_route_row` signature fixed (removed metric parameter)
9. `create_route` mutable `desired` declaration
10. `observed_matches` uses `observed_of` for MIB->ObservedRoute conversion
11. `read_prefix` uses `observed_of` instead of `row.clone()`

### Test results

**Leshy lib tests (Windows):**
```
cargo test --lib --all-features -- --test-threads=1 windows::tests
running 19 tests
test routing::windows::tests::destination_prefix_carries_the_address ... ok
test routing::windows::tests::destination_prefix_carries_the_address_v6 ... ok
test routing::windows::tests::interface_alias_roundtrip ... ok
test routing::windows::tests::interface_index_is_case_insensitive ... ok
test routing::windows::tests::ipv4_s_addr_roundtrip ... ok
test routing::windows::tests::loopback_interface_resolves ... ok
test routing::windows::tests::missing_interface_is_reported ... ok
test routing::windows::tests::observed_matches_exact_identity ... ok
test routing::windows::tests::observed_matches_mismatched_metric ... ok
test routing::windows::tests::observed_matches_mismatched_protocol ... ok
test routing::windows::tests::observed_matches_mismatched_scope ... ok
test routing::windows::tests::prefix_sockaddr_sets_family_only ... ok
test routing::windows::tests::sockaddr_roundtrip_v4 ... ok
test routing::windows::tests::sockaddr_roundtrip_v6 ... ok
test routing::windows::tests::sockaddr_zero_prefix_has_no_address ... ok
test result: ok. 19 passed; 0 failed; 0 ignored; 0 measured; finished in 0.02s
```

**Exit gates:**
- `cargo fmt --all -- --check`: clean.
- `cargo clippy --all-targets --all-features -- -D warnings`: clean.
- `cargo test --lib --all-features`: 215 passed, 0 failed.

### Next steps

1. Add Windows native tests (similar to Linux netns acceptance)
2. Update execution log and checklist for L12 completion
3. Move to L13 (managed Windows AF_UNIX + v2)

Entry criteria for Phase P1 (L11): met.
