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
}
on_exit() {
    rc=$?
    if [[ $rc -ne 0 ]]; then print_diagnostics; fi
    cleanup
    exit "$rc"
}
trap on_exit EXIT

# status_field extracts one JSON field from the core status snapshot.
status_field() {
    "$CORE" status --socket "$SOCKET" --json 2>/dev/null | python3 -c "
import json, sys
snap = json.load(sys.stdin)
role = next((r for r in snap.get('roles', []) if r.get('id') == 'primary'), {})
path = '$1'.split('.')
value = role
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
selected_v4_present() {
    v4_details | grep -E "^${SELECTED_V4}(/32)? dev ${MANAGED_IF} proto static scope link metric 0\b" >/dev/null
}
selected_v6_present() {
    v6_details | grep -E "^${SELECTED_V6}(/128)? dev ${MANAGED_IF} proto static metric 1024\b" >/dev/null
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
TOML

# The managed backend speaks the schema the generated config would produce.
# No activation fence is written: this run proves the released v1
# compatibility surface; the v2-only cut is a later roadmap phase (I1-I3).
cat > "$ROOT/leshy.toml" <<'TOML'
[server]
listen_address = "127.0.0.1:15353"
default_upstream = ["127.0.0.1:15354"]
route_failure_mode = "servfail"
max_dynamic_routes = 4096
routing_backend = "kikimora"
kikimora_socket = "/run/kk-managed-routing/core.sock"

[[zones]]
name = "primary"
route_type = "dev"
route_target = "/run/kk-managed-routing/leshy-vpn/primary.dev"
domains = ["example.com"]
ipv6 = true
TOML

echo "kkmanaged0" > "$PUB/primary.dev"

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
        --toad-binary "$READY_TOAD" >> "$ROOT/core.log" 2>&1 &
    CORE_PID=$!
}

start_leshy() {
    ip netns exec "$NS" "$LESHY" "$ROOT/leshy.toml" >> "$ROOT/leshy.log" 2>&1 &
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
[[ "$(status_field families.ipv4.ready)" == "True" ]] || die "row 1: IPv4 family is not Ready"
[[ "$(status_field families.ipv6.ready)" == "True" ]] || die "row 1: IPv6 family is not Ready"
GEN1="$(status_field generation)"
[[ -n "$GEN1" && "$GEN1" != "0" ]] || die "row 1: ToadGeneration is zero"
pass "row 1: role Ready, both families, generation $GEN1"

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
v4_details | grep -E "^${SELECTED_V4}" | head -1 > "$ROOT/v4-identity.txt"
v6_details | grep -E "^${SELECTED_V6}" | head -1 > "$ROOT/v6-identity.txt"
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
ip netns exec "$NS" "$ROUTEPROE" --socket "$SOCKET" flush --zone primary > /dev/null
deadline=50
until ! selected_v4_present; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 10: the selected /32 never retired after FlushZone"
    sleep 0.1
done
ip -n "$NS" route add "$SELECTED_V4/32" dev lo metric 99
FOREIGN_BEFORE=$(ip -n "$NS" -details -4 route show table main | grep -E "^${SELECTED_V4}" )
FOREIGN_RESULT=$(dnsq "$ZONE_DOMAIN" 1 || true)
[[ "$FOREIGN_RESULT" == "rcode=2"* ]] || die "row 10: a conflicting foreign row must SERVFAIL (got: $FOREIGN_RESULT)"
FOREIGN_AFTER=$(ip -n "$NS" -details -4 route show table main | grep -E "^${SELECTED_V4}")
[[ "$FOREIGN_BEFORE" == "$FOREIGN_AFTER" ]] || die "row 10: the foreign row was rewritten ($FOREIGN_BEFORE -> $FOREIGN_AFTER)"
pass "row 10: the foreign row survived the refused query byte-identically"
ip -n "$NS" route del "$SELECTED_V4/32" dev lo metric 99 2>/dev/null || ip -n "$NS" route del "$SELECTED_V4" dev lo metric 99
RESTORE_RESULT=$(dnsq "$ZONE_DOMAIN" 1)
[[ "$RESTORE_RESULT" == "rcode=0 answers=1"* ]] || die "row 11: the managed route was not restored by the query: $RESTORE_RESULT"
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
until [[ "$(status_field families.ipv6.ready)" != "True" ]]; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 14: ready-toad never published the IPv6 loss"
    sleep 0.1
done
[[ "$(status_field families.ipv4.ready)" == "True" ]] || die "row 14: IPv4 readiness collapsed with IPv6"
A_AFTER_LOSS=$(dnsq "$ZONE_DOMAIN" 1)
[[ "$A_AFTER_LOSS" == "rcode=0 answers=1"* ]] || die "row 14: the A query broke after the IPv6 loss: $A_AFTER_LOSS"
AAAA_SUPPRESSED=$(dnsq "$ZONE_DOMAIN" 28)
[[ "$AAAA_SUPPRESSED" == "rcode=0 answers=0"* ]] || die "row 14: AAAA must be family-suppressed (NODATA) after the loss: $AAAA_SUPPRESSED"
selected_v4_present || die "row 14: the v4 selected row disappeared during suppression"
ip -n "$NS" address add 2001:db8:203::1/64 dev "$MANAGED_IF"
deadline=50
until [[ "$(status_field families.ipv6.ready)" == "True" ]]; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "row 14: IPv6 readiness never returned"
    sleep 0.1
done
pass "row 14: family suppression engaged and recovered with the address"

note "row 15: killing the core fails closed with no local fallback"
kill "$CORE_PID"
wait "$CORE_PID" 2>/dev/null || true
CORE_PID=""
kill -0 "$LESHY_PID" 2>/dev/null || die "row 15: Leshy died with the core"
CORE_DOWN_RESULT=$(dnsq "$ZONE_DOMAIN" 1 || true)
[[ "$CORE_DOWN_RESULT" == "rcode=2"* ]] || die "row 15: the zone query must fail closed with the core down: $CORE_DOWN_RESULT"
v4_details | grep -q "$SELECTED_V4" && die "row 15: a local fallback route appeared with the core down"
pass "row 15: the core died, Leshy stayed up and no local route was installed"

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
    ip netns exec "$NS" "$ROUTEPROE" --socket "$SOCKET" call \
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
fi

note "row 17: Forget and FlushZone retire exact identities"
ip netns exec "$NS" "$ROUTEPROE" --socket "$SOCKET" flush --zone primary > /dev/null
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
ip netns exec "$NS" "$CORE" leshy-mode disable --socket "$SOCKET" > /dev/null
pass "row 18: the registry drained and the managed rollback was accepted"

note "MANAGED-LESHY ROUTING SUCCESS ($SUITE suite): PASS"
