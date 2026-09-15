#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$ROOT/../../.." && pwd)"
# shellcheck source=linux/tests/toad/lib/openconnect-real-profile.sh
source "$ROOT/lib/openconnect-real-profile.sh"

CALLER_UID="$(id -u)"
CALLER_GID="$(id -g)"
INTERFACE="kk-oc0"
DEFAULT_PROFILE="$ROOT/real-vps-openconnect.secret"
GOOGLE_HOST="www.google.com"
INTERNAL_HOST="gitlab.sca.ad-tech.ru"
UNIT="kikimora-toad-system-wide-openconnect-$CALLER_UID.service"
RUN_DIR="/run/kikimora-toad-system-wide-openconnect-$CALLER_UID"
RUN_STATE_DIR="$RUN_DIR/state"
RUN_CONFIG="$RUN_DIR/profile.toml"
RUN_PASSWORD="$RUN_DIR/password.secret"
RUN_TOKEN="$RUN_DIR/totp.secret"
RUN_DNS_OVERRIDE="$RUN_DIR/dns-override.txt"
OWNER_STATE="$RUN_DIR/owner.state"
DIAG_DIR="$RUN_DIR/diag"
LOCK_BASE="${XDG_RUNTIME_DIR:-/run/user/$CALLER_UID}"
LOCK_FILE="$LOCK_BASE/kikimora-toad-system-wide.lock"
ENDPOINT_ROUTE_METRIC=42742

BUILD_DIR=""
RUN_EXEC_DIR=""
RUN_BIN=""
PROFILE=""
UP_IN_PROGRESS=0
TOAD_STARTED=0
ENDPOINT_ROUTE_ADDED=0
DEFAULT_ROUTE_ADDED=0
DNS_CONFIGURED=0
DIAG_ENABLED=0
DIAG_ARCHIVE=""
DIAG_STARTED_AT=""
DIAG_START_EPOCH=""
GATEWAY=""
GATEWAY_HOST=""
ENDPOINT_IP=""
UNDERLAY_DEV=""
UNDERLAY_GATEWAY=""
UNDERLAY_SRC=""
EXPECTED_IFINDEX=""
PUBLIC_IP=""
GOOGLE_IP=""
GOOGLE_HTTP_CODE=""
GOOGLE_BYTES=""
INTERNAL_IP=""
INTERNAL_HTTP_CODE=""
INTERNAL_BYTES=""
DNS_SOURCE=""
DNS_RAW=""

log() { printf '[%s] %s\n' "$(date -Is)" "$*"; }
fail() { printf 'ERROR: %s\n' "$*" >&2; return 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"; }
is_ipv4() { [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; }
is_safe_interface() { [[ "$1" =~ ^[A-Za-z0-9_.:-]+$ ]]; }
unit_active() { systemctl is-active --quiet "$UNIT"; }

usage() {
    cat <<EOF
Usage:
  $0 up [--diag] [--diag-output FILE] [PROFILE]
  $0 status
  $0 down

PROFILE defaults to:
  $DEFAULT_PROFILE

The local PROFILE is a single chmod-0600 TOML containing gateway, username,
password and TOTP seed. It is ignored by Git when named *.secret.

up --diag starts diagnostic recording and leaves the VPN active. The archive is
finished and written by down, so manual browser testing between up/down is kept.
Legacy Kikimora is never stopped or modified by this script.
EOF
}

assert_not_root() {
    (( EUID != 0 )) || fail "run this controller as the desktop user, not through sudo"
}

acquire_lock() {
    [[ -d "$LOCK_BASE" && -w "$LOCK_BASE" ]] || fail "per-user runtime directory is unavailable: $LOCK_BASE"
    exec 9>"$LOCK_FILE"
    flock -n 9 || fail "another system-wide Toad controller command is running"
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
        if systemctl is-active --quiet "$unit"; then
            reasons+=("active unit $unit")
        fi
    done
    if ps -eo comm= | awk '$1 == "leshy" { found=1 } END { exit(found ? 0 : 1) }'; then
        reasons+=("running process leshy")
    fi
    while IFS= read -r unit; do
        [[ -n "$unit" && "$unit" != "$UNIT" ]] && reasons+=("active Toad unit $unit")
    done < <(systemctl list-units --type=service --state=active --no-legend 'kikimora-toad*.service' 2>/dev/null | awk '{print $1}')
    if command -v nmcli >/dev/null 2>&1; then
        while IFS= read -r line; do
            case "$line" in vpn:*|wireguard:*|tun:*) reasons+=("active NetworkManager connection $line") ;; esac
        done < <(nmcli -t -f TYPE,DEVICE connection show --active 2>/dev/null || true)
    fi
    while IFS= read -r dev; do
        [[ -n "$dev" && "$dev" != "$INTERFACE" ]] || continue
        if is_vpn_interface_name "$dev"; then
            reasons+=("active default route through $dev")
        fi
    done < <(ip -o -4 route show default 2>/dev/null | awk '{for(i=1;i<=NF;i++)if($i=="dev"){print $(i+1)}}' | sort -u)
    if (( ${#reasons[@]} > 0 )); then
        printf 'ERROR: another VPN appears active; stop it yourself before this test:\n' >&2
        printf '  - %s\n' "${reasons[@]}" >&2
        return 1
    fi
}

is_owned_exec_dir() {
    [[ "$RUN_EXEC_DIR" == "/tmp/kikimora-toad-system-wide-openconnect-$CALLER_UID."* ]] || return 1
    [[ "${RUN_EXEC_DIR##*.}" =~ ^[A-Za-z0-9]{6,}$ ]]
}

state_get() {
    local key="$1"
    sudo awk -F= -v wanted="$key" '$1==wanted {sub(/^[^=]*=/, ""); print; exit}' "$OWNER_STATE" 2>/dev/null || true
}

write_owner_state() {
    local tmp="$BUILD_DIR/owner.state"
    {
        printf 'version=1\n'
        printf 'owner_uid=%s\n' "$CALLER_UID"
        printf 'unit=%s\n' "$UNIT"
        printf 'interface=%s\n' "$INTERFACE"
        printf 'ifindex=%s\n' "$EXPECTED_IFINDEX"
        printf 'gateway=%s\n' "$GATEWAY"
        printf 'endpoint_ip=%s\n' "$ENDPOINT_IP"
        printf 'underlay_dev=%s\n' "$UNDERLAY_DEV"
        printf 'underlay_gateway=%s\n' "$UNDERLAY_GATEWAY"
        printf 'underlay_src=%s\n' "$UNDERLAY_SRC"
        printf 'endpoint_route_added=%s\n' "$ENDPOINT_ROUTE_ADDED"
        printf 'default_route_added=%s\n' "$DEFAULT_ROUTE_ADDED"
        printf 'dns_configured=%s\n' "$DNS_CONFIGURED"
        printf 'toad_started=%s\n' "$TOAD_STARTED"
        printf 'exec_dir=%s\n' "$RUN_EXEC_DIR"
        printf 'public_ip=%s\n' "$PUBLIC_IP"
        printf 'diag_enabled=%s\n' "$DIAG_ENABLED"
        printf 'diag_archive=%s\n' "$DIAG_ARCHIVE"
        printf 'diag_started_at=%s\n' "$DIAG_STARTED_AT"
        printf 'diag_start_epoch=%s\n' "$DIAG_START_EPOCH"
        printf 'dns_source=%s\n' "$DNS_SOURCE"
        printf 'google_ip=%s\n' "$GOOGLE_IP"
        printf 'internal_ip=%s\n' "$INTERNAL_IP"
    } >"$tmp"
    sudo install -o root -g root -m 0600 "$tmp" "$OWNER_STATE"
}

route_parts() {
    local route_text="$1"
    UNDERLAY_DEV="$(awk '{for(i=1;i<=NF;i++)if($i=="dev"){print $(i+1);exit}}' <<<"$route_text")"
    UNDERLAY_GATEWAY="$(awk '{for(i=1;i<=NF;i++)if($i=="via"){print $(i+1);exit}}' <<<"$route_text")"
    UNDERLAY_SRC="$(awk '{for(i=1;i<=NF;i++)if($i=="src"){print $(i+1);exit}}' <<<"$route_text")"
}

set_diag_archive() {
    local requested="$1" parent base
    [[ "$requested" == *.tar.gz ]] || fail "diagnostic output must end in .tar.gz"
    [[ "$requested" == /* ]] || requested="$PWD/$requested"
    parent="$(dirname -- "$requested")"
    base="$(basename -- "$requested")"
    [[ -d "$parent" && -w "$parent" ]] || fail "diagnostic output directory is not writable: $parent"
    DIAG_ARCHIVE="$(cd -- "$parent" && pwd -P)/$base"
    [[ ! -e "$DIAG_ARCHIVE" ]] || DIAG_ARCHIVE="${DIAG_ARCHIVE%.tar.gz}-$$.tar.gz"
}

prepare_up_args() {
    local output=""
    local profile_set=0
    PROFILE="$DEFAULT_PROFILE"
    DIAG_ENABLED=0
    while (( $# > 0 )); do
        case "$1" in
            --diag) DIAG_ENABLED=1 ;;
            --diag-output)
                (( $# >= 2 )) || fail "--diag-output requires a .tar.gz path"
                DIAG_ENABLED=1
                output="$2"
                shift
                ;;
            --*) fail "unknown up option: $1" ;;
            *)
                (( profile_set == 0 )) || fail "up accepts at most one PROFILE"
                PROFILE="$1"
                profile_set=1
                ;;
        esac
        shift
    done
    if (( DIAG_ENABLED )); then
        [[ -n "$output" ]] || output="$PWD/toad-system-wide-openconnect-controller-diag-$(date +%Y-%m-%d-%H%M%S).tar.gz"
        set_diag_archive "$output"
    fi
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
        sudo cp "$RUN_STATE_DIR/state.json" "$DIAG_DIR/state-$name.json"
    else
        printf '{"present":false}\n' | sudo tee "$DIAG_DIR/state-$name.json" >/dev/null
    fi
    if sudo test -s "$RUN_STATE_DIR/openconnect-network.env"; then
        sudo cp "$RUN_STATE_DIR/openconnect-network.env" "$DIAG_DIR/openconnect-network-$name.env"
    fi
}

diag_snapshot() {
    local phase="$1"
    local dir="$DIAG_DIR/$phase"
    (( DIAG_ENABLED )) || return 0
    sudo install -d -o root -g root -m 0700 "$dir"
    sudo -- bash -c '{ date -Is; uname -a; ip -details -statistics link show; ip -details -statistics addr show; } >"$1" 2>&1' bash "$dir/network.txt"
    sudo -- bash -c '{ echo "=== IPv4 routes ==="; ip -4 route show table all; echo; echo "=== IPv6 routes ==="; ip -6 route show table all; echo; echo "=== rules ==="; ip rule show; } >"$1" 2>&1' bash "$dir/routes.txt"
    sudo -- bash -c '{ echo "=== resolv.conf ==="; ls -l /etc/resolv.conf; sed -n "1,160p" /etc/resolv.conf; if command -v resolvectl >/dev/null 2>&1; then echo; echo "=== resolvectl ==="; resolvectl status || true; fi; } >"$1" 2>&1' bash "$dir/dns.txt"
    sudo -- bash -c '{ ss -tunap || true; echo; echo "=== processes without argv ==="; ps -eo pid,ppid,user,group,stat,etimes,comm; echo; echo "=== unit ==="; systemctl status "$2" --no-pager || true; } >"$1" 2>&1' bash "$dir/runtime.txt" "$UNIT"
}

diag_initialize() {
    (( DIAG_ENABLED )) || return 0
    DIAG_STARTED_AT="$(date -Is)"
    DIAG_START_EPOCH="$(date +%s)"
    sudo install -d -o root -g root -m 0700 "$DIAG_DIR"
    sudo install -o root -g root -m 0600 /dev/null "$DIAG_DIR/command.log"
    sudo install -o root -g root -m 0600 /dev/null "$DIAG_DIR/errors.txt"
    sudo install -o root -g root -m 0600 /dev/null "$DIAG_DIR/traffic-test.txt"
    diag_log "START persistent system-wide OpenConnect diagnostic recording"
    diag_snapshot before
    diag_capture_state before
}

diag_config_summary() {
    (( DIAG_ENABLED )) || return 0
    sudo python3 - "$RUN_CONFIG" "$DIAG_DIR/config-summary.json" <<'PY'
import json, pathlib, sys, tomllib
cfg = tomllib.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
oc = cfg.get("openconnect") or {}
summary = {
    "name": cfg.get("name"), "protocol": cfg.get("protocol"), "interface": cfg.get("interface"),
    "mtu": cfg.get("mtu"), "gateway": oc.get("gateway"), "vpn_protocol": oc.get("vpn_protocol"),
    "token_mode": oc.get("token_mode"), "disable_udp": oc.get("disable_udp"),
    "disable_ipv6": oc.get("disable_ipv6"), "reconnect_timeout": oc.get("reconnect_timeout")
}
pathlib.Path(sys.argv[2]).write_text(json.dumps(summary, indent=2, sort_keys=True)+"\n", encoding="utf-8")
PY
}

diag_redact() {
    (( DIAG_ENABLED )) || return 0
    sudo python3 - "$RUN_CONFIG" "$RUN_PASSWORD" "$RUN_TOKEN" "$DIAG_DIR" <<'PY'
import pathlib, re, sys, tomllib
config, password_path, token_path, root_path = map(pathlib.Path, sys.argv[1:])
secrets=[]
try:
    cfg=tomllib.loads(config.read_text(encoding="utf-8")); username=(cfg.get("openconnect") or {}).get("username")
    if isinstance(username,str) and username: secrets.append(username)
except Exception: pass
for p in (password_path, token_path):
    try:
        value=p.read_text(encoding="utf-8").strip()
        if value: secrets.append(value); secrets.append(value.replace(" ",""))
    except Exception: pass
patterns=[(re.compile(r"(?i)(password|passwd|token[_ -]?secret|totp[_ -]?secret)(\s*[:=]\s*)\S+"),r"\1\2<redacted>")]
for path in root_path.rglob("*"):
    if not path.is_file(): continue
    try: text=path.read_text(encoding="utf-8")
    except UnicodeDecodeError: continue
    for secret in sorted(set(secrets), key=len, reverse=True):
        if secret: text=text.replace(secret,"<redacted>")
    for pattern,replacement in patterns: text=pattern.sub(replacement,text)
    path.write_text(text,encoding="utf-8")
PY
}

diag_compact() {
    local aggressive="${1:-0}"
    (( DIAG_ENABLED )) || return 0
    sudo python3 - "$DIAG_DIR" "$aggressive" <<'PY'
import pathlib,sys
root=pathlib.Path(sys.argv[1]); aggressive=sys.argv[2]=="1"
limit=128*1024 if aggressive else 512*1024; keep=48*1024 if aggressive else 192*1024
report=[]
for path in root.rglob("*"):
    if not path.is_file() or path.name in {"summary.txt","size-report.txt"}: continue
    size=path.stat().st_size
    if size<=limit: continue
    data=path.read_bytes(); marker=f"\n\n--- compacted: original_bytes={size} ---\n\n".encode()
    path.write_bytes(data[:keep]+marker+data[-keep:]); report.append(f"{path.relative_to(root)} {size} -> {path.stat().st_size}")
(root/"size-report.txt").write_text("\n".join(report)+("\n" if report else "no files required compaction\n"),encoding="utf-8")
PY
}

diag_package() {
    local result="$1" bytes
    (( DIAG_ENABLED )) || return 0
    sudo -- bash -c '{ echo "=== controller journal ==="; journalctl --no-pager -u "$2" --since "@$3" -n 1500 || true; echo; echo "=== kernel journal ==="; journalctl -k --no-pager --since "@$3" -n 800 || true; } >"$1" 2>&1' bash "$DIAG_DIR/journal.txt" "$UNIT" "$DIAG_START_EPOCH"
    {
        printf 'result=%s\n' "$result"
        printf 'test_scope=system-wide-persistent-controller\n'
        printf 'protocol=openconnect\ninterface=%s\nifindex=%s\n' "$INTERFACE" "${EXPECTED_IFINDEX:-unavailable}"
        printf 'gateway=%s\nendpoint_ip=%s\nunderlay_dev=%s\n' "$GATEWAY" "$ENDPOINT_IP" "$UNDERLAY_DEV"
        printf 'dns_source=%s\npublic_ip=%s\n' "${DNS_SOURCE:-unavailable}" "${PUBLIC_IP:-unavailable}"
        printf 'google_ip=%s\ngoogle_http_code=%s\ngoogle_response_bytes=%s\n' "${GOOGLE_IP:-unavailable}" "${GOOGLE_HTTP_CODE:-unavailable}" "${GOOGLE_BYTES:-unavailable}"
        printf 'internal_host=%s\ninternal_ip=%s\ninternal_http_code=%s\ninternal_response_bytes=%s\n' "$INTERNAL_HOST" "${INTERNAL_IP:-unavailable}" "${INTERNAL_HTTP_CODE:-unavailable}" "${INTERNAL_BYTES:-unavailable}"
        printf 'chatgpt_probe=SKIPPED_EXPECTED_UNAVAILABLE\n'
        printf 'started_at=%s\nended_at=%s\n' "$DIAG_STARTED_AT" "$(date -Is)"
        printf '\n=== traffic ===\n'; sudo cat "$DIAG_DIR/traffic-test.txt" 2>/dev/null || true
        printf '\n=== errors ===\n'; sudo cat "$DIAG_DIR/errors.txt" 2>/dev/null || true
    } | sudo tee "$DIAG_DIR/summary.txt" >/dev/null
    diag_redact
    diag_compact 0
    sudo tar -czf "$DIAG_ARCHIVE" -C "$RUN_DIR" diag
    bytes="$(sudo stat -c '%s' "$DIAG_ARCHIVE")"
    if (( bytes > 4 * 1024 * 1024 )); then
        diag_compact 1
        sudo tar -czf "$DIAG_ARCHIVE" -C "$RUN_DIR" diag
    fi
    sudo chown "$CALLER_UID:$CALLER_GID" "$DIAG_ARCHIVE"
    chmod 0600 "$DIAG_ARCHIVE"
    printf 'Diagnostic archive: %s (%s bytes)\n' "$DIAG_ARCHIVE" "$(stat -c '%s' "$DIAG_ARCHIVE")"
}

remove_endpoint_route() {
    local -a owned_route
    (( ENDPOINT_ROUTE_ADDED )) || return 0
    owned_route=(sudo ip -4 route del "$ENDPOINT_IP/32")
    [[ -n "$UNDERLAY_GATEWAY" ]] && owned_route+=(via "$UNDERLAY_GATEWAY")
    owned_route+=(dev "$UNDERLAY_DEV")
    [[ -n "$UNDERLAY_SRC" ]] && owned_route+=(src "$UNDERLAY_SRC")
    owned_route+=(metric "$ENDPOINT_ROUTE_METRIC")
    "${owned_route[@]}" >/dev/null 2>&1 || true
    ENDPOINT_ROUTE_ADDED=0
}

remove_owned_runtime() {
    local failed=0
    if [[ "$RUN_DIR" == "/run/kikimora-toad-system-wide-openconnect-$CALLER_UID" ]]; then
        sudo rm -rf -- "$RUN_DIR" || failed=1
    else
        failed=1
    fi
    if [[ -n "$RUN_EXEC_DIR" ]]; then
        if is_owned_exec_dir; then sudo rm -rf -- "$RUN_EXEC_DIR" || failed=1; else failed=1; fi
    fi
    return "$failed"
}

rollback_up() {
    set +e
    log "up failed; rolling back owned OpenConnect state"
    if sudo test -d "$RUN_DIR"; then
        diag_error "UP failed; beginning rollback"
        diag_capture_state failed
        diag_snapshot failed
        if (( DEFAULT_ROUTE_ADDED )); then sudo ip -4 route del default dev "$INTERFACE" metric 4 >/dev/null 2>&1 || true; DEFAULT_ROUTE_ADDED=0; fi
        if (( DNS_CONFIGURED )); then sudo resolvectl revert "$INTERFACE" >/dev/null 2>&1 || true; DNS_CONFIGURED=0; fi
        remove_endpoint_route
        if (( TOAD_STARTED )); then sudo systemctl stop "$UNIT" >/dev/null 2>&1 || true; sudo systemctl reset-failed "$UNIT" >/dev/null 2>&1 || true; TOAD_STARTED=0; fi
        diag_snapshot after
        diag_capture_state after
        diag_package FAILED_UP || true
        remove_owned_runtime || true
    fi
    set -e
}

on_exit() {
    local status=$?
    trap - EXIT HUP INT TERM
    if (( status != 0 && UP_IN_PROGRESS )); then rollback_up; fi
    [[ -z "$BUILD_DIR" || ! -d "$BUILD_DIR" ]] || rm -rf -- "$BUILD_DIR"
    exit "$status"
}

cmd_up() {
    local source_bin temp_config temp_password temp_token temp_dns protocol endpoint_route existing_pin line response remote_ip dns ready
    local -a endpoint_add dns_servers
    prepare_up_args "$@"
    oc_profile_require_secret_file "$PROFILE"
    need openconnect
    sudo -v
    unit_active && fail "$UNIT is already active; use status or down"
    sudo test ! -e "$RUN_DIR" || fail "owned runtime state already exists at $RUN_DIR; run down first"
    ip link show dev "$INTERFACE" >/dev/null 2>&1 && fail "$INTERFACE already exists"
    assert_no_other_vpn
    if ip -6 route show default | grep -q .; then fail "an IPv6 default route exists; this IPv4-only test refuses to risk an IPv6 leak"; fi

    BUILD_DIR="$(mktemp -d "${TMPDIR:-/tmp}/toad-openconnect-system-wide.XXXXXX")"
    chmod 0700 "$BUILD_DIR"
    temp_config="$BUILD_DIR/profile.toml"
    temp_password="$BUILD_DIR/password.secret"
    temp_token="$BUILD_DIR/totp.secret"
    temp_dns="$BUILD_DIR/dns-override.txt"
    if [[ -n "${TOAD_BIN:-}" ]]; then
        [[ -x "$TOAD_BIN" ]] || fail "TOAD_BIN is not executable: $TOAD_BIN"
        source_bin="$TOAD_BIN"
    else
        need go
        source_bin="$BUILD_DIR/kikimora-toad"
        log "building checked-out kikimora-toad"
        (cd "$REPO_ROOT/toad" && go build -o "$source_bin" ./cmd/kikimora-toad)
    fi
    chmod 0755 "$source_bin"
    oc_profile_materialize "$PROFILE" "$temp_config" "$RUN_STATE_DIR" "$temp_password" "$temp_token" "$temp_dns" "system-wide-openconnect" "$INTERFACE"
    python3 - "$temp_config" "$RUN_PASSWORD" "$RUN_TOKEN" <<'PY'
import pathlib,re,sys
p=pathlib.Path(sys.argv[1]); text=p.read_text(encoding="utf-8")
q=lambda s:'"'+s.replace('\\','\\\\').replace('"','\\"')+'"'
text=re.sub(r'(?m)^password_file\s*=.*$', 'password_file = '+q(sys.argv[2]), text)
text=re.sub(r'(?m)^token_secret_file\s*=.*$', 'token_secret_file = '+q(sys.argv[3]), text)
p.write_text(text,encoding="utf-8")
PY
    "$source_bin" validate -config "$temp_config"
    protocol="$(python3 - "$temp_config" <<'PY'
import pathlib,sys,tomllib
print(tomllib.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")).get("protocol", ""))
PY
)"
    [[ "$protocol" == "openconnect" ]] || fail "profile materialized as unexpected protocol $protocol"
    while IFS='=' read -r key value; do
        case "$key" in gateway) GATEWAY="$value" ;; gateway_host) GATEWAY_HOST="$value" ;; esac
    done < <(oc_profile_public_fields "$temp_config")
    [[ -n "$GATEWAY_HOST" ]] || fail "could not parse OpenConnect gateway hostname"
    ENDPOINT_IP="$(getent ahostsv4 "$GATEWAY_HOST" | awk 'NR==1 {print $1; exit}' || true)"
    is_ipv4 "$ENDPOINT_IP" || fail "could not resolve OpenConnect gateway $GATEWAY_HOST to IPv4"
    endpoint_route="$(ip -4 route get "$ENDPOINT_IP" 2>/dev/null || true)"
    [[ -n "$endpoint_route" ]] || fail "no pre-VPN route to OpenConnect endpoint $ENDPOINT_IP"
    route_parts "$endpoint_route"
    is_safe_interface "$UNDERLAY_DEV" || fail "unsafe underlay interface name: $UNDERLAY_DEV"
    if is_vpn_interface_name "$UNDERLAY_DEV"; then fail "OpenConnect endpoint currently routes through VPN-like interface $UNDERLAY_DEV"; fi

    sudo install -d -o root -g root -m 0700 "$RUN_DIR" "$RUN_STATE_DIR"
    RUN_EXEC_DIR="$(sudo mktemp -d "/tmp/kikimora-toad-system-wide-openconnect-$CALLER_UID.XXXXXX")"
    is_owned_exec_dir || fail "could not create safe executable directory"
    RUN_BIN="$RUN_EXEC_DIR/kikimora-toad"
    sudo install -o root -g root -m 0755 "$source_bin" "$RUN_BIN"
    sudo install -o root -g root -m 0600 "$temp_config" "$RUN_CONFIG"
    sudo install -o root -g root -m 0600 "$temp_password" "$RUN_PASSWORD"
    sudo install -o root -g root -m 0600 "$temp_token" "$RUN_TOKEN"
    sudo install -o root -g root -m 0600 "$temp_dns" "$RUN_DNS_OVERRIDE"
    write_owner_state
    diag_initialize
    diag_config_summary
    UP_IN_PROGRESS=1

    existing_pin="$(ip -4 route show exact "$ENDPOINT_IP/32" 2>/dev/null || true)"
    if [[ -n "$existing_pin" ]]; then
        log "reusing existing endpoint route: $existing_pin"
    else
        endpoint_add=(sudo ip -4 route add "$ENDPOINT_IP/32")
        [[ -n "$UNDERLAY_GATEWAY" ]] && endpoint_add+=(via "$UNDERLAY_GATEWAY")
        endpoint_add+=(dev "$UNDERLAY_DEV")
        [[ -n "$UNDERLAY_SRC" ]] && endpoint_add+=(src "$UNDERLAY_SRC")
        endpoint_add+=(metric "$ENDPOINT_ROUTE_METRIC")
        "${endpoint_add[@]}"
        ENDPOINT_ROUTE_ADDED=1
        write_owner_state
    fi

    TOAD_STARTED=1
    write_owner_state
    sudo systemctl reset-failed "$UNIT" >/dev/null 2>&1 || true
    sudo systemd-run --unit="$UNIT" --collect --quiet --property=Type=simple --property=Restart=no \
        --property=KillMode=control-group --property=TimeoutStopSec=10s \
        --setenv=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
        "$RUN_BIN" run -config "$RUN_CONFIG"
    for _ in $(seq 1 600); do
        if line="$(ip -o link show dev "$INTERFACE" 2>/dev/null)"; then
            EXPECTED_IFINDEX="${line%%:*}"
            EXPECTED_IFINDEX="${EXPECTED_IFINDEX//[[:space:]]/}"
            break
        fi
        unit_active || fail "kikimora-toad exited before $INTERFACE appeared"
        sleep 0.1
    done
    [[ "$EXPECTED_IFINDEX" =~ ^[1-9][0-9]*$ ]] || fail "timed out waiting for $INTERFACE"
    ready=0
    for _ in $(seq 1 300); do
        if sudo python3 - "$RUN_STATE_DIR/state.json" <<'PY' >/dev/null 2>&1
import json,sys
try: s=json.load(open(sys.argv[1],encoding="utf-8"))
except Exception: raise SystemExit(1)
raise SystemExit(0 if s.get("state")=="online" and s.get("session",{}).get("connected") is True else 1)
PY
        then
            ready=1
            break
        fi
        unit_active || fail "OpenConnect Toad exited before online state"
        sleep 0.2
    done
    (( ready )) || fail "OpenConnect Toad did not reach online state"
    sudo test -s "$RUN_STATE_DIR/openconnect-network.env" || fail "OpenConnect server-pushed network metadata was not published"
    write_owner_state
    diag_capture_state connected
    diag_snapshot connected

    DNS_RAW="$(sudo sed -n '1p' "$RUN_DNS_OVERRIDE")"
    if [[ -n "$DNS_RAW" ]]; then
        DNS_SOURCE="profile-override"
    else
        DNS_RAW="$(sudo awk -F= '$1=="ipv4_dns" {sub(/^[^=]*=/, ""); print; exit}' "$RUN_STATE_DIR/openconnect-network.env")"
        DNS_SOURCE="server-pushed"
    fi
    read -r -a dns_servers <<<"$DNS_RAW"
    (( ${#dns_servers[@]} > 0 )) || fail "OpenConnect supplied no IPv4 DNS; set dns_servers in the local secret profile only if necessary"
    for dns in "${dns_servers[@]}"; do
        is_ipv4 "$dns" || fail "invalid IPv4 DNS from $DNS_SOURCE: $dns"
    done

    DEFAULT_ROUTE_ADDED=1
    write_owner_state
    sudo ip -4 route add default dev "$INTERFACE" metric 4
    ip -4 route get "$ENDPOINT_IP" | grep -Fq "dev $UNDERLAY_DEV" || fail "OpenConnect endpoint recursively entered $INTERFACE"
    need resolvectl
    resolvectl status >/dev/null 2>&1 || fail "systemd-resolved is unavailable"
    DNS_CONFIGURED=1
    write_owner_state
    sudo resolvectl dns "$INTERFACE" "${dns_servers[@]}"
    sudo resolvectl domain "$INTERFACE" '~.'
    sudo resolvectl default-route "$INTERFACE" yes

    GOOGLE_IP="$(getent ahostsv4 "$GOOGLE_HOST" | awk 'NR==1 {print $1; exit}' || true)"
    INTERNAL_IP="$(getent ahostsv4 "$INTERNAL_HOST" | awk 'NR==1 {print $1; exit}' || true)"
    is_ipv4 "$GOOGLE_IP" || fail "could not resolve Google through OpenConnect DNS"
    is_ipv4 "$INTERNAL_IP" || fail "could not resolve $INTERNAL_HOST through OpenConnect DNS"
    ip -4 route get "$GOOGLE_IP" | grep -Fq "dev $INTERFACE" || fail "Google is not routed through $INTERFACE"
    ip -4 route get "$INTERNAL_IP" | grep -Fq "dev $INTERFACE" || fail "$INTERNAL_HOST is not routed through $INTERFACE"

    response="$(env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY curl -4sS --noproxy '*' --interface "$INTERFACE" --connect-timeout 10 --max-time 30 --resolve "$GOOGLE_HOST:443:$GOOGLE_IP" -o /dev/null -w 'http_code=%{http_code} size_download=%{size_download} remote_ip=%{remote_ip}' "https://$GOOGLE_HOST/")" || fail "Google transport failed through OpenConnect"
    GOOGLE_HTTP_CODE="$(sed -n 's/.*http_code=\([^ ]*\).*/\1/p' <<<"$response")"
    GOOGLE_BYTES="$(sed -n 's/.*size_download=\([^ ]*\).*/\1/p' <<<"$response")"
    remote_ip="$(sed -n 's/.*remote_ip=\([^ ]*\).*/\1/p' <<<"$response")"
    if [[ "$GOOGLE_HTTP_CODE" != "200" || "$remote_ip" != "$GOOGLE_IP" || ! "$GOOGLE_BYTES" =~ ^[0-9]+$ ]] || (( GOOGLE_BYTES < 10000 )); then
        fail "Google verification failed: $response"
    fi
    if (( DIAG_ENABLED )); then printf 'google=%s\n' "$response" | sudo tee -a "$DIAG_DIR/traffic-test.txt" >/dev/null; fi

    response="$(env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY curl -4sS --noproxy '*' --interface "$INTERFACE" --connect-timeout 10 --max-time 30 --resolve "$INTERNAL_HOST:443:$INTERNAL_IP" -o /dev/null -w 'http_code=%{http_code} size_download=%{size_download} remote_ip=%{remote_ip}' "https://$INTERNAL_HOST/")" || fail "$INTERNAL_HOST transport/TLS failed through OpenConnect"
    INTERNAL_HTTP_CODE="$(sed -n 's/.*http_code=\([^ ]*\).*/\1/p' <<<"$response")"
    INTERNAL_BYTES="$(sed -n 's/.*size_download=\([^ ]*\).*/\1/p' <<<"$response")"
    remote_ip="$(sed -n 's/.*remote_ip=\([^ ]*\).*/\1/p' <<<"$response")"
    if [[ ! "$INTERNAL_HTTP_CODE" =~ ^(2[0-9][0-9]|3[0-9][0-9]|401|403)$ || "$remote_ip" != "$INTERNAL_IP" ]]; then
        fail "$INTERNAL_HOST verification failed: $response"
    fi
    if (( DIAG_ENABLED )); then printf 'internal_gitlab=%s\nchatgpt=SKIPPED_EXPECTED_UNAVAILABLE\n' "$response" | sudo tee -a "$DIAG_DIR/traffic-test.txt" >/dev/null; fi

    PUBLIC_IP="$(env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY curl -4fsS --noproxy '*' --connect-timeout 5 --max-time 15 https://api.ipify.org 2>/dev/null || true)"
    write_owner_state
    diag_capture_state active
    diag_snapshot active
    diag_log "UP verified: Google HTTP $GOOGLE_HTTP_CODE; internal GitLab HTTP $INTERNAL_HTTP_CODE; ChatGPT intentionally skipped"
    UP_IN_PROGRESS=0
    log "system-wide OpenConnect is UP"
    printf 'interface=%s ifindex=%s\n' "$INTERFACE" "$EXPECTED_IFINDEX"
    printf 'gateway=%s endpoint_ip=%s via=%s\n' "$GATEWAY" "$ENDPOINT_IP" "$UNDERLAY_DEV"
    printf 'dns_source=%s public_ip=%s\n' "$DNS_SOURCE" "${PUBLIC_IP:-unavailable}"
    if (( DIAG_ENABLED )); then printf 'diagnostic_archive_on_down=%s\n' "$DIAG_ARCHIVE"; fi
    printf 'Disable with: %s down\n' "$0"
}

load_down_state() {
    GATEWAY="$(state_get gateway)"
    ENDPOINT_IP="$(state_get endpoint_ip)"
    UNDERLAY_DEV="$(state_get underlay_dev)"
    UNDERLAY_GATEWAY="$(state_get underlay_gateway)"
    UNDERLAY_SRC="$(state_get underlay_src)"
    EXPECTED_IFINDEX="$(state_get ifindex)"
    ENDPOINT_ROUTE_ADDED="$(state_get endpoint_route_added)"
    DEFAULT_ROUTE_ADDED="$(state_get default_route_added)"
    DNS_CONFIGURED="$(state_get dns_configured)"
    TOAD_STARTED="$(state_get toad_started)"
    RUN_EXEC_DIR="$(state_get exec_dir)"
    PUBLIC_IP="$(state_get public_ip)"
    DIAG_ENABLED="$(state_get diag_enabled)"
    DIAG_ARCHIVE="$(state_get diag_archive)"
    DIAG_STARTED_AT="$(state_get diag_started_at)"
    DIAG_START_EPOCH="$(state_get diag_start_epoch)"
    DNS_SOURCE="$(state_get dns_source)"
    GOOGLE_IP="$(state_get google_ip)"
    INTERNAL_IP="$(state_get internal_ip)"
    [[ "$ENDPOINT_ROUTE_ADDED" =~ ^[01]$ ]] || ENDPOINT_ROUTE_ADDED=0
    [[ "$DEFAULT_ROUTE_ADDED" =~ ^[01]$ ]] || DEFAULT_ROUTE_ADDED=0
    [[ "$DNS_CONFIGURED" =~ ^[01]$ ]] || DNS_CONFIGURED=0
    [[ "$TOAD_STARTED" =~ ^[01]$ ]] || TOAD_STARTED=0
    [[ "$DIAG_ENABLED" =~ ^[01]$ ]] || DIAG_ENABLED=0
    if [[ -n "$RUN_EXEC_DIR" ]]; then is_owned_exec_dir || fail "runtime state has unexpected executable directory"; fi
    if (( DIAG_ENABLED )); then [[ "$DIAG_ARCHIVE" == /* && "$DIAG_START_EPOCH" =~ ^[0-9]+$ ]] || fail "invalid diagnostic runtime state"; fi
}

cmd_down() {
    local current_ifindex="" failed=0 diag_result="PASS"
    sudo -v
    if ! sudo test -r "$OWNER_STATE"; then
        if ! unit_active && ! ip link show dev "$INTERFACE" >/dev/null 2>&1 && ! ip -4 route show default dev "$INTERFACE" metric 4 | grep -q .; then
            log "system-wide OpenConnect is already DOWN"
            return 0
        fi
        fail "OpenConnect runtime exists without readable owner state; refusing blind cleanup"
    fi
    BUILD_DIR="$(mktemp -d "${TMPDIR:-/tmp}/toad-openconnect-down.XXXXXX")"
    chmod 0700 "$BUILD_DIR"
    load_down_state
    [[ "$(state_get owner_uid)" == "$CALLER_UID" && "$(state_get interface)" == "$INTERFACE" && "$(state_get unit)" == "$UNIT" ]] || fail "runtime state ownership mismatch"
    diag_capture_state before-down
    diag_snapshot before-down

    if (( DEFAULT_ROUTE_ADDED )); then sudo ip -4 route del default dev "$INTERFACE" metric 4 >/dev/null 2>&1 || failed=1; DEFAULT_ROUTE_ADDED=0; fi
    if (( DNS_CONFIGURED )); then sudo resolvectl revert "$INTERFACE" >/dev/null 2>&1 || failed=1; DNS_CONFIGURED=0; fi
    remove_endpoint_route || failed=1
    if (( TOAD_STARTED )) || unit_active; then sudo systemctl stop "$UNIT" >/dev/null 2>&1 || failed=1; sudo systemctl reset-failed "$UNIT" >/dev/null 2>&1 || true; TOAD_STARTED=0; fi
    for _ in $(seq 1 50); do ip link show dev "$INTERFACE" >/dev/null 2>&1 || break; sleep 0.1; done
    if ip -o link show dev "$INTERFACE" >/dev/null 2>&1; then
        current_ifindex="$(ip -o link show dev "$INTERFACE" | awk -F: '{gsub(/[[:space:]]/, "", $1); print $1}')"
        if [[ "$current_ifindex" == "$EXPECTED_IFINDEX" ]]; then sudo ip link delete dev "$INTERFACE" >/dev/null 2>&1 || failed=1; else failed=1; fi
    fi
    diag_snapshot after
    diag_capture_state after
    if (( failed )); then diag_result="CLEANUP_FAILED"; fi
    if (( DIAG_ENABLED )); then diag_package "$diag_result" || failed=1; fi
    if (( failed == 0 )); then remove_owned_runtime || failed=1; fi
    if (( failed )); then fail "down could not fully remove owned OpenConnect state; retained runtime begins at $RUN_DIR"; fi
    log "system-wide OpenConnect is DOWN"
}

cmd_status() {
    local active=0 iface=0 default_route=0 owned=0 endpoint="" public_ip="" diag=""
    sudo -v
    unit_active && active=1
    ip link show dev "$INTERFACE" >/dev/null 2>&1 && iface=1
    ip -4 route show default dev "$INTERFACE" metric 4 | grep -q . && default_route=1
    if sudo test -r "$OWNER_STATE"; then
        owned=1
        endpoint="$(state_get gateway)"
        public_ip="$(state_get public_ip)"
        diag="$(state_get diag_enabled)"
    fi
    if (( active && iface && default_route && owned )); then
        printf 'System-wide OpenConnect: UP\nunit=%s interface=%s gateway=%s public_ip=%s diag=%s\n' "$UNIT" "$INTERFACE" "${endpoint:-unavailable}" "${public_ip:-unavailable}" "${diag:-0}"
        return 0
    fi
    if (( ! active && ! iface && ! default_route && ! owned )); then
        printf 'System-wide OpenConnect: DOWN\n'
        return 0
    fi
    printf 'System-wide OpenConnect: INCONSISTENT (unit=%s interface=%s default_route=%s owner_state=%s)\n' "$active" "$iface" "$default_route" "$owned" >&2
    printf 'Run %s down to clean owned state.\n' "$0" >&2
    return 2
}

main() {
    local command="${1:-}"
    local tool
    assert_not_root
    [[ -n "$command" ]] || { usage; return 2; }
    shift
    for tool in awk basename chmod cp curl date dirname env flock getent grep install ip mktemp ps python3 rm sed seq sort stat sudo systemctl systemd-run tar tee; do need "$tool"; done
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
