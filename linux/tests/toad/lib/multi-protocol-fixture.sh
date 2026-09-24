#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# multi-protocol-fixture.sh — shared fixture library for multi-Toad tests
#
# Provides reusable namespace/veth topology, reference server setup, TOML
# config generation, and common helpers consumed by:
#   multi-toad-interop.sh
#   go-orchestration-acceptance.sh
#
# Required environment:
#   TOAD_BIN, AWG_REF_BIN, XRAY_REF_BIN, XRAY_COVER_BIN,
#   OPENCONNECT_BIN, OCSERV_BIN
#
# shellcheck source=linux/tests/toad/lib/netns.sh

if [[ -z "${_MULTI_PROTOCOL_FIXTURE_SH:-}" ]]; then
_MULTI_PROTOCOL_FIXTURE_SH=1

# Guard against direct execution.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "ERROR: this library must be sourced, not executed" >&2
    exit 1
fi

# Source netns helpers.
SCRIPT_DIR_FIXTURE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=linux/tests/toad/lib/netns.sh
source "$SCRIPT_DIR_FIXTURE/netns.sh"

# ---------------------------------------------------------------------------
# Validate required binaries
# ---------------------------------------------------------------------------
mpf_require_binaries() {
    local missing=0
    for var in TOAD_BIN AWG_REF_BIN XRAY_REF_BIN XRAY_COVER_BIN; do
        if [[ -z "${!var:-}" ]]; then
            echo "ERROR: $var is not set" >&2
            missing=1
        elif [[ ! -x "${!var}" ]]; then
            echo "ERROR: $var=${!var} is not executable" >&2
            missing=1
        fi
    done
    if [[ -z "${OPENCONNECT_BIN:-}" ]]; then
        OPENCONNECT_BIN="$(command -v openconnect || true)"
    fi
    if [[ -z "${OCSERV_BIN:-}" ]]; then
        OCSERV_BIN="$(command -v ocserv || true)"
    fi
    for var in OPENCONNECT_BIN OCSERV_BIN; do
        if [[ -z "${!var:-}" ]]; then
            echo "ERROR: $var is not set/found" >&2
            missing=1
        elif [[ ! -x "${!var}" ]]; then
            echo "ERROR: $var=${!var} is not executable" >&2
            missing=1
        fi
    done
    if (( missing )); then
        exit 1
    fi
}

# ---------------------------------------------------------------------------
# Namespace topology
# ---------------------------------------------------------------------------
MPF_CLIENT_NS=""
MPF_AWG_SRV_NS=""
MPF_XR_SRV_NS=""
MPF_OC_SRV_NS=""
MPF_TRANSIT_NS=""

MPF_AWG_CLIENT_VETH=""
MPF_AWG_SERVER_VETH=""
MPF_XR_CLIENT_VETH=""
MPF_XR_SERVER_VETH=""
MPF_OC_CLIENT_VETH=""
MPF_OC_SERVER_VETH=""
MPF_AWG_TRANSIT_VETH=""
MPF_XR_TRANSIT_VETH=""
MPF_OC_TRANSIT_VETH=""
MPF_TRANSIT_AWG_VETH=""
MPF_TRANSIT_XR_VETH=""
MPF_TRANSIT_OC_VETH=""

MPF_AWG_CLIENT_ADDR="192.0.2.1/30"
MPF_AWG_SERVER_ADDR="192.0.2.2/30"
MPF_AWG_SERVER_IP="192.0.2.2"
MPF_AWG_PAYLOAD_IP="10.77.0.1"
MPF_XR_CLIENT_ADDR="198.51.100.1/30"
MPF_XR_SERVER_ADDR="198.51.100.2/30"
MPF_XR_SERVER_IP="198.51.100.2"
MPF_OC_CLIENT_ADDR="203.0.113.1/30"
MPF_OC_SERVER_ADDR="203.0.113.2/30"
MPF_OC_SERVER_IP="203.0.113.2"

MPF_AWG_TUN="kk-awg0"
MPF_XR_TUN="kk-xray0"
MPF_OC_TUN="kk-oc0"
MPF_AWG_SERVER_IF=""
MPF_AWG_SOCKET=""

MPF_TMP=""
MPF_AWG_STATE_DIR=""
MPF_XR_STATE_DIR=""
MPF_OC_STATE_DIR=""

MPF_AWG_SRV_PID=""
MPF_XR_SRV_PID=""
MPF_XR_COVER_PID=""
MPF_OC_SRV_PID=""
MPF_XR_PRIVATE_KEY=""

mpf_setup_namespaces() {
    local suffix="$1"
    MPF_CLIENT_NS="mpf-client-${suffix}"
    MPF_AWG_SRV_NS="mpf-awg-srv-${suffix}"
    MPF_XR_SRV_NS="mpf-xr-srv-${suffix}"
    MPF_OC_SRV_NS="mpf-oc-srv-${suffix}"
    MPF_TRANSIT_NS="mpf-transit-${suffix}"

    MPF_AWG_CLIENT_VETH="mac${suffix}"
    MPF_AWG_SERVER_VETH="mas${suffix}"
    MPF_XR_CLIENT_VETH="mxc${suffix}"
    MPF_XR_SERVER_VETH="mxs${suffix}"
    MPF_OC_CLIENT_VETH="moc${suffix}"
    MPF_OC_SERVER_VETH="mos${suffix}"
    MPF_AWG_TRANSIT_VETH="mat${suffix}"
    MPF_XR_TRANSIT_VETH="mxt${suffix}"
    MPF_OC_TRANSIT_VETH="mot${suffix}"
    MPF_TRANSIT_AWG_VETH="mta${suffix}"
    MPF_TRANSIT_XR_VETH="mtx${suffix}"
    MPF_TRANSIT_OC_VETH="mto${suffix}"

    MPF_AWG_SERVER_IF="amg${suffix}"
    MPF_AWG_SOCKET="/var/run/amneziawg/${MPF_AWG_SERVER_IF}.sock"

    MPF_TMP="$(mktemp -d "${TMPDIR:-/tmp}/mpf-${suffix}.XXXXXX")"
    MPF_AWG_STATE_DIR="${MPF_TMP}/awg-state"
    MPF_XR_STATE_DIR="${MPF_TMP}/xray-state"
    MPF_OC_STATE_DIR="${MPF_TMP}/oc-state"

    netns_create_pair "$MPF_CLIENT_NS" "$MPF_AWG_SRV_NS" \
        "$MPF_AWG_CLIENT_VETH" "$MPF_AWG_SERVER_VETH" \
        "$MPF_AWG_CLIENT_ADDR" "$MPF_AWG_SERVER_ADDR"

    ip netns add "$MPF_XR_SRV_NS"
    ip link add "$MPF_XR_CLIENT_VETH" type veth peer name "$MPF_XR_SERVER_VETH"
    ip link set "$MPF_XR_CLIENT_VETH" netns "$MPF_CLIENT_NS"
    ip link set "$MPF_XR_SERVER_VETH" netns "$MPF_XR_SRV_NS"
    ip -n "$MPF_CLIENT_NS" addr add "$MPF_XR_CLIENT_ADDR" dev "$MPF_XR_CLIENT_VETH"
    ip -n "$MPF_XR_SRV_NS" addr add "$MPF_XR_SERVER_ADDR" dev "$MPF_XR_SERVER_VETH"
    ip -n "$MPF_XR_SRV_NS" link set lo up
    ip -n "$MPF_CLIENT_NS" link set "$MPF_XR_CLIENT_VETH" up
    ip -n "$MPF_XR_SRV_NS" link set "$MPF_XR_SERVER_VETH" up

    ip netns add "$MPF_OC_SRV_NS"
    ip link add "$MPF_OC_CLIENT_VETH" type veth peer name "$MPF_OC_SERVER_VETH"
    ip link set "$MPF_OC_CLIENT_VETH" netns "$MPF_CLIENT_NS"
    ip link set "$MPF_OC_SERVER_VETH" netns "$MPF_OC_SRV_NS"
    ip -n "$MPF_CLIENT_NS" addr add "$MPF_OC_CLIENT_ADDR" dev "$MPF_OC_CLIENT_VETH"
    ip -n "$MPF_OC_SRV_NS" addr add "$MPF_OC_SERVER_ADDR" dev "$MPF_OC_SERVER_VETH"
    ip -n "$MPF_OC_SRV_NS" link set lo up
    ip -n "$MPF_CLIENT_NS" link set "$MPF_OC_CLIENT_VETH" up
    ip -n "$MPF_OC_SRV_NS" link set "$MPF_OC_SERVER_VETH" up

    # Routed hermetic path for the OpenConnect endpoint through either
    # synthetic canonical underlay. Production endpoint policy deliberately
    # sends transport endpoints through the selected physical underlay. The
    # protocol server veths above are otherwise isolated, so without this
    # narrow transit the initial OpenConnect process can connect directly but
    # a replacement after endpoint-policy application cannot.
    #
    # OpenConnect is exported through both synthetic gateways. The Xray
    # endpoint is additionally exported through the primary AWG gateway so a
    # non-destructive Xray rebind has a real physical path. The AWG endpoint is
    # deliberately NOT exported through backup Xray: Phase B must still prove
    # that losing the AWG physical path makes the AWG peer unreachable.
    ip netns add "$MPF_TRANSIT_NS"
    ip -n "$MPF_TRANSIT_NS" link set lo up

    ip link add "$MPF_AWG_TRANSIT_VETH" type veth peer name "$MPF_TRANSIT_AWG_VETH"
    ip link set "$MPF_AWG_TRANSIT_VETH" netns "$MPF_AWG_SRV_NS"
    ip link set "$MPF_TRANSIT_AWG_VETH" netns "$MPF_TRANSIT_NS"
    ip -n "$MPF_AWG_SRV_NS" addr add 198.18.0.1/30 dev "$MPF_AWG_TRANSIT_VETH"
    ip -n "$MPF_TRANSIT_NS" addr add 198.18.0.2/30 dev "$MPF_TRANSIT_AWG_VETH"
    ip -n "$MPF_AWG_SRV_NS" link set "$MPF_AWG_TRANSIT_VETH" up
    ip -n "$MPF_TRANSIT_NS" link set "$MPF_TRANSIT_AWG_VETH" up

    ip link add "$MPF_XR_TRANSIT_VETH" type veth peer name "$MPF_TRANSIT_XR_VETH"
    ip link set "$MPF_XR_TRANSIT_VETH" netns "$MPF_XR_SRV_NS"
    ip link set "$MPF_TRANSIT_XR_VETH" netns "$MPF_TRANSIT_NS"
    ip -n "$MPF_XR_SRV_NS" addr add 198.18.0.5/30 dev "$MPF_XR_TRANSIT_VETH"
    ip -n "$MPF_TRANSIT_NS" addr add 198.18.0.6/30 dev "$MPF_TRANSIT_XR_VETH"
    ip -n "$MPF_XR_SRV_NS" link set "$MPF_XR_TRANSIT_VETH" up
    ip -n "$MPF_TRANSIT_NS" link set "$MPF_TRANSIT_XR_VETH" up

    ip link add "$MPF_OC_TRANSIT_VETH" type veth peer name "$MPF_TRANSIT_OC_VETH"
    ip link set "$MPF_OC_TRANSIT_VETH" netns "$MPF_OC_SRV_NS"
    ip link set "$MPF_TRANSIT_OC_VETH" netns "$MPF_TRANSIT_NS"
    ip -n "$MPF_OC_SRV_NS" addr add 198.18.0.9/30 dev "$MPF_OC_TRANSIT_VETH"
    ip -n "$MPF_TRANSIT_NS" addr add 198.18.0.10/30 dev "$MPF_TRANSIT_OC_VETH"
    ip -n "$MPF_OC_SRV_NS" link set "$MPF_OC_TRANSIT_VETH" up
    ip -n "$MPF_TRANSIT_NS" link set "$MPF_TRANSIT_OC_VETH" up

    ip netns exec "$MPF_AWG_SRV_NS" sh -c 'echo 1 > /proc/sys/net/ipv4/ip_forward'
    ip netns exec "$MPF_XR_SRV_NS" sh -c 'echo 1 > /proc/sys/net/ipv4/ip_forward'
    ip netns exec "$MPF_OC_SRV_NS" sh -c 'echo 1 > /proc/sys/net/ipv4/ip_forward'
    ip netns exec "$MPF_TRANSIT_NS" sh -c 'echo 1 > /proc/sys/net/ipv4/ip_forward'

    ip -n "$MPF_AWG_SRV_NS" route add 198.51.100.2/32 via 198.18.0.2 dev "$MPF_AWG_TRANSIT_VETH"
    ip -n "$MPF_AWG_SRV_NS" route add 203.0.113.0/30 via 198.18.0.2 dev "$MPF_AWG_TRANSIT_VETH"
    ip -n "$MPF_XR_SRV_NS" route add 192.0.2.1/32 via 198.18.0.6 dev "$MPF_XR_TRANSIT_VETH"
    ip -n "$MPF_XR_SRV_NS" route add 203.0.113.0/30 via 198.18.0.6 dev "$MPF_XR_TRANSIT_VETH"
    ip -n "$MPF_OC_SRV_NS" route add 192.0.2.1/32 via 198.18.0.10 dev "$MPF_OC_TRANSIT_VETH"
    ip -n "$MPF_OC_SRV_NS" route add 198.51.100.1/32 via 198.18.0.10 dev "$MPF_OC_TRANSIT_VETH"

    ip -n "$MPF_TRANSIT_NS" route add 192.0.2.0/30 via 198.18.0.1 dev "$MPF_TRANSIT_AWG_VETH"
    ip -n "$MPF_TRANSIT_NS" route add 198.51.100.0/30 via 198.18.0.5 dev "$MPF_TRANSIT_XR_VETH"
    ip -n "$MPF_TRANSIT_NS" route add 203.0.113.0/30 via 198.18.0.9 dev "$MPF_TRANSIT_OC_VETH"

    ensure_tun_device "$MPF_CLIENT_NS"
}

mpf_cleanup_namespaces() {
    set +e
    # Stop all reference daemons before deleting namespace handles/temp files.
    # This is deliberately idempotent so EXIT traps can use it after partial setup.
    mpf_stop_oc_server
    mpf_stop_xray_server
    mpf_stop_xray_cover
    mpf_stop_awg_server
    rm -rf -- "${MPF_TMP:-/dev/null}"
    netns_delete_if_present "${MPF_CLIENT_NS:-}"
    netns_delete_if_present "${MPF_AWG_SRV_NS:-}"
    netns_delete_if_present "${MPF_XR_SRV_NS:-}"
    netns_delete_if_present "${MPF_OC_SRV_NS:-}"
    netns_delete_if_present "${MPF_TRANSIT_NS:-}"
}

# ---------------------------------------------------------------------------
# AWG reference server
# ---------------------------------------------------------------------------
mpf_setup_awg_fixture() {
    mkdir -p "$MPF_AWG_STATE_DIR" /var/run/amneziawg
    chmod 0700 "$MPF_AWG_STATE_DIR"

    cat >"${MPF_TMP}/awg.toml" <<MPFEOF
name = "awg"
protocol = "amneziawg2"
interface = "$MPF_AWG_TUN"
address = ["10.77.0.2/24"]
mtu = 1380
state_dir = "$MPF_AWG_STATE_DIR"
leshy_zone = "awg"

[endpoint_policy]
rule_priority = 50

[awg2]
private_key = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
peer_public_key = "zo060cy2M+x7cMF4FKXHbs0CloUFDTRHRboFhw5YfVk="
endpoint = "$MPF_AWG_SERVER_IP:51820"
allowed_ips = ["0.0.0.0/0", "::/0"]
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
MPFEOF
    chmod 0600 "${MPF_TMP}/awg.toml"

    cat >"${MPF_TMP}/awg-server.uapi" <<'MPFEOF'
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
MPFEOF
    chmod 0600 "${MPF_TMP}/awg-server.uapi"
}

mpf_start_awg_server() {
    rm -f -- "$MPF_AWG_SOCKET"
    : >"${MPF_TMP}/awg-server.log"
    ip netns exec "$MPF_AWG_SRV_NS" env LOG_LEVEL=error \
        "$AWG_REF_BIN" -f "$MPF_AWG_SERVER_IF" >"${MPF_TMP}/awg-server.log" 2>&1 &
    MPF_AWG_SRV_PID=$!
    wait_until 10000 test -S "$MPF_AWG_SOCKET" || {
        echo "AWG reference did not create UAPI socket" >&2
        return 1
    }
    wait_until 10000 ip -n "$MPF_AWG_SRV_NS" link show dev "$MPF_AWG_SERVER_IF" >/dev/null 2>&1 || {
        echo "AWG reference interface did not appear" >&2
        return 1
    }
    mpf_awg_uapi_set || {
        echo "AWG UAPI config failed" >&2
        return 1
    }
    ip -n "$MPF_AWG_SRV_NS" addr add "$MPF_AWG_PAYLOAD_IP/24" dev "$MPF_AWG_SERVER_IF"
    ip -n "$MPF_AWG_SRV_NS" link set "$MPF_AWG_SERVER_IF" up
}

mpf_stop_awg_server() {
    if [[ -n "${MPF_AWG_SRV_PID:-}" ]]; then
        kill -TERM "$MPF_AWG_SRV_PID" 2>/dev/null || true
        wait "$MPF_AWG_SRV_PID" 2>/dev/null || true
        MPF_AWG_SRV_PID=""
    fi
    rm -f -- "$MPF_AWG_SOCKET"
}

mpf_awg_uapi_set() {
    python3 - "$MPF_AWG_SOCKET" "${MPF_TMP}/awg-server.uapi" <<'MPFPY'
import socket, sys
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
MPFPY
}

# ---------------------------------------------------------------------------
# Xray reference server
# ---------------------------------------------------------------------------
mpf_setup_xray_fixture() {
    mkdir -p "$MPF_XR_STATE_DIR"
    chmod 0700 "$MPF_XR_STATE_DIR"

    local key_output public_key uuid short_id
    key_output="$("$XRAY_REF_BIN" x25519)"
    MPF_XR_PRIVATE_KEY="$(echo "$key_output" | awk -F': ' '$1 == "PrivateKey" {print $2}')"
    public_key="$(echo "$key_output" | awk -F': ' '$1 == "Password (PublicKey)" {print $2}')"
    uuid="$(python3 -c 'import uuid; print(uuid.uuid4())')"
    short_id="$(python3 -c 'import secrets; print(secrets.token_hex(8))')"

    cat >"${MPF_TMP}/xray-server.json" <<MPFEOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "listen": "$MPF_XR_SERVER_IP",
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
        "privateKey": "$MPF_XR_PRIVATE_KEY",
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
MPFEOF
    chmod 0600 "${MPF_TMP}/xray-server.json"

    cat >"${MPF_TMP}/xray.toml" <<MPFEOF
name = "xray"
protocol = "vless-reality"
interface = "$MPF_XR_TUN"
address = ["10.41.0.2/30"]
mtu = 1380
state_dir = "$MPF_XR_STATE_DIR"
leshy_zone = "xray"

[endpoint_policy]
rule_priority = 51

[vless_reality]
endpoint = "$MPF_XR_SERVER_IP:443"
uuid = "$uuid"
server_name = "cover.test"
public_key = "$public_key"
short_id = "$short_id"
flow = "xtls-rprx-vision"
fingerprint = "chrome"
transport = "raw"
spider_x = "/"
MPFEOF
    chmod 0600 "${MPF_TMP}/xray.toml"
}

mpf_start_xray_server() {
    : >"${MPF_TMP}/xray-server.log"
    ip netns exec "$MPF_XR_SRV_NS" "$XRAY_REF_BIN" run \
        -config "${MPF_TMP}/xray-server.json" >"${MPF_TMP}/xray-server.log" 2>&1 &
    MPF_XR_SRV_PID=$!
    wait_until 10000 mpf_listener_ready "$MPF_XR_SRV_NS" 443 || {
        echo "Xray reference did not listen on 443" >&2
        return 1
    }
}

mpf_stop_xray_server() {
    if [[ -n "${MPF_XR_SRV_PID:-}" ]]; then
        kill -TERM "$MPF_XR_SRV_PID" 2>/dev/null || true
        wait "$MPF_XR_SRV_PID" 2>/dev/null || true
        MPF_XR_SRV_PID=""
    fi
}

mpf_start_xray_cover() {
    : >"${MPF_TMP}/xray-cover.log"
    ip netns exec "$MPF_XR_SRV_NS" "$XRAY_COVER_BIN" \
        -listen 127.0.0.1:8443 -server-name cover.test \
        >"${MPF_TMP}/xray-cover.log" 2>&1 &
    MPF_XR_COVER_PID=$!
    wait_until 5000 mpf_listener_ready "$MPF_XR_SRV_NS" 8443 || {
        echo "Xray REALITY cover did not start" >&2
        return 1
    }
}

mpf_stop_xray_cover() {
    if [[ -n "${MPF_XR_COVER_PID:-}" ]]; then
        kill -TERM "$MPF_XR_COVER_PID" 2>/dev/null || true
        wait "$MPF_XR_COVER_PID" 2>/dev/null || true
        MPF_XR_COVER_PID=""
    fi
}

# ---------------------------------------------------------------------------
# OpenConnect reference server (ocserv)
# ---------------------------------------------------------------------------
mpf_setup_openconnect_fixture() {
    mkdir -p "$MPF_OC_STATE_DIR"
    chmod 0711 "$MPF_TMP"
    chmod 0700 "$MPF_AWG_STATE_DIR" "$MPF_XR_STATE_DIR" "$MPF_OC_STATE_DIR"

    openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
        -subj '/CN=ocserv.test' \
        -keyout "${MPF_TMP}/oc-server.key" \
        -out "${MPF_TMP}/oc-server.crt" >/dev/null 2>&1
    chmod 0600 "${MPF_TMP}/oc-server.key"

    local pin
    pin="pin-sha256:$(openssl x509 -in "${MPF_TMP}/oc-server.crt" -pubkey -noout |
        openssl pkey -pubin -outform DER 2>/dev/null |
        openssl dgst -sha256 -binary |
        openssl base64 -A)"

    printf '%s\n%s\n' 'synthetic-openconnect-password' 'synthetic-openconnect-password' |
        ocpasswd -c "${MPF_TMP}/ocpasswd" toad-test >/dev/null 2>&1
    chmod 0600 "${MPF_TMP}/ocpasswd"
    printf '%s\n' 'synthetic-openconnect-password' >"${MPF_TMP}/oc-password"
    chmod 0600 "${MPF_TMP}/oc-password"

    local ocserv_run_user=nobody ocserv_run_group=nogroup
    if [[ "${KIKIMORA_ROOTLESS_TEST_NS:-0}" == 1 ]]; then
        ocserv_run_user=root
        ocserv_run_group=root
    fi

    cat >"${MPF_TMP}/ocserv.conf" <<MPFEOF
pid-file = ${MPF_TMP}/ocserv.pid
socket-file = ${MPF_TMP}/ocserv.sock
auth = "plain[passwd=${MPF_TMP}/ocpasswd]"
isolate-workers = false
tcp-port = 4443
udp-port = 4443
listen-host = $MPF_OC_SERVER_IP
run-as-user = $ocserv_run_user
run-as-group = $ocserv_run_group
server-cert = ${MPF_TMP}/oc-server.crt
server-key = ${MPF_TMP}/oc-server.key
device = ocserv
ipv4-network = 10.80.0.0/24
route = 10.79.0.1/255.255.255.255
max-clients = 4
max-same-clients = 2
keepalive = 5
dpd = 10
mobile-dpd = 10
try-mtu-discovery = true
MPFEOF
    chmod 0600 "${MPF_TMP}/ocserv.conf"

    cat >"${MPF_TMP}/openconnect.toml" <<MPFEOF
name = "oc"
protocol = "openconnect"
interface = "$MPF_OC_TUN"
mtu = 1380
state_dir = "$MPF_OC_STATE_DIR"
leshy_zone = "oc"

[endpoint_policy]
rule_priority = 52

[openconnect]
gateway = "https://$MPF_OC_SERVER_IP:4443"
vpn_protocol = "anyconnect"
username = "toad-test"
password_file = "${MPF_TMP}/oc-password"
token_mode = "none"
server_cert = "$pin"
disable_udp = false
disable_ipv6 = true
reconnect_timeout = 20
openconnect_binary = "$OPENCONNECT_BIN"
MPFEOF
    chmod 0600 "${MPF_TMP}/openconnect.toml"
}

mpf_start_oc_server() {
    rm -f -- "${MPF_TMP}/ocserv.pid" "${MPF_TMP}/ocserv.sock"
    : >"${MPF_TMP}/ocserv.log"
    ip netns exec "$MPF_OC_SRV_NS" "$OCSERV_BIN" -f -d 1 \
        -c "${MPF_TMP}/ocserv.conf" >>"${MPF_TMP}/ocserv.log" 2>&1 &
    MPF_OC_SRV_PID=$!
    wait_until 8000 mpf_listener_ready "$MPF_OC_SRV_NS" 4443 || {
        echo "ocserv did not listen on 4443" >&2
        return 1
    }
}

mpf_stop_oc_server() {
    if [[ -n "${MPF_OC_SRV_PID:-}" ]]; then
        kill -TERM "$MPF_OC_SRV_PID" 2>/dev/null || true
        wait "$MPF_OC_SRV_PID" 2>/dev/null || true
        MPF_OC_SRV_PID=""
    fi
}

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
mpf_listener_ready() {
    local ns="$1" port="$2"
    ip netns exec "$ns" sh -c "ss -ltn | grep -Eq '[:.]${port}[[:space:]]'"
}

mpf_interface_ready() {
    local iface="$1"
    ip -n "$MPF_CLIENT_NS" link show dev "$iface" >/dev/null 2>&1
}

mpf_ownership_config() {
    cat <<'MPFEOF'
routing_owner = "go"
tunnel_owner = "go"
endpoint_owner = "go"
MPFEOF
}

# ---------------------------------------------------------------------------
# Core JSON snapshot helpers (uses kikimora-core status --json)
# ---------------------------------------------------------------------------
mpf_core_status() {
    local socket="$1"
    "$CORE_BIN" status --socket "$socket" --json 2>/dev/null || echo '{"error":"core unreachable"}'
}

mpf_core_connect_all() {
    local socket="$1"
    "$CORE_BIN" start --socket "$socket" 2>/dev/null || true
}

mpf_core_disconnect_all() {
    local socket="$1"
    "$CORE_BIN" stop --socket "$socket" 2>/dev/null || true
}

mpf_core_connect_role() {
    local socket="$1" role="$2"
    "$CORE_BIN" connect --socket "$socket" --role "$role" 2>/dev/null || true
}

mpf_core_disconnect_role() {
    local socket="$1" role="$2"
    "$CORE_BIN" disconnect --socket "$socket" --role "$role" 2>/dev/null || true
}

# Wait for core socket to exist
mpf_wait_core_socket() {
    local socket="$1" timeout_ms="$2"
    wait_until "$timeout_ms" test -S "$socket"
}

# JSON predicate helper: wait for a python expression over the snapshot to be true.
# Keep the Python program as a single argv instead of embedding it into another
# shell command. This preserves quotes inside f-strings/dict lookups.
mpf_snapshot_matches() {
    local socket="$1" python_expr="$2"
    local program
    program=$'import json,sys\nsnap=json.load(sys.stdin)\n'"$python_expr"
    "$CORE_BIN" status --socket "$socket" --json 2>/dev/null |
        python3 -c "$program"
}

mpf_wait_snapshot() {
    local socket="$1" timeout_ms="$2" python_expr="$3"
    wait_until "$timeout_ms" mpf_snapshot_matches "$socket" "$python_expr"
}

# ---------------------------------------------------------------------------
# Synthetic default-underlay helpers (core-managed orchestration only)
#
# These helpers install isolated default routes in the client namespace for
# core underlay discovery. They are NOT used by standalone multi-toad-interop
# or per-protocol gates, which intentionally have no default route.
#
# The routes point to protocol server gateway IPs inside the isolated netns
# topology — there is no NAT or public Internet access through them.
# ---------------------------------------------------------------------------

# Enable core underlay: install primary (via AWG server, metric 100) and
# backup (via Xray server, metric 200) default routes in the client NS.
mpf_assert_xray_endpoint_primary_underlay() {
    local endpoint_prefix="${MPF_XR_SERVER_IP}/32"

    # Force the Xray transport endpoint through the primary synthetic underlay.
    # The AWG endpoint itself is never exported through backup Xray, so this
    # does not weaken the Phase B fail-closed proof.
    ip -n "$MPF_CLIENT_NS" route replace "$endpoint_prefix" \
        via "$MPF_AWG_SERVER_IP" dev "$MPF_AWG_CLIENT_VETH"
    ip netns exec "$MPF_CLIENT_NS" ping -c 1 -W 1 "$MPF_XR_SERVER_IP" >/dev/null || {
        echo "ERROR: Xray endpoint is unreachable through primary synthetic underlay" >&2
        ip -n "$MPF_CLIENT_NS" route del "$endpoint_prefix" 2>/dev/null || true
        return 1
    }

    ip -n "$MPF_CLIENT_NS" route del "$endpoint_prefix"
    echo "  Xray endpoint routed-underlay fixture: primary PASS"
}

mpf_assert_openconnect_endpoint_underlays() {
    local endpoint_prefix="${MPF_OC_SERVER_IP}/32"

    # Force a host route so the directly connected OC fixture link cannot hide
    # a broken routed-underlay topology.
    ip -n "$MPF_CLIENT_NS" route replace "$endpoint_prefix"         via "$MPF_AWG_SERVER_IP" dev "$MPF_AWG_CLIENT_VETH"
    ip netns exec "$MPF_CLIENT_NS" ping -c 1 -W 1 "$MPF_OC_SERVER_IP" >/dev/null || {
        echo "ERROR: OpenConnect endpoint is unreachable through primary synthetic underlay" >&2
        ip -n "$MPF_CLIENT_NS" route del "$endpoint_prefix" 2>/dev/null || true
        return 1
    }

    ip -n "$MPF_CLIENT_NS" route replace "$endpoint_prefix"         via "$MPF_XR_SERVER_IP" dev "$MPF_XR_CLIENT_VETH"
    ip netns exec "$MPF_CLIENT_NS" ping -c 1 -W 1 "$MPF_OC_SERVER_IP" >/dev/null || {
        echo "ERROR: OpenConnect endpoint is unreachable through backup synthetic underlay" >&2
        ip -n "$MPF_CLIENT_NS" route del "$endpoint_prefix" 2>/dev/null || true
        return 1
    }

    ip -n "$MPF_CLIENT_NS" route del "$endpoint_prefix"
    echo "  OpenConnect endpoint routed-underlay fixture: primary+backup PASS"
}

mpf_enable_core_underlay() {
    echo "  Enabling synthetic core underlay: primary via AWG, backup via Xray"
    ip -n "$MPF_CLIENT_NS" route replace default \
        via "$MPF_AWG_SERVER_IP" dev "$MPF_AWG_CLIENT_VETH" metric 100
    ip -n "$MPF_CLIENT_NS" route replace default \
        via "$MPF_XR_SERVER_IP" dev "$MPF_XR_CLIENT_VETH" metric 200
}

# Switch to backup underlay: remove the primary default route.
# The backup route (Xray, metric 200) remains and becomes the effective default.
mpf_switch_core_underlay_to_backup() {
    echo "  Switching core underlay to backup (Xray via $MPF_XR_SERVER_IP)"
    ip -n "$MPF_CLIENT_NS" route delete default \
        via "$MPF_AWG_SERVER_IP" dev "$MPF_AWG_CLIENT_VETH" metric 100 2>/dev/null || true
}

# Restore primary underlay after backup transition.
mpf_restore_core_underlay_primary() {
    echo "  Restoring primary core underlay (AWG via $MPF_AWG_SERVER_IP)"
    ip -n "$MPF_CLIENT_NS" route replace default \
        via "$MPF_AWG_SERVER_IP" dev "$MPF_AWG_CLIENT_VETH" metric 100
}

# Assert that expected underlay routes are present in the client namespace.
mpf_assert_core_underlay_routes() {
    local primary_expected="${1:-yes}"
    local backup_expected="${2:-yes}"

    if [[ "$primary_expected" == "yes" ]]; then
        if ! ip -n "$MPF_CLIENT_NS" route show default \
            via "$MPF_AWG_SERVER_IP" dev "$MPF_AWG_CLIENT_VETH" metric 100 | grep -q .; then
            echo "ERROR: expected primary underlay route (AWG $MPF_AWG_SERVER_IP) missing" >&2
            return 1
        fi
    else
        if ip -n "$MPF_CLIENT_NS" route show default \
            via "$MPF_AWG_SERVER_IP" dev "$MPF_AWG_CLIENT_VETH" metric 100 | grep -q .; then
            echo "ERROR: primary underlay route should be absent" >&2
            return 1
        fi
    fi

    if [[ "$backup_expected" == "yes" ]]; then
        if ! ip -n "$MPF_CLIENT_NS" route show default \
            via "$MPF_XR_SERVER_IP" dev "$MPF_XR_CLIENT_VETH" metric 200 | grep -q .; then
            echo "ERROR: expected backup underlay route (Xray $MPF_XR_SERVER_IP) missing" >&2
            return 1
        fi
    fi
}

fi # _MULTI_PROTOCOL_FIXTURE_SH