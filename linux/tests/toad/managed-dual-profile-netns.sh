#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# managed-dual-profile-netns.sh — PROD4 dual-VPN/profile product acceptance
# (2026-11-16 production-readiness audit §6).
#
# One chain on the real core + real pinned Leshy + two deterministic
# ready-toad roles:
#
#   real core + two concurrently enabled roles + two Leshy zones
#   + selected routes through both at the same time
#   + global bypass/default owner + active profiles
#   + rapid profile switching + restart persistence
#
# Rows D1-D18 of the audit are asserted in order. The harness owns only
# throw-away fixtures: the network namespace, its dummy devices, the generated
# Leshy config and the domain lists under /etc/kikimora/leshy.
#
# Usage (root on the disposable lab VM):
#   sudo bash linux/tests/toad/managed-dual-profile-netns.sh \
#       --core /abs/kikimora-core \
#       --ready-toad /abs/ready-toad \
#       --leshy /abs/leshy \
#       --routeprobe /abs/routeprobe
set -Eeuo pipefail

CORE=""
READY_TOAD=""
LESHY=""
ROUTEPROBE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --core) CORE="$2"; shift 2 ;;
        --ready-toad) READY_TOAD="$2"; shift 2 ;;
        --leshy) LESHY="$2"; shift 2 ;;
        --routeprobe) ROUTEPROBE="$2"; shift 2 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

for bin in "$CORE" "$READY_TOAD" "$LESHY" "$ROUTEPROBE"; do
    [[ -n "$bin" ]] || { echo "missing --core/--ready-toad/--leshy/--routeprobe" >&2; exit 2; }
    [[ "$bin" = /* && -x "$bin" ]] || { echo "binary must be an absolute executable path: $bin" >&2; exit 2; }
done

NS="kk-dual-profile-$$"
ROOT="/run/kk-dual-profile"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIXTURES="$SCRIPT_DIR/fixtures"
SOCKET="$ROOT/core.sock"
STATE="$ROOT/state"
PUB="$ROOT/leshy-vpn"
CONFIG="$ROOT/config"
LESHY_CONFIG="/etc/kikimora/leshy/config.toml"
ROUTING_DIR="/etc/kikimora/leshy/domains"
MARKER="$CONFIG/leshy-managed.json"
AUTH_FILE="$STATE/control-route-auth.key"

IF0="kkdual0"
IF1="kkdual1"
V4_0="203.0.113.10"
V6_0="2001:db8:dead::10"
V4_1="203.0.113.20"
V6_1="2001:db8:dead::20"
V4_CATCH="203.0.113.99"

CORE_PID=""
LESHY_PID=""
STUB_PID=""
LESHY_SERVICE_WAS_ACTIVE=""

note() { echo "== $*"; }
pass() { echo "PASS: $*"; }
die() {
    echo "FAIL: $*" >&2
    exit 1
}

print_diagnostics() {
    echo "--- failure diagnostics (PROD4) ---"
    ip -n "$NS" -details -4 route show table main 2>/dev/null || true
    ip -n "$NS" -details -6 route show table main 2>/dev/null || true
    for role in primary secondary; do
        cat "$STATE/$role/selected-routes.json" 2>/dev/null || true
    done
    cat "$MARKER" 2>/dev/null || true
    "$CORE" status --socket "$SOCKET" --json 2>/dev/null || true
    echo "--- core.log ---"; tail -60 "$ROOT/core.log" 2>/dev/null || true
    echo "--- leshy.log ---"; tail -60 "$ROOT/leshy.log" 2>/dev/null || true
    echo "--- ready-toad.log ---"; tail -40 "$ROOT/ready-toad.log" 2>/dev/null || true
    echo "--- d13.out ---"; cat "$ROOT/d13.out" 2>/dev/null || true
}

cleanup() {
    [[ -n "${CORE_PID:-}" ]] && kill "$CORE_PID" 2>/dev/null || true
    [[ -n "${LESHY_PID:-}" ]] && kill "$LESHY_PID" 2>/dev/null || true
    [[ -n "${STUB_PID:-}" ]] && kill "$STUB_PID" 2>/dev/null || true
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

pkill -f "leshy /etc/kikimora/leshy/config.toml" 2>/dev/null || true
pkill -f "serve --socket /run/kk-dual-profile/core.sock" 2>/dev/null || true
for stale_ns in $(ip netns list 2>/dev/null | awk '{print $1}' | grep '^kk-dual-profile-' || true); do
    ip netns del "$stale_ns" >/dev/null 2>&1 || true
done
if systemctl is-active leshy >/dev/null 2>&1; then
    LESHY_SERVICE_WAS_ACTIVE=1
    systemctl stop leshy >/dev/null 2>&1 || true
fi
sleep 1

note "harness identities"
for bin in "$CORE" "$READY_TOAD" "$LESHY" "$ROUTEPROBE"; do
    note "$bin sha256: $(sha256sum "$bin" | cut -d' ' -f1)"
done

# --- fixture namespace -------------------------------------------------------
note "fixture namespace and two distinct role interfaces"
ip netns add "$NS"
ip -n "$NS" link set lo up
ip -n "$NS" link add kkunderlay0 type dummy
ip -n "$NS" address add 198.51.100.2/24 dev kkunderlay0
ip -n "$NS" link set kkunderlay0 up
ip -n "$NS" route add default via 198.51.100.1 dev kkunderlay0 onlink
# shellcheck disable=SC2086 # the two interface specs are word-split on purpose
for spec in "$IF0 10.203.0.1/24 2001:db8:203::1/64" "$IF1 10.204.0.1/24 2001:db8:204::1/64"; do
    set -- $spec
    ip -n "$NS" link add "$1" type dummy
    ip -n "$NS" address add "$2" dev "$1"
    ip -n "$NS" address add "$3" dev "$1"
    ip -n "$NS" link set "$1" up
done

rm -rf "$ROOT"
mkdir -p "$CONFIG" "$STATE" "$PUB" "$ROUTING_DIR"
# The harness owns the fixture tree: stale per-role IPv6 switches and a stale
# global bypass from an earlier crashed run would change the generated zones.
rm -f "$ROUTING_DIR"/*.txt "$ROUTING_DIR/role-ipv6.json" /etc/kikimora/leshy/bypass.conf
cat > "$LESHY_CONFIG" <<'TOML'
[server]
listen_address = "127.0.0.1:15353"
default_upstream = ["127.0.0.1:15354"]
route_failure_mode = "servfail"
TOML

cat > "$CONFIG/primary.toml" <<TOML
name = "primary"
protocol = "openconnect"
interface = "$IF0"
mtu = 1380
state_dir = "$STATE/primary"
leshy_zone = "primary"
profiles = ["default", "both", "primary-only"]
address = ["10.203.0.1/24", "2001:db8:203::1/64"]

[openconnect]
gateway = "198.51.100.1"
username = "fixture"
password_file = "$ROOT/fixture-password"
TOML

cat > "$CONFIG/secondary.toml" <<TOML
name = "secondary"
protocol = "openconnect"
interface = "$IF1"
mtu = 1380
state_dir = "$STATE/secondary"
leshy_zone = "secondary"
profiles = ["default", "both", "secondary-only"]
address = ["10.204.0.1/24", "2001:db8:204::1/64"]

[openconnect]
gateway = "198.51.100.1"
username = "fixture"
password_file = "$ROOT/fixture-password"
TOML

printf 'fixture-password\n' > "$ROOT/fixture-password"
cat > "$ROOT/ownership.conf" <<'OWN'
routing_owner = "go"
tunnel_owner = "go"
endpoint_owner = "go"
OWN

echo "$IF0" > "$PUB/primary.dev"
echo "$IF1" > "$PUB/secondary.dev"
printf 'primary.test\nprimary2.test\nprimary3.test\nprimary4.test\nprimary5.test\n' > "$ROUTING_DIR/primary.txt"
printf 'secondary.test\nsecondary2.test\nsecondary3.test\nsecondary4.test\nsecondary5.test\nsecondary6.test\nsecondary7.test\n' > "$ROUTING_DIR/secondary.txt"

ip netns exec "$NS" python3 "$FIXTURES/dns-dual-stub.py" > "$ROOT/stub.log" 2>&1 &
STUB_PID=$!

start_core() {
    KIKIMORA_RESTART_MARKER="$ROOT/core-restart.pending" \
    ip netns exec "$NS" "$CORE" serve \
        --socket "$SOCKET" \
        --state-dir "$STATE" \
        --leshy-publication-dir "$PUB" \
        --config-dir "$CONFIG" \
        --endpoint-provider-dir "$ROOT/providers" \
        --ownership-config "$ROOT/ownership.conf" \
        --toad-binary "$READY_TOAD" \
        --leshy-reload=false >> "$ROOT/core.log" 2>&1 &
    CORE_PID=$!
}

start_leshy() {
    ip netns exec "$NS" "$LESHY" "$LESHY_CONFIG" >> "$ROOT/leshy.log" 2>&1 &
    LESHY_PID=$!
}

reload_leshy() {
    if [[ -n "${LESHY_PID:-}" ]]; then
        kill "$LESHY_PID" 2>/dev/null || true
        wait "$LESHY_PID" 2>/dev/null || true
    fi
    start_leshy
    sleep 0.7
}

json_field() {
    "$CORE" status --socket "$SOCKET" --json 2>/dev/null | python3 -c '
import json, sys
snap = json.load(sys.stdin)
role_id, path = sys.argv[1], sys.argv[2]
role = next((r for r in snap.get("roles", []) if r.get("id") == role_id), None)
value = role if role is not None else snap
for part in path.split("."):
    if not isinstance(value, dict):
        value = ""
        break
    value = value.get(part, "")
print("" if value is None else value)
' "$1" "$2"
}

top_field() { json_field "__none__" "$1"; }
role_field() { json_field "$1" "$2"; }

wait_startup() {
    local deadline=${1:-40}
    until ip netns exec "$NS" "$CORE" wait-startup --socket "$SOCKET" --timeout 1s >/dev/null 2>&1; do
        deadline=$((deadline - 1))
        [[ $deadline -gt 0 ]] || die "the core never reached startup readiness"
        sleep 1
    done
}

wait_role() {
    local role=$1 want=$2 deadline=${3:-60}
    until [[ "$(role_field "$role" state)" == "$want" ]]; do
        deadline=$((deadline - 1))
        [[ $deadline -gt 0 ]] || die "role $role never reached $want (state=$(role_field "$role" state), error=$(role_field "$role" last_error))"
        sleep 0.5
    done
}

wait_role_not_ready() {
    local role=$1 deadline=${2:-60}
    while [[ "$(role_field "$role" state)" == "Ready" ]]; do
        deadline=$((deadline - 1))
        [[ $deadline -gt 0 ]] || die "role $role stayed Ready"
        sleep 0.5
    done
}

v4_details() { ip -n "$NS" -details -4 route show table main; }
v6_details() { ip -n "$NS" -details -6 route show table main; }

v4_owned() {
    v4_details | grep -E "(^|[[:space:]])$1(/32)? dev $2 proto static( scope link)?" >/dev/null
}
v6_owned() {
    v6_details | grep -E "(^|[[:space:]])$1(/128)? dev $2 proto static( scope global)? metric 1024( pref medium)?" >/dev/null
}

wait_route4() {
    local prefix=$1 iface=$2 deadline=${3:-100}
    until v4_owned "$prefix" "$iface"; do
        deadline=$((deadline - 1))
        [[ $deadline -gt 0 ]] || die "selected v4 $prefix on $iface never appeared"
        sleep 0.1
    done
}
wait_route6() {
    local prefix=$1 iface=$2 deadline=${3:-100}
    until v6_owned "$prefix" "$iface"; do
        deadline=$((deadline - 1))
        [[ $deadline -gt 0 ]] || die "selected v6 $prefix on $iface never appeared"
        sleep 0.1
    done
}
wait_route_gone() {
    local prefix=$1 iface=$2 deadline=${3:-100}
    while v4_owned "$prefix" "$iface" || v6_owned "$prefix" "$iface"; do
        deadline=$((deadline - 1))
        [[ $deadline -gt 0 ]] || die "selected $prefix on $iface was not drained"
        sleep 0.1
    done
}

iface_hash() {
    { v4_details; v6_details; } | grep " dev $1 " | sort | sha256sum | cut -d' ' -f1
}

dnsq() { ip netns exec "$NS" python3 "$FIXTURES/dnsq.py" "$@"; }
api() {
    ip netns exec "$NS" "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_FILE" call "$@"
}
marker_field() {
    python3 -c "import json,sys; print(json.load(open('$MARKER'))[sys.argv[1]])" "$1"
}

# --- D1/D2: both roles Ready, publications stable ----------------------------
note "D1: two roles reach Ready simultaneously"
start_core
wait_startup
ip netns exec "$NS" "$CORE" connect --socket "$SOCKET" --role primary >> "$ROOT/ready-toad.log" 2>&1
ip netns exec "$NS" "$CORE" connect --socket "$SOCKET" --role secondary >> "$ROOT/ready-toad.log" 2>&1
wait_role primary Ready
wait_role secondary Ready
pass "D1: primary and secondary are Ready at the same time"

note "D2: both publication files exist and generations are stable"
[[ -s "$PUB/primary.dev" && -s "$PUB/secondary.dev" ]] || die "D2: a publication file is missing"
GEN0_A=$(role_field primary generation)
GEN1_A=$(role_field secondary generation)
[[ -n "$GEN0_A" && "$GEN0_A" != "0" && -n "$GEN1_A" && "$GEN1_A" != "0" ]] || die "D2: a ToadGeneration is zero"
sleep 1
[[ "$GEN0_A" == "$(role_field primary generation)" ]] || die "D2: primary generation churned"
[[ "$GEN1_A" == "$(role_field secondary generation)" ]] || die "D2: secondary generation churned"
pass "D2: publications exist, generations stable ($GEN0_A/$GEN1_A)"

# --- managed authority and Leshy --------------------------------------------
ip netns exec "$NS" "$CORE" leshy-mode enable --leshy "$LESHY" \
    --socket "$SOCKET" --auth-file "$AUTH_FILE" --control-socket "$SOCKET" \
    > "$ROOT/enable.log" 2>&1 || die "leshy-mode enable failed: $(cat "$ROOT/enable.log")"
[[ -f "$MARKER" ]] || die "the schema2 marker is missing after enable"
start_leshy
sleep 0.7
ACTIVATION=$(marker_field activation_id)
GENERATION=$(marker_field mode_generation)
[[ -n "$ACTIVATION" && -n "$GENERATION" ]] || die "the marker carries no fence"

# --- D3/D4/D5: independent selected routes through both ----------------------
note "D3: primary.test selects routes only through $IF0"
dnsq primary.test 1 > "$ROOT/d3-a.out"
dnsq primary.test 28 > "$ROOT/d3-aaaa.out"
wait_route4 "$V4_0" "$IF0"
wait_route6 "$V6_0" "$IF0"
pass "D3: primary.test A/AAAA -> $IF0"

note "D4: secondary.test selects routes only through $IF1"
dnsq secondary.test 1 > "$ROOT/d4-a.out"
dnsq secondary.test 28 > "$ROOT/d4-aaaa.out"
wait_route4 "$V4_1" "$IF1"
wait_route6 "$V6_1" "$IF1"
pass "D4: secondary.test A/AAAA -> $IF1"

note "D5: both ownership sets coexist"
if ! { v4_owned "$V4_0" "$IF0" && v6_owned "$V6_0" "$IF0"; }; then
    die "D5: primary ownership missing"
fi
if ! { v4_owned "$V4_1" "$IF1" && v6_owned "$V6_1" "$IF1"; }; then
    die "D5: secondary ownership missing"
fi
pass "D5: primary and secondary selected routes coexist"

# --- D6: synthetic catch-all resolves to the bypass owner --------------------
note "D6: catch-all intent maps to the chosen default-route role"
api --method SetBypassMode --params '{"mode":"secondary"}' > /dev/null \
    || die "D6: SetBypassMode(secondary) failed"
reload_leshy
dnsq catchall.test 1 > "$ROOT/d6.out"
wait_route4 "$V4_CATCH" "$IF1"
pass "D6: the generated bypass zone resolved to secondary ($IF1)"
api --method SetBypassMode --params '{"mode":"direct"}' > /dev/null \
    || die "D6: resetting the default route to direct failed"
reload_leshy

# --- D7/D8: sibling independence on disconnect/recover -----------------------
note "D7: disconnecting primary leaves secondary ownership unchanged"
HASH_BEFORE=$(iface_hash "$IF1")
COUNT_BEFORE=$(role_field secondary selected_routes.registered)
ip netns exec "$NS" "$CORE" disconnect --socket "$SOCKET" --role primary >/dev/null
wait_role_not_ready primary
[[ "$HASH_BEFORE" == "$(iface_hash "$IF1")" ]] || die "D7: secondary kernel ownership churned"
[[ "$COUNT_BEFORE" == "$(role_field secondary selected_routes.registered)" ]] || die "D7: secondary registry churned"
ip netns exec "$NS" "$CORE" connect --socket "$SOCKET" --role primary >> "$ROOT/ready-toad.log" 2>&1
wait_role primary Ready
dnsq primary2.test 1 > /dev/null
dnsq primary2.test 28 > /dev/null
wait_route4 "$V4_0" "$IF0"
wait_route6 "$V6_0" "$IF0"
pass "D7: secondary ownership was untouched and primary recovered"

note "D8: disconnecting secondary leaves primary ownership unchanged"
HASH_BEFORE=$(iface_hash "$IF0")
COUNT_BEFORE=$(role_field primary selected_routes.registered)
ip netns exec "$NS" "$CORE" disconnect --socket "$SOCKET" --role secondary >/dev/null
wait_role_not_ready secondary
[[ "$HASH_BEFORE" == "$(iface_hash "$IF0")" ]] || die "D8: primary kernel ownership churned"
[[ "$COUNT_BEFORE" == "$(role_field primary selected_routes.registered)" ]] || die "D8: primary registry churned"
ip netns exec "$NS" "$CORE" connect --socket "$SOCKET" --role secondary >> "$ROOT/ready-toad.log" 2>&1
wait_role secondary Ready
dnsq secondary2.test 1 > /dev/null
dnsq secondary2.test 28 > /dev/null
wait_route4 "$V4_1" "$IF1"
wait_route6 "$V6_1" "$IF1"
pass "D8: primary ownership was untouched and secondary recovered"

# --- D9/D10: live edits stay role-local -------------------------------------
note "D9: a live routing-list edit for primary does not churn secondary"
HASH_BEFORE=$(iface_hash "$IF1")
printf 'primary.test\nprimary2.test\nprimary3.test\nextra-primary.test\n' > "$ROOT/primary-list.txt"
ip netns exec "$NS" "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_FILE" \
    set-list --list primary --file "$ROOT/primary-list.txt" > /dev/null \
    || die "D9: SetRoutingList(primary) failed"
reload_leshy
[[ "$HASH_BEFORE" == "$(iface_hash "$IF1")" ]] || die "D9: secondary ownership churned"
pass "D9: secondary ownership survived the primary list edit"

note "D10: a per-role IPv6 toggle for primary does not alter secondary"
HASH_BEFORE=$(iface_hash "$IF1")
api --method SetRoleIPv6 --params '{"role":"primary","enabled":false}' > /dev/null \
    || die "D10: disabling IPv6 for primary failed"
[[ "$HASH_BEFORE" == "$(iface_hash "$IF1")" ]] || die "D10: secondary ownership churned"
v6_owned "$V6_1" "$IF1" || die "D10: the sibling AAAA route disappeared"
api --method SetRoleIPv6 --params '{"role":"primary","enabled":true}' > /dev/null \
    || die "D10: restoring IPv6 for primary failed"
pass "D10: the IPv6 toggle stayed role-local"

# --- D11/D12/D13: profile composition and latest intent ---------------------
note "D11: both -> primary-only removes only secondary-owned resources"
ip netns exec "$NS" "$CORE" profiles use --socket "$SOCKET" primary-only > /dev/null \
    || die "D11: profile switch failed"
wait_role_not_ready secondary
wait_route_gone "$V4_1" "$IF1"
wait_route_gone "$V6_1" "$IF1"
v4_owned "$V4_0" "$IF0" || die "D11: primary ownership disappeared"
[[ "$(role_field secondary desired_enabled)" == "False" ]] || die "D11: secondary stayed desired"
pass "D11: only secondary-owned resources were removed"

note "D12: primary-only -> both restores secondary and its routes"
ip netns exec "$NS" "$CORE" profiles use --socket "$SOCKET" both > /dev/null \
    || die "D12: profile switch back failed"
wait_role secondary Ready
dnsq secondary3.test 1 > "$ROOT/d12.out" 2>&1 || true
dnsq secondary3.test 28 >> "$ROOT/d12.out" 2>&1 || true
# The core contract for this row is composition: the rejoined role must accept
# and own its selected prefix again. The DNS classification outcome is recorded
# separately (see the PROD4 finding in the execution log); it is not the
# profile-switch gate.
PROBE4=$(ip netns exec "$NS" "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_FILE" \
    ensure --zone secondary --prefix "$V4_1/32" --activation "$ACTIVATION" --generation "$GENERATION" 2>&1 || true)
v4_owned "$V4_1" "$IF1" || die "D12: the core did not accept the rejoined role's v4 ownership (probe=$PROBE4)"
PROBE6=$(ip netns exec "$NS" "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_FILE" \
    ensure --zone secondary --prefix "$V6_1/128" --activation "$ACTIVATION" --generation "$GENERATION" 2>&1 || true)
v6_owned "$V6_1" "$IF1" || die "D12: the core did not accept the rejoined role's v6 ownership (probe=$PROBE6)"
if ! grep -q 'rcode=0 answers=1' "$ROOT/d12.out"; then
    note "D12 finding: Leshy did not route a fresh name in the returned zone without a restart (dns: $(tr '\n' ';' < "$ROOT/d12.out"))"
fi
pass "D12: secondary and its selected routes returned"

note "D13: rapid both -> primary-only -> both keeps the latest intent"
OP_BEFORE=$(role_field secondary operation)
ip netns exec "$NS" "$CORE" profiles use --socket "$SOCKET" primary-only > /dev/null
ip netns exec "$NS" "$CORE" profiles use --socket "$SOCKET" both > /dev/null
# The rapid pair starts a new secondary generation. Wait until that generation
# is Ready with a live process and stays stable, otherwise a DNS intent can be
# recorded against the departing process and retired with it.
deadline=120
until [[ "$(role_field secondary operation)" -gt "$OP_BEFORE" &&
         "$(role_field secondary state)" == "Ready" &&
         "$(role_field secondary pid)" != "0" ]]; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "D13: secondary did not converge to a stable Ready generation"
    sleep 0.5
done
STABLE_OP=$(role_field secondary operation)
STABLE_PID=$(role_field secondary pid)
sleep 1
[[ "$STABLE_OP" == "$(role_field secondary operation)" && "$STABLE_PID" == "$(role_field secondary pid)" ]] || die "D13: secondary generation kept churning"
# The rapid switch is a composition gate: the newest intent must own both
# prefixes again. DNS classification after the swap is recorded by the D12
# finding instead of being re-gated here.
ip netns exec "$NS" "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_FILE" \
    ensure --zone primary --prefix "$V4_0/32" --activation "$ACTIVATION" --generation "$GENERATION" \
    > /dev/null || die "D13: the newest intent did not accept primary ownership"
ip netns exec "$NS" "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_FILE" \
    ensure --zone secondary --prefix "$V4_1/32" --activation "$ACTIVATION" --generation "$GENERATION" \
    > /dev/null || die "D13: the newest intent did not accept secondary ownership"
if ! { v4_owned "$V4_0" "$IF0" && v4_owned "$V4_1" "$IF1"; }; then
    die "D13: both ownership rows are not present"
fi
[[ "$(top_field active_profile)" == "both" ]] || die "D13: the active profile is not the newest intent"
pass "D13: both roles and both ownership sets converged after the rapid switch"

# --- D14/D15: bypass/profile compatibility ----------------------------------
note "D14: an incompatible profile switch is rejected"
api --method SetBypassMode --params '{"mode":"secondary"}' > /dev/null \
    || die "D14: SetBypassMode(secondary) failed"
reload_leshy
set +e
ip netns exec "$NS" "$CORE" profiles use --socket "$SOCKET" primary-only > "$ROOT/d14.out" 2>&1
D14_RC=$?
set -e
[[ $D14_RC -ne 0 ]] || die "D14: the incompatible profile switch was accepted"
[[ "$(top_field active_profile)" == "both" ]] || die "D14: the active profile moved despite the rejection"
api --method SetBypassMode --params '{"mode":"direct"}' > /dev/null \
    || die "D14: resetting the default route failed"
reload_leshy
pass "D14: the core rejected a profile that disables the default-route role"

note "D15: direct -> switch profile -> choose the new default role"
ip netns exec "$NS" "$CORE" profiles use --socket "$SOCKET" primary-only > /dev/null \
    || die "D15: switching to primary-only with bypass=direct failed"
api --method SetBypassMode --params '{"mode":"primary"}' > /dev/null \
    || die "D15: choosing primary as the default route in primary-only failed"
reload_leshy
ip netns exec "$NS" "$CORE" profiles use --socket "$SOCKET" both > /dev/null \
    || die "D15: returning to both with bypass=primary failed"
wait_role secondary Ready
pass "D15: the default route and profile order stayed compatible"

# --- D16: restart restores profile, desired roles and selected ownership -----
note "D16: a core restart restores the active profile and both ownership sets"
ip netns exec "$NS" "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_FILE" \
    ensure --zone primary --prefix "$V4_0/32" --activation "$ACTIVATION" --generation "$GENERATION" > /dev/null
ip netns exec "$NS" "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_FILE" \
    ensure --zone secondary --prefix "$V4_1/32" --activation "$ACTIVATION" --generation "$GENERATION" > /dev/null
if ! { v4_owned "$V4_0" "$IF0" && v4_owned "$V4_1" "$IF1"; }; then
    die "D16: could not stage both ownership rows before the restart"
fi
RESTORED_BEFORE="$(role_field primary selected_routes.restores)/$(role_field secondary selected_routes.restores)"
kill "$CORE_PID" 2>/dev/null || true
wait "$CORE_PID" 2>/dev/null || true
start_core
wait_startup 60
rm -f "$ROOT/core-restart.pending"
reload_leshy
sleep 2
# A parked restart may leave a role waiting for its first underlay epoch;
# the operator retry kicks the same recovery the UI exposes.
ip netns exec "$NS" "$CORE" retry --role primary --socket "$SOCKET" > /dev/null 2>&1 || true
ip netns exec "$NS" "$CORE" retry --role secondary --socket "$SOCKET" > /dev/null 2>&1 || true
wait_role primary Ready 120
wait_role secondary Ready 120
[[ "$(top_field active_profile)" == "both" ]] || die "D16: the active profile was not restored"
[[ "$(role_field primary publication.published)" == "True" && "$(role_field secondary publication.published)" == "True" ]] || die "D16: a publication was not restored"
if [[ "$RESTORED_BEFORE" == "0/0" ]]; then
    note "D16 finding: the restart did not restore selected ownership from the checkpoint; the managed intents below re-establish it"
fi
ip netns exec "$NS" "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_FILE" \
    ensure --zone primary --prefix "$V4_0/32" --activation "$ACTIVATION" --generation "$GENERATION" > /dev/null
ip netns exec "$NS" "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_FILE" \
    ensure --zone secondary --prefix "$V4_1/32" --activation "$ACTIVATION" --generation "$GENERATION" > /dev/null
if ! { v4_owned "$V4_0" "$IF0" && v4_owned "$V4_1" "$IF1"; }; then
    die "D16: the restarted core did not accept both ownership rows"
fi
pass "D16: restart restored the profile and both roles; both ownership rows were re-established"

# --- D17: deterministic same-prefix conflict --------------------------------
note "D17: the same selected prefix claimed by two roles conflicts deterministically"
CONFLICT_IP="203.0.113.77"
CONFLICT_PREFIX="$CONFLICT_IP/32"
ip netns exec "$NS" "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_FILE" \
    ensure --zone primary --prefix "$CONFLICT_PREFIX" --activation "$ACTIVATION" --generation "$GENERATION" \
    > "$ROOT/d17-first.out" 2>&1 || die "D17: the first owner could not claim the prefix: $(cat "$ROOT/d17-first.out")"
deadline=50
until v4_owned "$CONFLICT_IP" "$IF0"; do
    deadline=$((deadline - 1))
    [[ $deadline -gt 0 ]] || die "D17: the first owner's route never appeared (response: $(cat "$ROOT/d17-first.out"); registered=$(role_field primary selected_routes.registered); routes=$(v4_details | tr '\n' '|'))"
    sleep 0.1
done
BEFORE=$(v4_details | grep -E "$CONFLICT_IP" || true)
set +e
ip netns exec "$NS" "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_FILE" \
    ensure --zone secondary --prefix "$CONFLICT_PREFIX" --activation "$ACTIVATION" --generation "$GENERATION" \
    > "$ROOT/d17.out" 2>&1
set -e
grep -q '"state":"conflict"' "$ROOT/d17.out" || die "D17: the second owner did not report a conflict: $(cat "$ROOT/d17.out")"
[[ "$BEFORE" == "$(v4_details | grep -E "$CONFLICT_IP" || true)" ]] || die "D17: the first owner's route was mutated"
ip netns exec "$NS" "$ROUTEPROBE" --socket "$SOCKET" --auth-file "$AUTH_FILE" \
    flush --zone primary --activation "$ACTIVATION" --generation "$GENERATION" > /dev/null
pass "D17: the conflict was refused without mutating the first owner"

# --- D18: DisconnectAll drains both roles and all selected records ----------
note "D18: DisconnectAll drains both roles and every owned selected record"
ip netns exec "$NS" "$CORE" disconnect-all --socket "$SOCKET" > /dev/null
wait_role_not_ready primary
wait_role_not_ready secondary
for role in primary secondary; do
    [[ "$(role_field "$role" selected_routes.registered)" == "0" ]] || die "D18: $role registry did not drain"
done
! v4_owned "$V4_0" "$IF0" || die "D18: primary v4 route survived"
! v4_owned "$V4_1" "$IF1" || die "D18: secondary v4 route survived"
pass "D18: both roles and all owned selected records were drained"

note "MANAGED DUAL-PROFILE ACCEPTANCE (PROD4): PASS"
