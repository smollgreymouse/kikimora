#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=linux/tests/toad/lib/netns.sh
source "$SCRIPT_DIR/lib/netns.sh"

: "${TOAD_BIN:?TOAD_BIN must point to the checked-out kikimora-toad binary}"
: "${AWG_REF_BIN:?AWG_REF_BIN must point to the pinned official amneziawg-go reference binary}"
: "${XRAY_REF_BIN:?XRAY_REF_BIN must point to the pinned official Xray binary}"
: "${XRAY_COVER_BIN:?XRAY_COVER_BIN must point to the hermetic Xray cover helper}"
OPENCONNECT_BIN="${OPENCONNECT_BIN:-$(command -v openconnect || true)}"
OCSERV_BIN="${OCSERV_BIN:-$(command -v ocserv || true)}"

require_root
require_commands ip ss python3 ping curl openssl ocpasswd openconnect ocserv awk grep sed
ensure_tun_device

for binary in "$TOAD_BIN" "$AWG_REF_BIN" "$XRAY_REF_BIN" "$XRAY_COVER_BIN" "$OPENCONNECT_BIN" "$OCSERV_BIN"; do
    [[ -x "$binary" ]] || {
        echo "ERROR: required test binary is not executable: $binary" >&2
        exit 1
    }
done

CLIENT_NS="toad-multi-client-$$"
AWG_SERVER_NS="toad-multi-awg-server-$$"
XRAY_SERVER_NS="toad-multi-xray-server-$$"
OC_SERVER_NS="toad-multi-oc-server-$$"

AWG_CLIENT_VETH="mac$$"
AWG_SERVER_VETH="mas$$"
XRAY_CLIENT_VETH="mxc$$"
XRAY_SERVER_VETH="mxs$$"
OC_CLIENT_VETH="moc$$"
OC_SERVER_VETH="mos$$"

AWG_TUN="kk-awg0"
XRAY_TUN="kk-xray0"
OC_TUN="kk-oc0"
AWG_SERVER_IF="amg$$"
AWG_SOCKET="/var/run/amneziawg/${AWG_SERVER_IF}.sock"

AWG_CLIENT_ADDR="192.0.2.1/30"
AWG_SERVER_ADDR="192.0.2.2/30"
AWG_SERVER_IP="192.0.2.2"
XRAY_CLIENT_ADDR="198.51.100.1/30"
XRAY_SERVER_ADDR="198.51.100.2/30"
XRAY_SERVER_IP="198.51.100.2"
OC_CLIENT_ADDR="203.0.113.1/30"
OC_SERVER_ADDR="203.0.113.2/30"
OC_SERVER_IP="203.0.113.2"

AWG_PAYLOAD_IP="10.77.0.1"
XRAY_PAYLOAD_IP="10.78.0.1"
XRAY_PAYLOAD_PORT=8080
OC_PAYLOAD_IP="10.79.0.1"
OC_PAYLOAD_PORT=8080

TMP="$(mktemp -d "${TMPDIR:-/tmp}/toad-multi-interop.XXXXXX")"
AWG_STATE_DIR="$TMP/awg-state"
XRAY_STATE_DIR="$TMP/xray-state"
OC_STATE_DIR="$TMP/oc-state"
AWG_CONFIG="$TMP/awg.toml"
XRAY_CONFIG="$TMP/xray.toml"
OC_CONFIG="$TMP/openconnect.toml"
AWG_SERVER_CONFIG="$TMP/awg-server.uapi"
XRAY_SERVER_CONFIG="$TMP/xray-server.json"
OCSERV_CONFIG="$TMP/ocserv.conf"

AWG_TOAD_PID=""
XRAY_TOAD_PID=""
OC_TOAD_PID=""
AWG_SERVER_PID=""
XRAY_SERVER_PID=""
XRAY_COVER_PID=""
XRAY_PAYLOAD_PID=""
OCSERV_PID=""
OC_PAYLOAD_PID=""

AWG_IFINDEX=""
XRAY_IFINDEX=""
OC_IFINDEX=""
XRAY_PRIVATE_KEY=""

stop_process() {
    local pid="$1" signal_name="${2:-TERM}" i
    [[ -n "$pid" ]] || return 0
    if ! kill -0 "$pid" 2>/dev/null; then
        wait "$pid" 2>/dev/null || true
        return 0
    fi
    kill "-$signal_name" "$pid" 2>/dev/null || true
    for ((i = 0; i < 100; i++)); do
        if ! kill -0 "$pid" 2>/dev/null; then
            wait "$pid" 2>/dev/null || true
            return 0
        fi
        sleep 0.05
    done
    kill -KILL "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
}

dump_diagnostics() {
    set +e
    echo "=== simultaneous multi-Toad diagnostics ===" >&2
    for ns in "$CLIENT_NS" "$AWG_SERVER_NS" "$XRAY_SERVER_NS" "$OC_SERVER_NS"; do
        dump_namespace "$ns"
    done
    for entry in         "AWG Toad:$TMP/awg-toad.log"         "AWG server:$TMP/awg-server.log"         "Xray Toad:$TMP/xray-toad.log"         "Xray server:$TMP/xray-server.log"         "Xray cover:$TMP/xray-cover.log"         "Xray payload:$TMP/xray-payload.log"         "OpenConnect Toad:$TMP/oc-toad.log"         "ocserv:$TMP/ocserv.log"         "OpenConnect payload:$TMP/oc-payload.log"; do
        local label="${entry%%:*}" path="${entry#*:}"
        echo "--- $label ---" >&2
        if [[ "$label" == "Xray server" && -n "$XRAY_PRIVATE_KEY" ]]; then
            tail -n 160 "$path" 2>/dev/null | sed "s/${XRAY_PRIVATE_KEY}/[REDACTED]/g" >&2 || true
        else
            tail -n 160 "$path" >&2 2>/dev/null || true
        fi
    done
    if [[ -S "$AWG_SOCKET" ]]; then
        echo "--- AWG reference UAPI (redacted) ---" >&2
        awg_uapi_get 2>/dev/null |
            sed -E 's/^(private_key|preshared_key|public_key)=.*/\1=<redacted>/' >&2 || true
    fi
    echo "===========================================" >&2
}

fail() {
    echo "ERROR: $*" >&2
    dump_diagnostics
    exit 1
}

cleanup() {
    local rc=$?
    trap - EXIT INT TERM
    set +e
    stop_process "$AWG_TOAD_PID" INT
    stop_process "$XRAY_TOAD_PID" INT
    stop_process "$OC_TOAD_PID" INT
    stop_process "$AWG_SERVER_PID" TERM
    stop_process "$XRAY_SERVER_PID" TERM
    stop_process "$XRAY_COVER_PID" TERM
    stop_process "$XRAY_PAYLOAD_PID" TERM
    stop_process "$OCSERV_PID" TERM
    stop_process "$OC_PAYLOAD_PID" TERM
    rm -f -- "$AWG_SOCKET"
    netns_delete_if_present "$CLIENT_NS"
    netns_delete_if_present "$AWG_SERVER_NS"
    netns_delete_if_present "$XRAY_SERVER_NS"
    netns_delete_if_present "$OC_SERVER_NS"
    rm -rf -- "$TMP"
    if (( rc != 0 )); then
        exit "$rc"
    fi
}
trap 'rc=$?; if (( rc != 0 )); then dump_diagnostics; fi; cleanup; exit "$rc"' EXIT
trap 'exit 130' INT TERM

assert_no_split_default_route() {
    local ns="$1"
    assert_no_default_route "$ns"
    if ip -n "$ns" -4 route show | grep -Eq '^(0\.0\.0\.0/1|128\.0\.0\.0/1)([[:space:]]|$)'; then
        fail "$ns unexpectedly has an IPv4 split-default route"
    fi
    if ip -n "$ns" -6 route show | grep -Eiq '^(::/1|8000::/1)([[:space:]]|$)'; then
        fail "$ns unexpectedly has an IPv6 split-default route"
    fi
}

assert_all_isolated() {
    local ns
    for ns in "$CLIENT_NS" "$AWG_SERVER_NS" "$XRAY_SERVER_NS" "$OC_SERVER_NS"; do
        assert_no_split_default_route "$ns"
    done
    assert_no_root_interface "$AWG_TUN"
    assert_no_root_interface "$XRAY_TUN"
    assert_no_root_interface "$OC_TUN"
    assert_no_root_interface "$AWG_SERVER_IF"
}

assert_route_dev() {
    local destination="$1" expected="$2" output
    output="$(ip -n "$CLIENT_NS" -4 route get "$destination")" || fail "no route to $destination"
    echo "route-evidence: $output"
    grep -Eq "(^|[[:space:]])dev[[:space:]]+$expected([[:space:]]|$)" <<<"$output" ||
        fail "route to $destination is not pinned to $expected: $output"
}

state_field() {
    local file="$1" field="$2"
    python3 - "$file" "$field" <<'PY'
import json
import sys
try:
    with open(sys.argv[1], "r", encoding="utf-8") as fh:
        value = json.load(fh)
    for part in sys.argv[2].split("."):
        value = value[part]
except (FileNotFoundError, KeyError, TypeError, json.JSONDecodeError):
    print("")
    raise SystemExit(0)
if isinstance(value, bool):
    print("true" if value else "false")
elif value is None:
    print("")
else:
    print(value)
PY
}

awg_uapi_get() {
    python3 - "$AWG_SOCKET" <<'PY'
import socket
import sys
sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.settimeout(2)
sock.connect(sys.argv[1])
sock.sendall(b"get=1\n\n")
data = bytearray()
while b"\n\n" not in data:
    part = sock.recv(65536)
    if not part:
        break
    data.extend(part)
sock.close()
sys.stdout.write(data.decode("utf-8", "replace"))
PY
}

awg_uapi_set() {
    python3 - "$AWG_SOCKET" "$AWG_SERVER_CONFIG" <<'PY'
import socket
import sys
with open(sys.argv[2], "rb") as fh:
    config = fh.read().rstrip(b"\n")
sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.settimeout(2)
sock.connect(sys.argv[1])
sock.sendall(b"set=1\n" + config + b"\n\n")
data = bytearray()
while b"\n\n" not in data:
    part = sock.recv(65536)
    if not part:
        break
    data.extend(part)
sock.close()
if b"errno=0\n\n" not in data:
    raise SystemExit(data.decode("utf-8", "replace"))
PY
}

listener_ready() {
    local ns="$1" port="$2"
    ip netns exec "$ns" sh -c "ss -ltn | grep -Eq '[:.]${port}[[:space:]]'"
}

listener_gone() {
    local ns="$1" port="$2"
    ! listener_ready "$ns" "$port"
}

setup_namespaces() {
    netns_create_pair "$CLIENT_NS" "$AWG_SERVER_NS" "$AWG_CLIENT_VETH" "$AWG_SERVER_VETH" "$AWG_CLIENT_ADDR" "$AWG_SERVER_ADDR"

    ip netns add "$XRAY_SERVER_NS"
    ip link add "$XRAY_CLIENT_VETH" type veth peer name "$XRAY_SERVER_VETH"
    ip link set "$XRAY_CLIENT_VETH" netns "$CLIENT_NS"
    ip link set "$XRAY_SERVER_VETH" netns "$XRAY_SERVER_NS"
    ip -n "$CLIENT_NS" addr add "$XRAY_CLIENT_ADDR" dev "$XRAY_CLIENT_VETH"
    ip -n "$XRAY_SERVER_NS" addr add "$XRAY_SERVER_ADDR" dev "$XRAY_SERVER_VETH"
    ip -n "$XRAY_SERVER_NS" link set lo up
    ip -n "$CLIENT_NS" link set "$XRAY_CLIENT_VETH" up
    ip -n "$XRAY_SERVER_NS" link set "$XRAY_SERVER_VETH" up

    ip netns add "$OC_SERVER_NS"
    ip link add "$OC_CLIENT_VETH" type veth peer name "$OC_SERVER_VETH"
    ip link set "$OC_CLIENT_VETH" netns "$CLIENT_NS"
    ip link set "$OC_SERVER_VETH" netns "$OC_SERVER_NS"
    ip -n "$CLIENT_NS" addr add "$OC_CLIENT_ADDR" dev "$OC_CLIENT_VETH"
    ip -n "$OC_SERVER_NS" addr add "$OC_SERVER_ADDR" dev "$OC_SERVER_VETH"
    ip -n "$OC_SERVER_NS" link set lo up
    ip -n "$CLIENT_NS" link set "$OC_CLIENT_VETH" up
    ip -n "$OC_SERVER_NS" link set "$OC_SERVER_VETH" up

    assert_all_isolated
}

setup_awg_fixture() {
    mkdir -p "$AWG_STATE_DIR" /var/run/amneziawg
    chmod 0700 "$AWG_STATE_DIR"
    [[ ! -e "$AWG_SOCKET" && ! -L "$AWG_SOCKET" ]] ||
        fail "refusing to replace existing AWG UAPI path $AWG_SOCKET"

    cat >"$AWG_CONFIG" <<EOF
name = "multi-awg"
protocol = "amneziawg2"
interface = "$AWG_TUN"
address = ["10.77.0.2/24"]
mtu = 1380
state_dir = "$AWG_STATE_DIR"

[awg2]
private_key = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
peer_public_key = "zo060cy2M+x7cMF4FKXHbs0CloUFDTRHRboFhw5YfVk="
endpoint = "$AWG_SERVER_IP:51820"
allowed_ips = ["$AWG_PAYLOAD_IP/32"]
persistent_keepalive = 1
jc = 4
jmin = 40
jmax = 80
s1 = 15
s2 = 16
s3 = 17
s4 = 18
h1 = "1001"
h2 = "1002"
h3 = "1003"
h4 = "1004"
i1 = "<r 8><t>"
i2 = "<rd 6>"
i3 = "<rc 6>"
i4 = "<b 0x01020304>"
i5 = "<r 4><rc 4>"
EOF
    chmod 0600 "$AWG_CONFIG"

    cat >"$AWG_SERVER_CONFIG" <<'EOF'
private_key=0202020202020202020202020202020202020202020202020202020202020202
listen_port=51820
replace_peers=true
jc=4
jmin=40
jmax=80
s1=15
s2=16
s3=17
s4=18
h1=1001
h2=1002
h3=1003
h4=1004
i1=<r 8><t>
i2=<rd 6>
i3=<rc 6>
i4=<b 0x01020304>
i5=<r 4><rc 4>
public_key=a4e09292b651c278b9772c569f5fa9bb13d906b46ab68c9df9dc2b4409f8a209
replace_allowed_ips=true
allowed_ip=10.77.0.2/32
persistent_keepalive_interval=1
EOF
    chmod 0600 "$AWG_SERVER_CONFIG"
}

setup_xray_fixture() {
    mkdir -p "$XRAY_STATE_DIR" "$TMP/xray-payload"
    chmod 0700 "$XRAY_STATE_DIR"
    printf '%s\n' 'toad-xray-multi-ok' >"$TMP/xray-payload/probe"
    ip -n "$XRAY_SERVER_NS" addr add "$XRAY_PAYLOAD_IP/32" dev lo

    local key_output public_key uuid short_id
    key_output="$("$XRAY_REF_BIN" x25519)"
    XRAY_PRIVATE_KEY="$(printf '%s\n' "$key_output" | awk -F': ' '$1 == "PrivateKey" {print $2}')"
    public_key="$(printf '%s\n' "$key_output" | awk -F': ' '$1 == "Password (PublicKey)" {print $2}')"
    uuid="$(python3 -c 'import uuid; print(uuid.uuid4())')"
    short_id="$(python3 -c 'import secrets; print(secrets.token_hex(8))')"
    [[ -n "$XRAY_PRIVATE_KEY" && -n "$public_key" && -n "$uuid" && "$short_id" =~ ^[0-9a-f]{16}$ ]] ||
        fail "failed to generate Xray REALITY credentials"

    cat >"$XRAY_SERVER_CONFIG" <<EOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "listen": "$XRAY_SERVER_IP",
    "port": 443,
    "protocol": "vless",
    "settings": {
      "clients": [{"id": "$uuid", "flow": "xtls-rprx-vision"}],
      "decryption": "none"
    },
    "streamSettings": {
      "network": "raw",
      "security": "reality",
      "realitySettings": {
        "show": false,
        "target": "127.0.0.1:8443",
        "xver": 0,
        "serverNames": ["cover.test"],
        "privateKey": "$XRAY_PRIVATE_KEY",
        "shortIds": ["$short_id"]
      }
    }
  }],
  "outbounds": [{
    "tag": "direct",
    "protocol": "freedom",
    "settings": {"finalRules": [{"action": "allow"}]}
  }]
}
EOF
    chmod 0600 "$XRAY_SERVER_CONFIG"

    cat >"$XRAY_CONFIG" <<EOF
name = "multi-xray"
protocol = "vless-reality"
interface = "$XRAY_TUN"
address = ["10.41.0.2/30"]
mtu = 1380
state_dir = "$XRAY_STATE_DIR"

[vless_reality]
endpoint = "$XRAY_SERVER_IP:443"
uuid = "$uuid"
server_name = "cover.test"
public_key = "$public_key"
short_id = "$short_id"
flow = "xtls-rprx-vision"
fingerprint = "chrome"
transport = "raw"
spider_x = "/"
EOF
    chmod 0600 "$XRAY_CONFIG"
}

setup_openconnect_fixture() {
    mkdir -p "$OC_STATE_DIR" "$TMP/oc-payload"
    # ocserv workers run as nobody and need to traverse to the IPC socket.
    chmod 0711 "$TMP"
    chmod 0700 "$AWG_STATE_DIR" "$XRAY_STATE_DIR" "$OC_STATE_DIR"
    printf '%s\n' 'toad-openconnect-multi-ok' >"$TMP/oc-payload/probe"
    ip -n "$OC_SERVER_NS" addr add "$OC_PAYLOAD_IP/32" dev lo

    openssl req -x509 -newkey rsa:2048 -nodes -days 1         -subj '/CN=ocserv.test'         -keyout "$TMP/oc-server.key" -out "$TMP/oc-server.crt" >/dev/null 2>&1
    chmod 0600 "$TMP/oc-server.key"
    local pin
    pin="pin-sha256:$(openssl x509 -in "$TMP/oc-server.crt" -pubkey -noout |
        openssl pkey -pubin -outform DER 2>/dev/null |
        openssl dgst -sha256 -binary |
        openssl base64 -A)"

    printf '%s\n%s\n' 'synthetic-openconnect-password' 'synthetic-openconnect-password' |
        ocpasswd -c "$TMP/ocpasswd" toad-test >/dev/null 2>&1
    chmod 0600 "$TMP/ocpasswd"
    printf '%s\n' 'synthetic-openconnect-password' >"$TMP/oc-password"
    chmod 0600 "$TMP/oc-password"

    cat >"$OCSERV_CONFIG" <<EOF
pid-file = $TMP/ocserv.pid
socket-file = $TMP/ocserv.sock
auth = "plain[passwd=$TMP/ocpasswd]"
isolate-workers = false
tcp-port = 4443
udp-port = 4443
listen-host = $OC_SERVER_IP
run-as-user = nobody
run-as-group = nogroup
server-cert = $TMP/oc-server.crt
server-key = $TMP/oc-server.key
device = ocserv
ipv4-network = 10.80.0.0/24
route = $OC_PAYLOAD_IP/255.255.255.255
max-clients = 4
max-same-clients = 2
keepalive = 5
dpd = 10
mobile-dpd = 10
try-mtu-discovery = true
EOF
    chmod 0600 "$OCSERV_CONFIG"

    cat >"$OC_CONFIG" <<EOF
name = "multi-openconnect"
protocol = "openconnect"
interface = "$OC_TUN"
mtu = 1380
state_dir = "$OC_STATE_DIR"

[openconnect]
gateway = "https://$OC_SERVER_IP:4443"
vpn_protocol = "anyconnect"
username = "toad-test"
password_file = "$TMP/oc-password"
token_mode = "none"
server_cert = "$pin"
disable_udp = false
disable_ipv6 = true
reconnect_timeout = 20
openconnect_binary = "$OPENCONNECT_BIN"
EOF
    chmod 0600 "$OC_CONFIG"

    [[ "$AWG_STATE_DIR" != "$XRAY_STATE_DIR" &&
       "$AWG_STATE_DIR" != "$OC_STATE_DIR" &&
       "$XRAY_STATE_DIR" != "$OC_STATE_DIR" ]] ||
        fail "Toad state directories are not distinct"
}

wait_awg_server_ready() {
    wait_until 10000 test -S "$AWG_SOCKET" || return 1
    wait_until 10000 ip -n "$AWG_SERVER_NS" link show dev "$AWG_SERVER_IF" >/dev/null 2>&1
}

start_awg_server() {
    rm -f -- "$AWG_SOCKET"
    : >"$TMP/awg-server.log"
    ip netns exec "$AWG_SERVER_NS" env LOG_LEVEL=error "$AWG_REF_BIN" -f "$AWG_SERVER_IF" >"$TMP/awg-server.log" 2>&1 &
    AWG_SERVER_PID=$!
    wait_awg_server_ready || fail "official AWG reference did not create UAPI/interface"
    assert_process_alive "$AWG_SERVER_PID" "AWG reference"
    awg_uapi_set || fail "failed to configure AWG reference"
    ip -n "$AWG_SERVER_NS" addr add "$AWG_PAYLOAD_IP/24" dev "$AWG_SERVER_IF"
    ip -n "$AWG_SERVER_NS" link set "$AWG_SERVER_IF" up
}

stop_awg_server() {
    stop_process "$AWG_SERVER_PID" TERM
    AWG_SERVER_PID=""
    rm -f -- "$AWG_SOCKET"
    wait_until 5000 bash -c "! ip -n '$AWG_SERVER_NS' link show dev '$AWG_SERVER_IF' >/dev/null 2>&1" ||
        fail "AWG reference interface survived server stop"
}

start_xray_server() {
    : >"$TMP/xray-server.log"
    ip netns exec "$XRAY_SERVER_NS" "$XRAY_REF_BIN" run -config "$XRAY_SERVER_CONFIG" >"$TMP/xray-server.log" 2>&1 &
    XRAY_SERVER_PID=$!
    wait_until 10000 listener_ready "$XRAY_SERVER_NS" 443 ||
        fail "reference Xray server did not listen on 443"
    assert_process_alive "$XRAY_SERVER_PID" "Xray reference"
}

stop_xray_server() {
    stop_process "$XRAY_SERVER_PID" TERM
    XRAY_SERVER_PID=""
    wait_until 5000 listener_gone "$XRAY_SERVER_NS" 443 ||
        fail "Xray reference listener survived server stop"
}

start_oc_server() {
    rm -f -- "$TMP/ocserv.pid" "$TMP/ocserv.sock"
    : >"$TMP/ocserv.log"
    ip netns exec "$OC_SERVER_NS" "$OCSERV_BIN" -f -d 1 -c "$OCSERV_CONFIG" >"$TMP/ocserv.log" 2>&1 &
    OCSERV_PID=$!
    wait_until 8000 listener_ready "$OC_SERVER_NS" 4443 ||
        fail "ocserv did not listen on 4443"
    assert_process_alive "$OCSERV_PID" ocserv
}

stop_oc_server() {
    stop_process "$OCSERV_PID" TERM
    OCSERV_PID=""
    wait_until 5000 listener_gone "$OC_SERVER_NS" 4443 ||
        fail "ocserv listener survived server stop"
    rm -f -- "$TMP/ocserv.pid" "$TMP/ocserv.sock"
}

start_all_servers() {
    start_awg_server

    ip netns exec "$XRAY_SERVER_NS" "$XRAY_COVER_BIN" -listen 127.0.0.1:8443 -server-name cover.test >"$TMP/xray-cover.log" 2>&1 &
    XRAY_COVER_PID=$!
    wait_until 5000 listener_ready "$XRAY_SERVER_NS" 8443 || fail "Xray REALITY cover did not start"
    assert_process_alive "$XRAY_COVER_PID" "Xray cover"

    ip netns exec "$XRAY_SERVER_NS" python3 -u -m http.server "$XRAY_PAYLOAD_PORT"         --bind "$XRAY_PAYLOAD_IP" --directory "$TMP/xray-payload" >"$TMP/xray-payload.log" 2>&1 &
    XRAY_PAYLOAD_PID=$!
    wait_until 5000 listener_ready "$XRAY_SERVER_NS" "$XRAY_PAYLOAD_PORT" ||
        fail "Xray private payload did not start"

    start_xray_server

    ip netns exec "$OC_SERVER_NS" python3 -u -m http.server "$OC_PAYLOAD_PORT"         --bind "$OC_PAYLOAD_IP" --directory "$TMP/oc-payload" >"$TMP/oc-payload.log" 2>&1 &
    OC_PAYLOAD_PID=$!
    wait_until 5000 listener_ready "$OC_SERVER_NS" "$OC_PAYLOAD_PORT" ||
        fail "OpenConnect private payload did not start"

    start_oc_server
}

start_all_toads() {
    ip netns exec "$CLIENT_NS" "$TOAD_BIN" run -config "$AWG_CONFIG" >"$TMP/awg-toad.log" 2>&1 &
    AWG_TOAD_PID=$!
    ip netns exec "$CLIENT_NS" "$TOAD_BIN" run -config "$XRAY_CONFIG" >"$TMP/xray-toad.log" 2>&1 &
    XRAY_TOAD_PID=$!
    ip netns exec "$CLIENT_NS" "$TOAD_BIN" run -config "$OC_CONFIG" >"$TMP/oc-toad.log" 2>&1 &
    OC_TOAD_PID=$!
}

interface_ready() {
    local iface="$1"
    ip -n "$CLIENT_NS" link show dev "$iface" >/dev/null 2>&1
}

awg_online() {
    [[ "$(state_field "$AWG_STATE_DIR/state.json" state)" == "online" &&
       "$(state_field "$AWG_STATE_DIR/state.json" session.connected)" == "true" ]]
}

oc_online() {
    [[ "$(state_field "$OC_STATE_DIR/state.json" state)" == "online" ]]
}

wait_all_ready() {
    wait_until 12000 interface_ready "$AWG_TUN" || fail "$AWG_TUN did not appear"
    wait_until 12000 interface_ready "$XRAY_TUN" || fail "$XRAY_TUN did not appear"
    wait_until 20000 interface_ready "$OC_TUN" || fail "$OC_TUN did not appear"

    assert_process_alive "$AWG_TOAD_PID" "AWG Toad"
    assert_process_alive "$XRAY_TOAD_PID" "Xray Toad"
    assert_process_alive "$OC_TOAD_PID" "OpenConnect Toad"

    wait_until 12000 awg_online || fail "AWG Toad did not establish a real handshake"
    wait_until 8000 oc_online || fail "OpenConnect Toad did not reach data phase"

    # Xray readiness is proven by its real payload below. Do not use the
    # currently-deferred health/counter semantics as a Stage-0 prerequisite.
}

record_initial_identity() {
    AWG_IFINDEX="$(interface_ifindex "$CLIENT_NS" "$AWG_TUN")" || fail "cannot read AWG ifindex"
    XRAY_IFINDEX="$(interface_ifindex "$CLIENT_NS" "$XRAY_TUN")" || fail "cannot read Xray ifindex"
    OC_IFINDEX="$(interface_ifindex "$CLIENT_NS" "$OC_TUN")" || fail "cannot read OpenConnect ifindex"
    [[ "$AWG_IFINDEX" != "$XRAY_IFINDEX" &&
       "$AWG_IFINDEX" != "$OC_IFINDEX" &&
       "$XRAY_IFINDEX" != "$OC_IFINDEX" ]] ||
        fail "managed TUN ifindices are not distinct"

    echo "multi-toad identity: awg(pid=$AWG_TOAD_PID ifindex=$AWG_IFINDEX) xray(pid=$XRAY_TOAD_PID ifindex=$XRAY_IFINDEX) openconnect(pid=$OC_TOAD_PID ifindex=$OC_IFINDEX)"
}

awg_probe() {
    ip netns exec "$CLIENT_NS" ping -I "$AWG_TUN" -c 1 -W 1 "$AWG_PAYLOAD_IP" >/dev/null 2>&1
}

xray_probe() {
    ip netns exec "$CLIENT_NS" python3 - "$XRAY_PAYLOAD_IP" "$XRAY_PAYLOAD_PORT" <<'PY' >/dev/null 2>&1
import socket
import sys
host, port = sys.argv[1], int(sys.argv[2])
try:
    with socket.create_connection((host, port), timeout=1.0) as sock:
        sock.settimeout(1.0)
        sock.sendall(b"GET /probe HTTP/1.0\r\nHost: payload.test\r\nConnection: close\r\n\r\n")
        data = bytearray()
        while len(data) < 65536:
            chunk = sock.recv(4096)
            if not chunk:
                break
            data.extend(chunk)
            if b"200 OK" in data and b"toad-xray-multi-ok" in data:
                raise SystemExit(0)
except OSError:
    pass
raise SystemExit(1)
PY
}

oc_probe() {
    ip netns exec "$CLIENT_NS" curl -4fsS --interface "$OC_TUN"         --connect-timeout 2 --max-time 5 "http://$OC_PAYLOAD_IP:$OC_PAYLOAD_PORT/probe" |
        grep -q 'toad-openconnect-multi-ok'
}

wait_probe() {
    local probe="$1" attempts="${2:-80}" i
    for ((i = 0; i < attempts; i++)); do
        if "$probe"; then
            return 0
        fi
        sleep 0.25
    done
    return 1
}

assert_other_two_unchanged() {
    local phase="$1"
    assert_process_alive "$XRAY_TOAD_PID" "Xray Toad during $phase"
    assert_process_alive "$OC_TOAD_PID" "OpenConnect Toad during $phase"
    assert_ifindex "$CLIENT_NS" "$XRAY_TUN" "$XRAY_IFINDEX" "$phase"
    assert_ifindex "$CLIENT_NS" "$OC_TUN" "$OC_IFINDEX" "$phase"
}

prove_all_payloads() {
    # The route-free protocol clients must not install application policy for
    # Xray/OpenConnect. Add only the explicit selected routes for this gate.
    if ip -n "$CLIENT_NS" route show "$XRAY_PAYLOAD_IP/32" | grep -q .; then
        fail "Xray backend unexpectedly installed the test payload route"
    fi
    if ip -n "$CLIENT_NS" route show "$OC_PAYLOAD_IP/32" | grep -q .; then
        fail "OpenConnect backend unexpectedly installed the test payload route"
    fi
    ip -n "$CLIENT_NS" route add "$XRAY_PAYLOAD_IP/32" dev "$XRAY_TUN"
    ip -n "$CLIENT_NS" route add "$OC_PAYLOAD_IP/32" dev "$OC_TUN"

    wait_probe awg_probe 80 || fail "AWG encrypted payload failed"
    wait_probe xray_probe 80 || fail "Xray REALITY/VLESS/Vision payload failed"
    wait_probe oc_probe 80 || fail "OpenConnect/ocserv payload failed"

    assert_route_dev "$AWG_PAYLOAD_IP" "$AWG_TUN"
    assert_route_dev "$XRAY_PAYLOAD_IP" "$XRAY_TUN"
    assert_route_dev "$OC_PAYLOAD_IP" "$OC_TUN"
    assert_all_isolated
    echo "multi-toad payloads: AWG=ok Xray=ok OpenConnect=ok"
}

phase_awg_failure() {
    echo "==> multi-Toad phase: AWG server failure isolation"
    stop_awg_server
    assert_process_alive "$AWG_TOAD_PID" "AWG Toad after reference stop"
    assert_ifindex "$CLIENT_NS" "$AWG_TUN" "$AWG_IFINDEX" "AWG server outage"
    assert_route_dev "$AWG_PAYLOAD_IP" "$AWG_TUN"
    if awg_probe; then
        fail "AWG payload unexpectedly succeeded while AWG reference was stopped"
    fi
    assert_other_two_unchanged "AWG server outage"
    xray_probe || fail "Xray payload was disturbed by AWG server outage"
    oc_probe || fail "OpenConnect payload was disturbed by AWG server outage"

    start_awg_server
    wait_probe awg_probe 100 || fail "AWG payload did not recover after server restart"
    assert_ifindex "$CLIENT_NS" "$AWG_TUN" "$AWG_IFINDEX" "AWG server recovery"
}

phase_xray_failure() {
    echo "==> multi-Toad phase: Xray server failure isolation"
    stop_xray_server
    assert_process_alive "$XRAY_TOAD_PID" "Xray Toad after reference stop"
    assert_ifindex "$CLIENT_NS" "$XRAY_TUN" "$XRAY_IFINDEX" "Xray server outage"
    assert_route_dev "$XRAY_PAYLOAD_IP" "$XRAY_TUN"
    if xray_probe; then
        fail "Xray payload unexpectedly succeeded while Xray reference was stopped"
    fi
    assert_process_alive "$AWG_TOAD_PID" "AWG Toad during Xray outage"
    assert_ifindex "$CLIENT_NS" "$AWG_TUN" "$AWG_IFINDEX" "Xray server outage"
    assert_process_alive "$OC_TOAD_PID" "OpenConnect Toad during Xray outage"
    assert_ifindex "$CLIENT_NS" "$OC_TUN" "$OC_IFINDEX" "Xray server outage"
    awg_probe || fail "AWG payload was disturbed by Xray server outage"
    oc_probe || fail "OpenConnect payload was disturbed by Xray server outage"

    start_xray_server
    wait_probe xray_probe 100 || fail "Xray payload did not recover after server restart"
    assert_ifindex "$CLIENT_NS" "$XRAY_TUN" "$XRAY_IFINDEX" "Xray server recovery"
}

phase_openconnect_failure() {
    echo "==> multi-Toad phase: OpenConnect server failure isolation"
    stop_oc_server
    assert_process_alive "$OC_TOAD_PID" "OpenConnect Toad after ocserv stop"
    assert_ifindex "$CLIENT_NS" "$OC_TUN" "$OC_IFINDEX" "ocserv outage"
    assert_route_dev "$OC_PAYLOAD_IP" "$OC_TUN"
    if oc_probe; then
        fail "OpenConnect payload unexpectedly succeeded while ocserv was stopped"
    fi
    assert_process_alive "$AWG_TOAD_PID" "AWG Toad during ocserv outage"
    assert_process_alive "$XRAY_TOAD_PID" "Xray Toad during ocserv outage"
    assert_ifindex "$CLIENT_NS" "$AWG_TUN" "$AWG_IFINDEX" "ocserv outage"
    assert_ifindex "$CLIENT_NS" "$XRAY_TUN" "$XRAY_IFINDEX" "ocserv outage"
    awg_probe || fail "AWG payload was disturbed by ocserv outage"
    xray_probe || fail "Xray payload was disturbed by ocserv outage"

    start_oc_server
    wait_probe oc_probe 120 || fail "OpenConnect payload did not recover after ocserv restart"
    assert_process_alive "$OC_TOAD_PID" "OpenConnect Toad after ocserv restart"
    assert_ifindex "$CLIENT_NS" "$OC_TUN" "$OC_IFINDEX" "ocserv restart recovery"
}

phase_underlay_isolation() {
    echo "==> multi-Toad phase: one physical underlay failure"
    ip -n "$CLIENT_NS" link set "$AWG_CLIENT_VETH" down
    assert_process_alive "$AWG_TOAD_PID" "AWG Toad during AWG underlay outage"
    assert_ifindex "$CLIENT_NS" "$AWG_TUN" "$AWG_IFINDEX" "AWG underlay outage"
    assert_route_dev "$AWG_PAYLOAD_IP" "$AWG_TUN"
    if awg_probe; then
        fail "AWG payload unexpectedly succeeded while only AWG underlay was down"
    fi

    assert_other_two_unchanged "AWG underlay outage"
    xray_probe || fail "Xray payload was disturbed by AWG underlay outage"
    oc_probe || fail "OpenConnect payload was disturbed by AWG underlay outage"

    ip -n "$CLIENT_NS" link set "$AWG_CLIENT_VETH" up
    wait_probe awg_probe 100 || fail "AWG payload did not recover after its private underlay returned"
    assert_ifindex "$CLIENT_NS" "$AWG_TUN" "$AWG_IFINDEX" "AWG underlay recovery"
}

phase_deliberate_toad_stop() {
    echo "==> multi-Toad phase: deliberate one-Toad shutdown"
    stop_process "$AWG_TOAD_PID" INT
    AWG_TOAD_PID=""
    wait_until 5000 bash -c "! ip -n '$CLIENT_NS' link show dev '$AWG_TUN' >/dev/null 2>&1" ||
        fail "$AWG_TUN survived deliberate AWG Toad shutdown"

    assert_process_alive "$XRAY_TOAD_PID" "Xray Toad after AWG Toad shutdown"
    assert_process_alive "$OC_TOAD_PID" "OpenConnect Toad after AWG Toad shutdown"
    assert_ifindex "$CLIENT_NS" "$XRAY_TUN" "$XRAY_IFINDEX" "AWG Toad shutdown"
    assert_ifindex "$CLIENT_NS" "$OC_TUN" "$OC_IFINDEX" "AWG Toad shutdown"
    xray_probe || fail "Xray payload was disturbed by deliberate AWG Toad shutdown"
    oc_probe || fail "OpenConnect payload was disturbed by deliberate AWG Toad shutdown"
}

setup_namespaces
setup_awg_fixture
setup_xray_fixture
setup_openconnect_fixture
start_all_servers
start_all_toads
wait_all_ready
record_initial_identity
prove_all_payloads
phase_awg_failure
phase_xray_failure
phase_openconnect_failure
phase_underlay_isolation
phase_deliberate_toad_stop

assert_no_root_interface "$AWG_TUN"
assert_no_root_interface "$XRAY_TUN"
assert_no_root_interface "$OC_TUN"
assert_all_isolated

echo "Toad simultaneous multi-protocol interop passed: awg_ifindex=$AWG_IFINDEX xray_ifindex=$XRAY_IFINDEX openconnect_ifindex=$OC_IFINDEX"
