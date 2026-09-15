#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

OC_IFACE="kk-oc0"
OC_GOOGLE_HOST="www.google.com"
OC_INTERNAL_HOST="gitlab.sca.ad-tech.ru"
OC_INTERNAL_URL="https://gitlab.sca.ad-tech.ru/"
OC_GOOGLE_URL="https://www.google.com/"
OC_HOLD_SECONDS="${TOAD_SYSTEM_WIDE_HOLD_SECONDS:-120}"
OC_TARGET_ARCHIVE_BYTES="${TOAD_DIAG_TARGET_BYTES:-4194304}"

oc_log() { printf '[%s] %s\n' "$(date -Is)" "$*" | tee -a "$OC_DIAG/command.log"; }
oc_fail() { printf '[%s] ERROR: %s\n' "$(date -Is)" "$*" | tee -a "$OC_DIAG/errors.txt" >&2; return 1; }
oc_warn() { printf '[%s] WARNING: %s\n' "$(date -Is)" "$*" | tee -a "$OC_DIAG/warnings.txt" >&2; }
oc_need() { command -v "$1" >/dev/null 2>&1 || oc_fail "required command not found: $1"; }

oc_snapshot() {
    local phase="$1" dir="$OC_DIAG/$1"
    mkdir -p "$dir"
    {
        date -Is; uname -a
        printf '\n=== links ===\n'; ip -details -statistics link show || true
        printf '\n=== addresses ===\n'; ip -details -statistics addr show || true
    } >"$dir/network.txt" 2>&1
    {
        printf '=== IPv4 routes ===\n'; ip -4 route show table all || true
        printf '\n=== IPv6 routes ===\n'; ip -6 route show table all || true
        printf '\n=== rules ===\n'; ip rule show || true
        [[ -n "${OC_ENDPOINT_IP:-}" ]] && { printf '\n=== endpoint route ===\n'; ip -4 route get "$OC_ENDPOINT_IP" || true; }
        [[ -n "${OC_GOOGLE_IP:-}" ]] && { printf '\n=== Google route ===\n'; ip -4 route get "$OC_GOOGLE_IP" || true; }
        [[ -n "${OC_INTERNAL_IP:-}" ]] && { printf '\n=== internal GitLab route ===\n'; ip -4 route get "$OC_INTERNAL_IP" || true; }
    } >"$dir/routes.txt" 2>&1
    {
        printf '=== resolv.conf ===\n'; ls -l /etc/resolv.conf || true; sed -n '1,160p' /etc/resolv.conf || true
        if command -v resolvectl >/dev/null 2>&1; then
            printf '\n=== resolvectl ===\n'; resolvectl status || true
        fi
    } >"$dir/dns.txt" 2>&1
    {
        ss -tunap || true
        printf '\n=== processes (argv intentionally omitted) ===\n'
        ps -eo pid,ppid,user,group,stat,etimes,comm || true
    } >"$dir/runtime.txt" 2>&1
}

oc_kernel_logs() {
    journalctl -k --since "${OC_STARTED_AT:-10 minutes ago}" --no-pager 2>/dev/null | tail -n 2000 >"$OC_DIAG/kernel-journal.txt" || true
    dmesg --ctime 2>/dev/null | tail -n 1000 >"$OC_DIAG/dmesg-tail.txt" || true
}

oc_prepare() {
    local profile="$1"
    umask 077
    for c in awk bash cat chmod cp curl date getent grep ip journalctl mktemp ps python3 sed ss sudo tar tcpdump tail; do oc_need "$c"; done
    oc_need openconnect
    [[ -f "$profile" ]] || oc_fail "OpenConnect profile not found: $profile"
    [[ "$(stat -c '%a' "$profile" 2>/dev/null || true)" == "600" ]] || oc_fail "profile must have mode 0600: $profile"

    OC_STARTED_AT="$(date -Is)"
    OC_WORK="$(mktemp -d "${TMPDIR:-/tmp}/toad-openconnect-real.XXXXXX")"
    OC_DIAG="$OC_WORK/diag"
    OC_PRIVATE="$OC_WORK/private"
    OC_STATE="$OC_PRIVATE/state"
    OC_CONFIG="$OC_PRIVATE/profile.toml"
    mkdir -p "$OC_DIAG" "$OC_PRIVATE" "$OC_STATE"
    : >"$OC_DIAG/command.log"; : >"$OC_DIAG/errors.txt"; : >"$OC_DIAG/warnings.txt"; : >"$OC_DIAG/toad.log"; : >"$OC_DIAG/traffic-test.txt"

    cp "$profile" "$OC_CONFIG"
    python3 - "$OC_CONFIG" "$OC_STATE" "$(command -v openconnect)" <<'PY'
import pathlib,re,sys
p=pathlib.Path(sys.argv[1]); text=p.read_text()
text=re.sub(r'(?m)^state_dir\s*=.*$', 'state_dir = "'+sys.argv[2].replace('\\','\\\\').replace('"','\\"')+'"', text)
text=re.sub(r'(?m)^interface\s*=.*$', 'interface = "kk-oc0"', text, count=1)
text=re.sub(r'(?m)^openconnect_binary\s*=.*$', 'openconnect_binary = "'+sys.argv[3].replace('\\','\\\\').replace('"','\\"')+'"', text)
p.write_text(text)
PY
    chmod 0600 "$OC_CONFIG"

    local repo_root
    repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../../.." && pwd)"
    if [[ -n "${TOAD_BIN:-}" ]]; then
        OC_TOAD="$TOAD_BIN"
    else
        oc_need go
        OC_TOAD="$OC_PRIVATE/kikimora-toad"
        (cd "$repo_root/toad" && go build -o "$OC_TOAD" ./cmd/kikimora-toad) >>"$OC_DIAG/command.log" 2>&1
    fi
    "$OC_TOAD" validate -config "$OC_CONFIG" >>"$OC_DIAG/command.log" 2>&1

    python3 - "$OC_CONFIG" "$OC_DIAG/config-summary.json" <<'PY'
import json,pathlib,sys,tomllib,urllib.parse
cfg=tomllib.loads(pathlib.Path(sys.argv[1]).read_text())
oc=cfg.get('openconnect') or {}
u=urllib.parse.urlparse(oc.get('gateway',''))
summary={
 'name':cfg.get('name'),'protocol':cfg.get('protocol'),'interface':cfg.get('interface'),'mtu':cfg.get('mtu'),
 'gateway':oc.get('gateway'),'gateway_host':u.hostname,'gateway_port':u.port or 443,
 'vpn_protocol':oc.get('vpn_protocol'),'username':oc.get('username'),'token_mode':oc.get('token_mode'),
 'disable_udp':oc.get('disable_udp'),'disable_ipv6':oc.get('disable_ipv6'),'reconnect_timeout':oc.get('reconnect_timeout')}
pathlib.Path(sys.argv[2]).write_text(json.dumps(summary,indent=2,sort_keys=True)+'\n')
PY
    OC_ENDPOINT_HOST="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["gateway_host"] or "")' "$OC_DIAG/config-summary.json")"
    [[ -n "$OC_ENDPOINT_HOST" ]] || oc_fail "gateway host missing in profile"
    OC_ENDPOINT_IP="$(getent ahostsv4 "$OC_ENDPOINT_HOST" | awk 'NR==1{print $1;exit}')"
    [[ "$OC_ENDPOINT_IP" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || oc_fail "cannot resolve OpenConnect gateway $OC_ENDPOINT_HOST"

    local route
    route="$(ip -4 route get "$OC_ENDPOINT_IP" 2>/dev/null || true)"
    [[ -n "$route" ]] || oc_fail "no route to OpenConnect gateway"
    OC_UNDERLAY_DEV="$(awk '{for(i=1;i<=NF;i++)if($i=="dev"){print $(i+1);exit}}' <<<"$route")"
    OC_UNDERLAY_GW="$(awk '{for(i=1;i<=NF;i++)if($i=="via"){print $(i+1);exit}}' <<<"$route")"
    case "$OC_UNDERLAY_DEV" in vpn0|amn0|kk-*|tun*|wg*) oc_fail "gateway currently routes through VPN-like interface $OC_UNDERLAY_DEV; stop old VPN first";; esac
}

oc_pin_endpoint() {
    if [[ -n "$OC_UNDERLAY_GW" ]]; then
        sudo ip -4 route replace "$OC_ENDPOINT_IP/32" via "$OC_UNDERLAY_GW" dev "$OC_UNDERLAY_DEV" metric 42740
    else
        sudo ip -4 route replace "$OC_ENDPOINT_IP/32" dev "$OC_UNDERLAY_DEV" metric 42740
    fi
    OC_ENDPOINT_PINNED=1
}

oc_start_toad() {
    sudo -- bash -c 'printf "%s\n" "$$" >"$1"; shift; exec "$@"' bash "$OC_PRIVATE/toad.pid" "$OC_TOAD" run -config "$OC_CONFIG" >>"$OC_DIAG/toad.log" 2>&1 &
    OC_SUDO_PID=$!
    for _ in $(seq 1 300); do
        [[ -s "$OC_PRIVATE/toad.pid" ]] && break
        sleep 0.05
    done
    OC_TOAD_PID="$(sudo sed -n '1p' "$OC_PRIVATE/toad.pid" 2>/dev/null || true)"
    [[ "$OC_TOAD_PID" =~ ^[0-9]+$ ]] || oc_fail "Toad pid was not published"
    for _ in $(seq 1 600); do
        ip link show dev "$OC_IFACE" >/dev/null 2>&1 && break
        kill -0 "$OC_SUDO_PID" 2>/dev/null || oc_fail "kikimora-toad exited before $OC_IFACE appeared"
        sleep 0.05
    done
    ip link show dev "$OC_IFACE" >/dev/null 2>&1 || oc_fail "$OC_IFACE did not appear"
    OC_IFINDEX="$(cat "/sys/class/net/$OC_IFACE/ifindex")"
    for _ in $(seq 1 200); do
        sudo grep -q '"state": "online"' "$OC_STATE/state.json" 2>/dev/null && break
        sleep 0.05
    done
    sudo grep -q '"state": "online"' "$OC_STATE/state.json" 2>/dev/null || oc_fail "OpenConnect Toad never reached online state"
}

oc_configure_split_dns() {
    [[ -s "$OC_STATE/openconnect-network.env" ]] || oc_fail "OpenConnect did not publish pushed network parameters"
    sudo cp "$OC_STATE/openconnect-network.env" "$OC_DIAG/openconnect-network.env"
    local dns split
    dns="$(sudo awk -F= '$1=="ipv4_dns"{sub(/^[^=]*=/,"");print;exit}' "$OC_STATE/openconnect-network.env")"
    split="$(sudo awk -F= '$1=="split_dns"{sub(/^[^=]*=/,"");print;exit}' "$OC_STATE/openconnect-network.env")"
    printf 'pushed_ipv4_dns=%s\npushed_split_dns=%s\n' "$dns" "$split" >>"$OC_DIAG/traffic-test.txt"
    if [[ -n "$dns" && $(command -v resolvectl || true) ]]; then
        # shellcheck disable=SC2086
        sudo resolvectl dns "$OC_IFACE" $dns
        if [[ -n "$split" ]]; then
            local domains="" d
            for d in $split; do domains+=" ~$d"; done
            # shellcheck disable=SC2086
            sudo resolvectl domain "$OC_IFACE" $domains
        else
            sudo resolvectl domain "$OC_IFACE" '~sca.ad-tech.ru'
        fi
        OC_DNS_CONFIGURED=1
    else
        oc_warn "no pushed IPv4 DNS or resolvectl unavailable; internal hostname must already resolve"
    fi
}

oc_resolve_targets() {
    OC_GOOGLE_IP="$(getent ahostsv4 "$OC_GOOGLE_HOST" | awk 'NR==1{print $1;exit}')"
    OC_INTERNAL_IP="$(getent ahostsv4 "$OC_INTERNAL_HOST" | awk 'NR==1{print $1;exit}')"
    [[ "$OC_GOOGLE_IP" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || oc_fail "cannot resolve Google"
    [[ "$OC_INTERNAL_IP" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || oc_fail "cannot resolve internal GitLab through VPN DNS"
    printf 'google_ip=%s\ninternal_gitlab_ip=%s\n' "$OC_GOOGLE_IP" "$OC_INTERNAL_IP" >>"$OC_DIAG/traffic-test.txt"
}

oc_start_trace() {
    sudo tcpdump -n -l -i "$OC_IFACE" -c 2000 >"$OC_DIAG/tun-trace.txt" 2>&1 & OC_TUN_TRACE=$!
    sudo tcpdump -n -l -i "$OC_UNDERLAY_DEV" -c 2000 "host $OC_ENDPOINT_IP or host $OC_GOOGLE_IP or host $OC_INTERNAL_IP" >"$OC_DIAG/underlay-trace.txt" 2>&1 & OC_UP_TRACE=$!
}

oc_probe() {
    local label="$1" url="$2" ip="$3" out="$OC_DIAG/${1}-probe.txt" code
    code="$(curl -4 -ksS --resolve "${url#https://}" 2>/dev/null || true)"
    : "$code"
    curl -4 -ksS --connect-timeout 8 --max-time 20 --resolve "$(python3 -c 'import urllib.parse,sys; u=urllib.parse.urlparse(sys.argv[1]); print(f"{u.hostname}:{u.port or 443}:{sys.argv[2]}")' "$url" "$ip")" \
        -o "$OC_PRIVATE/$label.body" -w 'http_code=%{http_code}\nsize_download=%{size_download}\nremote_ip=%{remote_ip}\ntotal_s=%{time_total}\n' "$url" >"$out" 2>>"$OC_DIAG/command.log" || true
    cat "$out" >>"$OC_DIAG/traffic-test.txt"
}

oc_assert_probes() {
    local gcode icode gbytes ibytes
    gcode="$(awk -F= '$1=="http_code"{print $2}' "$OC_DIAG/google-probe.txt")"; gbytes="$(awk -F= '$1=="size_download"{print int($2)}' "$OC_DIAG/google-probe.txt")"
    icode="$(awk -F= '$1=="http_code"{print $2}' "$OC_DIAG/internal-gitlab-probe.txt")"; ibytes="$(awk -F= '$1=="size_download"{print int($2)}' "$OC_DIAG/internal-gitlab-probe.txt")"
    [[ "$gcode" =~ ^2[0-9][0-9]$ && "$gbytes" -gt 10000 ]] || oc_fail "Google probe failed: HTTP $gcode bytes=$gbytes"
    [[ "$icode" =~ ^(2[0-9][0-9]|3[0-9][0-9]|401|403)$ && "$ibytes" -gt 0 ]] || oc_fail "internal GitLab is not reachable through OpenConnect: HTTP $icode bytes=$ibytes"
}

oc_archive() {
    local result="$1" archive="$2"
    oc_kernel_logs
    {
        printf 'result=%s\nprotocol=openconnect\ninterface=%s\nifindex=%s\n' "$result" "$OC_IFACE" "${OC_IFINDEX:-unavailable}"
        printf 'gateway_host=%s\ngateway_ip=%s\nunderlay_dev=%s\n' "$OC_ENDPOINT_HOST" "$OC_ENDPOINT_IP" "$OC_UNDERLAY_DEV"
        printf 'google_host=%s\ninternal_host=%s\nchatgpt_probe=disabled_expected_unavailable\n' "$OC_GOOGLE_HOST" "$OC_INTERNAL_HOST"
    } >"$OC_DIAG/summary.txt"
    rm -rf "$OC_PRIVATE"
    find "$OC_DIAG" -type f -size +512k -print0 | while IFS= read -r -d '' f; do
        { head -n 1500 "$f"; printf '\n--- compacted middle ---\n'; tail -n 1500 "$f"; } >"$f.compact" && mv "$f.compact" "$f"
    done
    tar -czf "$archive" -C "$OC_WORK" diag
    chmod 0600 "$archive"
    rm -rf "$OC_WORK"
    printf 'Diagnostic archive: %s\n' "$archive"
}

oc_cleanup_runtime() {
    set +e
    [[ -n "${OC_TUN_TRACE:-}" ]] && sudo kill "$OC_TUN_TRACE" 2>/dev/null
    [[ -n "${OC_UP_TRACE:-}" ]] && sudo kill "$OC_UP_TRACE" 2>/dev/null
    if [[ "${OC_DNS_CONFIGURED:-0}" == 1 ]]; then sudo resolvectl revert "$OC_IFACE" 2>/dev/null; fi
    if [[ "${OC_DEFAULT_ROUTE:-0}" == 1 ]]; then sudo ip -4 route del default dev "$OC_IFACE" metric 5 2>/dev/null; fi
    if [[ -n "${OC_GOOGLE_IP:-}" && "${OC_NARROW_ROUTES:-0}" == 1 ]]; then sudo ip -4 route del "$OC_GOOGLE_IP/32" dev "$OC_IFACE" 2>/dev/null; fi
    if [[ -n "${OC_INTERNAL_IP:-}" && "${OC_NARROW_ROUTES:-0}" == 1 ]]; then sudo ip -4 route del "$OC_INTERNAL_IP/32" dev "$OC_IFACE" 2>/dev/null; fi
    if [[ -n "${OC_TOAD_PID:-}" ]]; then sudo kill -INT "$OC_TOAD_PID" 2>/dev/null; fi
    [[ -n "${OC_SUDO_PID:-}" ]] && wait "$OC_SUDO_PID" 2>/dev/null
    if [[ "${OC_ENDPOINT_PINNED:-0}" == 1 ]]; then sudo ip -4 route del "$OC_ENDPOINT_IP/32" metric 42740 2>/dev/null; fi
    set -e
}
