#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$ROOT/../../.." && pwd)"
# shellcheck source=linux/tests/toad/lib/openconnect-real-profile.sh
source "$ROOT/lib/openconnect-real-profile.sh"

SECRET_FILE="${TOAD_OPENCONNECT_PROFILE:-$ROOT/real-vps-openconnect.secret}"
INTERFACE="kk-oc0"
NAME="real-openconnect-system-wide"
HOLD_SECONDS="${TOAD_SYSTEM_WIDE_HOLD_SECONDS:-120}"
GOOGLE_HOST="www.google.com"
INTERNAL_HOST="gitlab.sca.ad-tech.ru"
ENDPOINT_ROUTE_METRIC=42742
CALLER_UID="$(id -u)"
CALLER_GID="$(id -g)"
UNIT="kikimora-toad-openconnect-system-wide-$CALLER_UID-$$.service"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/toad-openconnect-system-wide.XXXXXX")"
STATE_DIR="$TMP/state"
CONFIG="$TMP/profile.toml"
PASSWORD_FILE="$TMP/password.secret"
TOKEN_FILE="$TMP/totp.secret"
DNS_OVERRIDE_FILE="$TMP/dns-override.txt"
BIN="$TMP/kikimora-toad"
DIAG="$TMP/diag"
ARCHIVE="${TOAD_SYSTEM_WIDE_DIAG_OUTPUT:-$PWD/toad-system-wide-openconnect-diag-$(date +%Y-%m-%d-%H%M%S).tar.gz}"

TOAD_STARTED=0
DEFAULT_ROUTE_ADDED=0
DNS_CONFIGURED=0
ENDPOINT_ROUTE_ADDED=0
TRACE_SUDO_PID=""
SAMPLER_PID=""
EXPECTED_IFINDEX=""
GATEWAY=""
GATEWAY_HOST=""
GATEWAY_PORT="443"
ENDPOINT_IP=""
UNDERLAY_DEV=""
UNDERLAY_GATEWAY=""
UNDERLAY_SRC=""
DNS_SOURCE=""
RESULT="FAIL"
FAIL_REASON=""
GOOGLE_IP=""
GOOGLE_HTTP_CODE=""
GOOGLE_BYTES=""
INTERNAL_IP=""
INTERNAL_HTTP_CODE=""
INTERNAL_BYTES=""
PUBLIC_IP=""
START_EPOCH="$(date +%s)"

log() {
    printf '[%s] %s\n' "$(date -Is)" "$*"
    printf '[%s] %s\n' "$(date -Is)" "$*" >>"$DIAG/command.log" 2>/dev/null || true
}

fail() {
    FAIL_REASON="$*"
    printf 'ERROR: %s\n' "$*" >&2
    printf '[%s] ERROR: %s\n' "$(date -Is)" "$*" >>"$DIAG/errors.txt" 2>/dev/null || true
    return 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

is_ipv4() {
    python3 - "$1" <<'PY' >/dev/null 2>&1
import ipaddress, sys
try:
    ip = ipaddress.ip_address(sys.argv[1])
except ValueError:
    raise SystemExit(1)
raise SystemExit(0 if ip.version == 4 else 1)
PY
}

is_safe_interface() {
    [[ "$1" =~ ^[A-Za-z0-9_.:-]+$ ]]
}

is_vpn_interface_name() {
    case "$1" in
        vpn*|tun*|tap*|wg*|amn*|kk-*|tailscale*|zt*|warp*|proton*) return 0 ;;
        *) return 1 ;;
    esac
}

assert_no_other_vpn() {
    local unit line dev
    local -a reasons=()

    for unit in leshy.service leshy-route-watch.service leshy-health-watch.service; do
        systemctl is-active --quiet "$unit" && reasons+=("active unit $unit")
    done
    if ps -eo comm= | awk '$1 == "leshy" { found=1 } END { exit(found ? 0 : 1) }'; then
        reasons+=("running process leshy")
    fi
    while IFS= read -r unit; do
        [[ -n "$unit" && "$unit" != "$UNIT" ]] && reasons+=("active Toad unit $unit")
    done < <(systemctl list-units --type=service --state=active --no-legend 'kikimora-toad*.service' 2>/dev/null | awk '{print $1}')
    if command -v nmcli >/dev/null 2>&1; then
        while IFS= read -r line; do
            case "$line" in
                vpn:*|wireguard:*|tun:*) reasons+=("active NetworkManager connection $line") ;;
            esac
        done < <(nmcli -t -f TYPE,DEVICE connection show --active 2>/dev/null || true)
    fi
    while IFS= read -r dev; do
        [[ -n "$dev" && "$dev" != "$INTERFACE" ]] || continue
        is_vpn_interface_name "$dev" && reasons+=("active default route through $dev")
    done < <(ip -o -4 route show default 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev"){print $(i+1)}}' | sort -u)

    if (( ${#reasons[@]} )); then
        printf 'ERROR: another VPN appears active; this test never stops it for you:\n' >&2
        printf '  - %s\n' "${reasons[@]}" >&2
        return 1
    fi
}

route_parts() {
    local route="$1"
    UNDERLAY_DEV="$(awk '{for(i=1;i<=NF;i++) if($i=="dev"){print $(i+1); exit}}' <<<"$route")"
    UNDERLAY_GATEWAY="$(awk '{for(i=1;i<=NF;i++) if($i=="via"){print $(i+1); exit}}' <<<"$route")"
    UNDERLAY_SRC="$(awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}' <<<"$route")"
}

collect_state() {
    local name="$1"
    if sudo test -s "$STATE_DIR/state.json"; then
        sudo cat "$STATE_DIR/state.json" >"$DIAG/state-$name.json" 2>/dev/null || true
    else
        printf '{"present":false}\n' >"$DIAG/state-$name.json"
    fi
    if sudo test -s "$STATE_DIR/openconnect-network.env"; then
        sudo cat "$STATE_DIR/openconnect-network.env" >"$DIAG/openconnect-network-$name.env" 2>/dev/null || true
    fi
}

collect_snapshot() {
    local phase="$1"
    local dir="$DIAG/$phase"
    mkdir -p "$dir"
    {
        date -Is
        uname -a
        printf '\n=== links ===\n'
        ip -details -statistics link show
        printf '\n=== addresses ===\n'
        ip -details -statistics addr show
    } >"$dir/network.txt" 2>&1 || true
    {
        printf '=== IPv4 routes all tables ===\n'
        ip -4 route show table all
        printf '\n=== IPv6 routes all tables ===\n'
        ip -6 route show table all
        printf '\n=== rules ===\n'
        ip rule show
    } >"$dir/routes.txt" 2>&1 || true
    {
        printf '=== /etc/resolv.conf ===\n'
        ls -l /etc/resolv.conf
        sed -n '1,160p' /etc/resolv.conf
        if command -v resolvectl >/dev/null 2>&1; then
            printf '\n=== resolvectl ===\n'
            resolvectl status || true
            printf '\n=== %s ===\n' "$INTERFACE"
            resolvectl status "$INTERFACE" || true
        fi
    } >"$dir/dns.txt" 2>&1 || true
    {
        ss -tunap || true
        printf '\n=== processes without argv ===\n'
        ps -eo pid,ppid,user,group,stat,etimes,comm
        printf '\n=== unit ===\n'
        sudo systemctl status "$UNIT" --no-pager || true
    } >"$dir/runtime.txt" 2>&1 || true
    collect_state "$phase"
}

write_config_summary() {
    python3 - "$CONFIG" "$DIAG/config-summary.json" <<'PY'
import hashlib, json, pathlib, sys, tomllib
cfg = tomllib.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
oc = cfg.get("openconnect") or {}
summary = {
    "fingerprint_sha256": hashlib.sha256(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest(),
    "name": cfg.get("name"),
    "protocol": cfg.get("protocol"),
    "interface": cfg.get("interface"),
    "mtu": cfg.get("mtu"),
    "gateway": oc.get("gateway"),
    "vpn_protocol": oc.get("vpn_protocol"),
    "token_mode": oc.get("token_mode"),
    "disable_udp": oc.get("disable_udp"),
    "disable_ipv6": oc.get("disable_ipv6"),
    "reconnect_timeout": oc.get("reconnect_timeout"),
    "password_file_present": bool(oc.get("password_file")),
    "token_secret_file_present": bool(oc.get("token_secret_file")),
}
pathlib.Path(sys.argv[2]).write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
}

redact_diag() {
    python3 - "$SECRET_FILE" "$DIAG" <<'PY'
import pathlib, re, sys, tomllib
source = pathlib.Path(sys.argv[1])
root = pathlib.Path(sys.argv[2])
try:
    cfg = tomllib.loads(source.read_text(encoding="utf-8"))
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
keep = 64 * 1024 if aggressive else 256 * 1024
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
        printf 'protocol=openconnect\n'
        printf 'interface=%s\n' "$INTERFACE"
        printf 'ifindex=%s\n' "${EXPECTED_IFINDEX:-unavailable}"
        printf 'gateway=%s\n' "$GATEWAY"
        printf 'endpoint_ip=%s\n' "$ENDPOINT_IP"
        printf 'underlay_dev=%s\n' "$UNDERLAY_DEV"
        printf 'dns_source=%s\n' "${DNS_SOURCE:-unavailable}"
        printf 'google_ip=%s\n' "${GOOGLE_IP:-unavailable}"
        printf 'google_http_code=%s\n' "${GOOGLE_HTTP_CODE:-unavailable}"
        printf 'google_response_bytes=%s\n' "${GOOGLE_BYTES:-unavailable}"
        printf 'internal_host=%s\n' "$INTERNAL_HOST"
        printf 'internal_ip=%s\n' "${INTERNAL_IP:-unavailable}"
        printf 'internal_http_code=%s\n' "${INTERNAL_HTTP_CODE:-unavailable}"
        printf 'internal_response_bytes=%s\n' "${INTERNAL_BYTES:-unavailable}"
        printf 'chatgpt_probe=SKIPPED_EXPECTED_UNAVAILABLE\n'
        printf 'public_ip=%s\n' "${PUBLIC_IP:-unavailable}"
        printf 'manual_window_seconds=%s\n' "$HOLD_SECONDS"
        printf 'started_epoch=%s\n' "$START_EPOCH"
        printf 'ended_at=%s\n' "$(date -Is)"
        printf '\n=== traffic test ===\n'
        cat "$DIAG/traffic-test.txt" 2>/dev/null || true
        printf '\n=== errors ===\n'
        cat "$DIAG/errors.txt" 2>/dev/null || true
        printf '\n=== final state ===\n'
        cat "$DIAG/state-active-final.json" 2>/dev/null || cat "$DIAG/state-active.json" 2>/dev/null || true
    } >"$DIAG/summary.txt"
}

package_diag() {
    local bytes
    sudo journalctl --no-pager -u "$UNIT" --since "@$START_EPOCH" -n 1200 >"$DIAG/toad-journal.txt" 2>&1 || true
    sudo journalctl -k --no-pager --since "@$START_EPOCH" -n 600 >"$DIAG/kernel-journal.txt" 2>&1 || true
    write_summary
    redact_diag
    compact_diag 0
    tar -czf "$ARCHIVE" -C "$TMP" diag
    bytes="$(stat -c '%s' "$ARCHIVE")"
    if (( bytes > 4 * 1024 * 1024 )); then
        compact_diag 1
        tar -czf "$ARCHIVE" -C "$TMP" diag
    fi
    chmod 0600 "$ARCHIVE"
    printf 'Diagnostic archive: %s (%s bytes)\n' "$ARCHIVE" "$(stat -c '%s' "$ARCHIVE")"
}

stop_background() {
    if [[ -n "$SAMPLER_PID" ]]; then
        kill "$SAMPLER_PID" 2>/dev/null || true
        wait "$SAMPLER_PID" 2>/dev/null || true
        SAMPLER_PID=""
    fi
    if [[ -n "$TRACE_SUDO_PID" ]]; then
        kill -INT "$TRACE_SUDO_PID" 2>/dev/null || true
        wait "$TRACE_SUDO_PID" 2>/dev/null || true
        TRACE_SUDO_PID=""
    fi
}

remove_endpoint_route() {
    local -a route
    (( ENDPOINT_ROUTE_ADDED )) || return 0
    route=(sudo ip -4 route del "$ENDPOINT_IP/32")
    [[ -n "$UNDERLAY_GATEWAY" ]] && route+=(via "$UNDERLAY_GATEWAY")
    route+=(dev "$UNDERLAY_DEV")
    [[ -n "$UNDERLAY_SRC" ]] && route+=(src "$UNDERLAY_SRC")
    route+=(metric "$ENDPOINT_ROUTE_METRIC")
    "${route[@]}" >/dev/null 2>&1 || true
    ENDPOINT_ROUTE_ADDED=0
}

rollback() {
    local current_ifindex=""
    set +e
    stop_background
    collect_state active-final
    collect_snapshot active-final
    if (( DEFAULT_ROUTE_ADDED )); then
        sudo ip -4 route del default dev "$INTERFACE" metric 4 >/dev/null 2>&1 || true
        DEFAULT_ROUTE_ADDED=0
    fi
    if (( DNS_CONFIGURED )); then
        sudo resolvectl revert "$INTERFACE" >/dev/null 2>&1 || true
        DNS_CONFIGURED=0
    fi
    if (( TOAD_STARTED )); then
        sudo systemctl stop "$UNIT" >/dev/null 2>&1 || true
        sudo systemctl reset-failed "$UNIT" >/dev/null 2>&1 || true
        TOAD_STARTED=0
    fi
    for _ in $(seq 1 80); do
        ip link show dev "$INTERFACE" >/dev/null 2>&1 || break
        sleep 0.1
    done
    if ip -o link show dev "$INTERFACE" >/dev/null 2>&1; then
        current_ifindex="$(ip -o link show dev "$INTERFACE" | awk -F: '{gsub(/[[:space:]]/, "", $1); print $1}')"
        if [[ -n "$EXPECTED_IFINDEX" && "$current_ifindex" == "$EXPECTED_IFINDEX" ]]; then
            sudo ip link delete dev "$INTERFACE" >/dev/null 2>&1 || true
        fi
    fi
    remove_endpoint_route
    collect_snapshot after
    collect_state after
    set -e
}

on_exit() {
    local status=$?
    trap - EXIT INT TERM
    if (( status != 0 )) && [[ -z "$FAIL_REASON" ]]; then
        FAIL_REASON="script exited with status $status"
    fi
    rollback
    package_diag || true
    sudo rm -rf -- "$STATE_DIR" 2>/dev/null || true
    rm -rf -- "$TMP" 2>/dev/null || true
    if [[ "$RESULT" == "PASS" && $status -eq 0 ]]; then
        exit 0
    fi
    exit "${status:-1}"
}
trap on_exit EXIT INT TERM

mkdir -p "$DIAG"
chmod 0700 "$TMP" "$DIAG"
: >"$DIAG/command.log"
: >"$DIAG/errors.txt"
: >"$DIAG/traffic-test.txt"
: >"$DIAG/address-trace.txt"
: >"$DIAG/manual-sampler.txt"

[[ "$HOLD_SECONDS" =~ ^[0-9]+$ ]] && (( HOLD_SECONDS >= 10 && HOLD_SECONDS <= 900 )) || fail "TOAD_SYSTEM_WIDE_HOLD_SECONDS must be 10..900"
[[ "$ARCHIVE" == *.tar.gz ]] || fail "diagnostic output must end in .tar.gz"
[[ "$ARCHIVE" == /* ]] || ARCHIVE="$PWD/$ARCHIVE"

if (( EUID == 0 )); then
    fail "run this test as the desktop user, not through sudo"
fi

for command in go sudo ip python3 openconnect systemd-run systemctl resolvectl getent curl tcpdump ss tar awk sed grep stat; do
    require_command "$command"
done
oc_profile_require_secret_file "$SECRET_FILE"
sudo -v
assert_no_other_vpn
if ip -6 route show default | grep -q .; then
    fail "an IPv6 default route exists; this IPv4-only system-wide test refuses to risk an IPv6 leak"
fi
ip link show dev "$INTERFACE" >/dev/null 2>&1 && fail "$INTERFACE already exists"

log "building checked-out kikimora-toad"
(cd "$REPO_ROOT/toad" && go build -o "$BIN" ./cmd/kikimora-toad)
chmod 0755 "$BIN"
mkdir -p "$STATE_DIR"
oc_profile_materialize "$SECRET_FILE" "$CONFIG" "$STATE_DIR" "$PASSWORD_FILE" "$TOKEN_FILE" "$DNS_OVERRIDE_FILE" "$NAME" "$INTERFACE"
"$BIN" validate -config "$CONFIG"
write_config_summary

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

collect_snapshot before
collect_state before
{
    printf 'baseline_gitlab_resolution='
    getent ahostsv4 "$INTERNAL_HOST" | awk 'NR==1 {print $1; exit}' || true
    printf 'baseline_gateway_route='
    ip -4 route get "$ENDPOINT_IP" || true
} >>"$DIAG/traffic-test.txt"

endpoint_route="$(ip -4 route get "$ENDPOINT_IP" 2>/dev/null || true)"
[[ -n "$endpoint_route" ]] || fail "no route to OpenConnect gateway $ENDPOINT_IP"
route_parts "$endpoint_route"
is_safe_interface "$UNDERLAY_DEV" || fail "unsafe underlay interface name: $UNDERLAY_DEV"
is_vpn_interface_name "$UNDERLAY_DEV" && fail "OpenConnect gateway currently routes through VPN-like interface $UNDERLAY_DEV; disable the old VPN yourself first"

if ! ip -4 route show exact "$ENDPOINT_IP/32" | grep -q .; then
    endpoint_add=(sudo ip -4 route add "$ENDPOINT_IP/32")
    [[ -n "$UNDERLAY_GATEWAY" ]] && endpoint_add+=(via "$UNDERLAY_GATEWAY")
    endpoint_add+=(dev "$UNDERLAY_DEV")
    [[ -n "$UNDERLAY_SRC" ]] && endpoint_add+=(src "$UNDERLAY_SRC")
    endpoint_add+=(metric "$ENDPOINT_ROUTE_METRIC")
    "${endpoint_add[@]}"
    ENDPOINT_ROUTE_ADDED=1
fi

log "starting real OpenConnect Toad against $GATEWAY_HOST:$GATEWAY_PORT"
TOAD_STARTED=1
sudo systemd-run --unit="$UNIT" --collect --quiet \
    --property=Type=simple --property=Restart=no --property=KillMode=control-group --property=TimeoutStopSec=10s \
    "$BIN" run -config "$CONFIG"

for _ in $(seq 1 300); do
    if line="$(ip -o link show dev "$INTERFACE" 2>/dev/null)"; then
        EXPECTED_IFINDEX="${line%%:*}"
        EXPECTED_IFINDEX="${EXPECTED_IFINDEX//[[:space:]]/}"
        break
    fi
    systemctl is-active --quiet "$UNIT" || fail "kikimora-toad exited before $INTERFACE appeared"
    sleep 0.1
done
[[ "$EXPECTED_IFINDEX" =~ ^[1-9][0-9]*$ ]] || fail "timed out waiting for $INTERFACE"

state_online=0
for _ in $(seq 1 150); do
    if sudo python3 - "$STATE_DIR/state.json" <<'PY' >/dev/null 2>&1
import json, sys
try:
    with open(sys.argv[1], encoding="utf-8") as fh:
        s = json.load(fh)
except Exception:
    raise SystemExit(1)
raise SystemExit(0 if s.get("state") == "online" and s.get("session", {}).get("connected") is True else 1)
PY
    then
        state_online=1
        break
    fi
    systemctl is-active --quiet "$UNIT" || fail "OpenConnect Toad exited before online state"
    sleep 0.2
done
(( state_online )) || fail "OpenConnect Toad did not publish online state"

for _ in $(seq 1 100); do
    sudo test -s "$STATE_DIR/openconnect-network.env" && break
    sleep 0.1
done
sudo test -s "$STATE_DIR/openconnect-network.env" || fail "OpenConnect server-pushed network metadata was not published"
collect_snapshot connected
collect_state connected

DNS_RAW="$(sed -n '1p' "$DNS_OVERRIDE_FILE")"
if [[ -n "$DNS_RAW" ]]; then
    DNS_SOURCE="local-profile-override"
else
    DNS_RAW="$(sudo awk -F= '$1=="ipv4_dns" {sub(/^[^=]*=/, ""); print; exit}' "$STATE_DIR/openconnect-network.env")"
    DNS_SOURCE="server-pushed"
fi
read -r -a DNS_SERVERS <<<"$DNS_RAW"
(( ${#DNS_SERVERS[@]} > 0 )) || fail "OpenConnect supplied no IPv4 DNS; set dns_servers in $SECRET_FILE only if the server really omits it"
for dns in "${DNS_SERVERS[@]}"; do
    is_ipv4 "$dns" || fail "invalid IPv4 DNS from $DNS_SOURCE: $dns"
done

DEFAULT_ROUTE_ADDED=1
sudo ip -4 route add default dev "$INTERFACE" metric 4
ip -4 route get "$ENDPOINT_IP" | grep -Fq "dev $UNDERLAY_DEV" || fail "OpenConnect gateway recursively entered $INTERFACE"
DNS_CONFIGURED=1
sudo resolvectl dns "$INTERFACE" "${DNS_SERVERS[@]}"
sudo resolvectl domain "$INTERFACE" '~.'
sudo resolvectl default-route "$INTERFACE" yes

GOOGLE_IP="$(getent ahostsv4 "$GOOGLE_HOST" | awk 'NR==1 {print $1; exit}' || true)"
is_ipv4 "$GOOGLE_IP" || fail "could not resolve Google through OpenConnect DNS"
INTERNAL_IP="$(getent ahostsv4 "$INTERNAL_HOST" | awk 'NR==1 {print $1; exit}' || true)"
is_ipv4 "$INTERNAL_IP" || fail "could not resolve internal $INTERNAL_HOST through OpenConnect DNS"
ip -4 route get "$GOOGLE_IP" | grep -Fq "dev $INTERFACE" || fail "Google is not routed through $INTERFACE"
ip -4 route get "$INTERNAL_IP" | grep -Fq "dev $INTERFACE" || fail "$INTERNAL_HOST is not routed through $INTERFACE"

trace_filter="host $ENDPOINT_IP or host $GOOGLE_IP or host $INTERNAL_IP"
printf 'capture_filter=%s\n' "$trace_filter" >>"$DIAG/address-trace.txt"
sudo tcpdump -nn -l -i any -s 96 -c 2000 "$trace_filter" >>"$DIAG/address-trace.txt" 2>&1 &
TRACE_SUDO_PID=$!
sleep 0.3

log "strict probe: Google must work through OpenConnect"
response="$(env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
    curl -4sS --noproxy '*' --connect-timeout 10 --max-time 30 \
    --resolve "$GOOGLE_HOST:443:$GOOGLE_IP" -o /dev/null \
    -w 'http_code=%{http_code} size_download=%{size_download} remote_ip=%{remote_ip} total_s=%{time_total}' \
    "https://$GOOGLE_HOST/")" || fail "Google transport failed through OpenConnect"
printf 'google=%s\n' "$response" >>"$DIAG/traffic-test.txt"
GOOGLE_HTTP_CODE="$(sed -n 's/.*http_code=\([^ ]*\).*/\1/p' <<<"$response")"
GOOGLE_BYTES="$(sed -n 's/.*size_download=\([^ ]*\).*/\1/p' <<<"$response")"
GOOGLE_REMOTE="$(sed -n 's/.*remote_ip=\([^ ]*\).*/\1/p' <<<"$response")"
[[ "$GOOGLE_HTTP_CODE" == "200" && "$GOOGLE_REMOTE" == "$GOOGLE_IP" ]] || fail "Google verification failed: $response"
[[ "$GOOGLE_BYTES" =~ ^[0-9]+$ ]] && (( GOOGLE_BYTES >= 10000 )) || fail "Google response was too short: ${GOOGLE_BYTES:-unknown} bytes"

log "strict probe: internal GitLab must be reachable only through this VPN"
response="$(env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
    curl -4sS --noproxy '*' --connect-timeout 10 --max-time 30 \
    --resolve "$INTERNAL_HOST:443:$INTERNAL_IP" -o /dev/null \
    -w 'http_code=%{http_code} size_download=%{size_download} remote_ip=%{remote_ip} total_s=%{time_total}' \
    "https://$INTERNAL_HOST/")" || fail "$INTERNAL_HOST transport/TLS failed through OpenConnect"
printf 'internal_gitlab=%s\n' "$response" >>"$DIAG/traffic-test.txt"
INTERNAL_HTTP_CODE="$(sed -n 's/.*http_code=\([^ ]*\).*/\1/p' <<<"$response")"
INTERNAL_BYTES="$(sed -n 's/.*size_download=\([^ ]*\).*/\1/p' <<<"$response")"
INTERNAL_REMOTE="$(sed -n 's/.*remote_ip=\([^ ]*\).*/\1/p' <<<"$response")"
[[ "$INTERNAL_HTTP_CODE" =~ ^[234][0-9][0-9]$ && "$INTERNAL_REMOTE" == "$INTERNAL_IP" ]] || fail "$INTERNAL_HOST verification failed: $response"

printf 'chatgpt=SKIPPED_EXPECTED_UNAVAILABLE\n' >>"$DIAG/traffic-test.txt"
PUBLIC_IP="$(env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
    curl -4fsS --noproxy '*' --connect-timeout 5 --max-time 15 https://api.ipify.org 2>/dev/null || true)"
collect_snapshot active
collect_state active

(
    limit=$(( (HOLD_SECONDS + 1) / 2 ))
    for _ in $(seq 1 "$limit"); do
        {
            printf '\n=== %s ===\n' "$(date -Is)"
            ip -4 route show default
            ip -4 route get "$ENDPOINT_IP" || true
            ip -s link show dev "$INTERFACE" || true
            sudo cat "$STATE_DIR/state.json" 2>/dev/null || true
        } >>"$DIAG/manual-sampler.txt" 2>&1
        sleep 2
    done
) &
SAMPLER_PID=$!

printf '\nSYSTEM-WIDE OPENCONNECT IS UP for up to %s seconds.\n' "$HOLD_SECONDS"
printf 'Try the browser now:\n'
printf '  Google: https://www.google.com/\n'
printf '  Internal: https://%s/\n' "$INTERNAL_HOST"
printf '  ChatGPT is expected NOT to work on this VPN and is not a test gate.\n'
printf 'Press Enter to finish early; otherwise automatic rollback happens after %s seconds.\n\n' "$HOLD_SECONDS"
read -r -t "$HOLD_SECONDS" _ || true

RESULT="PASS"
log "manual window finished; automatic rollback begins"
exit 0
