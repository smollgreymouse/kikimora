#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

# Real-host system-wide Toad diagnostic harness.
# The caller must provide the share link and protocol/interface identifiers.
# This harness never starts/stops/modifies the legacy Kikimora services.

SW_WORK=""
SW_DIAG_DIR=""
SW_PRIVATE_DIR=""
SW_ARCHIVE=""
SW_TOAD_BIN=""
SW_TOAD_PID=""
SW_SUDO_PID=""
SW_TRACE_TUN_PID=""
SW_TRACE_TUN_SUDO_PID=""
SW_TRACE_UPLINK_PID=""
SW_TRACE_UPLINK_SUDO_PID=""
SW_SAMPLE_PID=""
SW_PROTOCOL=""
SW_INTERFACE=""
SW_PROFILE_NAME=""
SW_STATE_DIR=""
SW_CONFIG=""
SW_ENDPOINT=""
SW_ENDPOINT_HOST=""
SW_ENDPOINT_IP=""
SW_ENDPOINT_PORT=""
SW_UNDERLAY_DEV=""
SW_UNDERLAY_GATEWAY=""
SW_UNDERLAY_SRC=""
SW_ENDPOINT_ROUTE_ADDED=0
SW_DEFAULT_ROUTE_ADDED=0
SW_RESOLVED_CONFIGURED=0
SW_TOAD_STARTED=0
SW_RESULT="FAIL"
SW_DATA_PLANE_RESULT="NOT_RUN"
SW_CHATGPT_RESULT="NOT_RUN"
SW_WARNING_COUNT=0
SW_STARTED_AT=""
SW_START_EPOCH=""
SW_START_MS=""
SW_ACTIVE_START_MS=""
SW_ACTIVE_DURATION_MS=""
SW_IMPORT_MS=""
SW_INTERFACE_WAIT_MS=""
SW_IFINDEX=""
SW_MTU=""
SW_RX_BEFORE=""
SW_TX_BEFORE=""
SW_RX_AFTER=""
SW_TX_AFTER=""
SW_GOOGLE_HOST="www.google.com"
SW_GOOGLE_IP=""
SW_GOOGLE_HTTP_CODE=""
SW_GOOGLE_BYTES=""
SW_GOOGLE_REMOTE_IP=""
SW_CHATGPT_HOST="${TOAD_TEST_HOST:-chatgpt.com}"
SW_CHATGPT_IP=""
SW_CHATGPT_HTTP_CODE=""
SW_CHATGPT_BYTES=""
SW_CHATGPT_REMOTE_IP=""
SW_PUBLIC_IP_BEFORE=""
SW_PUBLIC_IP_DURING=""
SW_MANUAL_TIMEOUT="${TOAD_SYSTEM_WIDE_HOLD_SECONDS:-600}"
SW_DNS_SERVERS="${TOAD_SYSTEM_WIDE_DNS:-1.1.1.1 1.0.0.1}"

sw_now_ms() {
    date +%s%3N
}

sw_log() {
    printf '[%s] %s\n' "$(date -Is)" "$*" | tee -a "$SW_DIAG_DIR/command.log"
}

sw_warn() {
    SW_WARNING_COUNT=$((SW_WARNING_COUNT + 1))
    printf '[%s] %s\n' "$(date -Is)" "$*" >>"$SW_DIAG_DIR/warnings.txt"
    printf 'WARNING: %s\n' "$*" >&2
}

sw_error() {
    printf '[%s] %s\n' "$(date -Is)" "$*" >>"$SW_DIAG_DIR/errors.txt"
    printf 'ERROR: %s\n' "$*" >&2
    return 1
}

sw_require() {
    command -v "$1" >/dev/null 2>&1 || sw_error "required command not found: $1"
}

sw_capture_cmd() {
    local file="$1"
    shift
    {
        printf '$'
        printf ' %q' "$@"
        printf '\n'
        "$@"
    } >>"$file" 2>&1 || true
}

sw_collect_snapshot() {
    local phase="$1"
    local prefix="$SW_DIAG_DIR/$phase"

    mkdir -p "$prefix"

    {
        date -Is
        uname -a
        printf '\n=== ip link ===\n'
        ip -details -statistics link show || true
        printf '\n=== ip addr ===\n'
        ip -details -statistics addr show || true
        printf '\n=== ip neigh ===\n'
        ip neigh show || true
    } >"$prefix/network.txt" 2>&1

    {
        printf '=== IPv4 routes, all tables ===\n'
        ip -4 route show table all || true
        printf '\n=== IPv6 routes, all tables ===\n'
        ip -6 route show table all || true
        printf '\n=== rules ===\n'
        ip rule show || true
        printf '\n=== endpoint route ===\n'
        [[ -n "$SW_ENDPOINT_IP" ]] && ip -4 route get "$SW_ENDPOINT_IP" || true
        printf '\n=== Google route ===\n'
        [[ -n "$SW_GOOGLE_IP" ]] && ip -4 route get "$SW_GOOGLE_IP" || true
        printf '\n=== ChatGPT route ===\n'
        [[ -n "$SW_CHATGPT_IP" ]] && ip -4 route get "$SW_CHATGPT_IP" || true
    } >"$prefix/routes.txt" 2>&1

    {
        printf '=== /etc/resolv.conf ===\n'
        ls -l /etc/resolv.conf 2>/dev/null || true
        sed -n '1,200p' /etc/resolv.conf 2>/dev/null || true
        if command -v resolvectl >/dev/null 2>&1; then
            printf '\n=== resolvectl status ===\n'
            resolvectl status || true
            if [[ -n "$SW_INTERFACE" ]]; then
                printf '\n=== resolvectl interface ===\n'
                resolvectl status "$SW_INTERFACE" || true
            fi
        fi
    } >"$prefix/dns.txt" 2>&1

    {
        ss -tunap || true
        printf '\n=== processes ===\n'
        # Omit argv: the test may have been invoked with a secret share link.
        ps -eo pid,ppid,user,group,stat,etimes,comm || true
    } >"$prefix/runtime.txt" 2>&1

    {
        if command -v nmcli >/dev/null 2>&1; then
            printf '=== nmcli general ===\n'
            nmcli general status || true
            printf '\n=== nmcli devices ===\n'
            nmcli device status || true
            printf '\n=== nmcli active connections ===\n'
            nmcli -f NAME,UUID,TYPE,DEVICE connection show --active || true
        else
            printf 'nmcli unavailable\n'
        fi
        if command -v networkctl >/dev/null 2>&1; then
            printf '\n=== networkctl ===\n'
            networkctl status --no-pager || true
        fi
    } >"$prefix/network-manager.txt" 2>&1

    {
        printf '=== nftables ===\n'
        if command -v nft >/dev/null 2>&1; then
            sudo nft list ruleset || true
        else
            printf 'nft unavailable\n'
        fi
        printf '\n=== iptables-save ===\n'
        if command -v iptables-save >/dev/null 2>&1; then
            sudo iptables-save || true
        else
            printf 'iptables-save unavailable\n'
        fi
        printf '\n=== ip6tables-save ===\n'
        if command -v ip6tables-save >/dev/null 2>&1; then
            sudo ip6tables-save || true
        else
            printf 'ip6tables-save unavailable\n'
        fi
    } >"$prefix/firewall.txt" 2>&1

    {
        printf '=== selected sysctls ===\n'
        sysctl net.ipv4.ip_forward 2>/dev/null || true
        sysctl net.ipv4.conf.all.rp_filter 2>/dev/null || true
        sysctl net.ipv4.conf.default.rp_filter 2>/dev/null || true
        sysctl net.ipv6.conf.all.disable_ipv6 2>/dev/null || true
    } >"$prefix/sysctl.txt" 2>&1
}

sw_capture_state() {
    local target="$1"
    if [[ -s "$SW_STATE_DIR/state.json" ]]; then
        sudo cat "$SW_STATE_DIR/state.json" >"$target" 2>/dev/null || printf '{"present":false}\n' >"$target"
    else
        printf '{"present":false}\n' >"$target"
    fi
}

sw_read_counter() {
    local name="$1"
    sed -n '1p' "/sys/class/net/$SW_INTERFACE/statistics/$name" 2>/dev/null || true
}

sw_process_alive() {
    [[ -n "$SW_TOAD_PID" && -d "/proc/$SW_TOAD_PID" ]]
}

sw_wait_interface() {
    local started line
    started="$(sw_now_ms)"
    for _ in $(seq 1 150); do
        if line="$(ip -o link show dev "$SW_INTERFACE" 2>/dev/null)"; then
            SW_IFINDEX="${line%%:*}"
            SW_IFINDEX="${SW_IFINDEX//[[:space:]]/}"
            SW_MTU="$(awk '{for (i=1; i<=NF; i++) if ($i == "mtu") {print $(i+1); exit}}' <<<"$line")"
            SW_INTERFACE_WAIT_MS=$(( $(sw_now_ms) - started ))
            return 0
        fi
        sw_process_alive || sw_error "kikimora-toad exited before $SW_INTERFACE appeared"
        sleep 0.1
    done
    sw_error "timed out waiting for $SW_INTERFACE"
}

sw_wait_state() {
    for _ in $(seq 1 100); do
        sudo test -s "$SW_STATE_DIR/state.json" && return 0
        sw_process_alive || sw_error "kikimora-toad exited before publishing state.json"
        sleep 0.1
    done
    sw_error "timed out waiting for state.json"
}

sw_wait_awg_online() {
    for _ in $(seq 1 100); do
        if sudo python3 - "$SW_STATE_DIR/state.json" <<'PY'
import json
import sys
try:
    with open(sys.argv[1], encoding="utf-8") as fh:
        state = json.load(fh)
except (FileNotFoundError, json.JSONDecodeError):
    raise SystemExit(1)
raise SystemExit(0 if state.get("state") == "online" and state.get("session", {}).get("connected") is True else 1)
PY
        then
            return 0
        fi
        sw_process_alive || sw_error "kikimora-toad exited before reporting an AWG handshake"
        sleep 0.2
    done
    sw_error "AWG client did not report online after system-wide traffic"
}

sw_route_parts() {
    local route="$1"
    SW_UNDERLAY_DEV="$(awk '{for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}' <<<"$route")"
    SW_UNDERLAY_GATEWAY="$(awk '{for (i=1; i<=NF; i++) if ($i == "via") {print $(i+1); exit}}' <<<"$route")"
    SW_UNDERLAY_SRC="$(awk '{for (i=1; i<=NF; i++) if ($i == "src") {print $(i+1); exit}}' <<<"$route")"
}

sw_add_endpoint_route() {
    local -a route=(sudo ip -4 route add "$SW_ENDPOINT_IP/32")
    if [[ -n "$SW_UNDERLAY_GATEWAY" ]]; then
        route+=(via "$SW_UNDERLAY_GATEWAY")
    fi
    route+=(dev "$SW_UNDERLAY_DEV")
    if [[ -n "$SW_UNDERLAY_SRC" ]]; then
        route+=(src "$SW_UNDERLAY_SRC")
    fi
    route+=(metric 4)
    "${route[@]}"
    SW_ENDPOINT_ROUTE_ADDED=1
}

sw_remove_endpoint_route() {
    if (( SW_ENDPOINT_ROUTE_ADDED )); then
        sudo ip -4 route del "$SW_ENDPOINT_IP/32" metric 4 >/dev/null 2>&1 || true
        SW_ENDPOINT_ROUTE_ADDED=0
    fi
}

sw_enable_default_route() {
    sudo ip -4 route add default dev "$SW_INTERFACE" metric 4
    SW_DEFAULT_ROUTE_ADDED=1
}

sw_disable_default_route() {
    if (( SW_DEFAULT_ROUTE_ADDED )); then
        sudo ip -4 route del default dev "$SW_INTERFACE" metric 4 >/dev/null 2>&1 || true
        SW_DEFAULT_ROUTE_ADDED=0
    fi
}

sw_configure_dns() {
    if ! command -v resolvectl >/dev/null 2>&1; then
        sw_warn "resolvectl is unavailable; leaving existing system DNS configuration unchanged"
        return 0
    fi
    if ! resolvectl status >/dev/null 2>&1; then
        sw_warn "systemd-resolved is not available; leaving existing system DNS configuration unchanged"
        return 0
    fi

    # The TUN interface is new, so there is no pre-existing per-link resolver
    # state to preserve. Revert only this interface during teardown.
    if sudo resolvectl dns "$SW_INTERFACE" $SW_DNS_SERVERS && \
        sudo resolvectl domain "$SW_INTERFACE" '~.' && \
        sudo resolvectl default-route "$SW_INTERFACE" yes; then
        SW_RESOLVED_CONFIGURED=1
        sw_log "system DNS routed through $SW_INTERFACE via: $SW_DNS_SERVERS"
    else
        sw_warn "could not install per-link systemd-resolved DNS on $SW_INTERFACE; browser test will use existing resolver configuration"
    fi
}

sw_restore_dns() {
    if (( SW_RESOLVED_CONFIGURED )); then
        sudo resolvectl revert "$SW_INTERFACE" >/dev/null 2>&1 || true
        SW_RESOLVED_CONFIGURED=0
    fi
}

sw_start_trace() {
    : >"$SW_DIAG_DIR/tun-trace.txt"
    : >"$SW_DIAG_DIR/underlay-trace.txt"

    sudo -- bash -c 'printf "%s\n" "$$" >"$1"; shift; exec "$@"' \
        bash "$SW_PRIVATE_DIR/tun-trace.pid" tcpdump -nn -tttt -l -i "$SW_INTERFACE" -s 96 \
        >>"$SW_DIAG_DIR/tun-trace.txt" 2>&1 &
    SW_TRACE_TUN_SUDO_PID=$!

    sudo -- bash -c 'printf "%s\n" "$$" >"$1"; shift; exec "$@"' \
        bash "$SW_PRIVATE_DIR/uplink-trace.pid" tcpdump -nn -tttt -l -i "$SW_UNDERLAY_DEV" -s 96 \
        host "$SW_ENDPOINT_IP" and port "$SW_ENDPOINT_PORT" \
        >>"$SW_DIAG_DIR/underlay-trace.txt" 2>&1 &
    SW_TRACE_UPLINK_SUDO_PID=$!

    for _ in $(seq 1 50); do
        [[ -s "$SW_PRIVATE_DIR/tun-trace.pid" && -s "$SW_PRIVATE_DIR/uplink-trace.pid" ]] && break
        sleep 0.05
    done
    SW_TRACE_TUN_PID="$(sudo sed -n '1p' "$SW_PRIVATE_DIR/tun-trace.pid" 2>/dev/null || true)"
    SW_TRACE_UPLINK_PID="$(sudo sed -n '1p' "$SW_PRIVATE_DIR/uplink-trace.pid" 2>/dev/null || true)"
}

sw_stop_one_pid() {
    local pid="$1"
    [[ -n "$pid" && -d "/proc/$pid" ]] || return 0
    sudo kill -INT "$pid" >/dev/null 2>&1 || true
    for _ in $(seq 1 20); do
        [[ ! -d "/proc/$pid" ]] && return 0
        sleep 0.1
    done
    sudo kill -TERM "$pid" >/dev/null 2>&1 || true
}

sw_stop_trace() {
    sw_stop_one_pid "$SW_TRACE_TUN_PID"
    sw_stop_one_pid "$SW_TRACE_UPLINK_PID"
    [[ -n "$SW_TRACE_TUN_SUDO_PID" ]] && wait "$SW_TRACE_TUN_SUDO_PID" 2>/dev/null || true
    [[ -n "$SW_TRACE_UPLINK_SUDO_PID" ]] && wait "$SW_TRACE_UPLINK_SUDO_PID" 2>/dev/null || true
    SW_TRACE_TUN_PID=""
    SW_TRACE_UPLINK_PID=""
    SW_TRACE_TUN_SUDO_PID=""
    SW_TRACE_UPLINK_SUDO_PID=""
}

sw_sampler_loop() {
    while true; do
        {
            printf '\n===== %s =====\n' "$(date -Is)"
            printf 'toad_alive=%s\n' "$(sw_process_alive && printf yes || printf no)"
            printf 'route_endpoint='; ip -4 route get "$SW_ENDPOINT_IP" 2>&1 || true
            printf 'route_google='; ip -4 route get "$SW_GOOGLE_IP" 2>&1 || true
            printf 'route_chatgpt='; ip -4 route get "$SW_CHATGPT_IP" 2>&1 || true
            printf 'rx_bytes=%s\n' "$(sw_read_counter rx_bytes)"
            printf 'tx_bytes=%s\n' "$(sw_read_counter tx_bytes)"
            if [[ -s "$SW_STATE_DIR/state.json" ]]; then
                printf 'state='; sudo cat "$SW_STATE_DIR/state.json" 2>/dev/null || true
                printf '\n'
            fi
        } >>"$SW_DIAG_DIR/active-sampler.txt" 2>&1
        sleep 2
    done
}

sw_start_sampler() {
    : >"$SW_DIAG_DIR/active-sampler.txt"
    sw_sampler_loop &
    SW_SAMPLE_PID=$!
}

sw_stop_sampler() {
    if [[ -n "$SW_SAMPLE_PID" && -d "/proc/$SW_SAMPLE_PID" ]]; then
        kill "$SW_SAMPLE_PID" >/dev/null 2>&1 || true
        wait "$SW_SAMPLE_PID" 2>/dev/null || true
    fi
    SW_SAMPLE_PID=""
}

sw_http_probes() {
    local response

    SW_GOOGLE_IP="$(getent ahostsv4 "$SW_GOOGLE_HOST" | awk 'NR == 1 {print $1; exit}' || true)"
    SW_CHATGPT_IP="$(getent ahostsv4 "$SW_CHATGPT_HOST" | awk 'NR == 1 {print $1; exit}' || true)"
    [[ "$SW_GOOGLE_IP" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || sw_error "could not resolve $SW_GOOGLE_HOST after system-wide cutover"
    [[ "$SW_CHATGPT_IP" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || sw_error "could not resolve $SW_CHATGPT_HOST after system-wide cutover"

    response="$(env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
        curl -4sS --noproxy '*' --connect-timeout 10 --max-time 30 \
        -o /dev/null -w 'http_code=%{http_code} size_download=%{size_download} remote_ip=%{remote_ip} total_s=%{time_total}' \
        "https://$SW_GOOGLE_HOST/")" || sw_error "Google transport probe failed after system-wide cutover"
    printf 'google=%s\n' "$response" >>"$SW_DIAG_DIR/traffic-test.txt"
    SW_GOOGLE_HTTP_CODE="$(sed -n 's/.*http_code=\([^ ]*\).*/\1/p' <<<"$response")"
    SW_GOOGLE_BYTES="$(sed -n 's/.*size_download=\([^ ]*\).*/\1/p' <<<"$response")"
    SW_GOOGLE_REMOTE_IP="$(sed -n 's/.*remote_ip=\([^ ]*\).*/\1/p' <<<"$response")"
    [[ "$SW_GOOGLE_HTTP_CODE" == "200" ]] || sw_error "Google returned HTTP $SW_GOOGLE_HTTP_CODE instead of 200"
    [[ "$SW_GOOGLE_REMOTE_IP" == "$SW_GOOGLE_IP" ]] || sw_warn "Google DNS answer changed between resolution and curl remote IP ($SW_GOOGLE_IP -> $SW_GOOGLE_REMOTE_IP)"
    [[ "$SW_GOOGLE_BYTES" =~ ^[0-9]+$ ]] && (( SW_GOOGLE_BYTES >= 10000 )) || sw_error "Google response body was unexpectedly short: ${SW_GOOGLE_BYTES:-unknown}"

    response=""
    if response="$(env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
        curl -4sS --noproxy '*' --connect-timeout 10 --max-time 30 \
        -o /dev/null -w 'http_code=%{http_code} size_download=%{size_download} remote_ip=%{remote_ip} total_s=%{time_total}' \
        "https://$SW_CHATGPT_HOST/")"; then
        printf 'chatgpt=%s\n' "$response" >>"$SW_DIAG_DIR/traffic-test.txt"
        SW_CHATGPT_HTTP_CODE="$(sed -n 's/.*http_code=\([^ ]*\).*/\1/p' <<<"$response")"
        SW_CHATGPT_BYTES="$(sed -n 's/.*size_download=\([^ ]*\).*/\1/p' <<<"$response")"
        SW_CHATGPT_REMOTE_IP="$(sed -n 's/.*remote_ip=\([^ ]*\).*/\1/p' <<<"$response")"
        if [[ "$SW_CHATGPT_HTTP_CODE" =~ ^2[0-9][0-9]$ ]]; then
            SW_CHATGPT_RESULT="PASS_2XX"
        elif [[ "$SW_CHATGPT_HTTP_CODE" =~ ^[1-5][0-9][0-9]$ ]]; then
            SW_CHATGPT_RESULT="WARN_HTTP_${SW_CHATGPT_HTTP_CODE}"
            sw_warn "ChatGPT returned HTTP $SW_CHATGPT_HTTP_CODE; system-wide VPN transport is still independently proven by Google"
        else
            SW_CHATGPT_RESULT="WARN_INVALID_HTTP"
            sw_warn "ChatGPT returned an invalid HTTP status: ${SW_CHATGPT_HTTP_CODE:-empty}"
        fi
    else
        SW_CHATGPT_RESULT="WARN_TRANSPORT_FAILED"
        sw_warn "ChatGPT transport probe failed; continuing because Google is the independent data-plane acceptance gate"
    fi

    SW_DATA_PLANE_RESULT="PASS"
}

sw_public_ip() {
    env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
        curl -4fsS --connect-timeout 5 --max-time 15 https://api.ipify.org 2>/dev/null || true
}

sw_write_config_summary() {
    python3 - "$SW_CONFIG" "$SW_DIAG_DIR/config-summary.json" <<'PY'
import hashlib
import json
import pathlib
import sys
import tomllib

cfg = tomllib.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
section = cfg.get("awg2", {})
fingerprint_source = dict(cfg)
fingerprint_source["state_dir"] = "<state-dir>"
fingerprint = hashlib.sha256(json.dumps(fingerprint_source, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
summary = {
    "config_fingerprint_sha256": fingerprint,
    "name": cfg.get("name"),
    "protocol": cfg.get("protocol"),
    "interface": cfg.get("interface"),
    "address": cfg.get("address", []),
    "mtu": cfg.get("mtu"),
    "endpoint": section.get("endpoint"),
    "allowed_ips": section.get("allowed_ips", []),
    "persistent_keepalive": section.get("persistent_keepalive", 0),
}
pathlib.Path(sys.argv[2]).write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
}

sw_redact_tree() {
    python3 - "$SW_DIAG_DIR" <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
patterns = [
    (re.compile(r"(?i)\b(?:vless|vpn|wg|wireguard|amneziawg)://[^\s\"']+"), "<redacted-share-link>"),
    (re.compile(r"(?im)(\b(?:uuid|private[_ -]?key|preshared[_ -]?key|psk|reality[_ -]?private[_ -]?key)\b\s*[:=]\s*)\S+"), r"\1<redacted>"),
    (re.compile(r"(?<![A-Za-z0-9+/])[A-Za-z0-9+/]{43}=(?![A-Za-z0-9+/=])"), "<redacted-wireguard-key>"),
    (re.compile(r"(?i)\b[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\b"), "<redacted-uuid>"),
]
for path in root.rglob("*"):
    if not path.is_file():
        continue
    try:
        text = path.read_text(encoding="utf-8")
    except UnicodeDecodeError:
        continue
    for pattern, replacement in patterns:
        text = pattern.sub(replacement, text)
    path.write_text(text, encoding="utf-8")
PY
}

sw_write_metadata() {
    local status="$1"
    local ended_at end_ms
    ended_at="$(date -Is)"
    end_ms="$(sw_now_ms)"
    [[ -e "$SW_DIAG_DIR/warnings.txt" ]] || : >"$SW_DIAG_DIR/warnings.txt"
    {
        printf 'started_at=%s\n' "$SW_STARTED_AT"
        printf 'ended_at=%s\n' "$ended_at"
        printf 'duration_ms=%s\n' "$((end_ms - SW_START_MS))"
        printf 'result=%s\n' "$SW_RESULT"
        printf 'data_plane_result=%s\n' "$SW_DATA_PLANE_RESULT"
        printf 'chatgpt_application_result=%s\n' "$SW_CHATGPT_RESULT"
        printf 'warning_count=%s\n' "$SW_WARNING_COUNT"
        printf 'exit_status=%s\n' "$status"
        printf 'test_scope=system-wide-real-vps-cutover\n'
        printf 'protocol=%s\n' "$SW_PROTOCOL"
        printf 'interface=%s\n' "$SW_INTERFACE"
        printf 'ifindex=%s\n' "${SW_IFINDEX:-unavailable}"
        printf 'mtu=%s\n' "${SW_MTU:-unavailable}"
        printf 'endpoint=%s\n' "$SW_ENDPOINT"
        printf 'endpoint_ip=%s\n' "$SW_ENDPOINT_IP"
        printf 'underlay_dev=%s\n' "$SW_UNDERLAY_DEV"
        printf 'underlay_gateway=%s\n' "${SW_UNDERLAY_GATEWAY:-direct}"
        printf 'underlay_src=%s\n' "${SW_UNDERLAY_SRC:-unavailable}"
        printf 'import_ms=%s\n' "${SW_IMPORT_MS:-unavailable}"
        printf 'interface_wait_ms=%s\n' "${SW_INTERFACE_WAIT_MS:-unavailable}"
        printf 'active_duration_ms=%s\n' "${SW_ACTIVE_DURATION_MS:-unavailable}"
        printf 'rx_before=%s\n' "${SW_RX_BEFORE:-unavailable}"
        printf 'tx_before=%s\n' "${SW_TX_BEFORE:-unavailable}"
        printf 'rx_after=%s\n' "${SW_RX_AFTER:-unavailable}"
        printf 'tx_after=%s\n' "${SW_TX_AFTER:-unavailable}"
        printf 'google_ip=%s\n' "${SW_GOOGLE_IP:-unavailable}"
        printf 'google_http_code=%s\n' "${SW_GOOGLE_HTTP_CODE:-unavailable}"
        printf 'google_response_bytes=%s\n' "${SW_GOOGLE_BYTES:-unavailable}"
        printf 'google_remote_ip=%s\n' "${SW_GOOGLE_REMOTE_IP:-unavailable}"
        printf 'chatgpt_ip=%s\n' "${SW_CHATGPT_IP:-unavailable}"
        printf 'chatgpt_http_code=%s\n' "${SW_CHATGPT_HTTP_CODE:-unavailable}"
        printf 'chatgpt_response_bytes=%s\n' "${SW_CHATGPT_BYTES:-unavailable}"
        printf 'chatgpt_remote_ip=%s\n' "${SW_CHATGPT_REMOTE_IP:-unavailable}"
        printf 'public_ip_before=%s\n' "${SW_PUBLIC_IP_BEFORE:-unavailable}"
        printf 'public_ip_during=%s\n' "${SW_PUBLIC_IP_DURING:-unavailable}"
        printf 'manual_timeout_seconds=%s\n' "$SW_MANUAL_TIMEOUT"
        printf 'host=%s\n' "$(hostname)"
        printf 'kernel=%s\n' "$(uname -r)"
    } >"$SW_DIAG_DIR/metadata.txt"
}

sw_stop_toad() {
    if sw_process_alive; then
        sudo kill -INT "$SW_TOAD_PID" >/dev/null 2>&1 || true
        for _ in $(seq 1 50); do
            ! sw_process_alive && break
            sleep 0.1
        done
    fi
    if sw_process_alive; then
        sw_warn "kikimora-toad did not stop after SIGINT; sending SIGTERM"
        sudo kill -TERM "$SW_TOAD_PID" >/dev/null 2>&1 || true
        sleep 1
    fi
    if sw_process_alive; then
        sw_warn "kikimora-toad did not stop after SIGTERM; sending SIGKILL"
        sudo kill -KILL "$SW_TOAD_PID" >/dev/null 2>&1 || true
    fi
    [[ -n "$SW_SUDO_PID" ]] && wait "$SW_SUDO_PID" 2>/dev/null || true
    SW_TOAD_PID=""
    SW_SUDO_PID=""
}

sw_finalize() {
    local status="$1"
    local archive_status=0

    trap - ERR EXIT INT TERM
    set +e

    sw_stop_sampler
    SW_RX_AFTER="${SW_RX_AFTER:-$(sw_read_counter rx_bytes)}"
    SW_TX_AFTER="${SW_TX_AFTER:-$(sw_read_counter tx_bytes)}"
    sw_capture_state "$SW_DIAG_DIR/state-active-final.json"
    sw_collect_snapshot active-final

    # Restore host connectivity before stopping the VPN process.
    sw_disable_default_route
    sw_restore_dns
    sw_remove_endpoint_route
    sw_stop_trace
    sw_stop_toad

    sleep 0.2
    sw_collect_snapshot after
    sw_capture_state "$SW_DIAG_DIR/state-after.json"

    {
        printf '=== journal since test start ===\n'
        journalctl --no-pager --since "@$SW_START_EPOCH" -n 1500 2>/dev/null || true
        printf '\n=== kernel journal since test start ===\n'
        journalctl -k --no-pager --since "@$SW_START_EPOCH" -n 1000 2>/dev/null || true
        printf '\n=== dmesg tail ===\n'
        dmesg --ctime 2>/dev/null | tail -n 500 || true
    } >"$SW_DIAG_DIR/system-journal.txt" 2>&1

    if [[ "$SW_RESULT" != "PASS" && "$status" -eq 0 ]]; then
        status=1
    fi
    sw_write_metadata "$status"
    sw_redact_tree
    rm -rf -- "$SW_PRIVATE_DIR"

    tar -czf "$SW_ARCHIVE" -C "$SW_WORK" diag
    archive_status=$?
    if (( archive_status == 0 )); then
        chmod 0600 "$SW_ARCHIVE" 2>/dev/null || true
        printf 'Diagnostic archive: %s\n' "$SW_ARCHIVE"
        rm -rf -- "$SW_WORK"
    else
        printf 'ERROR: failed to create diagnostic archive; sanitized directory remains at %s\n' "$SW_DIAG_DIR" >&2
        status="$archive_status"
    fi

    exit "$status"
}

sw_err_trap() {
    local status="$1"
    local line="$2"
    printf '[%s] command failed at line %s (exit=%s)\n' "$(date -Is)" "$line" "$status" >>"$SW_DIAG_DIR/errors.txt"
    return "$status"
}

run_system_wide_awg_diag() {
    SW_PROTOCOL="$1"
    SW_INTERFACE="$2"
    SW_PROFILE_NAME="$3"
    local share_link="$4"
    local script_dir repo_root import_started endpoint_route preflight_default_dev

    umask 077
    SW_STARTED_AT="$(date -Is)"
    SW_START_EPOCH="$(date +%s)"
    SW_START_MS="$(sw_now_ms)"
    SW_WORK="$(mktemp -d "${TMPDIR:-/tmp}/toad-system-wide.XXXXXX")"
    SW_DIAG_DIR="$SW_WORK/diag"
    SW_PRIVATE_DIR="$SW_WORK/private"
    SW_STATE_DIR="$SW_PRIVATE_DIR/state"
    SW_CONFIG="$SW_PRIVATE_DIR/profile.toml"
    mkdir -p "$SW_DIAG_DIR" "$SW_PRIVATE_DIR" "$SW_STATE_DIR"
    chmod 0700 "$SW_WORK" "$SW_DIAG_DIR" "$SW_PRIVATE_DIR" "$SW_STATE_DIR"
    : >"$SW_DIAG_DIR/command.log"
    : >"$SW_DIAG_DIR/errors.txt"
    : >"$SW_DIAG_DIR/warnings.txt"
    : >"$SW_DIAG_DIR/toad.log"
    : >"$SW_DIAG_DIR/traffic-test.txt"

    SW_ARCHIVE="${TOAD_DIAG_OUTPUT:-$PWD/toad-system-wide-awg-diag-$(date +%Y-%m-%d-%H%M%S).tar.gz}"
    [[ "$SW_ARCHIVE" == /* ]] || SW_ARCHIVE="$PWD/$SW_ARCHIVE"
    [[ ! -e "$SW_ARCHIVE" ]] || SW_ARCHIVE="${SW_ARCHIVE%.tar.gz}-$$.tar.gz"

    trap 'sw_err_trap "$?" "$LINENO"' ERR
    trap 'sw_finalize "$?"' EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM

    for command in awk bash cat chmod curl date env getent grep ip journalctl ps python3 sed seq ss sudo sysctl tar tcpdump tee; do
        sw_require "$command"
    done
    [[ "$SW_MANUAL_TIMEOUT" =~ ^[1-9][0-9]*$ ]] || sw_error "TOAD_SYSTEM_WIDE_HOLD_SECONDS must be a positive integer"
    [[ "$share_link" =~ ^(vpn|wg|wireguard|amneziawg):// ]] || sw_error "expected an Amnezia VPN/AWG/WireGuard share link"

    sudo -v
    sw_log "START system-wide AWG test; legacy Kikimora will not be modified"
    sw_collect_snapshot before
    SW_PUBLIC_IP_BEFORE="$(sw_public_ip)"

    script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
    repo_root="$(cd -- "$script_dir/../../../.." && pwd)"
    if [[ -n "${TOAD_BIN:-}" ]]; then
        [[ -x "$TOAD_BIN" ]] || sw_error "TOAD_BIN is not executable: $TOAD_BIN"
        SW_TOAD_BIN="$TOAD_BIN"
    else
        sw_require go
        SW_TOAD_BIN="$SW_PRIVATE_DIR/kikimora-toad"
        sw_log "build checked-out kikimora-toad"
        (cd "$repo_root/toad" && go build -o "$SW_TOAD_BIN" ./cmd/kikimora-toad) >>"$SW_DIAG_DIR/command.log" 2>&1
        chmod 0755 "$SW_TOAD_BIN"
    fi

    import_started="$(sw_now_ms)"
    printf '%s\n' "$share_link" | "$SW_TOAD_BIN" import \
        -name "$SW_PROFILE_NAME" -interface "$SW_INTERFACE" -state-dir "$SW_STATE_DIR" \
        >"$SW_CONFIG" 2>>"$SW_DIAG_DIR/command.log"
    SW_IMPORT_MS=$(( $(sw_now_ms) - import_started ))
    share_link=""
    chmod 0600 "$SW_CONFIG"
    "$SW_TOAD_BIN" validate -config "$SW_CONFIG" >>"$SW_DIAG_DIR/command.log" 2>&1
    sw_write_config_summary

    SW_ENDPOINT="$(python3 - "$SW_DIAG_DIR/config-summary.json" <<'PY'
import json, sys
print(json.load(open(sys.argv[1], encoding="utf-8")).get("endpoint") or "")
PY
)"
    SW_ENDPOINT_HOST="$(python3 - "$SW_ENDPOINT" <<'PY'
import sys
endpoint=sys.argv[1]
print(endpoint[1:].split(']',1)[0] if endpoint.startswith('[') else endpoint.rsplit(':',1)[0])
PY
)"
    SW_ENDPOINT_PORT="$(python3 - "$SW_ENDPOINT" <<'PY'
import sys
endpoint=sys.argv[1]
print(endpoint.rsplit(':',1)[1] if ':' in endpoint else '')
PY
)"
    SW_ENDPOINT_IP="$(getent ahostsv4 "$SW_ENDPOINT_HOST" | awk 'NR == 1 {print $1; exit}' || true)"
    [[ "$SW_ENDPOINT_IP" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || sw_error "could not resolve VPN endpoint to IPv4"
    [[ "$SW_ENDPOINT_PORT" =~ ^[1-9][0-9]{0,4}$ ]] && (( SW_ENDPOINT_PORT <= 65535 )) || sw_error "invalid VPN endpoint port"

    endpoint_route="$(ip -4 route get "$SW_ENDPOINT_IP" 2>/dev/null || true)"
    [[ -n "$endpoint_route" ]] || sw_error "no pre-VPN route to endpoint $SW_ENDPOINT_IP"
    sw_route_parts "$endpoint_route"
    [[ -n "$SW_UNDERLAY_DEV" ]] || sw_error "could not determine physical underlay interface from: $endpoint_route"
    case "$SW_UNDERLAY_DEV" in
        vpn0|amn0|kk-*|tun*|wg*)
            sw_error "VPN endpoint currently routes through $SW_UNDERLAY_DEV; stop the old VPN first. The test will not modify it."
            ;;
    esac
    preflight_default_dev="$(ip -4 route show default | awk 'NR==1 {for(i=1;i<=NF;i++) if($i=="dev"){print $(i+1); exit}}')"
    case "$preflight_default_dev" in
        vpn0|amn0|kk-*|tun*|wg*)
            sw_error "current default route still uses VPN-like interface $preflight_default_dev; stop the old VPN first"
            ;;
    esac
    if ip link show dev "$SW_INTERFACE" >/dev/null 2>&1; then
        sw_error "interface $SW_INTERFACE already exists before the test"
    fi

    sw_log "pin endpoint $SW_ENDPOINT_IP via underlay dev=$SW_UNDERLAY_DEV gateway=${SW_UNDERLAY_GATEWAY:-direct}"
    sw_add_endpoint_route
    ip -4 route get "$SW_ENDPOINT_IP" | grep -Fq "dev $SW_UNDERLAY_DEV" || sw_error "pinned endpoint route does not use $SW_UNDERLAY_DEV"

    sw_log "start kikimora-toad in root namespace"
    sudo -- bash -c 'printf "%s\n" "$$" >"$1"; shift; exec "$@"' \
        bash "$SW_PRIVATE_DIR/toad.pid" "$SW_TOAD_BIN" run -config "$SW_CONFIG" \
        >>"$SW_DIAG_DIR/toad.log" 2>&1 &
    SW_SUDO_PID=$!
    for _ in $(seq 1 100); do
        [[ -s "$SW_PRIVATE_DIR/toad.pid" ]] && break
        kill -0 "$SW_SUDO_PID" 2>/dev/null || sw_error "sudo failed to start kikimora-toad"
        sleep 0.05
    done
    SW_TOAD_PID="$(sudo sed -n '1p' "$SW_PRIVATE_DIR/toad.pid")"
    SW_TOAD_STARTED=1

    sw_wait_interface
    sw_wait_state
    SW_RX_BEFORE="$(sw_read_counter rx_bytes)"
    SW_TX_BEFORE="$(sw_read_counter tx_bytes)"
    sw_capture_state "$SW_DIAG_DIR/state-before-cutover.json"
    sw_collect_snapshot toad-up

    sw_start_trace
    SW_ACTIVE_START_MS="$(sw_now_ms)"
    sw_log "install system-wide IPv4 default route through $SW_INTERFACE"
    sw_enable_default_route
    sw_configure_dns

    # Resolve after the cutover to exercise the system resolver in its active state.
    sw_http_probes
    sw_wait_awg_online
    SW_PUBLIC_IP_DURING="$(sw_public_ip)"

    ip -4 route get "$SW_GOOGLE_IP" | grep -Fq "dev $SW_INTERFACE" || sw_error "Google is not routed through $SW_INTERFACE"
    ip -4 route get "$SW_CHATGPT_IP" | grep -Fq "dev $SW_INTERFACE" || sw_error "ChatGPT is not routed through $SW_INTERFACE"
    ip -4 route get "$SW_ENDPOINT_IP" | grep -Fq "dev $SW_UNDERLAY_DEV" || sw_error "VPN endpoint recursively entered $SW_INTERFACE"

    SW_RX_AFTER="$(sw_read_counter rx_bytes)"
    SW_TX_AFTER="$(sw_read_counter tx_bytes)"
    [[ "$SW_RX_AFTER" =~ ^[0-9]+$ && "$SW_RX_BEFORE" =~ ^[0-9]+$ ]] && (( SW_RX_AFTER > SW_RX_BEFORE )) || sw_error "TUN RX counter did not advance"
    [[ "$SW_TX_AFTER" =~ ^[0-9]+$ && "$SW_TX_BEFORE" =~ ^[0-9]+$ ]] && (( SW_TX_AFTER > SW_TX_BEFORE )) || sw_error "TUN TX counter did not advance"

    sw_collect_snapshot active
    sw_capture_state "$SW_DIAG_DIR/state-active.json"
    sw_start_sampler

    printf '\nSYSTEM-WIDE TOAD VPN IS ACTIVE.\n'
    printf 'Use the browser now. The test will NOT touch legacy Kikimora.\n'
    printf 'Press Enter to finish and restore the original route, or it will auto-restore after %s seconds.\n\n' "$SW_MANUAL_TIMEOUT"
    if [[ -t 0 ]]; then
        if ! read -r -t "$SW_MANUAL_TIMEOUT" _; then
            sw_warn "manual browser window reached the ${SW_MANUAL_TIMEOUT}s safety timeout; restoring routes automatically"
        fi
    else
        sw_log "stdin is not interactive; keeping system-wide VPN active for ${SW_MANUAL_TIMEOUT}s"
        sleep "$SW_MANUAL_TIMEOUT"
    fi

    SW_ACTIVE_DURATION_MS=$(( $(sw_now_ms) - SW_ACTIVE_START_MS ))
    sw_stop_sampler
    SW_RX_AFTER="$(sw_read_counter rx_bytes)"
    SW_TX_AFTER="$(sw_read_counter tx_bytes)"
    sw_capture_state "$SW_DIAG_DIR/state-active-final.json"
    sw_collect_snapshot active-final

    SW_RESULT="PASS"
    sw_log "PASS: system-wide route, Google data plane, endpoint underlay pin and Toad lifecycle validated; beginning automatic rollback"
}
