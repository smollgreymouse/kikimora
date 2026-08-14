#!/usr/bin/env bash
set -Eeuo pipefail

[[ ${EUID} -eq 0 ]] || {
  printf 'SKIP: route-parking-netns requires root\n'
  exit 0
}

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly ROOT
readonly LIFECYCLE="${1:-${ROOT}/linux/files/route-lifecycle}"
readonly NS="kikimora-parking-$$"
tmp="$(mktemp -d)"
cleanup() {
  ip netns del "$NS" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT

mkdir -p "$tmp/runtime" "$tmp/state"

run_lifecycle() {
  ip netns exec "$NS" env \
    PATH="/usr/sbin:/usr/bin:/sbin:/bin" \
    KIKIMORA_VPN_RUNTIME_DIR="$tmp/runtime" \
    KIKIMORA_ROUTE_LIFECYCLE_STATE_DIR="$tmp/state" \
    KIKIMORA_ROUTE_PARKING_METRIC=42760 \
    "$LIFECYCLE" "$@"
}

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  printf '%s\n' '--- main routes ---' >&2
  ip -n "$NS" -4 route show table main >&2 2>/dev/null || true
  printf '%s\n' '--- lifecycle state ---' >&2
  find "$tmp/state" -maxdepth 1 -type f -print -exec sh -c 'printf "  "; cat "$1"' _ {} \; >&2 2>/dev/null || true
  exit 1
}

ip netns add "$NS"
ip -n "$NS" link set lo up
ip -n "$NS" link add uplink0 type dummy
ip -n "$NS" link set uplink0 up
ip -n "$NS" addr add 192.0.2.2/24 dev uplink0
ip -n "$NS" route add default via 192.0.2.1 dev uplink0 onlink metric 600

ip -n "$NS" link add vpn0 type dummy
ip -n "$NS" link set vpn0 up
ip -n "$NS" addr add 10.0.0.81/24 dev vpn0
printf 'vpn0\n' > "$tmp/runtime/secondary.dev"

# This route predates Leshy and therefore belongs to the VPN/user baseline. It
# must never become a Kikimora parking route.
ip -n "$NS" route add 198.51.100.10/32 dev vpn0 proto static
run_lifecycle snapshot >/dev/null

grep -Fqx $'vpn0\t198.51.100.10/32' "$tmp/state/before.routes" || \
  fail 'baseline route was not captured'

# Simulate two routes created later by Leshy. Observation records only routes
# absent from the startup baseline.
ip -n "$NS" route add 203.0.113.10/32 dev vpn0 proto static
ip -n "$NS" route add 203.0.113.11/32 dev vpn0 proto static
run_lifecycle observe-device vpn0 >/dev/null

grep -Fqx $'vpn0\t203.0.113.10/32' "$tmp/state/owned.routes" || \
  fail 'first Leshy route was not observed'
grep -Fqx $'vpn0\t203.0.113.11/32' "$tmp/state/owned.routes" || \
  fail 'second Leshy route was not observed'
if grep -Fq '198.51.100.10/32' "$tmp/state/owned.routes"; then
  fail 'baseline route was incorrectly claimed as Leshy-owned'
fi

# A route removed while the VPN is still healthy must disappear from the
# ownership cache. It must not be resurrected as a stale park on a later drop.
ip -n "$NS" route del 203.0.113.11/32 dev vpn0
run_lifecycle observe-device vpn0 >/dev/null
if grep -Fq '203.0.113.11/32' "$tmp/state/owned.routes"; then
  fail 'stale Leshy route survived a healthy observation'
fi

# Simulate a hard VPN disappearance. The kernel removes all dev routes before
# cleanup-device runs, so parking must use the last observation rather than the
# already-empty device route table.
rm -f -- "$tmp/runtime/secondary.dev"
ip -n "$NS" link del vpn0
run_lifecycle cleanup-device vpn0 >/dev/null

ip -n "$NS" -4 route show 203.0.113.10/32 | grep -Eq '^unreachable 203\.0\.113\.10( |$)' || \
  fail 'owned destination was not parked as unreachable'
grep -Fqx $'vpn0\t203.0.113.10/32' "$tmp/state/parked.routes" || \
  fail 'parked route state was not recorded'

if ip -n "$NS" -4 route show 203.0.113.11/32 | grep -q .; then
  fail 'stale removed destination was parked'
fi
if ip -n "$NS" -4 route show 198.51.100.10/32 | grep -q .; then
  fail 'baseline destination was parked'
fi

if ip netns exec "$NS" ip -4 route get 203.0.113.10 >/dev/null 2>&1; then
  fail 'parked destination leaked through the ordinary default route'
fi
ip -n "$NS" -4 route get 203.0.113.11 | grep -Fq 'dev uplink0' || \
  fail 'non-owned destination unexpectedly stopped following the default route'

# Internal Leshy restarts must preserve the fail-closed route. This is the path
# used by route-watch when a VPN comes back and Leshy needs its route cache
# rebuilt.
run_lifecycle prepare-restart >/dev/null
run_lifecycle cleanup >/dev/null
[[ -e "$tmp/state/restart.pending" ]] || \
  fail 'internal restart marker disappeared during service cleanup'
ip -n "$NS" -4 route show 203.0.113.10/32 | grep -Eq '^unreachable 203\.0\.113\.10( |$)' || \
  fail 'parking did not survive internal service cleanup'

# Recreate the VPN and start a fresh lifecycle snapshot. Snapshot clears the
# restart marker but intentionally leaves the park until a real route exists.
ip -n "$NS" link add vpn0 type dummy
ip -n "$NS" link set vpn0 up
ip -n "$NS" addr add 10.0.0.81/24 dev vpn0
printf 'vpn0\n' > "$tmp/runtime/secondary.dev"
run_lifecycle snapshot >/dev/null
[[ ! -e "$tmp/state/restart.pending" ]] || \
  fail 'restart marker survived the new lifecycle snapshot'
ip -n "$NS" -4 route show 203.0.113.10/32 | grep -Eq '^unreachable 203\.0\.113\.10( |$)' || \
  fail 'snapshot removed parking before route recovery'

# This is the critical kernel property: Leshy must be able to install a normal
# lower-metric /32 while the high-metric unreachable fallback still exists.
ip -n "$NS" route add 203.0.113.10/32 dev vpn0 proto static
ip -n "$NS" -4 route get 203.0.113.10 | grep -Fq 'dev vpn0' || \
  fail 'restored VPN route did not win over the parked fallback'

# The next observation confirms recovery and only then deletes the fallback.
run_lifecycle observe-device vpn0 >/dev/null
if grep -Fq '203.0.113.10/32' "$tmp/state/parked.routes"; then
  fail 'confirmed restored route remained parked'
fi
if ip -n "$NS" -4 route show 203.0.113.10/32 | grep -q '^unreachable '; then
  fail 'unreachable fallback remained after restored route observation'
fi
ip -n "$NS" -4 route get 203.0.113.10 | grep -Fq 'dev vpn0' || \
  fail 'real VPN route disappeared when parking was released'

# Ordinary service shutdown restores unmanaged routing semantics: Leshy-created
# routes and all parking state are removed.
run_lifecycle cleanup >/dev/null
[[ ! -e "$tmp/state" ]] || fail 'ordinary lifecycle cleanup left runtime state behind'
ip -n "$NS" -4 route get 203.0.113.10 | grep -Fq 'dev uplink0' || \
  fail 'ordinary cleanup did not restore the default route path'

printf 'Leshy route parking network namespace regression: OK\n'
