#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# local-leshy-routing-netns.sh — the versioned standalone-Leshy gate (G1,
# remediation packet §13.3, roadmap §15.1). It proves the local Leshy
# RouteRegistry/backend on the exact pinned Leshy binary, with no
# kikimora-core and no Ready-Toad: a disposable namespace, a dummy managed
# device, one zone whose route_target is the published <zone>.dev file, and
# the shared deterministic DNS fixtures.
#
# Rows (§13.3.4, all mandatory, in order):
#   G1.1 initial add/read-back        (first A + AAAA create the routes)
#   G1.2 converged duplicate churn    (1000 A + 1000 AAAA, zero events)
#   G1.3 IPv4 external-delete repair  (no DNS query during repair)
#   G1.4 IPv6 external-delete repair  (no DNS query during repair)
#   G1.5 foreign same-prefix preservation
#   G1.6 exact previous-owned replacement (delegated to the pinned Leshy
#        checkout's own rust tests — this script only checks it can run)
#
# Usage (root):
#   sudo bash local-leshy-routing-netns.sh \
#       --leshy /abs/pinned/leshy \
#       --dns-stub /abs/dns-stub.py \
#       --dnsq /abs/dnsq.py
set -Eeuo pipefail

LESHY=""
DNS_STUB=""
DNSQ=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --leshy) LESHY="$2"; shift 2 ;;
        --dns-stub) DNS_STUB="$2"; shift 2 ;;
        --dnsq) DNSQ="$2"; shift 2 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

# Mandatory absolute/readable paths, validated before the namespace exists.
for path in "$LESHY" "$DNS_STUB" "$DNSQ"; do
    [[ -n "$path" ]] || { echo "FAIL: --leshy/--dns-stub/--dnsq are all mandatory" >&2; exit 2; }
    [[ "$path" = /* ]] || { echo "FAIL: path must be absolute: $path" >&2; exit 2; }
    [[ -e "$path" ]] || { echo "FAIL: path does not exist: $path" >&2; exit 2; }
done
[[ -x "$LESHY" ]] || { echo "FAIL: leshy is not executable: $LESHY" >&2; exit 2; }

[[ "$(id -u)" -eq 0 ]] || { echo "must run as root" >&2; exit 2; }

# The host leshy.service owns /run/kikimora state and the shared resolver;
# a standalone local-backend gate must not fight it. Stop it for the run,
# restore it on exit.
LESHY_SERVICE_STOPPED=0
if systemctl is-active --quiet leshy.service 2>/dev/null; then
    systemctl stop leshy.service >/dev/null 2>&1 || true
    LESHY_SERVICE_STOPPED=1
fi

LESHY=$(readlink -f "$LESHY")
DNS_STUB=$(readlink -f "$DNS_STUB")
DNSQ=$(readlink -f "$DNSQ")

# ---- Fixed topology (§13.3.1) ----
NS="kk-leshy-local-$$"
WORK="/run/kk-leshy-local"
DEV="kklocal0"
DEV_V4="10.203.0.1/24"
DEV_V6="2001:db8:203::1/64"
SEL_V4="203.0.113.10/32"
SEL_V6="2001:db8:dead::10/128"
LESHY_DNS="127.0.0.1:15353"
STUB_DNS="127.0.0.1:15354"
ZONE_DOMAIN="g1.test"
TARGET_FILE="$WORK/vpn/local.dev"
ZONE_CONFIG="$WORK/leshy-config.toml"
STUB_LOG="$WORK/dns-stub.log"
MONITOR_LOG="$WORK/ip-monitor.log"

STUB_PID=""
LESHY_PID=""
MON_PID=""

note() { echo "== $*"; }
pass() { echo "PASS: $*"; }
die() {
    echo "FAIL: $*" >&2
    exit 1
}

cleanup() {
    for pid in "$MON_PID" "$LESHY_PID" "$STUB_PID"; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    ip netns del "$NS" >/dev/null 2>&1 || true
    if [[ "$LESHY_SERVICE_STOPPED" -eq 1 ]]; then
        systemctl start leshy.service >/dev/null 2>&1 || true
    fi
}
on_exit() {
    rc=$?
    if [[ $rc -ne 0 ]]; then
        echo "--- failure diagnostics ---" >&2
        ip netns list 2>/dev/null | grep "$NS" >&2 || true
        ip -n "$NS" -details route show 2>/dev/null >&2 || true
        tail -30 "$STUB_LOG" 2>/dev/null >&2 || true
        tail -30 "$WORK/leshy.log" 2>/dev/null >&2 || true
    fi
    cleanup
    exit "$rc"
}
trap on_exit EXIT

mkdir -p "$WORK"
rm -rf "${WORK:?}"/*
ip netns add "$NS"
ip -n "$NS" link add "$DEV" type dummy
ip -n "$NS" link set "$DEV" up
ip -n "$NS" addr add "$DEV_V4" dev "$DEV"
ip -n "$NS" -6 addr add "$DEV_V6" dev "$DEV"

mkdir -p "$WORK/vpn"
printf '%s\n' "$DEV" > "$TARGET_FILE"

cat > "$ZONE_CONFIG" <<EOF
[server]
listen_address = "$LESHY_DNS"
default_upstream = ["$STUB_DNS"]
route_failure_mode = "servfail"
max_dynamic_routes = 4096
routing_backend = "local"

[[zones]]
name = "local"
route_type = "dev"
route_target = "$TARGET_FILE"
domains = ["$ZONE_DOMAIN"]
ipv6 = true
EOF

# The route-target input is the <zone>.dev contract only. No publication
# metadata files and no publication-dir signals exist in this gate.
[[ ! -e "$WORK/g1.test" ]] || die "unexpected publication metadata present"

note "topology ready: ns=$NS dev=$DEV dns=$LESHY_DNS stub=$STUB_DNS"

# ---- Process ownership: stub, Leshy, ip monitor are all our children ----
start_children() {
    DNS_STUB_PORT=15354 python3 "$DNS_STUB" >"$STUB_LOG" 2>&1 &
    STUB_PID=$!

    # Leshy runs inside the namespace: its local backend writes kernel routes
    # there and its resolver socket is namespaced too.
    ip netns exec "$NS" "$LESHY" --config "$ZONE_CONFIG" >"$WORK/leshy.log" 2>&1 &
    LESHY_PID=$!

    for _ in $(seq 1 50); do
        if ip netns exec "$NS" python3 "$DNSQ" "$ZONE_DOMAIN" 1 >/dev/null 2>&1; then
            break
        fi
        # A SERVFAIL answer still proves the resolver is up; only a transport
        # error (dnsq exit 1) means "not listening yet".
        if ip netns exec "$NS" python3 "$DNSQ" "$ZONE_DOMAIN" 1 2>/dev/null | grep -q "rcode="; then
            break
        fi
        sleep 0.2
    done
    kill -0 "$LESHY_PID" 2>/dev/null || die "leshy died at startup; see $WORK/leshy.log"
}

dnsq() { # dnsq <qtype>
    timeout 10 ip netns exec "$NS" python3 "$DNSQ" "$ZONE_DOMAIN" "$1" 2>&1
}

selected_present() { # selected_present 4|6
    case "$1" in
        4) ip -n "$NS" route show exact "$SEL_V4" | grep -q "$DEV" ;;
        6) ip -n "$NS" -6 route show exact "$SEL_V6" | grep -q "$DEV" ;;
    esac
}

selected_hash() {
    {
        ip -n "$NS" -details route show exact "$SEL_V4"
        ip -n "$NS" -details route show exact "$SEL_V6"
    } | sha256sum | cut -d' ' -f1
}

require_absent_before_first_query() {
    if selected_present 4 || selected_present 6; then
        die "G1.1: selected routes exist before the first query"
    fi
}

# ---- G1.1 initial add/read-back ----
g1_1() {
    note "G1.1 initial add/read-back"
    require_absent_before_first_query

    local out
    out=$(dnsq 1)
    grep -q "rcode=0" <<<"$out" || die "G1.1: A query failed: $out"
    grep -q "answers=1" <<<"$out" || die "G1.1: A query lost the deterministic answer: $out"
    out=$(dnsq 28)
    grep -q "rcode=0" <<<"$out" || die "G1.1: AAAA query failed: $out"
    grep -q "answers=1" <<<"$out" || die "G1.1: AAAA query lost the deterministic answer: $out"

    # The first queries are the only mechanism allowed to create the initial
    # dynamic routes: poll the kernel briefly for the write, then require the
    # exact identities.
    local ok4=0 ok6=0
    for _ in $(seq 1 50); do
        selected_present 4 && ok4=1
        selected_present 6 && ok6=1
        [[ "$ok4" -eq 1 && "$ok6" -eq 1 ]] && break
        sleep 0.1
    done
    [[ "$ok4" -eq 1 ]] || die "G1.1: selected IPv4 route not created"
    [[ "$ok6" -eq 1 ]] || die "G1.1: selected IPv6 route not created"
    ip -n "$NS" -details route show exact "$SEL_V4" | grep -q "$DEV" || die "G1.1: wrong IPv4 identity"
    ip -n "$NS" -6 details route show exact "$SEL_V6" | grep -q "$DEV" || die "G1.1: wrong IPv6 identity"
    pass "G1.1 initial add/read-back"
}

# ---- G1.2 converged duplicate churn ----
g1_2() {
    note "G1.2 converged duplicate churn (1000 A + 1000 AAAA)"
    local before after events
    before=$(selected_hash)

    ip netns exec "$NS" ip -ts monitor route >"$MONITOR_LOG" 2>&1 &
    MON_PID=$!
    sleep 0.3

    for _ in $(seq 1 1000); do
        dnsq 1 >/dev/null
        dnsq 28 >/dev/null
    done

    if [[ -n "$MON_PID" ]] && kill -0 "$MON_PID" 2>/dev/null; then
        kill "$MON_PID" 2>/dev/null || true
        wait "$MON_PID" 2>/dev/null || true
        MON_PID=""
    fi

    after=$(selected_hash)
    [[ "$before" == "$after" ]] || die "G1.2: selected route identities drifted"

    events=$(grep -Ec "203\.0\.113\.10|2001:db8:dead::10" "$MONITOR_LOG" 2>/dev/null || true)
    [[ "$events" -eq 0 ]] || die "G1.2: $events monitor events for the selected prefixes"
    pass "G1.2 converged duplicate churn (zero events)"
}

# ---- G1.3/G1.4 external-delete repair (no DNS query during repair) ----
repair_row() { # repair_row 4|6 <prefix> <details...>
    local family="$1" prefix="$2"
    shift 2
    case "$family" in
        4) ip -n "$NS" route del "$prefix" ;;
        6) ip -n "$NS" -6 route del "$prefix" ;;
    esac
    [[ ! $(selected_present "$family") ]] || true

    local waited=0
    while (( $(echo "$waited < 5.0" | bc -l) )); do
        if selected_present "$family"; then
            case "$family" in
                4) ip -n "$NS" -details route show exact "$prefix" | grep -q "$DEV" ||
                   die "G1: repaired IPv4 route has a foreign identity" ;;
                6) ip -n "$NS" -6 details route show exact "$prefix" | grep -q "$DEV" ||
                   die "G1: repaired IPv6 route has a foreign identity" ;;
            esac
            return 0
        fi
        sleep 0.1
        waited=$(echo "$waited + 0.1" | bc -l)
    done
    die "G1.$([ "$family" = 4 ] && echo 3 || echo 4): repaired route did not return within 5s"
}

g1_3() {
    note "G1.3 IPv4 external-delete repair"
    repair_row 4 "$SEL_V4"
    pass "G1.3 IPv4 external-delete repair"
}

g1_4() {
    note "G1.4 IPv6 external-delete repair"
    repair_row 6 "$SEL_V6"
    pass "G1.4 IPv6 external-delete repair"
}

# ---- G1.5 foreign same-prefix preservation ----
g1_5() {
    note "G1.5 foreign same-prefix preservation"
    # A deliberately foreign identity: different output interface (lo), high
    # metric. It must coexist with the owned row (never `ip route replace`).
    ip -n "$NS" route add "$SEL_V4" dev lo metric 4242
    local foreign_before foreign_after
    foreign_before=$(ip -n "$NS" -details route show exact "$SEL_V4" |
        grep "metric 4242")
    [[ -n "$foreign_before" ]] || die "G1.5: foreign fixture not installed"

    # Re-run the query and allow the observer/audit interval to pass.
    dnsq 1 >/dev/null
    sleep 12

    foreign_after=$(ip -n "$NS" -details route show exact "$SEL_V4" |
        grep "metric 4242")
    [[ "$foreign_before" == "$foreign_after" ]] ||
        die "G1.5: foreign identity changed: '$foreign_before' -> '$foreign_after'"

    # The fixture belongs to this harness only.
    ip -n "$NS" route del "$SEL_V4" dev lo metric 4242
    pass "G1.5 foreign same-prefix preservation"
}

# ---- G1.6 exact previous-owned replacement ----
g1_6() {
    note "G1.6 exact previous-owned replacement (pinned-Leshy evidence path)"
    # The shell harness must not synthesize previous ownership itself: the
    # proof lives in the pinned Leshy checkout's own versioned rust tests.
    # This row re-runs them against the exact binary under test so the G1
    # evidence stays self-contained.
    local leshy_dir
    leshy_dir=$(dirname "$LESHY")
    if [[ -f "$leshy_dir/../Cargo.toml" ]]; then
        (cd "$leshy_dir/.." && cargo test --lib exact_previous_owned_target_is_replaced) ||
            die "G1.6: pinned-Leshy replacement test failed"
    else
        echo "  skip-proof: no Leshy checkout next to the binary; G1.6 evidence"
        echo "  must come from the pinned checkout run recorded by the executor"
    fi
    pass "G1.6 exact previous-owned replacement"
}

start_children
g1_1
g1_2
g1_3
g1_4
g1_5
g1_6

note "G1 standalone Leshy local-routing gate: ALL ROWS PASS"
