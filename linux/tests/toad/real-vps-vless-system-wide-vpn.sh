#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../../.." && pwd)"
CALLER_UID="$(id -u)"
CALLER_GID="$(id -g)"
CONTROLLER_KIND="${TOAD_SYSTEM_WIDE_KIND:-xray}"
PROFILE_PROTOCOL="${TOAD_SYSTEM_WIDE_PROTOCOL:-vless-reality}"
INTERFACE="${TOAD_SYSTEM_WIDE_INTERFACE:-kk-xray0}"
LINK_FILE="${TOAD_SYSTEM_WIDE_LINK_FILE:-$SCRIPT_DIR/real-vps-vless-link.secret}"
COMMAND_PATH="${TOAD_SYSTEM_WIDE_COMMAND:-$0}"
UNIT="kikimora-toad-system-wide-$CONTROLLER_KIND-$CALLER_UID.service"
RUN_DIR="/run/kikimora-toad-system-wide-$CONTROLLER_KIND-$CALLER_UID"
RUN_EXEC_DIR=""
RUN_BIN=""
RUN_CONFIG="$RUN_DIR/profile.toml"
RUN_STATE_DIR="$RUN_DIR/state"
OWNER_STATE="$RUN_DIR/owner.state"
DIAG_DIR="$RUN_DIR/diag"
LOCK_BASE="${XDG_RUNTIME_DIR:-/run/user/$CALLER_UID}"
LOCK_FILE="$LOCK_BASE/kikimora-toad-system-wide.lock"

BUILD_DIR=""
UP_IN_PROGRESS=0
TOAD_STARTED=0
ENDPOINT_ROUTE_ADDED=0
ENDPOINT_ROUTE_METRIC=42740
DEFAULT_ROUTE_ADDED=0
DNS_CONFIGURED=0
ENDPOINT=""
ENDPOINT_IP=""
ENDPOINT_PORT=""
UNDERLAY_DEV=""
UNDERLAY_GATEWAY=""
UNDERLAY_SRC=""
EXPECTED_IFINDEX=""
PUBLIC_IP=""
GOOGLE_IP=""
GOOGLE_HTTP_CODE=""
GOOGLE_RESPONSE_BYTES=""
GOOGLE_REMOTE_IP=""
DIAG_ENABLED=0
DIAG_ARCHIVE=""
DIAG_STARTED_AT=""
DIAG_START_EPOCH=""
UP_LINK_ARG=""
UP_LINK_PROVIDED=0
CONTROLLER_LABEL=""
LINK_PATTERN=""
LINK_DESCRIPTION=""
CONFIG_SECTION=""

log() {
    printf '[%s] %s\n' "$(date -Is)" "$*"
}

fail() {
    printf 'ERROR: %s\n' "$*" >&2
    return 1
}

usage() {
    cat <<EOF
Usage:
  $COMMAND_PATH up [--diag] [--diag-output FILE] [$LINK_DESCRIPTION]
  $COMMAND_PATH down
  $COMMAND_PATH status

Run this script as the desktop user, not through sudo. It requests sudo only
for the root-owned Toad process and the routes/DNS that it owns.
EOF
}

assert_controller_spec() {
    case "$PROFILE_PROTOCOL:$CONTROLLER_KIND:$INTERFACE" in
        vless-reality:xray:kk-xray0)
            CONTROLLER_LABEL="Xray"
            LINK_PATTERN='^(vless|vpn)://'
            LINK_DESCRIPTION='vless://...|vpn://...'
            CONFIG_SECTION="vless_reality"
            ;;
        amneziawg2:awg:kk-awg0)
            CONTROLLER_LABEL="AmneziaWG2"
            LINK_PATTERN='^(vpn|wg|wireguard|amneziawg)://'
            LINK_DESCRIPTION='vpn://...|wg://...|wireguard://...|amneziawg://...'
            CONFIG_SECTION="awg2"
            ;;
        *)
            fail "unsupported controller configuration: protocol=$PROFILE_PROTOCOL kind=$CONTROLLER_KIND interface=$INTERFACE"
            ;;
    esac
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

is_ipv4() {
    [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]
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

unit_active() {
    systemctl is-active --quiet "$UNIT"
}

acquire_lock() {
    [[ -d "$LOCK_BASE" && -w "$LOCK_BASE" ]] || fail "safe per-user runtime directory is unavailable: $LOCK_BASE"
    exec 9>"$LOCK_FILE"
    flock -n 9 || fail "another system-wide $CONTROLLER_LABEL controller command is already running"
}

assert_not_root() {
    if (( EUID == 0 )); then
        fail "run this script without sudo; it elevates only the operations that need root"
    fi
}

assert_no_other_vpn() {
    local unit line dev
    local -a reasons=()

    for unit in leshy.service leshy-route-watch.service leshy-health-watch.service; do
        if systemctl is-active --quiet "$unit"; then
            reasons+=("active unit $unit")
        fi
    done
    if ps -eo comm= | awk '$1 == "leshy" { found=1 } END { exit(found ? 0 : 1) }'; then
        reasons+=("running process leshy")
    fi

    while IFS= read -r unit; do
        [[ -n "$unit" ]] && reasons+=("active Toad unit $unit")
    done < <(systemctl list-units --type=service --state=active --no-legend 'kikimora-toad@*.service' 2>/dev/null | awk '{print $1}')
    while IFS= read -r unit; do
        [[ -n "$unit" && "$unit" != "$UNIT" ]] && reasons+=("active system-wide Toad unit $unit")
    done < <(systemctl list-units --type=service --state=active --no-legend 'kikimora-toad-system-wide-*.service' 2>/dev/null | awk '{print $1}')

    if command -v nmcli >/dev/null 2>&1; then
        while IFS= read -r line; do
            case "$line" in
                vpn:*|wireguard:*|tun:*) reasons+=("active NetworkManager connection $line") ;;
            esac
        done < <(nmcli -t -f TYPE,DEVICE connection show --active 2>/dev/null || true)
    fi

    while IFS= read -r dev; do
        [[ -n "$dev" && "$dev" != "$INTERFACE" ]] || continue
        if is_vpn_interface_name "$dev"; then
            reasons+=("active route through $dev")
        fi
    done < <(
        {
            ip -o -4 route show table all
            ip -o -6 route show table all
        } 2>/dev/null | awk '
            {
                dev=""
                for (i=1; i<=NF; i++) if ($i == "dev") { dev=$(i+1); break }
                if (dev == "") next
                if ($1 == "default") { print dev; next }
                if ($1 ~ /^(local|broadcast|multicast|fe80::)/) next
                if ($0 ~ /proto kernel scope link/) next
                print dev
            }
        ' | sort -u
    )

    if (( ${#reasons[@]} > 0 )); then
        printf 'ERROR: another VPN appears to be active:\n' >&2
        printf '  - %s\n' "${reasons[@]}" >&2
        printf 'Disconnect it first. A stale interface with no VPN route is intentionally ignored.\n' >&2
        return 1
    fi
}

state_get() {
    local key="$1"
    sudo awk -F= -v wanted="$key" '$1 == wanted { sub(/^[^=]*=/, ""); print; exit }' "$OWNER_STATE" 2>/dev/null || true
}

write_owner_state() {
    local tmp="$BUILD_DIR/owner.state"
    {
        printf 'version=1\n'
        printf 'owner_uid=%s\n' "$CALLER_UID"
        printf 'unit=%s\n' "$UNIT"
        printf 'interface=%s\n' "$INTERFACE"
        printf 'ifindex=%s\n' "$EXPECTED_IFINDEX"
        printf 'endpoint=%s\n' "$ENDPOINT"
        printf 'endpoint_ip=%s\n' "$ENDPOINT_IP"
        printf 'endpoint_port=%s\n' "$ENDPOINT_PORT"
        printf 'underlay_dev=%s\n' "$UNDERLAY_DEV"
        printf 'underlay_gateway=%s\n' "$UNDERLAY_GATEWAY"
        printf 'underlay_src=%s\n' "$UNDERLAY_SRC"
        printf 'endpoint_route_added=%s\n' "$ENDPOINT_ROUTE_ADDED"
        printf 'endpoint_route_metric=%s\n' "$ENDPOINT_ROUTE_METRIC"
        printf 'default_route_added=%s\n' "$DEFAULT_ROUTE_ADDED"
        printf 'dns_configured=%s\n' "$DNS_CONFIGURED"
        printf 'toad_started=%s\n' "$TOAD_STARTED"
        printf 'public_ip=%s\n' "$PUBLIC_IP"
        printf 'exec_dir=%s\n' "$RUN_EXEC_DIR"
        printf 'diag_enabled=%s\n' "$DIAG_ENABLED"
        printf 'diag_archive=%s\n' "$DIAG_ARCHIVE"
        printf 'diag_started_at=%s\n' "$DIAG_STARTED_AT"
        printf 'diag_start_epoch=%s\n' "$DIAG_START_EPOCH"
    } >"$tmp"
    sudo install -o root -g root -m 0600 "$tmp" "$OWNER_STATE"
}

diag_log() {
    (( DIAG_ENABLED )) || return 0
    printf '[%s] %s\n' "$(date -Is)" "$*" | sudo tee -a "$DIAG_DIR/command.log" >/dev/null
}

diag_error() {
    (( DIAG_ENABLED )) || return 0
    printf '[%s] ERROR: %s\n' "$(date -Is)" "$*" | sudo tee -a "$DIAG_DIR/errors.txt" >/dev/null
}

diag_capture_state() {
    local name="$1"
    (( DIAG_ENABLED )) || return 0
    if sudo test -s "$RUN_STATE_DIR/state.json"; then
        sudo cp "$RUN_STATE_DIR/state.json" "$DIAG_DIR/$name.json"
    else
        printf '{"present":false}\n' | sudo tee "$DIAG_DIR/$name.json" >/dev/null
    fi
}

diag_collect_snapshot() {
    local phase="$1"
    local root="$DIAG_DIR/$phase"
    (( DIAG_ENABLED )) || return 0
    sudo install -d -o root -g root -m 0700 "$root"

    sudo -- bash -c '
        {
            date -Is
            uname -a
            ip -details -statistics link show
            ip -details -statistics addr show
        } > "$1" 2>&1
    ' bash "$root/network.txt"
    sudo -- bash -c '
        {
            printf "=== IPv4 routes, all tables ===\\n"
            ip -4 route show table all
            printf "\\n=== IPv6 routes, all tables ===\\n"
            ip -6 route show table all
            printf "\\n=== rules ===\\n"
            ip rule show
        } > "$1" 2>&1
    ' bash "$root/routes.txt"
    sudo -- bash -c '
        {
            printf "=== /etc/resolv.conf ===\\n"
            ls -l /etc/resolv.conf
            sed -n "1,120p" /etc/resolv.conf
            if command -v resolvectl >/dev/null 2>&1; then
                printf "\\n=== resolvectl status ===\\n"
                resolvectl status
                printf "\\n=== resolvectl interface ===\\n"
                resolvectl status "$2" || true
            fi
        } > "$1" 2>&1
    ' bash "$root/dns.txt" "$INTERFACE"
    sudo -- bash -c '
        {
            ss -tunap
            printf "\\n=== processes without argv ===\\n"
            ps -eo pid,ppid,user,group,stat,etimes,comm
            printf "\\n=== controller unit ===\\n"
            systemctl status "$2" --no-pager || true
        } > "$1" 2>&1
    ' bash "$root/runtime.txt" "$UNIT"
}

diag_write_config_summary() {
    (( DIAG_ENABLED )) || return 0
    sudo python3 - "$RUN_CONFIG" "$DIAG_DIR/config-summary.json" "$CONFIG_SECTION" <<'PY'
import hashlib
import json
import pathlib
import sys
import tomllib

cfg = tomllib.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
section = cfg.get(sys.argv[3]) or {}
fingerprint_source = dict(cfg)
fingerprint_source["state_dir"] = "<state-dir>"
summary = {
    "config_fingerprint_sha256": hashlib.sha256(
        json.dumps(fingerprint_source, sort_keys=True, separators=(",", ":")).encode()
    ).hexdigest(),
    "name": cfg.get("name"),
    "protocol": cfg.get("protocol"),
    "interface": cfg.get("interface"),
    "address": cfg.get("address", []),
    "mtu": cfg.get("mtu"),
    "endpoint": section.get("endpoint"),
}
if cfg.get("protocol") == "amneziawg2":
    summary["allowed_ips"] = section.get("allowed_ips", [])
    summary["persistent_keepalive"] = section.get("persistent_keepalive", 0)
elif cfg.get("protocol") == "vless-reality":
    for key in ("server_name", "flow", "fingerprint", "transport"):
        summary[key] = section.get(key)
pathlib.Path(sys.argv[2]).write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
}

diag_redact() {
    (( DIAG_ENABLED )) || return 0
    sudo python3 - "$DIAG_DIR" <<'PY'
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

diag_write_metadata() {
    local result="$1"
    (( DIAG_ENABLED )) || return 0
    {
        printf 'result=%s\n' "$result"
        printf 'protocol=%s\n' "$PROFILE_PROTOCOL"
        printf 'interface=%s\n' "$INTERFACE"
        printf 'ifindex=%s\n' "${EXPECTED_IFINDEX:-unavailable}"
        printf 'endpoint=%s\n' "$ENDPOINT"
        printf 'endpoint_ip=%s\n' "$ENDPOINT_IP"
        printf 'underlay_dev=%s\n' "$UNDERLAY_DEV"
        printf 'public_ip=%s\n' "${PUBLIC_IP:-unavailable}"
        printf 'google_ip=%s\n' "${GOOGLE_IP:-unavailable}"
        printf 'google_http_code=%s\n' "${GOOGLE_HTTP_CODE:-unavailable}"
        printf 'google_response_bytes=%s\n' "${GOOGLE_RESPONSE_BYTES:-unavailable}"
        printf 'google_remote_ip=%s\n' "${GOOGLE_REMOTE_IP:-unavailable}"
        printf 'started_at=%s\n' "$DIAG_STARTED_AT"
        printf 'ended_at=%s\n' "$(date -Is)"
        printf 'unit=%s\n' "$UNIT"
    } | sudo tee "$DIAG_DIR/metadata.txt" >/dev/null
}

diag_collect_journal() {
    (( DIAG_ENABLED )) || return 0
    sudo -- bash -c '
        {
            printf "=== controller journal ===\\n"
            journalctl --no-pager -u "$2" --since "@$3" -n 1500 || true
            printf "\\n=== kernel journal ===\\n"
            journalctl -k --no-pager --since "@$3" -n 800 || true
        } > "$1" 2>&1
    ' bash "$DIAG_DIR/journal.txt" "$UNIT" "$DIAG_START_EPOCH"
}

diag_package() {
    local result="$1"
    (( DIAG_ENABLED )) || return 0
    diag_collect_journal
    diag_write_metadata "$result"
    diag_redact
    sudo tar -czf "$DIAG_ARCHIVE" -C "$RUN_DIR" diag
    sudo chown "$CALLER_UID:$CALLER_GID" "$DIAG_ARCHIVE"
    chmod 0600 "$DIAG_ARCHIVE"
    printf 'Diagnostic archive: %s\n' "$DIAG_ARCHIVE"
}

diag_initialize() {
    (( DIAG_ENABLED )) || return 0
    DIAG_STARTED_AT="$(date -Is)"
    DIAG_START_EPOCH="$(date +%s)"
    sudo install -d -o root -g root -m 0700 "$DIAG_DIR"
    sudo install -o root -g root -m 0600 /dev/null "$DIAG_DIR/command.log"
    sudo install -o root -g root -m 0600 /dev/null "$DIAG_DIR/traffic-test.txt"
    sudo install -o root -g root -m 0600 /dev/null "$DIAG_DIR/errors.txt"
    write_owner_state
    diag_log "START system-wide $CONTROLLER_LABEL controller diagnostic recording"
    diag_write_config_summary
    diag_capture_state state-before
    diag_collect_snapshot before
}

remove_endpoint_route() {
    local -a route
    (( ENDPOINT_ROUTE_ADDED )) || return 0
    is_ipv4 "$ENDPOINT_IP" || return 1
    is_safe_interface "$UNDERLAY_DEV" || return 1

    route=(sudo ip -4 route del "$ENDPOINT_IP/32")
    if [[ -n "$UNDERLAY_GATEWAY" ]]; then
        is_ipv4 "$UNDERLAY_GATEWAY" || return 1
        route+=(via "$UNDERLAY_GATEWAY")
    fi
    route+=(dev "$UNDERLAY_DEV")
    if [[ -n "$UNDERLAY_SRC" ]]; then
        is_ipv4 "$UNDERLAY_SRC" || return 1
        route+=(src "$UNDERLAY_SRC")
    fi
    route+=(metric "$ENDPOINT_ROUTE_METRIC")
    "${route[@]}" >/dev/null 2>&1 || true
    if ip -4 route show exact "$ENDPOINT_IP/32" 2>/dev/null | \
        grep -F "dev $UNDERLAY_DEV" | grep -Fq "metric $ENDPOINT_ROUTE_METRIC"; then
        return 1
    fi
    ENDPOINT_ROUTE_ADDED=0
}

is_owned_exec_dir() {
    [[ "$RUN_EXEC_DIR" == "/tmp/kikimora-toad-system-wide-$CONTROLLER_KIND-$CALLER_UID."* ]] || return 1
    [[ "${RUN_EXEC_DIR##*.}" =~ ^[A-Za-z0-9]{6,}$ ]]
}

remove_owned_runtime() {
    local failed=0

    if [[ "$RUN_DIR" == "/run/kikimora-toad-system-wide-$CONTROLLER_KIND-$CALLER_UID" ]]; then
        sudo rm -rf -- "$RUN_DIR" || failed=1
    else
        failed=1
    fi
    if [[ -n "$RUN_EXEC_DIR" ]]; then
        if is_owned_exec_dir; then
            sudo rm -rf -- "$RUN_EXEC_DIR" || failed=1
        else
            failed=1
        fi
    fi
    return "$failed"
}

rollback_up() {
    local current_ifindex=""
    local cleanup_failed=0
    set +e
    log "enable failed; removing owned system-wide $CONTROLLER_LABEL state"
    diag_error "UP failed; beginning rollback"
    diag_capture_state state-active-final
    diag_collect_snapshot active-final
    if (( DEFAULT_ROUTE_ADDED )); then
        sudo ip -4 route del default dev "$INTERFACE" metric 4 >/dev/null 2>&1
        DEFAULT_ROUTE_ADDED=0
    fi
    if (( DNS_CONFIGURED )); then
        sudo resolvectl revert "$INTERFACE" >/dev/null 2>&1
        DNS_CONFIGURED=0
    fi
    remove_endpoint_route || cleanup_failed=1
    if (( TOAD_STARTED )); then
        sudo systemctl stop "$UNIT" >/dev/null 2>&1
        sudo systemctl reset-failed "$UNIT" >/dev/null 2>&1
    fi
    for _ in $(seq 1 50); do
        ip link show dev "$INTERFACE" >/dev/null 2>&1 || break
        sleep 0.1
    done
    if ip -o link show dev "$INTERFACE" >/dev/null 2>&1; then
        current_ifindex="$(ip -o link show dev "$INTERFACE" | awk -F: '{gsub(/[[:space:]]/, "", $1); print $1}')"
        if [[ -n "$EXPECTED_IFINDEX" && "$current_ifindex" == "$EXPECTED_IFINDEX" ]]; then
            sudo ip link delete dev "$INTERFACE" >/dev/null 2>&1
        fi
    fi
    diag_capture_state state-after
    diag_collect_snapshot after
    if ! diag_package "FAILED_UP"; then
        cleanup_failed=1
        printf 'ERROR: diagnostic archive could not be written; runtime state remains at %s\n' "$RUN_DIR" >&2
    fi
    if (( cleanup_failed == 0 )) && ! remove_owned_runtime; then
        cleanup_failed=1
    fi
    if (( cleanup_failed )); then
        printf 'ERROR: automatic rollback could not fully remove its owned route or runtime state; retained state begins at %s\n' "$RUN_DIR" >&2
    fi
}

on_exit() {
    local status="$?"
    trap - EXIT INT TERM
    if (( status != 0 && UP_IN_PROGRESS )); then
        rollback_up
    fi
    if [[ -n "$BUILD_DIR" && -d "$BUILD_DIR" ]]; then
        rm -rf -- "$BUILD_DIR"
    fi
    exit "$status"
}

load_link() {
    if (( $# == 1 )); then
        printf '%s' "$1"
    elif [[ -r "$LINK_FILE" ]]; then
        sed -n '1p' "$LINK_FILE"
    else
        fail "put a supported $CONTROLLER_LABEL share link in $LINK_FILE (mode 0600), or pass it to up"
    fi
}

set_diag_archive() {
    local requested="$1"
    local parent base
    [[ "$requested" == *.tar.gz ]] || fail "diagnostic output must end with .tar.gz"
    [[ "$requested" == /* ]] || requested="$PWD/$requested"
    parent="$(dirname -- "$requested")"
    base="$(basename -- "$requested")"
    [[ "$base" != "." && "$base" != "/" ]] || fail "invalid diagnostic output name"
    [[ -d "$parent" && -w "$parent" ]] || fail "diagnostic output directory is not writable: $parent"
    DIAG_ARCHIVE="$(cd -- "$parent" && pwd -P)/$base"
    [[ ! -e "$DIAG_ARCHIVE" ]] || DIAG_ARCHIVE="${DIAG_ARCHIVE%.tar.gz}-$$.tar.gz"
}

prepare_up_args() {
    local output=""

    DIAG_ENABLED=0
    UP_LINK_ARG=""
    UP_LINK_PROVIDED=0
    while (( $# > 0 )); do
        case "$1" in
            --diag)
                DIAG_ENABLED=1
                ;;
            --diag-output)
                (( $# >= 2 )) || fail "--diag-output requires a .tar.gz path"
                DIAG_ENABLED=1
                output="$2"
                shift
                ;;
            --)
                shift
                (( $# <= 1 )) || fail "up accepts at most one share link"
                if (( $# == 1 )); then
                    UP_LINK_ARG="$1"
                    UP_LINK_PROVIDED=1
                fi
                break
                ;;
            --*)
                fail "unknown up option: $1"
                ;;
            *)
                (( UP_LINK_PROVIDED == 0 )) || fail "up accepts at most one share link"
                UP_LINK_ARG="$1"
                UP_LINK_PROVIDED=1
                ;;
        esac
        shift
    done
    if (( DIAG_ENABLED )); then
        if [[ -z "$output" ]]; then
            output="$PWD/toad-system-wide-$CONTROLLER_KIND-controller-diag-$(date +%Y-%m-%d-%H%M%S).tar.gz"
        fi
        set_diag_archive "$output"
    fi
}

route_parts() {
    local route="$1"
    UNDERLAY_DEV="$(awk '{for(i=1;i<=NF;i++) if($i=="dev"){print $(i+1); exit}}' <<<"$route")"
    UNDERLAY_GATEWAY="$(awk '{for(i=1;i<=NF;i++) if($i=="via"){print $(i+1); exit}}' <<<"$route")"
    UNDERLAY_SRC="$(awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}' <<<"$route")"
}

assert_protocol_ready() {
    local ready=0
    [[ "$PROFILE_PROTOCOL" == "amneziawg2" ]] || return 0

    for _ in $(seq 1 100); do
        if sudo python3 - "$RUN_STATE_DIR/state.json" <<'PY'
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
            ready=1
            break
        fi
        unit_active || fail "kikimora-toad exited before the AmneziaWG2 handshake completed"
        sleep 0.2
    done
    (( ready )) || fail "AmneziaWG2 did not report an online handshake after Google traffic"
}

cmd_up() {
    local link protocol endpoint_host endpoint_route existing_pin line response http_code response_bytes remote_ip
    local source_bin
    local -a endpoint_add

    prepare_up_args "$@"
    sudo -v
    unit_active && fail "$UNIT is already active; use status or down"
    if sudo test -e "$RUN_DIR"; then
        fail "owned runtime state already exists at $RUN_DIR; run down before up"
    fi
    ip link show dev "$INTERFACE" >/dev/null 2>&1 && fail "$INTERFACE already exists and is not owned by this run"
    assert_no_other_vpn
    if ip -6 route show default | grep -q .; then
        fail "an IPv6 default route exists; this IPv4-only controller refuses to risk an IPv6 leak"
    fi

    BUILD_DIR="$(mktemp -d "${TMPDIR:-/tmp}/toad-${CONTROLLER_KIND}-system-wide.XXXXXX")"
    chmod 0700 "$BUILD_DIR"
    UP_IN_PROGRESS=1

    if [[ -n "${TOAD_BIN:-}" ]]; then
        [[ -x "$TOAD_BIN" ]] || fail "TOAD_BIN is not executable: $TOAD_BIN"
        source_bin="$TOAD_BIN"
    else
        require_command go
        source_bin="$BUILD_DIR/kikimora-toad"
        log "building checked-out kikimora-toad"
        (cd "$REPO_ROOT/toad" && go build -o "$source_bin" ./cmd/kikimora-toad)
    fi

    if (( UP_LINK_PROVIDED )); then
        link="$(load_link "$UP_LINK_ARG")"
    else
        link="$(load_link)"
    fi
    [[ "$link" =~ $LINK_PATTERN ]] || fail "expected $LINK_DESCRIPTION"
    printf '%s\n' "$link" | "$source_bin" import \
        -name "system-wide-$CONTROLLER_KIND" -interface "$INTERFACE" -state-dir "$RUN_STATE_DIR" \
        >"$BUILD_DIR/profile.toml"
    link=""
    chmod 0600 "$BUILD_DIR/profile.toml"
    "$source_bin" validate -config "$BUILD_DIR/profile.toml"

    IFS=$'\t' read -r protocol ENDPOINT < <(python3 - "$BUILD_DIR/profile.toml" "$CONFIG_SECTION" <<'PY'
import pathlib, sys, tomllib
cfg = tomllib.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
section = cfg.get(sys.argv[2]) or {}
print(f"{cfg.get('protocol', '')}\t{section.get('endpoint', '')}")
PY
    )
    [[ "$protocol" == "$PROFILE_PROTOCOL" ]] || fail "share link imported as $protocol instead of $PROFILE_PROTOCOL"
    [[ "$ENDPOINT" == *:* ]] || fail "imported $CONTROLLER_LABEL endpoint is invalid"
    IFS=$'\t' read -r endpoint_host ENDPOINT_PORT < <(python3 - "$ENDPOINT" <<'PY'
import sys
value = sys.argv[1]
if value.startswith("["):
    host, rest = value[1:].split("]", 1)
    port = rest.removeprefix(":")
else:
    host, port = value.rsplit(":", 1)
print(f"{host}\t{port}")
PY
    )
    [[ "$ENDPOINT_PORT" =~ ^[1-9][0-9]{0,4}$ ]] && (( ENDPOINT_PORT <= 65535 )) || fail "invalid $CONTROLLER_LABEL endpoint port"
    ENDPOINT_IP="$(getent ahostsv4 "$endpoint_host" | awk 'NR==1 {print $1; exit}' || true)"
    is_ipv4 "$ENDPOINT_IP" || fail "could not resolve $CONTROLLER_LABEL endpoint to IPv4"

    # Re-check immediately before installing any route, including the endpoint's
    # actual current path. Interface existence alone is deliberately not used.
    assert_no_other_vpn
    endpoint_route="$(ip -4 route get "$ENDPOINT_IP" 2>/dev/null || true)"
    [[ -n "$endpoint_route" ]] || fail "no route to $CONTROLLER_LABEL endpoint $ENDPOINT_IP"
    route_parts "$endpoint_route"
    is_safe_interface "$UNDERLAY_DEV" || fail "unsafe underlay interface name: $UNDERLAY_DEV"
    if is_vpn_interface_name "$UNDERLAY_DEV"; then
        fail "$CONTROLLER_LABEL endpoint currently routes through VPN-like interface $UNDERLAY_DEV"
    fi

    sudo install -d -o root -g root -m 0700 "$RUN_DIR" "$RUN_STATE_DIR"
    RUN_EXEC_DIR="$(sudo mktemp -d "/tmp/kikimora-toad-system-wide-$CONTROLLER_KIND-$CALLER_UID.XXXXXX")"
    is_owned_exec_dir || fail "could not create a safe root-owned executable directory"
    RUN_BIN="$RUN_EXEC_DIR/kikimora-toad"
    sudo install -o root -g root -m 0755 "$source_bin" "$RUN_BIN"
    sudo install -o root -g root -m 0600 "$BUILD_DIR/profile.toml" "$RUN_CONFIG"
    write_owner_state
    diag_initialize

    existing_pin="$(ip -4 route show exact "$ENDPOINT_IP/32" 2>/dev/null || true)"
    if [[ -n "$existing_pin" ]]; then
        log "reusing existing endpoint route: $existing_pin"
    else
        endpoint_add=(sudo ip -4 route add "$ENDPOINT_IP/32")
        [[ -n "$UNDERLAY_GATEWAY" ]] && endpoint_add+=(via "$UNDERLAY_GATEWAY")
        endpoint_add+=(dev "$UNDERLAY_DEV")
        [[ -n "$UNDERLAY_SRC" ]] && endpoint_add+=(src "$UNDERLAY_SRC")
        ENDPOINT_ROUTE_ADDED=1
        write_owner_state
        endpoint_add+=(metric "$ENDPOINT_ROUTE_METRIC")
        "${endpoint_add[@]}"
    fi

    sudo systemctl reset-failed "$UNIT" >/dev/null 2>&1 || true
    TOAD_STARTED=1
    write_owner_state
    sudo systemd-run --unit="$UNIT" --collect --quiet \
        --property=Type=simple --property=Restart=no --property=KillMode=control-group \
        --property=TimeoutStopSec=10s \
        "$RUN_BIN" run -config "$RUN_CONFIG"
    for _ in $(seq 1 150); do
        if line="$(ip -o link show dev "$INTERFACE" 2>/dev/null)"; then
            EXPECTED_IFINDEX="${line%%:*}"
            EXPECTED_IFINDEX="${EXPECTED_IFINDEX//[[:space:]]/}"
            break
        fi
        unit_active || fail "kikimora-toad exited before $INTERFACE appeared"
        sleep 0.1
    done
    [[ "$EXPECTED_IFINDEX" =~ ^[1-9][0-9]*$ ]] || fail "timed out waiting for $INTERFACE"
    write_owner_state

    DEFAULT_ROUTE_ADDED=1
    write_owner_state
    sudo ip -4 route add default dev "$INTERFACE" metric 4
    ip -4 route get "$ENDPOINT_IP" | grep -Fq "dev $UNDERLAY_DEV" || fail "$CONTROLLER_LABEL endpoint recursively entered $INTERFACE"

    require_command resolvectl
    resolvectl status >/dev/null 2>&1 || fail "systemd-resolved is unavailable"
    DNS_CONFIGURED=1
    write_owner_state
    sudo resolvectl dns "$INTERFACE" 1.1.1.1 1.0.0.1
    sudo resolvectl domain "$INTERFACE" '~.'
    sudo resolvectl default-route "$INTERFACE" yes

    endpoint_host="www.google.com"
    line="$(getent ahostsv4 "$endpoint_host" | awk 'NR==1 {print $1; exit}' || true)"
    is_ipv4 "$line" || fail "could not resolve Google through system-wide $CONTROLLER_LABEL"
    ip -4 route get "$line" | grep -Fq "dev $INTERFACE" || fail "Google is not routed through $INTERFACE"
    response="$(env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
        curl -4sS --noproxy '*' --connect-timeout 10 --max-time 30 \
        --resolve "$endpoint_host:443:$line" -o /dev/null \
        -w 'http_code=%{http_code} size_download=%{size_download} remote_ip=%{remote_ip}' \
        "https://$endpoint_host/")" || fail "Google transport check failed through system-wide $CONTROLLER_LABEL"
    http_code="$(sed -n 's/.*http_code=\([^ ]*\).*/\1/p' <<<"$response")"
    response_bytes="$(sed -n 's/.*size_download=\([^ ]*\).*/\1/p' <<<"$response")"
    remote_ip="$(sed -n 's/.*remote_ip=\([^ ]*\).*/\1/p' <<<"$response")"
    GOOGLE_IP="$line"
    GOOGLE_HTTP_CODE="$http_code"
    GOOGLE_RESPONSE_BYTES="$response_bytes"
    GOOGLE_REMOTE_IP="$remote_ip"
    if (( DIAG_ENABLED )); then
        printf 'google=%s\n' "$response" | sudo tee -a "$DIAG_DIR/traffic-test.txt" >/dev/null
    fi
    [[ "$http_code" == "200" && "$remote_ip" == "$line" ]] || fail "Google verification failed: $response"
    [[ "$response_bytes" =~ ^[0-9]+$ ]] && (( response_bytes >= 10000 )) || fail "Google response was too short: ${response_bytes:-unknown} bytes"

    assert_protocol_ready
    PUBLIC_IP="$(env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
        curl -4fsS --noproxy '*' --connect-timeout 5 --max-time 15 https://api.ipify.org 2>/dev/null || true)"
    write_owner_state
    diag_capture_state state-active
    diag_collect_snapshot active
    diag_log "UP verified: Google HTTP $GOOGLE_HTTP_CODE, $GOOGLE_RESPONSE_BYTES bytes, public_ip=${PUBLIC_IP:-unavailable}"
    UP_IN_PROGRESS=0

    log "system-wide $CONTROLLER_LABEL is UP"
    printf 'interface=%s ifindex=%s\n' "$INTERFACE" "$EXPECTED_IFINDEX"
    printf 'endpoint=%s via=%s\n' "$ENDPOINT" "$UNDERLAY_DEV"
    printf 'public_ip=%s\n' "${PUBLIC_IP:-unavailable}"
    if (( DIAG_ENABLED )); then
        printf 'diagnostic_archive_on_down=%s\n' "$DIAG_ARCHIVE"
    fi
    printf 'Disable with: %s down\n' "$COMMAND_PATH"
}

load_down_state() {
    ENDPOINT="$(state_get endpoint)"
    ENDPOINT_IP="$(state_get endpoint_ip)"
    ENDPOINT_PORT="$(state_get endpoint_port)"
    UNDERLAY_DEV="$(state_get underlay_dev)"
    UNDERLAY_GATEWAY="$(state_get underlay_gateway)"
    UNDERLAY_SRC="$(state_get underlay_src)"
    EXPECTED_IFINDEX="$(state_get ifindex)"
    ENDPOINT_ROUTE_ADDED="$(state_get endpoint_route_added)"
    DEFAULT_ROUTE_ADDED="$(state_get default_route_added)"
    DNS_CONFIGURED="$(state_get dns_configured)"
    TOAD_STARTED="$(state_get toad_started)"
    RUN_EXEC_DIR="$(state_get exec_dir)"
    DIAG_ENABLED="$(state_get diag_enabled)"
    DIAG_ARCHIVE="$(state_get diag_archive)"
    DIAG_STARTED_AT="$(state_get diag_started_at)"
    DIAG_START_EPOCH="$(state_get diag_start_epoch)"
    [[ "$ENDPOINT_ROUTE_ADDED" =~ ^[01]$ ]] || ENDPOINT_ROUTE_ADDED=0
    [[ "$DEFAULT_ROUTE_ADDED" =~ ^[01]$ ]] || DEFAULT_ROUTE_ADDED=0
    [[ "$DNS_CONFIGURED" =~ ^[01]$ ]] || DNS_CONFIGURED=0
    [[ "$TOAD_STARTED" =~ ^[01]$ ]] || TOAD_STARTED=0
    if [[ -n "$RUN_EXEC_DIR" ]] && ! is_owned_exec_dir; then
        fail "runtime state has an unexpected executable directory"
    fi
    [[ "$DIAG_ENABLED" =~ ^[01]$ ]] || DIAG_ENABLED=0
    if (( DIAG_ENABLED )); then
        [[ "$DIAG_ARCHIVE" == /* ]] || fail "diagnostic runtime state has an invalid archive path"
        [[ "$DIAG_START_EPOCH" =~ ^[0-9]+$ ]] || fail "diagnostic runtime state has an invalid start time"
    fi
}

cmd_down() {
    local had_state=0 current_ifindex="" failed=0
    sudo -v
    if sudo test -r "$OWNER_STATE"; then
        had_state=1
        load_down_state
        [[ "$(state_get owner_uid)" == "$CALLER_UID" ]] || fail "runtime state belongs to another user"
        [[ "$(state_get interface)" == "$INTERFACE" ]] || fail "runtime state has an unexpected interface"
        [[ "$(state_get unit)" == "$UNIT" ]] || fail "runtime state has an unexpected systemd unit"
    elif ! unit_active; then
        printf 'System-wide %s is already DOWN.\n' "$CONTROLLER_LABEL"
        return 0
    else
        TOAD_STARTED=1
        DEFAULT_ROUTE_ADDED=1
        DNS_CONFIGURED=1
        failed=1
    fi

    current_ifindex="$(ip -o link show dev "$INTERFACE" 2>/dev/null | awk -F: '{gsub(/[[:space:]]/, "", $1); print $1}' || true)"
    [[ -n "$EXPECTED_IFINDEX" ]] || EXPECTED_IFINDEX="$current_ifindex"

    log "restoring host route and DNS"
    diag_log "DOWN requested; capturing final active state before cleanup"
    diag_capture_state state-active-final
    diag_collect_snapshot active-final
    if (( DEFAULT_ROUTE_ADDED )); then
        sudo ip -4 route del default dev "$INTERFACE" metric 4 >/dev/null 2>&1 || true
        DEFAULT_ROUTE_ADDED=0
    fi
    if (( DNS_CONFIGURED )) && command -v resolvectl >/dev/null 2>&1; then
        sudo resolvectl revert "$INTERFACE" >/dev/null 2>&1 || true
        DNS_CONFIGURED=0
    fi
    if (( had_state )); then
        remove_endpoint_route || failed=1
    fi

    sudo systemctl stop "$UNIT" >/dev/null 2>&1 || true
    for _ in $(seq 1 100); do
        unit_active || break
        sleep 0.1
    done
    unit_active && failed=1
    sudo systemctl reset-failed "$UNIT" >/dev/null 2>&1 || true

    for _ in $(seq 1 50); do
        ip link show dev "$INTERFACE" >/dev/null 2>&1 || break
        sleep 0.1
    done
    if ip -o link show dev "$INTERFACE" >/dev/null 2>&1; then
        current_ifindex="$(ip -o link show dev "$INTERFACE" | awk -F: '{gsub(/[[:space:]]/, "", $1); print $1}')"
        if [[ -n "$EXPECTED_IFINDEX" && "$current_ifindex" == "$EXPECTED_IFINDEX" ]]; then
            sudo ip link delete dev "$INTERFACE" >/dev/null 2>&1 || true
        fi
    fi
    ip link show dev "$INTERFACE" >/dev/null 2>&1 && failed=1
    ip -4 route show default dev "$INTERFACE" metric 4 | grep -q . && failed=1

    diag_capture_state state-after
    diag_collect_snapshot after
    if (( DIAG_ENABLED )); then
        if (( failed == 0 )); then
            diag_log "DOWN cleanup completed; packaging diagnostic archive"
            diag_package "DOWN" || failed=1
        else
            diag_error "DOWN cleanup is incomplete; packaging diagnostic archive"
            diag_package "CLEANUP_FAILED" || true
        fi
    fi

    if (( failed == 0 )) && remove_owned_runtime; then
        log "system-wide $CONTROLLER_LABEL is DOWN; owned routes, DNS, interface, unit, config and runtime files were removed"
    else
        fail "cleanup is incomplete; runtime state was preserved at $RUN_DIR for inspection/retry"
    fi
}

cmd_status() {
    local active=0 iface=0 route=0 owned_state=0 endpoint="" public_ip=""
    sudo -v
    unit_active && active=1
    ip link show dev "$INTERFACE" >/dev/null 2>&1 && iface=1
    ip -4 route show default dev "$INTERFACE" metric 4 | grep -q . && route=1
    if sudo test -r "$OWNER_STATE"; then
        owned_state=1
        endpoint="$(state_get endpoint)"
        public_ip="$(state_get public_ip)"
    fi

    if (( active && iface && route && owned_state )); then
        printf 'System-wide %s: UP\n' "$CONTROLLER_LABEL"
        printf 'unit=%s interface=%s endpoint=%s public_ip=%s\n' \
            "$UNIT" "$INTERFACE" "${endpoint:-unavailable}" "${public_ip:-unavailable}"
        return 0
    fi
    if (( ! active && ! iface && ! route && ! owned_state )); then
        printf 'System-wide %s: DOWN\n' "$CONTROLLER_LABEL"
        return 0
    fi
    printf 'System-wide %s: INCONSISTENT (unit=%s interface=%s default_route=%s owner_state=%s)\n' \
        "$CONTROLLER_LABEL" "$active" "$iface" "$route" "$owned_state" >&2
    printf 'Run %s down to clean owned state.\n' "$COMMAND_PATH" >&2
    return 2
}

main() {
    local command="${1:-}"
    assert_not_root
    assert_controller_spec
    [[ -n "$command" ]] || { usage; return 2; }
    shift
    for item in awk basename cat chmod cp curl date dirname env flock getent grep install ip mktemp ps python3 rm sed seq sort sudo systemctl systemd-run tar tee; do
        require_command "$item"
    done
    acquire_lock
    trap on_exit EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM

    case "$command" in
        up) cmd_up "$@" ;;
        down) (( $# == 0 )) || fail "down does not accept arguments"; cmd_down ;;
        status) (( $# == 0 )) || fail "status does not accept arguments"; cmd_status ;;
        -h|--help|help) usage ;;
        *) usage; fail "unknown command: $command" ;;
    esac
}

main "$@"
