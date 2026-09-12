#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

# Shared, client-only collector and runner for real VPS tests.
# Call run_real_vps_diag with: protocol, interface, profile name, share link.

DIAG_WORK=""
DIAG_DIR=""
DIAG_PRIVATE_DIR=""
DIAG_ARCHIVE=""
DIAG_PROTOCOL=""
DIAG_INTERFACE=""
DIAG_PROFILE_NAME=""
DIAG_TOAD_BIN=""
DIAG_TOAD_PID=""
DIAG_SUDO_PID=""
DIAG_TOAD_STARTED=0
DIAG_SLIRP_PID=""
DIAG_SLIRP_SUDO_PID=""
DIAG_TRACE_PID=""
DIAG_TRACE_SUDO_PID=""
DIAG_NETNS=""
DIAG_NETNS_CREATED=0
DIAG_NETNS_ETC=""
DIAG_NETNS_ETC_CREATED=0
DIAG_UPLINK_INTERFACE="toad-uplink0"
DIAG_TEST_IP=""
DIAG_TEST_HOST="${TOAD_TEST_HOST:-chatgpt.com}"
DIAG_GOOGLE_HOST="www.google.com"
DIAG_GOOGLE_PATH="/"
DIAG_GOOGLE_MIN_BYTES="${TOAD_GOOGLE_MIN_BYTES:-10000}"
DIAG_GOOGLE_IP=""
DIAG_GOOGLE_HTTP_CODE=""
DIAG_GOOGLE_BYTES=""
DIAG_GOOGLE_REMOTE_IP=""
DIAG_CHATGPT_MIN_BYTES="${TOAD_CHATGPT_MIN_BYTES:-1024}"
DIAG_CHATGPT_HTTP_CODE=""
DIAG_CHATGPT_BYTES=""
DIAG_CHATGPT_REMOTE_IP=""
DIAG_STATE_DIR=""
DIAG_CONFIG=""
DIAG_PID_FILE=""
DIAG_SLIRP_PID_FILE=""
DIAG_TRACE_PID_FILE=""
DIAG_STARTED_AT=""
DIAG_START_EPOCH=""
DIAG_START_MS=""
DIAG_IMPORT_MS=""
DIAG_INTERFACE_WAIT_MS=""
DIAG_TRAFFIC_MS=""
DIAG_IFINDEX=""
DIAG_MTU=""
DIAG_RX_BEFORE=""
DIAG_TX_BEFORE=""
DIAG_RX_AFTER=""
DIAG_TX_AFTER=""
DIAG_ROOT_IFINDEX_BEFORE=""
DIAG_RESULT="FAIL"
DIAG_AFTER_COLLECTED=0

diag_now_ms() {
    date +%s%3N
}

diag_log() {
    printf '[%s] %s\n' "$(date -Is)" "$*" | tee -a "$DIAG_DIR/command.log"
}

diag_record_error() {
    printf '[%s] %s\n' "$(date -Is)" "$*" >>"$DIAG_DIR/errors.txt"
}

diag_fail() {
    diag_record_error "$*"
    printf 'ERROR: %s\n' "$*" >&2
    return 1
}

diag_err_trap() {
    local status="$1"
    local line="$2"
    diag_record_error "command failed at line $line (exit=$status)"
    return "$status"
}

diag_require() {
    command -v "$1" >/dev/null 2>&1 || diag_fail "required command not found: $1"
}

diag_capture_state() {
    local target="$1"
    if [[ -s "$DIAG_STATE_DIR/state.json" ]]; then
        if [[ -r "$DIAG_STATE_DIR/state.json" ]]; then
            cp -- "$DIAG_STATE_DIR/state.json" "$target"
        else
            sudo cat -- "$DIAG_STATE_DIR/state.json" >"$target"
        fi
    else
        printf '{"present":false}\n' >"$target"
    fi
}

diag_ensure_contract_files() {
    local file
    for file in \
        network-before.txt routes-before.txt dns-before.txt interfaces-before.txt \
        network-after.txt routes-after.txt dns-after.txt interfaces-after.txt \
        sockets.txt processes.txt kernel.txt journal.txt address-trace.txt; do
        [[ -e "$DIAG_DIR/$file" ]] || printf '%s\n' 'unavailable: test ended before collection' >"$DIAG_DIR/$file"
    done
    [[ -e "$DIAG_DIR/config-summary.json" ]] || printf '{"available":false}\n' >"$DIAG_DIR/config-summary.json"
    [[ -e "$DIAG_DIR/state-before.json" ]] || printf '{"present":false}\n' >"$DIAG_DIR/state-before.json"
    [[ -e "$DIAG_DIR/state-after.json" ]] || printf '{"present":false}\n' >"$DIAG_DIR/state-after.json"
}

collect_diag() {
    local out="$1"
    local phase="$2"
    mkdir -p "$out"

    {
        ip -details -statistics link show || true
        ip addr show || true
    } >"$out/network-$phase.txt" 2>&1

    {
        ip -4 route show table all || true
        ip -6 route show table all || true
        ip rule show || true
    } >"$out/routes-$phase.txt" 2>&1

    {
        if command -v resolvectl >/dev/null 2>&1; then
            resolvectl status || true
        else
            printf '%s\n' 'resolvectl is unavailable; /etc/resolv.conf follows'
            sed -n '1,200p' /etc/resolv.conf 2>/dev/null || true
        fi
    } >"$out/dns-$phase.txt" 2>&1

    ip -details -statistics addr show >"$out/interfaces-$phase.txt" 2>&1 || true

    if (( DIAG_NETNS_CREATED )) && diag_netns_exists; then
        {
            printf '\n=== isolated namespace: %s ===\n' "$DIAG_NETNS"
            sudo ip -n "$DIAG_NETNS" -details -statistics link show || true
            sudo ip -n "$DIAG_NETNS" addr show || true
        } >>"$out/network-$phase.txt" 2>&1
        {
            printf '\n=== isolated namespace: %s ===\n' "$DIAG_NETNS"
            sudo ip -n "$DIAG_NETNS" -4 route show table all || true
            sudo ip -n "$DIAG_NETNS" -6 route show table all || true
            sudo ip netns exec "$DIAG_NETNS" ip rule show || true
        } >>"$out/routes-$phase.txt" 2>&1
        {
            printf '\n=== isolated namespace: %s ===\n' "$DIAG_NETNS"
            sudo ip netns exec "$DIAG_NETNS" sed -n '1,200p' /etc/resolv.conf || true
        } >>"$out/dns-$phase.txt" 2>&1
        {
            printf '\n=== isolated namespace: %s ===\n' "$DIAG_NETNS"
            sudo ip -n "$DIAG_NETNS" -details -statistics addr show || true
        } >>"$out/interfaces-$phase.txt" 2>&1
    fi
}

diag_collect_runtime() {
    ss -tunap >"$DIAG_DIR/sockets.txt" 2>&1 || true
    if (( DIAG_NETNS_CREATED )) && diag_netns_exists; then
        {
            printf '\n=== isolated namespace: %s ===\n' "$DIAG_NETNS"
            sudo ip netns exec "$DIAG_NETNS" ss -tunap || true
        } >>"$DIAG_DIR/sockets.txt" 2>&1
    fi

    # Deliberately omit argv: the harness itself was invoked with a secret link.
    ps -eo pid,ppid,user,group,stat,etimes,comm >"$DIAG_DIR/processes.txt" 2>&1 || true
    if (( DIAG_NETNS_CREATED )) && diag_netns_exists; then
        {
            printf '\nnamespace_pids='
            sudo ip netns pids "$DIAG_NETNS" | tr '\n' ' '
            printf '\n'
        } >>"$DIAG_DIR/processes.txt" 2>&1 || true
    fi

    {
        uname -a || true
        printf '\n/proc/version:\n'
        sed -n '1,20p' /proc/version 2>/dev/null || true
        printf '\nrecent dmesg:\n'
        dmesg --ctime 2>/dev/null | tail -n 300 || true
    } >"$DIAG_DIR/kernel.txt" 2>&1

    if command -v journalctl >/dev/null 2>&1; then
        journalctl --no-pager --since "@$DIAG_START_EPOCH" -n 500 >"$DIAG_DIR/journal.txt" 2>&1 || true
    else
        printf '%s\n' 'journalctl is unavailable' >"$DIAG_DIR/journal.txt"
    fi
}

diag_write_config_summary() {
    python3 - "$DIAG_CONFIG" "$DIAG_DIR/config-summary.json" <<'PY'
import hashlib
import json
import pathlib
import sys
import tomllib

config_path = pathlib.Path(sys.argv[1])
cfg = tomllib.loads(config_path.read_text(encoding="utf-8"))
protocol = cfg.get("protocol", "")
section = cfg.get("awg2", {}) if protocol == "amneziawg2" else cfg.get("vless_reality", {})

# The fingerprint covers the normalized config, including credentials, but a
# per-run state directory is normalized so the same imported profile is stable.
fingerprint_source = dict(cfg)
fingerprint_source["state_dir"] = "<state-dir>"
fingerprint = hashlib.sha256(
    json.dumps(fingerprint_source, sort_keys=True, separators=(",", ":")).encode("utf-8")
).hexdigest()

summary = {
    "config_fingerprint_sha256": fingerprint,
    "name": cfg.get("name"),
    "protocol": protocol,
    "interface": cfg.get("interface"),
    "address": cfg.get("address", []),
    "mtu": cfg.get("mtu"),
    "endpoint": section.get("endpoint"),
}
if protocol == "amneziawg2":
    summary.update({
        "allowed_ips": section.get("allowed_ips", []),
        "persistent_keepalive": section.get("persistent_keepalive", 0),
    })
elif protocol == "vless-reality":
    summary.update({
        "server_name": section.get("server_name"),
        "transport": section.get("transport"),
        "flow": section.get("flow"),
        "client_fingerprint": section.get("fingerprint"),
    })

pathlib.Path(sys.argv[2]).write_text(
    json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8"
)
PY
}

diag_redact_tree() {
    python3 - "$DIAG_DIR" <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
patterns = [
    (re.compile(r"(?i)\b(?:vless|vpn|wg|wireguard|amneziawg)://[^\s\"']+"), "<redacted-share-link>"),
    (re.compile(r"(?i)([\"'](?:uuid|id|private_?key|privateKey|preshared_?key|psk|reality_private_key)[\"']\s*:\s*[\"'])[^\"']+"), r"\1<redacted>"),
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

diag_write_metadata() {
    local status="$1"
    local ended_at end_ms duration_ms
    ended_at="$(date -Is)"
    end_ms="$(diag_now_ms)"
    duration_ms=$((end_ms - DIAG_START_MS))
    {
        printf 'started_at=%s\n' "$DIAG_STARTED_AT"
        printf 'ended_at=%s\n' "$ended_at"
        printf 'duration_ms=%s\n' "$duration_ms"
        printf 'result=%s\n' "$DIAG_RESULT"
        printf 'exit_status=%s\n' "$status"
        printf 'protocol=%s\n' "$DIAG_PROTOCOL"
        printf 'network_namespace=%s\n' "${DIAG_NETNS:-unavailable}"
        printf 'uplink_interface=%s\n' "$DIAG_UPLINK_INTERFACE"
        printf 'interface=%s\n' "$DIAG_INTERFACE"
        printf 'ifindex=%s\n' "${DIAG_IFINDEX:-unavailable}"
        printf 'mtu=%s\n' "${DIAG_MTU:-unavailable}"
        printf 'google_test_host=%s\n' "$DIAG_GOOGLE_HOST"
        printf 'google_test_ip=%s\n' "${DIAG_GOOGLE_IP:-unavailable}"
        printf 'google_http_code=%s\n' "${DIAG_GOOGLE_HTTP_CODE:-unavailable}"
        printf 'google_response_bytes=%s\n' "${DIAG_GOOGLE_BYTES:-unavailable}"
        printf 'google_remote_ip=%s\n' "${DIAG_GOOGLE_REMOTE_IP:-unavailable}"
        printf 'google_minimum_bytes=%s\n' "$DIAG_GOOGLE_MIN_BYTES"
        printf 'chatgpt_test_host=%s\n' "$DIAG_TEST_HOST"
        printf 'chatgpt_test_ip=%s\n' "${DIAG_TEST_IP:-unavailable}"
        printf 'chatgpt_http_code=%s\n' "${DIAG_CHATGPT_HTTP_CODE:-unavailable}"
        printf 'chatgpt_response_bytes=%s\n' "${DIAG_CHATGPT_BYTES:-unavailable}"
        printf 'chatgpt_remote_ip=%s\n' "${DIAG_CHATGPT_REMOTE_IP:-unavailable}"
        printf 'chatgpt_minimum_bytes=%s\n' "$DIAG_CHATGPT_MIN_BYTES"
        printf 'import_ms=%s\n' "${DIAG_IMPORT_MS:-unavailable}"
        printf 'interface_wait_ms=%s\n' "${DIAG_INTERFACE_WAIT_MS:-unavailable}"
        printf 'traffic_probe_ms=%s\n' "${DIAG_TRAFFIC_MS:-unavailable}"
        printf 'rx_before=%s\n' "${DIAG_RX_BEFORE:-unavailable}"
        printf 'tx_before=%s\n' "${DIAG_TX_BEFORE:-unavailable}"
        printf 'rx_after=%s\n' "${DIAG_RX_AFTER:-unavailable}"
        printf 'tx_after=%s\n' "${DIAG_TX_AFTER:-unavailable}"
        printf 'host=%s\n' "$(hostname)"
        printf 'kernel=%s\n' "$(uname -r)"
    } >"$DIAG_DIR/metadata.txt"
}

diag_process_alive() {
    [[ -n "$DIAG_TOAD_PID" && -d "/proc/$DIAG_TOAD_PID" ]]
}

diag_slirp_alive() {
    [[ -n "$DIAG_SLIRP_PID" && -d "/proc/$DIAG_SLIRP_PID" ]]
}

diag_trace_alive() {
    [[ -n "$DIAG_TRACE_PID" && -d "/proc/$DIAG_TRACE_PID" ]]
}

diag_stop_trace() {
    local _
    if diag_trace_alive; then
        sudo kill -INT "$DIAG_TRACE_PID" >/dev/null 2>&1 || true
        for _ in $(seq 1 30); do
            ! diag_trace_alive && break
            sleep 0.1
        done
    fi
    if diag_trace_alive; then
        sudo kill -TERM "$DIAG_TRACE_PID" >/dev/null 2>&1 || true
    fi
    if [[ -n "$DIAG_TRACE_SUDO_PID" ]]; then
        wait "$DIAG_TRACE_SUDO_PID" 2>/dev/null || true
    fi
    DIAG_TRACE_PID=""
    DIAG_TRACE_SUDO_PID=""
}

diag_trace_contains() {
    local interface="$1"
    local address="$2"
    awk -v interface="$interface" -v address="$address" '
        index($0, interface) && index($0, address) { found = 1 }
        END { exit(found ? 0 : 1) }
    ' "$DIAG_DIR/address-trace.txt"
}

diag_trace_count() {
    local interface="$1"
    local address="$2"
    awk -v interface="$interface" -v address="$address" '
        index($0, interface) && index($0, address) { count++ }
        END { print count + 0 }
    ' "$DIAG_DIR/address-trace.txt"
}

diag_netns_exists() {
    [[ -n "$DIAG_NETNS" ]] && sudo ip netns list | grep -q "^${DIAG_NETNS}\\b"
}

diag_read_counter() {
    local name="$1"
    sudo ip netns exec "$DIAG_NETNS" sed -n '1p' \
        "/sys/class/net/$DIAG_INTERFACE/statistics/$name"
}

diag_wait_uplink() {
    for _ in $(seq 1 200); do
        if sudo ip -n "$DIAG_NETNS" link show dev "$DIAG_UPLINK_INTERFACE" >/dev/null 2>&1 &&
            sudo ip -n "$DIAG_NETNS" -4 route show default dev "$DIAG_UPLINK_INTERFACE" | grep -q .; then
            return 0
        fi
        diag_slirp_alive || diag_fail "slirp4netns exited before the isolated uplink became ready"
        sleep 0.05
    done
    diag_fail "timed out waiting for isolated slirp4netns uplink"
}

diag_wait_interface() {
    local started now line
    started="$(diag_now_ms)"
    for _ in $(seq 1 150); do
        if line="$(sudo ip -n "$DIAG_NETNS" -o link show dev "$DIAG_INTERFACE" 2>/dev/null)"; then
            DIAG_IFINDEX="${line%%:*}"
            DIAG_IFINDEX="${DIAG_IFINDEX//[[:space:]]/}"
            DIAG_MTU="$(awk '{for (i=1; i<=NF; i++) if ($i == "mtu") {print $(i+1); exit}}' <<<"$line")"
            [[ "$DIAG_IFINDEX" =~ ^[1-9][0-9]*$ ]] || diag_fail "invalid ifindex for $DIAG_INTERFACE"
            [[ "$DIAG_MTU" =~ ^[1-9][0-9]*$ ]] || diag_fail "invalid MTU for $DIAG_INTERFACE"
            now="$(diag_now_ms)"
            DIAG_INTERFACE_WAIT_MS=$((now - started))
            return 0
        fi
        diag_process_alive || diag_fail "kikimora-toad exited before $DIAG_INTERFACE appeared"
        sleep 0.1
    done
    diag_fail "timed out waiting for interface $DIAG_INTERFACE"
}

diag_wait_state() {
    for _ in $(seq 1 100); do
        sudo test -s "$DIAG_STATE_DIR/state.json" && return 0
        diag_process_alive || diag_fail "kikimora-toad exited before publishing state.json"
        sleep 0.1
    done
    diag_fail "timed out waiting for state.json"
}

diag_wait_awg_connected() {
    for _ in $(seq 1 100); do
        if sudo python3 - "$DIAG_STATE_DIR/state.json" <<'PY'
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
        diag_process_alive || diag_fail "kikimora-toad exited before reporting an AWG handshake"
        sleep 0.2
    done
    diag_fail "AWG client did not report an online handshake after HTTPS traffic"
}

diag_stop_toad() {
    local _
    local forced=0
    if diag_process_alive; then
        sudo kill -INT "$DIAG_TOAD_PID" >/dev/null 2>&1 || true
        for _ in $(seq 1 50); do
            ! diag_process_alive && break
            sleep 0.1
        done
    fi
    if diag_process_alive; then
        diag_record_error "kikimora-toad did not stop after SIGINT; sending SIGTERM"
        forced=1
        sudo kill -TERM "$DIAG_TOAD_PID" >/dev/null 2>&1 || true
        for _ in $(seq 1 20); do
            ! diag_process_alive && break
            sleep 0.1
        done
    fi
    if diag_process_alive; then
        diag_record_error "kikimora-toad did not stop after SIGTERM; sending SIGKILL"
        sudo kill -KILL "$DIAG_TOAD_PID" >/dev/null 2>&1 || true
    fi
    if [[ -n "$DIAG_SUDO_PID" ]]; then
        wait "$DIAG_SUDO_PID" 2>/dev/null || true
    fi
    DIAG_TOAD_PID=""
    DIAG_SUDO_PID=""
    return "$forced"
}

diag_stop_slirp() {
    local _
    if diag_slirp_alive; then
        sudo kill -TERM "$DIAG_SLIRP_PID" >/dev/null 2>&1 || true
        for _ in $(seq 1 50); do
            ! diag_slirp_alive && break
            sleep 0.1
        done
    fi
    if diag_slirp_alive; then
        diag_record_error "slirp4netns did not stop after SIGTERM; sending SIGKILL"
        sudo kill -KILL "$DIAG_SLIRP_PID" >/dev/null 2>&1 || true
    fi
    if [[ -n "$DIAG_SLIRP_SUDO_PID" ]]; then
        wait "$DIAG_SLIRP_SUDO_PID" 2>/dev/null || true
    fi
    DIAG_SLIRP_PID=""
    DIAG_SLIRP_SUDO_PID=""
}

diag_delete_namespace() {
    local failed=0
    if (( DIAG_NETNS_CREATED )); then
        if ! sudo ip netns delete "$DIAG_NETNS" >/dev/null 2>&1; then
            diag_record_error "could not request deletion of isolated namespace $DIAG_NETNS"
            failed=1
        elif diag_netns_exists; then
            diag_record_error "could not delete isolated namespace $DIAG_NETNS"
            failed=1
        else
            DIAG_NETNS_CREATED=0
        fi
    fi
    if (( DIAG_NETNS_ETC_CREATED )); then
        if ! sudo rm -f -- "$DIAG_NETNS_ETC/resolv.conf" >/dev/null 2>&1; then
            diag_record_error "could not remove namespace resolver file"
            failed=1
        fi
        if ! sudo rmdir -- "$DIAG_NETNS_ETC" >/dev/null 2>&1; then
            diag_record_error "could not remove namespace resolver directory"
            failed=1
        fi
        if ! sudo test ! -e "$DIAG_NETNS_ETC"; then
            diag_record_error "could not remove resolver directory $DIAG_NETNS_ETC"
            failed=1
        else
            DIAG_NETNS_ETC_CREATED=0
        fi
    fi
    return "$failed"
}

diag_collect_after_once() {
    if (( DIAG_AFTER_COLLECTED )); then
        return 0
    fi
    collect_diag "$DIAG_DIR" after
    diag_capture_state "$DIAG_DIR/state-after.json"
    diag_collect_runtime
    DIAG_AFTER_COLLECTED=1
}

diag_finalize() {
    local status="$1"
    local archive_status redact_status
    trap - ERR EXIT INT TERM
    set +e

    if [[ "$DIAG_RESULT" != "PASS" && "$status" -eq 0 ]]; then
        status=1
    fi
    diag_stop_trace
    diag_collect_after_once
    if ! diag_stop_toad; then
        DIAG_RESULT="FAIL"
        status=1
    fi

    if (( DIAG_TOAD_STARTED && DIAG_NETNS_CREATED )) && diag_netns_exists; then
        for _ in $(seq 1 30); do
            ! sudo ip -n "$DIAG_NETNS" link show dev "$DIAG_INTERFACE" >/dev/null 2>&1 && break
            sleep 0.1
        done
        if sudo ip -n "$DIAG_NETNS" link show dev "$DIAG_INTERFACE" >/dev/null 2>&1; then
            diag_record_error "interface $DIAG_INTERFACE survived client shutdown in $DIAG_NETNS"
            DIAG_RESULT="FAIL"
            status=1
        fi
    fi

    diag_stop_slirp
    if ! diag_delete_namespace; then
        DIAG_RESULT="FAIL"
        status=1
    fi

    local root_ifindex_after=""
    root_ifindex_after="$(ip -o link show dev "$DIAG_INTERFACE" 2>/dev/null | awk -F: 'NR == 1 {gsub(/[[:space:]]/, "", $1); print $1}' || true)"
    if [[ "$root_ifindex_after" != "$DIAG_ROOT_IFINDEX_BEFORE" ]]; then
        diag_record_error "root interface identity changed for $DIAG_INTERFACE (before=${DIAG_ROOT_IFINDEX_BEFORE:-absent}, after=${root_ifindex_after:-absent})"
        DIAG_RESULT="FAIL"
        status=1
    fi

    diag_write_metadata "$status"
    diag_ensure_contract_files
    diag_redact_tree
    redact_status=$?
    rm -rf -- "$DIAG_PRIVATE_DIR"

    if (( redact_status != 0 )); then
        printf 'ERROR: redaction failed; refusing to create an unsafe archive\n' >&2
        rm -rf -- "$DIAG_WORK"
        exit 1
    fi

    tar -czf "$DIAG_ARCHIVE" -C "$DIAG_WORK" diag
    archive_status=$?
    if (( archive_status != 0 )); then
        printf 'ERROR: could not create diagnostic archive; sanitized files remain at %s\n' "$DIAG_DIR" >&2
        status="$archive_status"
    else
        chmod 0600 "$DIAG_ARCHIVE" 2>/dev/null || true
        printf 'Diagnostic archive: %s\n' "$DIAG_ARCHIVE"
        rm -rf -- "$DIAG_WORK"
    fi

    exit "$status"
}

run_real_vps_diag() {
    DIAG_PROTOCOL="$1"
    DIAG_INTERFACE="$2"
    DIAG_PROFILE_NAME="$3"
    local share_link="$4"
    local script_dir repo_root build_started import_started probe_started
    local probe_response endpoint endpoint_host endpoint_ip endpoint_port
    local google_probe_ok chatgpt_probe_ok trace_filter
    local baseline_response attempt

    umask 077
    DIAG_STARTED_AT="$(date -Is)"
    DIAG_START_EPOCH="$(date +%s)"
    DIAG_START_MS="$(diag_now_ms)"
    DIAG_WORK="$(mktemp -d "${TMPDIR:-/tmp}/toad-real-vps.XXXXXX")"
    DIAG_DIR="$DIAG_WORK/diag"
    DIAG_PRIVATE_DIR="$DIAG_WORK/private"
    DIAG_STATE_DIR="$DIAG_PRIVATE_DIR/state"
    DIAG_CONFIG="$DIAG_PRIVATE_DIR/profile.toml"
    DIAG_PID_FILE="$DIAG_PRIVATE_DIR/toad.pid"
    DIAG_SLIRP_PID_FILE="$DIAG_PRIVATE_DIR/slirp.pid"
    DIAG_TRACE_PID_FILE="$DIAG_PRIVATE_DIR/tcpdump.pid"
    DIAG_NETNS="toad-real-${UID}-$$"
    DIAG_NETNS_ETC="/etc/netns/$DIAG_NETNS"
    mkdir -p "$DIAG_DIR" "$DIAG_STATE_DIR"
    chmod 0700 "$DIAG_WORK" "$DIAG_DIR" "$DIAG_PRIVATE_DIR" "$DIAG_STATE_DIR"
    : >"$DIAG_DIR/command.log"
    : >"$DIAG_DIR/errors.txt"
    : >"$DIAG_DIR/toad.log"
    : >"$DIAG_DIR/traffic-test.txt"
    : >"$DIAG_DIR/address-trace.txt"

    DIAG_ARCHIVE="${TOAD_DIAG_OUTPUT:-$PWD/toad-real-vps-diag-$(date +%Y-%m-%d-%H%M%S).tar.gz}"
    if [[ "$DIAG_ARCHIVE" != /* ]]; then
        DIAG_ARCHIVE="$PWD/$DIAG_ARCHIVE"
    fi
    if [[ -e "$DIAG_ARCHIVE" ]]; then
        DIAG_ARCHIVE="${DIAG_ARCHIVE%.tar.gz}-$$.tar.gz"
    fi

    trap 'diag_err_trap "$?" "$LINENO"' ERR
    trap 'diag_finalize "$?"' EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM

    diag_log "START protocol=$DIAG_PROTOCOL interface=$DIAG_INTERFACE"

    for command in awk bash cat chmod curl date env getent grep install ip ps python3 rm rmdir sed seq slirp4netns ss sudo tail tar tcpdump tee test tr; do
        diag_require "$command"
    done
    if [[ -z "${TOAD_BIN:-}" ]]; then
        diag_require go
    fi
    [[ "$DIAG_TEST_HOST" =~ ^[A-Za-z0-9.-]+$ ]] || diag_fail "invalid TOAD_TEST_HOST"
    [[ "$DIAG_GOOGLE_MIN_BYTES" =~ ^[1-9][0-9]*$ ]] || diag_fail "invalid TOAD_GOOGLE_MIN_BYTES"
    [[ "$DIAG_CHATGPT_MIN_BYTES" =~ ^[1-9][0-9]*$ ]] || diag_fail "invalid TOAD_CHATGPT_MIN_BYTES"

    case "$DIAG_PROTOCOL" in
        amneziawg2)
            [[ "$share_link" =~ ^(vpn|wg|wireguard|amneziawg):// ]] || diag_fail "expected an Amnezia VPN/AWG/WireGuard share link"
            ;;
        vless-reality)
            [[ "$share_link" == vless://* ]] || diag_fail "expected a vless:// share link"
            ;;
        *)
            diag_fail "unsupported protocol: $DIAG_PROTOCOL"
            ;;
    esac

    diag_log "collect BEFORE"
    collect_diag "$DIAG_DIR" before
    diag_capture_state "$DIAG_DIR/state-before.json"
    DIAG_ROOT_IFINDEX_BEFORE="$(ip -o link show dev "$DIAG_INTERFACE" 2>/dev/null | awk -F: 'NR == 1 {gsub(/[[:space:]]/, "", $1); print $1}' || true)"
    sudo -v

    script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
    repo_root="$(cd -- "$script_dir/../../../.." && pwd)"
    if [[ -n "${TOAD_BIN:-}" ]]; then
        [[ -x "$TOAD_BIN" ]] || diag_fail "TOAD_BIN is not executable: $TOAD_BIN"
        DIAG_TOAD_BIN="$TOAD_BIN"
        diag_log "use prebuilt kikimora-toad from TOAD_BIN"
    else
        DIAG_TOAD_BIN="$DIAG_PRIVATE_DIR/kikimora-toad"
        build_started="$(diag_now_ms)"
        diag_log "build checked-out kikimora-toad"
        (
            cd "$repo_root/toad"
            go build -o "$DIAG_TOAD_BIN" ./cmd/kikimora-toad
        ) >>"$DIAG_DIR/command.log" 2>&1
        chmod 0755 "$DIAG_TOAD_BIN"
        diag_log "build completed in $(( $(diag_now_ms) - build_started ))ms"
    fi

    diag_log "import share link from stdin"
    import_started="$(diag_now_ms)"
    printf '%s\n' "$share_link" | "$DIAG_TOAD_BIN" import \
        -name "$DIAG_PROFILE_NAME" \
        -interface "$DIAG_INTERFACE" \
        -state-dir "$DIAG_STATE_DIR" \
        >"$DIAG_CONFIG" 2>>"$DIAG_DIR/command.log"
    DIAG_IMPORT_MS=$(( $(diag_now_ms) - import_started ))
    share_link=""

    chmod 0600 "$DIAG_CONFIG"
    "$DIAG_TOAD_BIN" validate -config "$DIAG_CONFIG" >>"$DIAG_DIR/command.log" 2>&1
    diag_write_config_summary

    endpoint="$(python3 - "$DIAG_DIR/config-summary.json" <<'PY'
import json
import sys
print(json.load(open(sys.argv[1], encoding="utf-8")).get("endpoint") or "")
PY
)"

    DIAG_TEST_IP="$(getent ahostsv4 "$DIAG_TEST_HOST" | awk 'NR == 1 { print $1; exit }' || true)"
    [[ "$DIAG_TEST_IP" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || diag_fail "could not resolve an IPv4 address for $DIAG_TEST_HOST"
    DIAG_GOOGLE_IP="$(getent ahostsv4 "$DIAG_GOOGLE_HOST" | awk 'NR == 1 { print $1; exit }' || true)"
    [[ "$DIAG_GOOGLE_IP" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || diag_fail "could not resolve an IPv4 address for $DIAG_GOOGLE_HOST"

    endpoint_host="$(python3 - "$endpoint" <<'PY'
import sys

endpoint = sys.argv[1]
if endpoint.startswith("["):
    host = endpoint[1:].split("]", 1)[0]
else:
    host = endpoint.rsplit(":", 1)[0]
print(host)
PY
)"
    endpoint_port="$(python3 - "$endpoint" <<'PY'
import sys

endpoint = sys.argv[1]
print(endpoint.rsplit(":", 1)[1] if ":" in endpoint else "")
PY
)"
    endpoint_ip="$(getent ahostsv4 "$endpoint_host" 2>/dev/null | awk 'NR == 1 { print $1; exit }' || true)"
    [[ "$endpoint_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || diag_fail "could not resolve the VPN endpoint to IPv4"
    [[ "$endpoint_port" =~ ^[1-9][0-9]{0,4}$ ]] && (( endpoint_port <= 65535 )) || diag_fail "VPN endpoint has an invalid port"
    [[ "$endpoint_ip" != "$DIAG_TEST_IP" && "$endpoint_ip" != "$DIAG_GOOGLE_IP" ]] || \
        diag_fail "a test destination resolves to the VPN endpoint; refusing a recursive route"

    diag_log "create isolated network namespace $DIAG_NETNS"
    if sudo ip netns list | grep -q "^${DIAG_NETNS}\\b"; then
        diag_fail "network namespace $DIAG_NETNS already exists"
    fi
    if sudo test -e "$DIAG_NETNS_ETC"; then
        diag_fail "resolver directory $DIAG_NETNS_ETC already exists"
    fi
    sudo ip netns add "$DIAG_NETNS"
    DIAG_NETNS_CREATED=1
    sudo install -d -m 0755 -- "$DIAG_NETNS_ETC"
    DIAG_NETNS_ETC_CREATED=1
    printf 'nameserver 192.0.2.3\noptions timeout:2 attempts:2\n' | \
        sudo tee "$DIAG_NETNS_ETC/resolv.conf" >/dev/null
    sudo chmod 0644 "$DIAG_NETNS_ETC/resolv.conf"

    diag_log "start isolated slirp4netns uplink"
    sudo -- bash -c 'printf "%s\n" "$$" >"$1"; shift; exec "$@"' \
        bash "$DIAG_SLIRP_PID_FILE" slirp4netns \
        --configure --disable-host-loopback --mtu=65520 --cidr=192.0.2.0/24 \
        --netns-type=path "/run/netns/$DIAG_NETNS" "$DIAG_UPLINK_INTERFACE" \
        >>"$DIAG_DIR/command.log" 2>&1 &
    DIAG_SLIRP_SUDO_PID=$!
    for _ in $(seq 1 100); do
        [[ -s "$DIAG_SLIRP_PID_FILE" ]] && break
        kill -0 "$DIAG_SLIRP_SUDO_PID" 2>/dev/null || diag_fail "sudo failed to start slirp4netns"
        sleep 0.05
    done
    [[ -s "$DIAG_SLIRP_PID_FILE" ]] || diag_fail "timed out waiting for slirp4netns PID"
    DIAG_SLIRP_PID="$(sudo sed -n '1p' "$DIAG_SLIRP_PID_FILE")"
    diag_wait_uplink
    sudo ip -n "$DIAG_NETNS" -4 route get "$endpoint_ip" | \
        grep -F "dev $DIAG_UPLINK_INTERFACE" >>"$DIAG_DIR/traffic-test.txt" || \
        diag_fail "VPN endpoint is not routed through the isolated uplink"

    {
        printf 'google_host=%s\n' "$DIAG_GOOGLE_HOST"
        printf 'google_ip=%s\n' "$DIAG_GOOGLE_IP"
        printf 'chatgpt_host=%s\n' "$DIAG_TEST_HOST"
        printf 'chatgpt_ip=%s\n' "$DIAG_TEST_IP"
        printf 'endpoint_transport=%s:%s\n' "$endpoint_ip" "$endpoint_port"
    } >>"$DIAG_DIR/traffic-test.txt"
    baseline_response=""
    if baseline_response="$(sudo ip netns exec "$DIAG_NETNS" env \
        -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
        curl -4sS --noproxy '*' --connect-timeout 5 --max-time 15 \
        --resolve "$DIAG_TEST_HOST:443:$DIAG_TEST_IP" -o /dev/null \
        -w 'http_code=%{http_code} remote_ip=%{remote_ip} total_s=%{time_total}' \
        "https://$DIAG_TEST_HOST/" 2>>"$DIAG_DIR/traffic-test.txt")"; then
        printf 'isolated_uplink_baseline=ok %s\n' "$baseline_response" >>"$DIAG_DIR/traffic-test.txt"
    else
        printf 'isolated_uplink_baseline=failed (diagnostic only)\n' >>"$DIAG_DIR/traffic-test.txt"
    fi

    diag_log "start kikimora-toad inside $DIAG_NETNS"
    sudo -- bash -c 'printf "%s\n" "$$" >"$1"; shift; exec "$@"' \
        bash "$DIAG_PID_FILE" ip netns exec "$DIAG_NETNS" \
        "$DIAG_TOAD_BIN" run -config "$DIAG_CONFIG" \
        >>"$DIAG_DIR/toad.log" 2>&1 &
    DIAG_SUDO_PID=$!
    DIAG_TOAD_STARTED=1
    for _ in $(seq 1 100); do
        [[ -s "$DIAG_PID_FILE" ]] && break
        kill -0 "$DIAG_SUDO_PID" 2>/dev/null || diag_fail "sudo failed to start kikimora-toad"
        sleep 0.05
    done
    [[ -s "$DIAG_PID_FILE" ]] || diag_fail "timed out waiting for kikimora-toad PID"
    DIAG_TOAD_PID="$(sudo sed -n '1p' "$DIAG_PID_FILE")"

    diag_log "wait for interface $DIAG_INTERFACE"
    diag_wait_interface
    diag_wait_state
    DIAG_RX_BEFORE="$(diag_read_counter rx_bytes)"
    DIAG_TX_BEFORE="$(diag_read_counter tx_bytes)"

    diag_log "route Google and ChatGPT addresses through $DIAG_INTERFACE inside $DIAG_NETNS"
    sudo ip -n "$DIAG_NETNS" -4 route replace "$DIAG_GOOGLE_IP/32" dev "$DIAG_INTERFACE"
    sudo ip -n "$DIAG_NETNS" -4 route replace "$DIAG_TEST_IP/32" dev "$DIAG_INTERFACE"
    {
        printf 'route_google='
        sudo ip -n "$DIAG_NETNS" -4 route get "$DIAG_GOOGLE_IP"
        printf 'route_chatgpt='
        sudo ip -n "$DIAG_NETNS" -4 route get "$DIAG_TEST_IP"
        printf 'route_endpoint='
        sudo ip -n "$DIAG_NETNS" -4 route get "$endpoint_ip"
    } >>"$DIAG_DIR/traffic-test.txt"
    sudo ip -n "$DIAG_NETNS" -4 route get "$DIAG_GOOGLE_IP" | grep -Fq "dev $DIAG_INTERFACE" || \
        diag_fail "Google destination is not routed through $DIAG_INTERFACE"
    sudo ip -n "$DIAG_NETNS" -4 route get "$DIAG_TEST_IP" | grep -Fq "dev $DIAG_INTERFACE" || \
        diag_fail "ChatGPT destination is not routed through $DIAG_INTERFACE"
    sudo ip -n "$DIAG_NETNS" -4 route get "$endpoint_ip" | grep -Fq "dev $DIAG_UPLINK_INTERFACE" || \
        diag_fail "VPN endpoint became recursively routed through $DIAG_INTERFACE"

    trace_filter="host $DIAG_GOOGLE_IP or host $DIAG_TEST_IP or host $endpoint_ip"
    printf 'capture_filter=%s\n' "$trace_filter" >>"$DIAG_DIR/address-trace.txt"
    sudo -- bash -c 'printf "%s\n" "$$" >"$1"; shift; exec "$@"' \
        bash "$DIAG_TRACE_PID_FILE" ip netns exec "$DIAG_NETNS" \
        tcpdump -nn -l -i any -s 96 "$trace_filter" \
        >>"$DIAG_DIR/address-trace.txt" 2>&1 &
    DIAG_TRACE_SUDO_PID=$!
    for _ in $(seq 1 100); do
        [[ -s "$DIAG_TRACE_PID_FILE" ]] && break
        kill -0 "$DIAG_TRACE_SUDO_PID" 2>/dev/null || diag_fail "sudo failed to start address trace"
        sleep 0.05
    done
    [[ -s "$DIAG_TRACE_PID_FILE" ]] || diag_fail "timed out waiting for address trace PID"
    DIAG_TRACE_PID="$(sudo sed -n '1p' "$DIAG_TRACE_PID_FILE")"
    sleep 0.2

    diag_log "run strict Google 200 probe and ChatGPT access probe through isolated Toad interface"
    probe_started="$(diag_now_ms)"
    google_probe_ok=0
    for attempt in 1 2 3; do
        probe_response=""
        if probe_response="$(sudo ip netns exec "$DIAG_NETNS" env \
            -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
            curl -4sS --noproxy '*' --interface "$DIAG_INTERFACE" \
            --connect-timeout 10 --max-time 30 \
            --resolve "$DIAG_GOOGLE_HOST:443:$DIAG_GOOGLE_IP" -o /dev/null \
            -w 'http_code=%{http_code} size_download=%{size_download} remote_ip=%{remote_ip} total_s=%{time_total}' \
            "https://$DIAG_GOOGLE_HOST$DIAG_GOOGLE_PATH" 2>>"$DIAG_DIR/traffic-test.txt")"; then
            DIAG_GOOGLE_HTTP_CODE="$(sed -n 's/.*http_code=\([^ ]*\).*/\1/p' <<<"$probe_response")"
            DIAG_GOOGLE_BYTES="$(sed -n 's/.*size_download=\([^ ]*\).*/\1/p' <<<"$probe_response")"
            DIAG_GOOGLE_REMOTE_IP="$(sed -n 's/.*remote_ip=\([^ ]*\).*/\1/p' <<<"$probe_response")"
            if [[ "$DIAG_GOOGLE_HTTP_CODE" == "200" && "$DIAG_GOOGLE_REMOTE_IP" == "$DIAG_GOOGLE_IP" && "$DIAG_GOOGLE_BYTES" =~ ^[0-9]+$ ]] && \
                (( DIAG_GOOGLE_BYTES >= DIAG_GOOGLE_MIN_BYTES )); then
                printf 'probe=google attempt=%s status=ok %s minimum_bytes=%s\n' "$attempt" "$probe_response" "$DIAG_GOOGLE_MIN_BYTES" >>"$DIAG_DIR/traffic-test.txt"
                google_probe_ok=1
                break
            fi
            printf 'probe=google attempt=%s status=bad-code-or-short-body %s minimum_bytes=%s\n' "$attempt" "$probe_response" "$DIAG_GOOGLE_MIN_BYTES" >>"$DIAG_DIR/traffic-test.txt"
        else
            printf 'probe=google attempt=%s status=transport-failed\n' "$attempt" >>"$DIAG_DIR/traffic-test.txt"
        fi
        sleep 1
    done
    (( google_probe_ok )) || diag_fail "Google probe did not return HTTP 200 with at least $DIAG_GOOGLE_MIN_BYTES bytes through $DIAG_INTERFACE"

    chatgpt_probe_ok=0
    for attempt in 1 2 3; do
        probe_response=""
        if probe_response="$(sudo ip netns exec "$DIAG_NETNS" env \
            -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
            curl -4sS --noproxy '*' --interface "$DIAG_INTERFACE" \
            --connect-timeout 10 --max-time 30 \
            --resolve "$DIAG_TEST_HOST:443:$DIAG_TEST_IP" -o /dev/null \
            -w 'http_code=%{http_code} size_download=%{size_download} remote_ip=%{remote_ip} total_s=%{time_total}' \
            "https://$DIAG_TEST_HOST/" 2>>"$DIAG_DIR/traffic-test.txt")"; then
            DIAG_CHATGPT_HTTP_CODE="$(sed -n 's/.*http_code=\([^ ]*\).*/\1/p' <<<"$probe_response")"
            DIAG_CHATGPT_BYTES="$(sed -n 's/.*size_download=\([^ ]*\).*/\1/p' <<<"$probe_response")"
            DIAG_CHATGPT_REMOTE_IP="$(sed -n 's/.*remote_ip=\([^ ]*\).*/\1/p' <<<"$probe_response")"
            if [[ "$DIAG_CHATGPT_HTTP_CODE" =~ ^2[0-9][0-9]$ && "$DIAG_CHATGPT_REMOTE_IP" == "$DIAG_TEST_IP" && "$DIAG_CHATGPT_BYTES" =~ ^[0-9]+$ ]] && \
                (( DIAG_CHATGPT_BYTES >= DIAG_CHATGPT_MIN_BYTES )); then
                printf 'probe=chatgpt attempt=%s status=ok %s minimum_bytes=%s\n' "$attempt" "$probe_response" "$DIAG_CHATGPT_MIN_BYTES" >>"$DIAG_DIR/traffic-test.txt"
                chatgpt_probe_ok=1
                break
            fi
            printf 'probe=chatgpt attempt=%s status=bad-code-or-short-body %s minimum_bytes=%s\n' "$attempt" "$probe_response" "$DIAG_CHATGPT_MIN_BYTES" >>"$DIAG_DIR/traffic-test.txt"
        else
            printf 'probe=chatgpt attempt=%s status=transport-failed\n' "$attempt" >>"$DIAG_DIR/traffic-test.txt"
        fi
        sleep 1
    done
    (( chatgpt_probe_ok )) || diag_fail "ChatGPT probe did not return 2xx with at least $DIAG_CHATGPT_MIN_BYTES bytes through $DIAG_INTERFACE"

    diag_stop_trace
    diag_trace_contains "$DIAG_INTERFACE" "$DIAG_GOOGLE_IP" || \
        diag_fail "address trace did not observe Google traffic on $DIAG_INTERFACE"
    diag_trace_contains "$DIAG_INTERFACE" "$DIAG_TEST_IP" || \
        diag_fail "address trace did not observe ChatGPT traffic on $DIAG_INTERFACE"
    diag_trace_contains "$DIAG_UPLINK_INTERFACE" "$endpoint_ip" || \
        diag_fail "address trace did not observe VPN endpoint transport on $DIAG_UPLINK_INTERFACE"
    if diag_trace_contains "$DIAG_UPLINK_INTERFACE" "$DIAG_GOOGLE_IP" || \
        diag_trace_contains "$DIAG_UPLINK_INTERFACE" "$DIAG_TEST_IP"; then
        diag_fail "address trace observed direct test-destination traffic leaking onto $DIAG_UPLINK_INTERFACE"
    fi
    {
        printf 'trace_google_via_tun=ok interface=%s destination=%s:443 packets=%s\n' "$DIAG_INTERFACE" "$DIAG_GOOGLE_IP" "$(diag_trace_count "$DIAG_INTERFACE" "$DIAG_GOOGLE_IP")"
        printf 'trace_chatgpt_via_tun=ok interface=%s destination=%s:443 packets=%s\n' "$DIAG_INTERFACE" "$DIAG_TEST_IP" "$(diag_trace_count "$DIAG_INTERFACE" "$DIAG_TEST_IP")"
        printf 'trace_vpn_transport_via_uplink=ok interface=%s endpoint=%s:%s packets=%s\n' "$DIAG_UPLINK_INTERFACE" "$endpoint_ip" "$endpoint_port" "$(diag_trace_count "$DIAG_UPLINK_INTERFACE" "$endpoint_ip")"
        printf 'trace_direct_target_leak=absent interface=%s\n' "$DIAG_UPLINK_INTERFACE"
    } >>"$DIAG_DIR/traffic-test.txt"
    DIAG_TRAFFIC_MS=$(( $(diag_now_ms) - probe_started ))
    diag_process_alive || diag_fail "kikimora-toad exited during the traffic probe"
    if [[ "$DIAG_PROTOCOL" == "amneziawg2" ]]; then
        diag_wait_awg_connected
    fi

    DIAG_RX_AFTER="$(diag_read_counter rx_bytes)"
    DIAG_TX_AFTER="$(diag_read_counter tx_bytes)"
    (( DIAG_RX_AFTER > DIAG_RX_BEFORE )) || diag_fail "interface RX counter did not advance"
    (( DIAG_TX_AFTER > DIAG_TX_BEFORE )) || diag_fail "interface TX counter did not advance"

    diag_log "collect AFTER"
    diag_collect_after_once
    DIAG_RESULT="PASS"
    diag_log "PASS: Google returned 200, ChatGPT returned 2xx, response sizes passed, and address trace proved both crossed $DIAG_INTERFACE"
}
