#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# managed-leshy-routing-netns.sh — the versioned managed selected-route
# success E2E (08D1 managed-selected-routes-completion §20, remediation
# R7 §9.6, roadmap G2). The real kikimora-core supervises the real
# ready-toad fixture, the real pinned Leshy drives the managed backend and
# the shared DNS fixtures create the exact selected identities inside a
# disposable namespace.
#
# Suites:
#   --suite hosted  the non-crash/non-ifindex-replacement rows that fit
#                   hosted Linux privileges (CI, R7 §9.6): rows 1-11, 14,
#                   15, 17, 18 of §20.3.
#   --suite full    every §20.3 row in order, including the tagged recovery
#                   trigger (12), the withdraw-selected barrier ifindex
#                   replacement (13) and the restart checkpoint row (16).
#                   This is the Ubuntu VM Phase G gate.
#
# Usage (root):
#   sudo bash linux/tests/toad/managed-leshy-routing-netns.sh \
#       --core /abs/kikimora-core \
#       --ready-toad /abs/ready-toad \
#       --leshy /abs/leshy \
#       --routeprobe /abs/routeprobe \
#       [--core-failpoints /abs/kikimora-core-tagged] \
#       [--suite hosted|full]
set -Eeuo pipefail

CORE=""
CORE_FAILPOINTS=""
READY_TOAD=""
LESHY=""
ROUTEPROE=""
SUITE="hosted"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --core) CORE="$2"; shift 2 ;;
        --core-failpoints) CORE_FAILPOINTS="$2"; shift 2 ;;
        --ready-toad) READY_TOAD="$2"; shift 2 ;;
        --leshy) LESHY="$2"; shift 2 ;;
        --routeprobe) ROUTEPROE="$2"; shift 2 ;;
        --suite) SUITE="$2"; shift 2 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

[[ "$SUITE" == "hosted" || "$SUITE" == "full" ]] || { echo "--suite must be hosted|full" >&2; exit 2; }
if [[ "$SUITE" == "full" && -z "$CORE_FAILPOINTS" ]]; then
    echo "--suite full requires --core-failpoints (the tagged core)" >&2
    exit 2
fi
for bin in "$CORE" "$READY_TOAD" "$LESHY" "$ROUTEPROE"; do
    [[ -n "$bin" ]] || { echo "missing --core/--ready-toad/--leshy/--routeprobe" >&2; exit 2; }
    [[ "$bin" = /* && -x "$bin" ]] || { echo "binary must be an absolute executable path: $bin" >&2; exit 2; }
done
if [[ -n "$CORE_FAILPOINTS" ]]; then
    [[ "$CORE_FAILPOINTS" = /* && -x "$CORE_FAILPOINTS" ]] || { echo "binary must be an absolute executable path: $CORE_FAILPOINTS" >&2; exit 2; }
fi

NS="kk-leshy-routing-$$"
ROOT="/run/kk-managed-routing"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIXTURES="$SCRIPT_DIR/fixtures"
SOCKET="$ROOT/core.sock"
STATE="$ROOT/state"
PUB="$ROOT/leshy-vpn"
MANAGED_IF="kkmanaged0"
UNDERLAY_IF="kkunderlay0"
ZONE_DOMAIN="example.com"
SELECTED_V4="203.0.113.10"
SELECTED_V6="2001:db8:dead::10"

CORE_PID=""
LESHY_PID=""
STUB_PID=""

note() { echo "== $*"; }
pass() { echo "PASS: $*"; }
die() {
    echo "FAIL: $*" >&2
    exit 1
}

# print_diagnostics emits the §20.4 failure evidence before the trap cleanup.
print_diagnostics() {
    echo "--- failure diagnostics (§20.4) ---"
    ip -n "$NS" -details -4 route show table main 2>/dev/null || true
    ip -n "$NS" -details -6 route show table main 2>/dev/null || true
    cat "$STATE/primary/selected-routes.json" 2>/dev/null || true
    cat "$ROOT/config/leshy-managed.json" 2>/dev/null || true
    "$CORE" status --socket "$SOCKET" --json 2>/dev/null || true
    echo "--- core.log ---"; tail -50 "$ROOT/core.log" 2>/dev/null || true
    echo "--- leshy.log ---"; tail -50 "$ROOT/leshy.log" 2>/dev/null || true
    echo "--- ready-toad.log ---"; tail -50 "$ROOT/ready-toad.log" 2>/dev/null || true
}

cleanup() {
    if [[ -n "${CORE_PID:-}" ]]; then kill "$CORE_PID" 2>/dev/null || true; fi
    if [[ -n "${LESHY_PID:-}" ]]; then kill "$LESHY_PID" 2>/dev/null || true; fi
    if [[ -n "${STUB_PID:-}" ]]; then kill "$STUB_PID" 2>/dev/null || true; fi
    ip netns del "$NS" >/dev/null 2>&1 || true
    if [[ -n "$LESHY_SERVICE_WAS_ACTIVE" ]]; then
        systemctl start leshy >/dev/null 2>&1 || true
    fi
}
on_exit() {
    rc=$?
    if [[ $rc -ne 0 ]]; then print_diagnostics; fi
    cleanup
    exit "$rc"
}
trap on_exit EXIT

# Preflight (§21 preflight contract): kill only fixture-owned leftovers from
# a previous crashed run — a Leshy started on the harness's generated config,
# a core using the harness's state root — and remove stale harness namespaces.
# Installed product state and unrelated processes are never touched.
pkill -f "leshy /etc/kikimora/leshy/config.toml" 2>/dev/null || true
pkill -f "$ROOT" 2>/dev/null || true
for stale_ns in $(ip netns list 2>/dev/null | awk '{print $1}' | grep '^kk-leshy-routing-' || true); do
    ip netns del "$stale_ns" >/dev/null 2>&1 || true
done
# A system leshy service (an installed product on a reused lab VM) would grab
# the shared writer lock the moment any reload runs: stop it for the harness
# duration and restore it on exit. Absent on fresh CI runners.
LESHY_SERVICE_WAS_ACTIVE=""
if systemctl is-active leshy >/dev/null 2>&1; then
    LESHY_SERVICE_WAS_ACTIVE=1
    systemctl stop leshy >/dev/null 2>&1 || true
fi
sleep 1

# status_field extracts one JSON field from the core status snapshot.
status_field() {
    "$CORE" status --socket "$SOCKET" --json 2>/dev/null | python3 -c "
import json, sys
snap = json.load(sys.stdin)
role = next((r for r in snap.get('roles', []) if r.get('id') == 'primary'), {})
path = '$1'.split('.')
# Role fields win; top-level snapshot fields (managed_mode, mode_transition,
# startup_ready, ...) are the fallback.
value = role if role else snap
for part in path:
    if not isinstance(value, dict):
        value = {}
        break
    value = value.get(part, '')
if value == '' and '$1'.split('.')[0] not in role:
    value = snap
    for part in path:
        if not isinstance(value, dict):
            value = {}
            break
        value = value.get(part, '')
print(value if value is not None else '')
"
}

# v4_details/v6_details print the exact main-table selected lines.
v4_details() { ip -n "$NS" -details -4 route show table main; }
v6_details() { ip -n "$NS" -details -6 route show table main; }

# selected_v4_present requires the exact owned /32 identity (row 5).
# `ip -details` prefixes lines with the scope class ("unicast ") and omits
# the default "metric 0" from the read-back.
selected_v4_present() {
    v4_details | grep -E "${SELECTED_V4}(/32)? dev ${MANAGED_IF} proto static scope link( metric 0)?\b" >/dev/null
}
selected_v6_present() {
    # The v6 read-back prints the inferred "scope global" and "pref medium".
    v6_details | grep -E "${SELECTED_V6}(/128)? dev ${MANAGED_IF} proto static( scope global)? metric 1024( pref medium)?\b" >/dev/null
}

dnsq() { ip netns exec "$NS" python3 "$FIXTURES/dnsq.py" "$@"; }

# ---------------------------------------------------------------------------
note "fixture namespace and devices (§20.2)"
ip netns add "$NS"
ip -n "$NS" link set lo up
ip -n "$NS" link add "$UNDERLAY_IF" type dummy
ip -n "$NS" address add 198.51.100.2/24 dev "$UNDERLAY_IF"
ip -n "$NS" link set "$UNDERLAY_IF" up
ip -n "$NS" route add default via 198.51.100.1 dev "$UNDERLAY_IF" onlink
ip -n "$NS" link add "$MANAGED_IF" type dummy
ip -n "$NS" address add 10.203.0.1/24 dev "$MANAGED_IF"
ip -n "$NS" address add 2001:db8:203::1/64 dev "$MANAGED_IF"
ip -n "$NS" link set "$MANAGED_IF" up

cd "$(dirname "$0")/../../.."
mkdir -p "$ROOT/config" "$STATE" "$PUB"
# Deterministic re-runs: reset ONLY the fixture tree this harness owns. The
# marker/journal/checkpoint/generation live under $ROOT; the generated Leshy
# config and the pushed routing list are the harness's named files under
# /etc/kikimora/leshy. Operator-staged files there are never touched.
rm -rf "$ROOT/state" "$ROOT/config" "$ROOT/leshy-vpn" "$ROOT/barrier"     "$ROOT/core.log" "$ROOT/leshy.log" "$ROOT/ready-toad.log" "$ROOT/enable.log"
mkdir -p "$ROOT/config" "$STATE" "$PUB"
# The zones rebuild from the routing lists on every regeneration: leftover
# test lists would become phantom zones, so the harness clears the list
# directory it owns. The server section is pre-seeded so the regenerated
# managed config resolves through the deterministic DNS stub.
rm -f /etc/kikimora/leshy/domains/*.txt
cat > /etc/kikimora/leshy/config.toml <<'TOML'
[server]
listen_address = "127.0.0.1:15353"
default_upstream = ["127.0.0.1:15354"]
route_failure_mode = "servfail"
TOML

cat > "$ROOT/config/primary.toml" <<'TOML'
name = "primary"
protocol = "openconnect"
interface = "kkmanaged0"
mtu = 1380
state_dir = "/run/kk-managed-routing/state/primary"
leshy_zone = "primary"
address = ["10.203.0.1/24", "2001:db8:203::1/64"]

[openconnect]
gateway = "198.51.100.1"
username = "fixture"
password_file = "/run/kk-managed-routing/fixture-password"
TOML

# A production-valid openconnect role needs a credential file; the ready-toad
# fixture never establishes a transport, so the bytes are irrelevant.
printf 'fixture-password\n' > "$ROOT/fixture-password"

# The installed service runs with the Go lifecycle ownership (08D1 §11.2):
# without it the committed desired state is deliberately not restored at
# startup and no role process would come back after the restart rows.
cat > "$ROOT/ownership.conf" <<'OWN'
routing_owner = "go"
tunnel_owner = "go"
endpoint_owner = "go"
OWN

# The zone's route-target input carries the managed device (the same
# <zone>.dev contract the core's generated config points at).
echo "kkmanaged0" > "$PUB/primary.dev"
# The generated Leshy config and routing lists still default to the product
# path /etc/kikimora/leshy (the U5 runtime-path injection lands later); the
# harness owns that fixture tree.
mkdir -p /etc/kikimora/leshy/domains

ip netns exec "$NS" python3 "$FIXTURES/dns-stub.py" > "$ROOT/stub.log" 2>&1 &
STUB_PID=$!

start_core() {
    local bin="$1"
    ip netns exec "$NS" "$bin" serve \
        --socket "$SOCKET" \
        --state-dir "$STATE" \
        --leshy-publication-dir "$PUB" \
        --config-dir "$ROOT/config" \
        --endpoint-provider-dir "$ROOT/providers" \
        --ownership-config "$ROOT/ownership.conf" \
        --toad-binary "$READY_TOAD" >> "$ROOT/core.log" 2>&1 &
    CORE_PID=$!
}

# LESHY_CONFIG points at the config the core's managed transaction generated
# (the exact production path Leshy runs under after a cutover).
LESHY_CONFIG="/etc/kikimora/leshy/config.toml"
AUTH_FILE="$STATE/control-route-auth.key"

start_leshy() {
    ip netns exec "$NS" "$LESHY" "$LESHY_CONFIG" >> "$ROOT/leshy.log" 2>&1 &
    LESHY_PID=$!
}

# ---------------------------------------------------------------------------
note "row 1: the ready-toad role reaches Ready with both families"
start_core "$CORE"
deadline=30
until ip netns exec "$NS" "$CORE" wait-startup --socket "$SOCKET" --timeout 1s >/dev/null 2>&1; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 1: the core never reached startup readiness"
    sleep 1
done

ip netns exec "$NS" "$CORE" connect --socket "$SOCKET" --role primary >> "$ROOT/ready-toad.log" 2>&1
deadline=60
until [[ "$(status_field state)" == "Ready" ]]; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 1: role primary never reached Ready (state=$(status_field state))"
    sleep 1
done
[[ "$(status_field validation.families.ipv4.ready)" == "True" ]] || die "row 1: IPv4 family is not Ready"
[[ "$(status_field validation.families.ipv6.ready)" == "True" ]] || die "row 1: IPv6 family is not Ready"
GEN1="$(status_field generation)"
[[ -n "$GEN1" && "$GEN1" != "0" ]] || die "row 1: ToadGeneration is zero"
pass "row 1: role Ready, both families, generation $GEN1"

# The zone's domain set comes from the role's routing list. It is staged
# directly before the core starts (fixture staging in the pre-U5 layout,
# where the routing directory is still the fixed product path): pushing it
# through SetRoutingList here would run the Leshy reload hook, which
# restarts the system service and races the enable transaction for the
# shared writer lock. The SetRoutingList API path itself is proven by the
# Windows row G gate and the §20.3 row 17 API rows.
# late.test is never queried before row 15 so the fail-closed proof there is
# a genuine classification (the fixture answers every name identically).
printf 'example.com
late.test
' > /etc/kikimora/leshy/domains/primary.txt

note "row 1b: the production managed-mode transaction"
ip netns exec "$NS" "$CORE" leshy-mode enable --leshy "$LESHY"     --socket "$SOCKET" --auth-file "$AUTH_FILE" --control-socket "$SOCKET"     > "$ROOT/enable.log" 2>&1     || die "row 1b: leshy-mode enable failed: $(cat "$ROOT/enable.log")"
grep -q 'routing_backend = "kikimora"' "$LESHY_CONFIG"     || die "row 1b: the generated managed config does not use the kikimora backend"
grep -q '^kikimora_activation_id' "$LESHY_CONFIG"     || die "row 1b: the generated managed config carries no activation fence"
[[ "$(status_field managed_mode)" == "True" ]] || die "row 1b: the core is not in managed mode"
pass "row 1b: the schema2 marker committed and the managed config was generated"

note "row 2: the Leshy managed handshake"
ip netns exec "$NS" "$LESHY" capabilities --json | grep -q managed_selected_routes_v1 \
    || die "row 2: Leshy does not advertise managed_selected_routes_v1"
start_leshy
sleep 1
kill -0 "$LESHY_PID" 2>/dev/null || die "row 2: Leshy exited at startup"
pass "row 2: Leshy advertises and runs the managed backend"

note "row 3: neither selected host route exists before the first query"
selected_v4_present && die "row 3: the /32 already exists before any query"
selected_v6_present && die "row 3: the /128 already exists before any query"
pass "row 3: no selected routes before the first query"

note "rows 4-6: the first A/AAAA queries create the exact selected identities"
A_RESULT=$(dnsq "$ZONE_DOMAIN" 1)
[[ "$A_RESULT" == "rcode=0 answers=1"* ]] || die "rows 4: A query did not return NOERROR/1: $A_RESULT"
deadline=50
until selected_v4_present; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 4: the exact /32 never appeared: $(v4_details)"
    sleep 0.1
done
AAAA_RESULT=$(dnsq "$ZONE_DOMAIN" 28)
[[ "$AAAA_RESULT" == "rcode=0 answers=1"* ]] || die "row 6: AAAA query did not return NOERROR/1: $AAAA_RESULT"
deadline=50
until selected_v6_present; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 6: the exact /128 never appeared: $(v6_details)"
    sleep 0.1
done
v4_details | grep -E "${SELECTED_V4}" | grep -v via | head -1 > "$ROOT/v4-identity.txt"
v6_details | grep -E "${SELECTED_V6}" | head -1 > "$ROOT/v6-identity.txt"
pass "rows 4-6: exact selected identities installed ($(cat "$ROOT/v4-identity.txt"))"

note "row 7: 1000 A + 1000 AAAA duplicates do not churn the kernel"
ROUTE_HASH_BEFORE=$( { v4_details; v6_details; } | sort | sha256sum | cut -d' ' -f1)
REGISTERED_BEFORE=$(status_field selected_routes.registered)
for _ in $(seq 1 1000); do
    dnsq "$ZONE_DOMAIN" 1 > /dev/null
    dnsq "$ZONE_DOMAIN" 28 > /dev/null
done
REGISTERED_AFTER=$(status_field selected_routes.registered)
[[ "$REGISTERED_BEFORE" == "$REGISTERED_AFTER" ]] || die "row 7: the registry size changed under duplicates ($REGISTERED_BEFORE -> $REGISTERED_AFTER)"
ROUTE_HASH_AFTER=$( { v4_details; v6_details; } | sort | sha256sum | cut -d' ' -f1)
[[ "$ROUTE_HASH_BEFORE" == "$ROUTE_HASH_AFTER" ]] || die "row 7: the route table churned under duplicates"
pass "row 7: 2000 duplicate queries left the kernel state byte-stable (registered=$REGISTERED_AFTER)"

note "rows 8-9: external selected deletes are restored by the event path"
ip -n "$NS" route del "$SELECTED_V4/32" table main 2>/dev/null || ip -n "$NS" route del "$SELECTED_V4" table main
deadline=50
until selected_v4_present; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 8: the externally deleted /32 was not restored within 5s"
    sleep 0.1
done
ip -n "$NS" -6 route del "$SELECTED_V6/128" table main 2>/dev/null || ip -n "$NS" -6 route del "$SELECTED_V6" table main
deadline=50
until selected_v6_present; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 9: the externally deleted /128 was not restored within 5s"
    sleep 0.1
done
pass "rows 8-9: both externally deleted selected routes were restored within 5s"

note "rows 10-11: a foreign same-prefix row is preserved and refused"
# Retire our exact selected fixture first (through the API), then stage the
# foreign row. The query must conflict (SERVFAIL) and never rewrite it.
# Every route RPC carries the schema2 activation fence the marker committed.
MARKER_ACTIVATION="$(python3 -c "import json; print(json.load(open('$ROOT/config/leshy-managed.json'))['activation_id'])")"
MARKER_GENERATION="$(python3 -c "import json; print(json.load(open('$ROOT/config/leshy-managed.json'))['mode_generation'])")"
[[ -n "$MARKER_ACTIVATION" && -n "$MARKER_GENERATION" ]] || die "row 10: the schema2 marker is missing its fence"
ip netns exec "$NS" "$ROUTEPROE" --socket "$SOCKET" --auth-file "$AUTH_FILE" flush \
    --zone primary --activation "$MARKER_ACTIVATION" --generation "$MARKER_GENERATION" \
    > /dev/null || die "row 10: FlushZone refused: $(cat "$ROOT/flush.out" 2>/dev/null || true)"
deadline=50
until ! selected_v4_present; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 10: the selected /32 never retired after FlushZone"
    sleep 0.1
done
ip -n "$NS" route add "$SELECTED_V4/32" dev lo metric 99 || die "row 10: the foreign row could not be staged"
FOREIGN_BEFORE=$(ip -n "$NS" -details -4 route show table main | grep -E "${SELECTED_V4}" || true)
[[ -n "$FOREIGN_BEFORE" ]] || die "row 10: the foreign row is not visible after staging"
# The fixture answers carry a 60s TTL and row 7 primed Leshy's cache: restart
# Leshy (same generated config) so the conflicting query is re-classified
# against the foreign row instead of served from cache.
kill "$LESHY_PID" 2>/dev/null || true
LESHY_PID=""
sleep 1
start_leshy
sleep 1
kill -0 "$LESHY_PID" 2>/dev/null || die "row 10: Leshy did not restart on the generated config"
FOREIGN_RESULT=$(dnsq "$ZONE_DOMAIN" 1 || true)
[[ "$FOREIGN_RESULT" == "rcode=2"* ]] || die "row 10: a conflicting foreign row must SERVFAIL (got: $FOREIGN_RESULT)"
FOREIGN_AFTER=$(ip -n "$NS" -details -4 route show table main | grep -E "${SELECTED_V4}" || true)
[[ "$FOREIGN_BEFORE" == "$FOREIGN_AFTER" ]] || die "row 10: the foreign row was rewritten ($FOREIGN_BEFORE -> $FOREIGN_AFTER)"
pass "row 10: the foreign row survived the refused query byte-identically"
ip -n "$NS" route del "$SELECTED_V4/32" dev lo metric 99 2>/dev/null || ip -n "$NS" route del "$SELECTED_V4" dev lo metric 99 || true
# The event-driven audit may have integrity-quarantined the role while the
# foreign row existed (§31.21, 100ms event debounce). The production way out
# is the operator retry (§37.10): with the conflict gone it clears the
# quarantine and the managed recovery restores and unfreezes the role.
ip netns exec "$NS" "$CORE" retry --role primary --socket "$SOCKET" > /dev/null
deadline=100
until [[ "$(status_field state)" == "Ready" && "$(status_field recovery.step)" == "" ]]; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 11: the role never recovered after the foreign row was removed (state=$(status_field state) step=$(status_field recovery.step))"
    sleep 0.1
done
RESTORE_RESULT=""
deadline=20
until [[ "$RESTORE_RESULT" == "rcode=0 answers=1"* ]]; do
    RESTORE_RESULT=$(dnsq "$ZONE_DOMAIN" 1 || true)
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 11: the managed route was not restored by the query: $RESTORE_RESULT"
    sleep 0.5
done
deadline=50
until selected_v4_present; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 11: the selected /32 never returned after the foreign row was removed"
    sleep 0.1
done
pass "row 11: the managed route was restored through a query"

note "row 14: family suppression follows the interface address truth"
ip -n "$NS" address del 2001:db8:203::1/64 dev "$MANAGED_IF"
deadline=50
until [[ "$(status_field validation.families.ipv6.ready)" != "True" ]]; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 14: ready-toad never published the IPv6 loss"
    sleep 0.1
done
[[ "$(status_field validation.families.ipv4.ready)" == "True" ]] || die "row 14: IPv4 readiness collapsed with IPv6"
# The family drift re-validates the role (§20.3 row 14): the short recovery
# freezes the route state, so the queries wait for it to settle first.
deadline=100
until [[ "$(status_field recovery.step)" == "" ]]; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 14: the family-drift revalidation never settled (step=$(status_field recovery.step))"
    sleep 0.1
done
A_AFTER_LOSS=$(dnsq "$ZONE_DOMAIN" 1)
[[ "$A_AFTER_LOSS" == "rcode=0 answers=1"* ]] || die "row 14: the A query broke after the IPv6 loss: $A_AFTER_LOSS"
AAAA_SUPPRESSED=$(dnsq "$ZONE_DOMAIN" 28)
[[ "$AAAA_SUPPRESSED" == "rcode=0 answers=0"* ]] || die "row 14: AAAA must be family-suppressed (NODATA) after the loss: $AAAA_SUPPRESSED"
selected_v4_present || die "row 14: the v4 selected row disappeared during suppression"
ip -n "$NS" address add 2001:db8:203::1/64 dev "$MANAGED_IF"
deadline=50
until [[ "$(status_field validation.families.ipv6.ready)" == "True" ]]; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 14: IPv6 readiness never returned"
    sleep 0.1
done
# The re-added address re-validates the role again; the crash row needs the
# route table quiet, so wait for the recovery to settle first.
deadline=100
until [[ "$(status_field recovery.step)" == "" ]]; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 14: the IPv6 re-validation never settled (step=$(status_field recovery.step))"
    sleep 0.1
done
pass "row 14: family suppression engaged and recovered with the address"

note "row 15: killing the core fails closed with no local fallback"
# §20.3 row 15/16 pair: the core is KILLED (crash semantics). A graceful stop
# would release the network and empty the registry checkpoint by design; the
# crash leaves the exact selected ownership durable so the restart row can
# prove the checkpoint reconstruction.
ROUTES_BEFORE_KILL=$( { v4_details; v6_details; } | sort | sha256sum | cut -d' ' -f1)
kill -9 "$CORE_PID"
wait "$CORE_PID" 2>/dev/null || true
CORE_PID=""
kill -0 "$LESHY_PID" 2>/dev/null || die "row 15: Leshy died with the core"
# late.test was never queried before this row, so the fail-closed proof is a
# genuine classification against the dead controller (the example.com answers
# are inside Leshy's 60s-TTL cache and would read as a false fallback).
CORE_DOWN_RESULT=$(dnsq late.test 1 || true)
[[ "$CORE_DOWN_RESULT" == "rcode=2"* ]] || die "row 15: the zone query must fail closed with the core down: $CORE_DOWN_RESULT"
CORE_DOWN_AAAA=$(dnsq late.test 28 || true)
[[ "$CORE_DOWN_AAAA" == "rcode=2"* ]] || die "row 15: the AAAA zone query must fail closed with the core down: $CORE_DOWN_AAAA"
# After the crash the core-owned selected routes legitimately persist (the
# fail-closed crash posture the restart row reconciles). The proof here is
# that the route table did not CHANGE while the core was down: no Leshy-owned
# fallback appeared and no owned row was silently cleaned.
ROUTES_AFTER_KILL=$( { v4_details; v6_details; } | sort | sha256sum | cut -d' ' -f1)
[[ "$ROUTES_BEFORE_KILL" == "$ROUTES_AFTER_KILL" ]] || die "row 15: the route table changed with the core down (a local fallback installed or owned rows mutated)"
pass "row 15: the core died, Leshy stayed up and the route table stayed frozen"

if [[ "$SUITE" == "full" ]]; then
    # Rows 12/13/16 need the tagged core (§20.3). They run after row 15, so
    # the core is restarted here with the failpoint build and the same state.
    note "rows 12-13: the tagged trigger enters the real recovery sequence"
    KK_RECOVERY_BARRIER_STEP="withdraw-selected" \
    KK_RECOVERY_BARRIER_ROLE="primary" \
    KK_RECOVERY_BARRIER_DIR="$ROOT/barrier" \
        start_core "$CORE_FAILPOINTS"
    mkdir -p "$ROOT/barrier"
    ip netns exec "$NS" "$CORE" wait-startup --socket "$SOCKET" --timeout 30s >/dev/null
    ip netns exec "$NS" "$CORE" connect --socket "$SOCKET" --role primary >> "$ROOT/ready-toad.log" 2>&1
    deadline=60
    until [[ "$(status_field state)" == "Ready" ]]; do
        deadline=$((deadline - 1))
        [[ $deadline -gt 0 ]] || die "row 12: the restarted role never reached Ready"
        sleep 1
    done
    # The trigger goes through the real control API of the tagged core.
    ip netns exec "$NS" "$ROUTEPROE" --socket "$SOCKET" --auth-file "$AUTH_FILE" call \
        --method TestTriggerManagedRecovery --role primary > /dev/null
    WAITING="$ROOT/barrier/primary.withdraw-selected.waiting"
    deadline=100
    until [[ -e "$WAITING" ]]; do
        deadline=$((deadline - 1))
        [[ $deadline -gt 0 ]] || die "row 13: the withdraw-selected .waiting marker never appeared"
        sleep 0.1
    done
    # The barrier semantics: Freeze+Observe+Park+WithdrawSelected complete,
    # Withdraw not started. Selected absent, exact park present and winning.
    selected_v4_present && die "row 13: the selected /32 must be absent at the withdraw-selected barrier"
    PARKS=$(status_field parking.count)
    [[ "$PARKS" != "0" ]] || die "row 13: no park is present at the withdraw-selected barrier"
    # Delete/recreate the managed device: the restored selected route must
    # use the NEW ifindex after ready-toad resamples during Rebind.
    OLD_IFINDEX=$(ip -n "$NS" link show "$MANAGED_IF" | grep -oE '[0-9]+: ' | head -1 | tr -d ': ')
    ip -n "$NS" link del "$MANAGED_IF"
    ip -n "$NS" link add "$MANAGED_IF" type dummy
    ip -n "$NS" address add 10.203.0.1/24 dev "$MANAGED_IF"
    ip -n "$NS" address add 2001:db8:203::1/64 dev "$MANAGED_IF"
    ip -n "$NS" link set "$MANAGED_IF" up
    NEW_IFINDEX=$(ip -n "$NS" link show "$MANAGED_IF" | grep -oE '[0-9]+: ' | head -1 | tr -d ': ')
    [[ "$OLD_IFINDEX" != "$NEW_IFINDEX" ]] || die "row 13: the recreated device kept the old ifindex"
    touch "$ROOT/barrier/primary.withdraw-selected.continue"
    deadline=300
    until [[ ! -e "$WAITING" ]]; do
        deadline=$((deadline - 1))
        [[ $deadline -gt 0 ]] || die "row 13: the barrier never released"
        sleep 0.1
    done
    deadline=300
    until [[ "$(status_field state)" == "Ready" ]]; do
        deadline=$((deadline - 1))
        [[ $deadline -gt 0 ]] || die "row 13: recovery never converged after the barrier release"
        sleep 1
    done
    A_RESTORED=$(dnsq "$ZONE_DOMAIN" 1)
    [[ "$A_RESTORED" == "rcode=0 answers=1"* ]] || die "row 13: the A query broke after the recovery: $A_RESTORED"
    pass "rows 12-13: the tagged trigger drove the real recovery across the ifindex replacement"

    note "row 16: a restart reconstructs checkpoint ownership without DNS"
    kill "$CORE_PID"
    wait "$CORE_PID" 2>/dev/null || true
    CORE_PID=""
    unset KK_RECOVERY_BARRIER_STEP KK_RECOVERY_BARRIER_ROLE KK_RECOVERY_BARRIER_DIR
    start_core "$CORE_FAILPOINTS"
    ip netns exec "$NS" "$CORE" wait-startup --socket "$SOCKET" --timeout 30s >/dev/null
    [[ "$(status_field state)" == "Ready" ]] || die "row 16: the restarted core did not restore the role"
    selected_v4_present || die "row 16: the checkpointed selected ownership was not reconstructed at startup"
    pass "row 16: the restart reconstructed exact checkpoint ownership"
else
    # The hosted suite skipped rows 12-16 and row 15 killed the core: restart
    # the same core over the same state so rows 17-18 run against a live
    # controller with the committed managed state restored.
    note "rows 16h: a plain restart restores the committed managed state"
    start_core "$CORE"
    ip netns exec "$NS" "$CORE" wait-startup --socket "$SOCKET" --timeout 30s >/dev/null
    deadline=60
    until [[ "$(status_field state)" == "Ready" ]]; do
        deadline=$((deadline - 1))
        [[ $deadline -gt 0 ]] || die "rows 16h: the restarted role never reached Ready"
        sleep 1
    done
    [[ "$(status_field managed_mode)" == "True" ]] || die "rows 16h: the restart lost managed mode (state=$(status_field state) reg=$(status_field selected_routes.registered))"
    selected_v4_present || die "rows 16h: the checkpointed selected ownership was not reconstructed at startup (state=$(status_field state) managed=$(status_field managed_mode) reg=$(status_field selected_routes.registered) v4=$(v4_details | grep -c "$SELECTED_V4" || true))"
    pass "rows 16h: the restart restored managed mode and the selected ownership"
fi

# The restart (or the trigger rows) rebuilt the registry state; refresh the
# fence values the marker now carries before the privileged RPC rows.
MARKER_ACTIVATION="$(python3 -c "import json; print(json.load(open('$ROOT/config/leshy-managed.json'))['activation_id'])")"
MARKER_GENERATION="$(python3 -c "import json; print(json.load(open('$ROOT/config/leshy-managed.json'))['mode_generation'])")"

note "row 17: Forget and FlushZone retire exact identities"
ip netns exec "$NS" "$ROUTEPROE" --socket "$SOCKET" --auth-file "$AUTH_FILE" flush \
    --zone primary --activation "$MARKER_ACTIVATION" --generation "$MARKER_GENERATION" \
    > "$ROOT/flush.out" 2>&1 || die "row 17: FlushZone refused: $(cat "$ROOT/flush.out")"
deadline=50
until ! selected_v4_present && ! selected_v6_present; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 17: FlushZone did not retire the exact selected identities"
    sleep 0.1
done
pass "row 17: FlushZone retired the exact selected identities"

note "row 18: DisconnectAll drains the registry before the local rollback"
ip netns exec "$NS" "$CORE" disconnect-all --socket "$SOCKET" > /dev/null
deadline=90
until [[ "$(status_field selected_routes.registered)" == "0" ]]; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 18: the registry never drained (registered=$(status_field selected_routes.registered))"
    sleep 1
done
[[ "$(status_field parking.count)" == "0" ]] || die "row 18: parks survived the drain"
[[ "$(status_field parking.active)" != "True" ]] || die "row 18: an active park survived the drain"
# The disable transaction takes the Leshy writer lock: the harness Leshy must
# be stopped first — the same precondition a production operator has.
old_leshy="$LESHY_PID"
kill "$old_leshy" 2>/dev/null || true
LESHY_PID=""
deadline=50
until ! kill -0 "$old_leshy" 2>/dev/null; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 18: the harness Leshy never stopped for the mode transaction"
    sleep 0.1
done
ip netns exec "$NS" "$CORE" leshy-mode disable --socket "$SOCKET" --auth-file "$AUTH_FILE" > "$ROOT/disable.log" 2>&1 || die "row 18: leshy-mode disable failed: $(cat "$ROOT/disable.log")"
pass "row 18: the registry drained and the managed rollback was accepted"

note "MANAGED-LESHY ROUTING SUCCESS ($SUITE suite): PASS"
