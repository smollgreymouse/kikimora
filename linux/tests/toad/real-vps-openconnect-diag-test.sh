#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$ROOT/../../.." && pwd)"
# shellcheck source=linux/tests/toad/lib/openconnect-real-profile.sh
source "$ROOT/lib/openconnect-real-profile.sh"

PROFILE="${1:-$ROOT/real-vps-openconnect.secret}"
INTERFACE="kk-oc0"
GOOGLE_HOST="www.google.com"
INTERNAL_HOST="gitlab.sca.ad-tech.ru"
UPLINK_IF="toad-uplink0"
NETNS="toad-oc-real-${UID}-$$"
NETNS_ETC="/etc/netns/$NETNS"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/toad-openconnect-real.XXXXXX")"
PRIVATE="$WORK/private"
STATE_DIR="$PRIVATE/state"
CONFIG="$PRIVATE/profile.toml"
PASSWORD_FILE="$PRIVATE/password.secret"
TOKEN_FILE="$PRIVATE/totp.secret"
DNS_OVERRIDE_FILE="$PRIVATE/dns-override.txt"
BIN="$PRIVATE/kikimora-toad"
DIAG="$WORK/diag"
PID_FILE="$PRIVATE/toad.pid"
SLIRP_PID_FILE="$PRIVATE/slirp.pid"
TUN_TRACE_PID_FILE="$PRIVATE/tun-trace.pid"
UP_TRACE_PID_FILE="$PRIVATE/uplink-trace.pid"
ARCHIVE="${TOAD_DIAG_OUTPUT:-$PWD/toad-real-vps-openconnect-diag-$(date +%Y-%m-%d-%H%M%S).tar.gz}"

NETNS_CREATED=0
NETNS_ETC_CREATED=0
TOAD_STARTED=0
SLIRP_STARTED=0
TOAD_SUDO_PID=""
TOAD_PID=""
SLIRP_SUDO_PID=""
SLIRP_PID=""
TUN_TRACE_SUDO_PID=""
TUN_TRACE_PID=""
UP_TRACE_SUDO_PID=""
UP_TRACE_PID=""
RESULT="FAIL"
FAIL_REASON=""
STARTED_AT="$(date -Is)"
START_EPOCH="$(date +%s)"
IFINDEX=""
MTU=""
RX_BEFORE=""
TX_BEFORE=""
RX_AFTER=""
TX_AFTER=""
GATEWAY=""
GATEWAY_HOST=""
GATEWAY_PORT="443"
ENDPOINT_IP=""
DNS_SOURCE=""
DNS_RAW=""
GOOGLE_IP=""
GOOGLE_HTTP_CODE=""
GOOGLE_BYTES=""
GOOGLE_REMOTE=""
INTERNAL_IP=""
INTERNAL_HTTP_CODE=""
INTERNAL_BYTES=""
INTERNAL_REMOTE=""

log() {
    local now
    now="$(date -Is)"
    printf '[%s] %s\n' "$now" "$*"
    printf '[%s] %s\n' "$now" "$*" >>"$DIAG/command.log" 2>/dev/null || true
}

fail() {
    FAIL_REASON="$*"
    printf 'ERROR: %s\n' "$*" >&2
    printf '[%s] ERROR: %s\n' "$(date -Is)" "$*" >>"$DIAG/errors.txt" 2>/dev/null || true
    return 1
}

need() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

is_ipv4() {
    [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]
}

ns_exists() {
    sudo ip netns list 2>/dev/null | grep -q "^${NETNS}\\b"
}

ns_capture() {
    local phase="$1"
    local dir="$DIAG/$phase"
    mkdir -p "$dir"
    {
        date -Is
        uname -a
        printf '\n=== host links ===\n'
        ip -details -statistics link show || true
        printf '\n=== namespace links ===\n'
        if (( NETNS_CREATED )) && ns_exists; then
            sudo ip -n "$NETNS" -details -statistics link show || true
            sudo ip -n "$NETNS" addr show || true
        fi
    } >"$dir/network.txt" 2>&1
    {
        printf '=== host IPv4 routes ===\n'
        ip -4 route show table all || true
        printf '\n=== host IPv6 routes ===\n'
        ip -6 route show table all || true
        if (( NETNS_CREATED )) && ns_exists; then
            printf '\n=== namespace IPv4 routes ===\n'
            sudo ip -n "$NETNS" -4 route show table all || true
            printf '\n=== namespace IPv6 routes ===\n'
            sudo ip -n "$NETNS" -6 route show table all || true
            printf '\n=== namespace rules ===\n'
            sudo ip netns exec "$NETNS" ip rule show || true
        fi
    } >"$dir/routes.txt" 2>&1
    {
        printf '=== host resolv.conf ===\n'
        sed -n '1,160p' /etc/resolv.conf 2>/dev/null || true
        if (( NETNS_CREATED )) && ns_exists; then
            printf '\n=== namespace resolv.conf ===\n'
            sudo ip netns exec "$NETNS" sed -n '1,160p' /etc/resolv.conf || true
        fi
    } >"$dir/dns.txt" 2>&1
    {
        ss -tunap || true
        if (( NETNS_CREATED )) && ns_exists; then
            printf '\n=== namespace sockets ===\n'
            sudo ip netns exec "$NETNS" ss -tunap || true
        fi
        printf '\n=== processes without argv ===\n'
        ps -eo pid,ppid,user,group,stat,etimes,comm || true
    } >"$dir/runtime.txt" 2>&1
}

capture_state() {
    local name="$1"
    if [[ -s "$STATE_DIR/state.json" ]]; then
        sudo cat "$STATE_DIR/state.json" >"$DIAG/state-$name.json" 2>/dev/null || true
    else
        printf '{"present":false}\n' >"$DIAG/state-$name.json"
    fi
    if [[ -s "$STATE_DIR/openconnect-network.env" ]]; then
        sudo cat "$STATE_DIR/openconnect-network.env" >"$DIAG/openconnect-network-$name.env" 2>/dev/null || true
    fi
}

stop_trace() {
    local pid
    for pid in "$TUN_TRACE_PID" "$UP_TRACE_PID"; do
        if [[ "$pid" =~ ^[1-9][0-9]*$ ]]; then
            sudo kill -INT "$pid" >/dev/null 2>&1 || true
        fi
    done
    for pid in "$TUN_TRACE_SUDO_PID" "$UP_TRACE_SUDO_PID"; do
        if [[ "$pid" =~ ^[1-9][0-9]*$ ]]; then
            wait "$pid" 2>/dev/null || true
        fi
    done
    TUN_TRACE_PID=""
    UP_TRACE_PID=""
    TUN_TRACE_SUDO_PID=""
    UP_TRACE_SUDO_PID=""
}

stop_runtime() {
    set +e
    stop_trace
    if (( TOAD_STARTED )) && [[ "$TOAD_PID" =~ ^[1-9][0-9]*$ ]]; then
        sudo kill -INT "$TOAD_PID" >/dev/null 2>&1 || true
        for _ in $(seq 1 50); do
            sudo kill -0 "$TOAD_PID" >/dev/null 2>&1 || break
            sleep 0.1
        done
        sudo kill -KILL "$TOAD_PID" >/dev/null 2>&1 || true
    fi
    if [[ "$TOAD_SUDO_PID" =~ ^[1-9][0-9]*$ ]]; then
        wait "$TOAD_SUDO_PID" 2>/dev/null || true
    fi
    TOAD_STARTED=0
    if (( SLIRP_STARTED )) && [[ "$SLIRP_PID" =~ ^[1-9][0-9]*$ ]]; then
        sudo kill -TERM "$SLIRP_PID" >/dev/null 2>&1 || true
    fi
    if [[ "$SLIRP_SUDO_PID" =~ ^[1-9][0-9]*$ ]]; then
        wait "$SLIRP_SUDO_PID" 2>/dev/null || true
    fi
    SLIRP_STARTED=0
    if (( NETNS_CREATED )); then
        sudo ip netns delete "$NETNS" >/dev/null 2>&1 || true
        NETNS_CREATED=0
    fi
    if (( NETNS_ETC_CREATED )); then
        sudo rm -f -- "$NETNS_ETC/resolv.conf" >/dev/null 2>&1 || true
        sudo rmdir -- "$NETNS_ETC" >/dev/null 2>&1 || true
        NETNS_ETC_CREATED=0
    fi
    set -e
}

redact_diag() {
    python3 - "$PROFILE" "$DIAG" <<'PY'
import pathlib, re, sys, tomllib
profile = pathlib.Path(sys.argv[1])
root = pathlib.Path(sys.argv[2])
try:
    cfg = tomllib.loads(profile.read_text(encoding="utf-8"))
except Exception:
    cfg = {}
secrets = []
for key in ("password", "totp_secret", "username"):
    value = cfg.get(key)
    if isinstance(value, str) and value:
        secrets.append(value)
        if key == "totp_secret":
            secrets.append(value.replace(" ", ""))
patterns = [
    (re.compile(r"(?i)(password|passwd|token[_ -]?secret|totp[_ -]?secret)(\s*[:=]\s*)\S+"), r"\1\2<redacted>"),
]
for path in root.rglob("*"):
    if not path.is_file():
        continue
    try:
        text = path.read_text(encoding="utf-8")
    except UnicodeDecodeError:
        continue
    for secret in sorted(set(secrets), key=len, reverse=True):
        if secret:
            text = text.replace(secret, "<redacted>")
    for pattern, replacement in patterns:
        text = pattern.sub(replacement, text)
    path.write_text(text, encoding="utf-8")
PY
}

compact_diag() {
    local aggressive="${1:-0}"
    python3 - "$DIAG" "$aggressive" <<'PY'
import pathlib, sys
root = pathlib.Path(sys.argv[1])
aggressive = sys.argv[2] == "1"
limit = 128 * 1024 if aggressive else 512 * 1024
keep = 48 * 1024 if aggressive else 192 * 1024
report = []
for path in root.rglob("*"):
    if not path.is_file() or path.name in {"summary.txt", "size-report.txt"}:
        continue
    size = path.stat().st_size
    if size <= limit:
        continue
    data = path.read_bytes()
    marker = f"\n\n--- compacted: original_bytes={size} ---\n\n".encode()
    path.write_bytes(data[:keep] + marker + data[-keep:])
    report.append(f"{path.relative_to(root)} {size} -> {path.stat().st_size}")
(root / "size-report.txt").write_text("\n".join(report) + ("\n" if report else "no files required compaction\n"), encoding="utf-8")
PY
}

write_summary() {
    {
        printf 'result=%s\n' "$RESULT"
        printf 'failure_reason=%s\n' "${FAIL_REASON:-none}"
        printf 'test_scope=isolated-real-server-preflight\n'
        printf 'protocol=openconnect\n'
        printf 'namespace=%s\n' "$NETNS"
        printf 'interface=%s\n' "$INTERFACE"
        printf 'ifindex=%s\n' "${IFINDEX:-unavailable}"
        printf 'mtu=%s\n' "${MTU:-unavailable}"
        printf 'gateway=%s\n' "$GATEWAY"
        printf 'gateway_ip=%s\n' "$ENDPOINT_IP"
        printf 'dns_source=%s\n' "${DNS_SOURCE:-unavailable}"
        printf 'google_ip=%s\n' "${GOOGLE_IP:-unavailable}"
        printf 'google_http_code=%s\n' "${GOOGLE_HTTP_CODE:-unavailable}"
        printf 'google_response_bytes=%s\n' "${GOOGLE_BYTES:-unavailable}"
        printf 'internal_host=%s\n' "$INTERNAL_HOST"
        printf 'internal_ip=%s\n' "${INTERNAL_IP:-unavailable}"
        printf 'internal_http_code=%s\n' "${INTERNAL_HTTP_CODE:-unavailable}"
        printf 'internal_response_bytes=%s\n' "${INTERNAL_BYTES:-unavailable}"
        printf 'chatgpt_probe=SKIPPED_EXPECTED_UNAVAILABLE\n'
        printf 'rx_before=%s\n' "${RX_BEFORE:-unavailable}"
        printf 'tx_before=%s\n' "${TX_BEFORE:-unavailable}"
        printf 'rx_after=%s\n' "${RX_AFTER:-unavailable}"
        printf 'tx_after=%s\n' "${TX_AFTER:-unavailable}"
        printf 'started_at=%s\n' "$STARTED_AT"
        printf 'ended_at=%s\n' "$(date -Is)"
        printf '\n=== traffic ===\n'
        cat "$DIAG/traffic-test.txt" 2>/dev/null || true
        printf '\n=== errors ===\n'
        cat "$DIAG/errors.txt" 2>/dev/null || true
        printf '\n=== final state ===\n'
        cat "$DIAG/state-active.json" 2>/dev/null || true
    } >"$DIAG/summary.txt"
}

package_diag() {
    local bytes
    {
        uname -a
        printf '\n=== dmesg tail ===\n'
        dmesg --ctime 2>/dev/null | tail -n 500 || true
    } >"$DIAG/kernel.txt" 2>&1
    journalctl --no-pager --since "@$START_EPOCH" -n 1000 >"$DIAG/journal.txt" 2>&1 || true
    write_summary
    redact_diag
    compact_diag 0
    rm -rf -- "$PRIVATE"
    tar -czf "$ARCHIVE" -C "$WORK" diag
    bytes="$(stat -c '%s' "$ARCHIVE")"
    if (( bytes > 4 * 1024 * 1024 )); then
        compact_diag 1
        tar -czf "$ARCHIVE" -C "$WORK" diag
    fi
    chmod 0600 "$ARCHIVE"
    printf 'Diagnostic archive: %s (%s bytes)\n' "$ARCHIVE" "$(stat -c '%s' "$ARCHIVE")"
}

on_exit() {
    local status=$?
    trap - EXIT INT TERM
    set +e
    if (( NETNS_CREATED )) && ns_exists; then
        RX_AFTER="$(sudo ip netns exec "$NETNS" cat "/sys/class/net/$INTERFACE/statistics/rx_bytes" 2>/dev/null || true)"
        TX_AFTER="$(sudo ip netns exec "$NETNS" cat "/sys/class/net/$INTERFACE/statistics/tx_bytes" 2>/dev/null || true)"
        capture_state active
        ns_capture after
    fi
    stop_runtime
    if (( status == 0 )); then
        RESULT="PASS"
    elif [[ -z "$FAIL_REASON" ]]; then
        FAIL_REASON="script exited with status $status"
    fi
    package_diag || true
    rm -rf -- "$WORK" 2>/dev/null || true
    if (( status == 0 )); then
        exit 0
    fi
    exit "$status"
}
trap on_exit EXIT INT TERM

mkdir -p "$PRIVATE" "$STATE_DIR" "$DIAG"
chmod 0700 "$WORK" "$PRIVATE" "$STATE_DIR" "$DIAG"
: >"$DIAG/command.log"
: >"$DIAG/errors.txt"
: >"$DIAG/toad.log"
: >"$DIAG/traffic-test.txt"
: >"$DIAG/tun-trace.txt"
: >"$DIAG/uplink-trace.txt"

[[ "$ARCHIVE" == *.tar.gz ]] || fail "TOAD_DIAG_OUTPUT must end in .tar.gz"
if [[ "$ARCHIVE" != /* ]]; then
    ARCHIVE="$PWD/$ARCHIVE"
fi
(( EUID != 0 )) || fail "run this test as the desktop user, not through sudo"
for cmd in awk cat chmod curl date getent grep ip journalctl openssl openconnect ps python3 sed seq slirp4netns ss stat sudo tail tar tcpdump; do
    need "$cmd"
done
if [[ -z "${TOAD_BIN:-}" ]]; then
    need go
fi
oc_profile_require_secret_file "$PROFILE"
sudo -v

log "collect host baseline and build/materialize OpenConnect profile"
ns_capture before
if [[ -n "${TOAD_BIN:-}" ]]; then
    [[ -x "$TOAD_BIN" ]] || fail "TOAD_BIN is not executable: $TOAD_BIN"
    cp -- "$TOAD_BIN" "$BIN"
else
    (cd "$REPO_ROOT/toad" && go build -o "$BIN" ./cmd/kikimora-toad) >>"$DIAG/command.log" 2>&1
fi
chmod 0755 "$BIN"
oc_profile_materialize "$PROFILE" "$CONFIG" "$STATE_DIR" "$PASSWORD_FILE" "$TOKEN_FILE" "$DNS_OVERRIDE_FILE" "real-openconnect" "$INTERFACE"
"$BIN" validate -config "$CONFIG" >>"$DIAG/command.log" 2>&1

while IFS='=' read -r key value; do
    case "$key" in
        gateway) GATEWAY="$value" ;;
        gateway_host) GATEWAY_HOST="$value" ;;
        gateway_port) GATEWAY_PORT="$value" ;;
    esac
done < <(oc_profile_public_fields "$CONFIG")
[[ -n "$GATEWAY_HOST" ]] || fail "could not parse OpenConnect gateway hostname"
ENDPOINT_IP="$(getent ahostsv4 "$GATEWAY_HOST" | awk 'NR==1 {print $1; exit}' || true)"
is_ipv4 "$ENDPOINT_IP" || fail "could not resolve OpenConnect gateway $GATEWAY_HOST to IPv4"

python3 - "$CONFIG" "$DIAG/config-summary.json" <<'PY'
import json, pathlib, sys, tomllib
cfg = tomllib.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
oc = cfg.get("openconnect") or {}
summary = {
    "name": cfg.get("name"), "protocol": cfg.get("protocol"), "interface": cfg.get("interface"),
    "mtu": cfg.get("mtu"), "gateway": oc.get("gateway"), "vpn_protocol": oc.get("vpn_protocol"),
    "token_mode": oc.get("token_mode"), "disable_udp": oc.get("disable_udp"),
    "disable_ipv6": oc.get("disable_ipv6"), "reconnect_timeout": oc.get("reconnect_timeout")
}
pathlib.Path(sys.argv[2]).write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY

log "create isolated namespace $NETNS and slirp uplink"
sudo ip netns add "$NETNS"
NETNS_CREATED=1
sudo install -d -m 0755 -- "$NETNS_ETC"
NETNS_ETC_CREATED=1
printf 'nameserver 192.0.2.3\noptions timeout:2 attempts:2\n' | sudo tee "$NETNS_ETC/resolv.conf" >/dev/null
sudo chmod 0644 "$NETNS_ETC/resolv.conf"
sudo -- bash -c 'printf "%s\n" "$$" >"$1"; shift; exec "$@"' \
    bash "$SLIRP_PID_FILE" slirp4netns --configure --disable-host-loopback --mtu=65520 \
    --cidr=192.0.2.0/24 --netns-type=path "/run/netns/$NETNS" "$UPLINK_IF" \
    >>"$DIAG/command.log" 2>&1 &
SLIRP_SUDO_PID=$!
SLIRP_STARTED=1
for _ in $(seq 1 100); do
    [[ -s "$SLIRP_PID_FILE" ]] && break
    kill -0 "$SLIRP_SUDO_PID" 2>/dev/null || fail "slirp4netns failed to start"
    sleep 0.05
done
SLIRP_PID="$(sudo sed -n '1p' "$SLIRP_PID_FILE" 2>/dev/null || true)"
[[ "$SLIRP_PID" =~ ^[1-9][0-9]*$ ]] || fail "timed out waiting for slirp4netns PID"
for _ in $(seq 1 100); do
    sudo ip -n "$NETNS" link show dev "$UPLINK_IF" >/dev/null 2>&1 && break
    sleep 0.05
done
sudo ip -n "$NETNS" link show dev "$UPLINK_IF" >/dev/null 2>&1 || fail "isolated uplink did not appear"
sudo ip -n "$NETNS" route get "$ENDPOINT_IP" | grep -Fq "dev $UPLINK_IF" || fail "OpenConnect endpoint does not use isolated uplink"
ns_capture namespace-ready

log "start kikimora-toad inside isolated namespace"
sudo -- bash -c 'printf "%s\n" "$$" >"$1"; shift; exec "$@"' \
    bash "$PID_FILE" ip netns exec "$NETNS" "$BIN" run -config "$CONFIG" \
    >>"$DIAG/toad.log" 2>&1 &
TOAD_SUDO_PID=$!
TOAD_STARTED=1
for _ in $(seq 1 100); do
    [[ -s "$PID_FILE" ]] && break
    kill -0 "$TOAD_SUDO_PID" 2>/dev/null || fail "sudo failed to start kikimora-toad"
    sleep 0.05
done
TOAD_PID="$(sudo sed -n '1p' "$PID_FILE" 2>/dev/null || true)"
[[ "$TOAD_PID" =~ ^[1-9][0-9]*$ ]] || fail "timed out waiting for kikimora-toad PID"
for _ in $(seq 1 600); do
    if sudo ip -n "$NETNS" link show dev "$INTERFACE" >/dev/null 2>&1; then
        break
    fi
    sudo kill -0 "$TOAD_PID" >/dev/null 2>&1 || fail "kikimora-toad exited before $INTERFACE appeared"
    sleep 0.1
done
sudo ip -n "$NETNS" link show dev "$INTERFACE" >/dev/null 2>&1 || fail "$INTERFACE did not appear"
IFINDEX="$(sudo ip -n "$NETNS" -o link show dev "$INTERFACE" | awk -F: '{gsub(/[[:space:]]/, "", $1); print $1}')"
MTU="$(sudo ip -n "$NETNS" -o link show dev "$INTERFACE" | awk '{for(i=1;i<=NF;i++)if($i=="mtu"){print $(i+1);exit}}')"
for _ in $(seq 1 300); do
    if sudo python3 - "$STATE_DIR/state.json" <<'PY' >/dev/null 2>&1
import json, sys
try:
    state = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception:
    raise SystemExit(1)
raise SystemExit(0 if state.get("state") == "online" and state.get("session", {}).get("connected") is True else 1)
PY
    then
        break
    fi
    sudo kill -0 "$TOAD_PID" >/dev/null 2>&1 || fail "OpenConnect Toad exited before online state"
    sleep 0.2
done
sudo test -s "$STATE_DIR/state.json" || fail "Toad state.json was not published"
for _ in $(seq 1 100); do
    sudo test -s "$STATE_DIR/openconnect-network.env" && break
    sleep 0.1
done
sudo test -s "$STATE_DIR/openconnect-network.env" || fail "server-pushed OpenConnect network metadata was not published"
capture_state connected
RX_BEFORE="$(sudo ip netns exec "$NETNS" cat "/sys/class/net/$INTERFACE/statistics/rx_bytes")"
TX_BEFORE="$(sudo ip netns exec "$NETNS" cat "/sys/class/net/$INTERFACE/statistics/tx_bytes")"

DNS_RAW="$(sed -n '1p' "$DNS_OVERRIDE_FILE")"
if [[ -n "$DNS_RAW" ]]; then
    DNS_SOURCE="profile-override"
else
    DNS_RAW="$(sudo awk -F= '$1=="ipv4_dns" {sub(/^[^=]*=/, ""); print; exit}' "$STATE_DIR/openconnect-network.env")"
    DNS_SOURCE="server-pushed"
fi
read -r -a DNS_SERVERS <<<"$DNS_RAW"
(( ${#DNS_SERVERS[@]} > 0 )) || fail "OpenConnect supplied no IPv4 DNS; set dns_servers in the local secret profile only if necessary"
for dns in "${DNS_SERVERS[@]}"; do
    is_ipv4 "$dns" || fail "invalid IPv4 DNS from $DNS_SOURCE: $dns"
    sudo ip -n "$NETNS" route replace "$dns/32" dev "$INTERFACE"
done
{
    for dns in "${DNS_SERVERS[@]}"; do
        printf 'nameserver %s\n' "$dns"
    done
    printf 'options timeout:3 attempts:2\n'
} | sudo tee "$NETNS_ETC/resolv.conf" >/dev/null

GOOGLE_IP="$(sudo ip netns exec "$NETNS" getent ahostsv4 "$GOOGLE_HOST" | awk 'NR==1 {print $1; exit}' || true)"
INTERNAL_IP="$(sudo ip netns exec "$NETNS" getent ahostsv4 "$INTERNAL_HOST" | awk 'NR==1 {print $1; exit}' || true)"
is_ipv4 "$GOOGLE_IP" || fail "could not resolve Google using OpenConnect DNS"
is_ipv4 "$INTERNAL_IP" || fail "could not resolve $INTERNAL_HOST using OpenConnect DNS"
sudo ip -n "$NETNS" route replace "$GOOGLE_IP/32" dev "$INTERFACE"
sudo ip -n "$NETNS" route replace "$INTERNAL_IP/32" dev "$INTERFACE"
sudo ip -n "$NETNS" route get "$GOOGLE_IP" | grep -Fq "dev $INTERFACE" || fail "Google is not routed through $INTERFACE"
sudo ip -n "$NETNS" route get "$INTERNAL_IP" | grep -Fq "dev $INTERFACE" || fail "$INTERNAL_HOST is not routed through $INTERFACE"
sudo ip -n "$NETNS" route get "$ENDPOINT_IP" | grep -Fq "dev $UPLINK_IF" || fail "OpenConnect endpoint recursively entered $INTERFACE"
{
    printf 'dns_source=%s\n' "$DNS_SOURCE"
    printf 'dns_servers=%s\n' "$DNS_RAW"
    printf 'google_ip=%s\n' "$GOOGLE_IP"
    printf 'internal_gitlab_ip=%s\n' "$INTERNAL_IP"
    printf 'chatgpt=SKIPPED_EXPECTED_UNAVAILABLE\n'
} >>"$DIAG/traffic-test.txt"

log "start bounded traces and probe Google + internal GitLab"
sudo -- bash -c 'printf "%s\n" "$$" >"$1"; shift; exec "$@"' \
    bash "$TUN_TRACE_PID_FILE" ip netns exec "$NETNS" tcpdump -nn -l -i "$INTERFACE" -s 96 -c 1500 \
    "host $GOOGLE_IP or host $INTERNAL_IP" >>"$DIAG/tun-trace.txt" 2>&1 &
TUN_TRACE_SUDO_PID=$!
sudo -- bash -c 'printf "%s\n" "$$" >"$1"; shift; exec "$@"' \
    bash "$UP_TRACE_PID_FILE" ip netns exec "$NETNS" tcpdump -nn -l -i "$UPLINK_IF" -s 96 -c 1500 \
    "host $ENDPOINT_IP or host $GOOGLE_IP or host $INTERNAL_IP" >>"$DIAG/uplink-trace.txt" 2>&1 &
UP_TRACE_SUDO_PID=$!
for _ in $(seq 1 50); do
    sudo test -s "$TUN_TRACE_PID_FILE" && sudo test -s "$UP_TRACE_PID_FILE" && break
    sleep 0.05
done
TUN_TRACE_PID="$(sudo sed -n '1p' "$TUN_TRACE_PID_FILE" 2>/dev/null || true)"
UP_TRACE_PID="$(sudo sed -n '1p' "$UP_TRACE_PID_FILE" 2>/dev/null || true)"
[[ "$TUN_TRACE_PID" =~ ^[1-9][0-9]*$ && "$UP_TRACE_PID" =~ ^[1-9][0-9]*$ ]] || fail "could not start packet traces"
sleep 0.2

response="$(sudo ip netns exec "$NETNS" env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
    curl -4sS --noproxy '*' --interface "$INTERFACE" --connect-timeout 10 --max-time 30 \
    --resolve "$GOOGLE_HOST:443:$GOOGLE_IP" -o /dev/null \
    -w 'http_code=%{http_code} size_download=%{size_download} remote_ip=%{remote_ip} total_s=%{time_total}' \
    "https://$GOOGLE_HOST/")" || fail "Google transport failed through OpenConnect"
printf 'google=%s\n' "$response" >>"$DIAG/traffic-test.txt"
GOOGLE_HTTP_CODE="$(sed -n 's/.*http_code=\([^ ]*\).*/\1/p' <<<"$response")"
GOOGLE_BYTES="$(sed -n 's/.*size_download=\([^ ]*\).*/\1/p' <<<"$response")"
GOOGLE_REMOTE="$(sed -n 's/.*remote_ip=\([^ ]*\).*/\1/p' <<<"$response")"
[[ "$GOOGLE_HTTP_CODE" == "200" && "$GOOGLE_REMOTE" == "$GOOGLE_IP" ]] || fail "Google verification failed: $response"
[[ "$GOOGLE_BYTES" =~ ^[0-9]+$ ]] && (( GOOGLE_BYTES >= 10000 )) || fail "Google response too short: ${GOOGLE_BYTES:-unknown} bytes"

response="$(sudo ip netns exec "$NETNS" env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
    curl -4sS --noproxy '*' --interface "$INTERFACE" --connect-timeout 10 --max-time 30 \
    --resolve "$INTERNAL_HOST:443:$INTERNAL_IP" -o /dev/null \
    -w 'http_code=%{http_code} size_download=%{size_download} remote_ip=%{remote_ip} total_s=%{time_total}' \
    "https://$INTERNAL_HOST/")" || fail "$INTERNAL_HOST transport/TLS failed through OpenConnect"
printf 'internal_gitlab=%s\n' "$response" >>"$DIAG/traffic-test.txt"
INTERNAL_HTTP_CODE="$(sed -n 's/.*http_code=\([^ ]*\).*/\1/p' <<<"$response")"
INTERNAL_BYTES="$(sed -n 's/.*size_download=\([^ ]*\).*/\1/p' <<<"$response")"
INTERNAL_REMOTE="$(sed -n 's/.*remote_ip=\([^ ]*\).*/\1/p' <<<"$response")"
[[ "$INTERNAL_HTTP_CODE" =~ ^(2[0-9][0-9]|3[0-9][0-9]|401|403)$ && "$INTERNAL_REMOTE" == "$INTERNAL_IP" ]] || fail "$INTERNAL_HOST verification failed: $response"
[[ "$INTERNAL_BYTES" =~ ^[0-9]+$ ]] || fail "$INTERNAL_HOST returned invalid byte count"

sleep 0.5
stop_trace
grep -Fq "$GOOGLE_IP" "$DIAG/tun-trace.txt" || fail "Google packets were not observed on $INTERFACE"
grep -Fq "$INTERNAL_IP" "$DIAG/tun-trace.txt" || fail "$INTERNAL_HOST packets were not observed on $INTERFACE"
grep -Fq "$ENDPOINT_IP" "$DIAG/uplink-trace.txt" || fail "OpenConnect transport was not observed on $UPLINK_IF"
if grep -Fq "$GOOGLE_IP" "$DIAG/uplink-trace.txt" || grep -Fq "$INTERNAL_IP" "$DIAG/uplink-trace.txt"; then
    fail "target traffic leaked directly onto isolated uplink $UPLINK_IF"
fi
RX_AFTER="$(sudo ip netns exec "$NETNS" cat "/sys/class/net/$INTERFACE/statistics/rx_bytes")"
TX_AFTER="$(sudo ip netns exec "$NETNS" cat "/sys/class/net/$INTERFACE/statistics/tx_bytes")"
(( RX_AFTER > RX_BEFORE )) || fail "TUN RX counter did not advance"
(( TX_AFTER > TX_BEFORE )) || fail "TUN TX counter did not advance"
capture_state active
ns_capture active
RESULT="PASS"
log "PASS: real OpenConnect server, Google and internal GitLab crossed isolated $INTERFACE; ChatGPT intentionally skipped"
