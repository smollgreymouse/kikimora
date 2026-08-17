#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly ROOT
readonly PROVIDERS="${ROOT}/linux/files/endpoint-providers"

die() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/endpoints" "$tmp/bin"

cat >"$tmp/endpoints/secondary.txt" <<'EOF_STATIC'
# comment
ve.ad-tech.ru
46.243.227.103
EOF_STATIC
static_output="$(KIKIMORA_ENDPOINTS_DIR="$tmp/endpoints" bash "$PROVIDERS/static" secondary)"
grep -Fq '# kikimora-endpoint-provider-mode: static' <<<"$static_output" || die 'static provider mode header missing'
grep -Fq 've.ad-tech.ru' <<<"$static_output" || die 'static provider did not return role endpoint file'

cat >"$tmp/custom-provider" <<'EOF_COMMAND'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "${KIKIMORA_ENDPOINT_ROLE:-}" == secondary ]] || exit 70
[[ "${1:-}" == secondary ]] || exit 71
printf '# kikimora-endpoint-provider-mode: dynamic-additive\n203.0.113.77\n'
EOF_COMMAND
chmod +x "$tmp/custom-provider"
command_output="$(bash "$PROVIDERS/command" secondary "$tmp/custom-provider")"
grep -Fq 'dynamic-additive' <<<"$command_output" || die 'command provider did not pass through provider mode'
grep -Fq '203.0.113.77' <<<"$command_output" || die 'command provider did not pass through endpoints'

cat >"$tmp/bin/ip" <<'EOF_IP'
#!/usr/bin/env bash
set -Eeuo pipefail
case "$*" in
  '-4 -o address show dev tun0')
    printf '7: tun0    inet 172.18.0.1/30 scope global tun0\n'
    ;;
  '-6 -o address show dev tun0') ;;
  '-4 -o address show dev wlp0s20f3')
    printf '2: wlp0s20f3    inet 10.240.219.226/20 brd 10.240.223.255 scope global dynamic wlp0s20f3\n'
    ;;
  '-6 -o address show dev wlp0s20f3') ;;
  *) exit 0 ;;
esac
EOF_IP
chmod +x "$tmp/bin/ip"

cat >"$tmp/bin/ss" <<'EOF_SS'
#!/usr/bin/env bash
set -Eeuo pipefail
cat <<'EOF_SOCKETS'
udp ESTAB 0 0 10.240.219.226%wlp0s20f3:37111 8.8.8.8:53 users:(("sing-box",pid=3441088,fd=10))
tcp ESTAB 0 0 172.18.0.1:52840 185.163.159.103:443 users:(("xray",pid=3441072,fd=18))
tcp ESTAB 0 0 10.240.219.226%wlp0s20f3:35078 185.163.159.103:443 users:(("sing-box",pid=3441088,fd=12))
tcp ESTAB 0 0 172.18.0.1:35140 153.80.241.175:443 users:(("xray",pid=3441072,fd=40))
tcp ESTAB 0 0 10.240.219.226%wlp0s20f3:34244 153.80.241.175:443 users:(("sing-box",pid=3441088,fd=346))
tcp ESTAB 0 0 10.240.219.226%wlp0s20f3:32804 31.76.19.84:80 users:(("sing-box",pid=3441088,fd=250))
tcp ESTAB 0 0 172.18.0.1:43364 77.88.55.88:443 users:(("xray",pid=3441072,fd=64))
tcp ESTAB 0 0 172.18.0.1:45449 172.18.0.2:10143 users:(("sing-box",pid=3441088,fd=220))
EOF_SOCKETS
EOF_SS
chmod +x "$tmp/bin/ss"

happ_output="$(
    KIKIMORA_ENDPOINT_INTERFACE=tun0 \
    KIKIMORA_UNDERLAY4_DEVICE=wlp0s20f3 \
    KIKIMORA_IP="$tmp/bin/ip" \
    KIKIMORA_SS="$tmp/bin/ss" \
    bash "$PROVIDERS/happ" secondary
)"

grep -Fq '# kikimora-endpoint-provider-mode: dynamic-additive' <<<"$happ_output" || die 'Happ provider mode header missing'
grep -Fxq '185.163.159.103' <<<"$happ_output" || die 'Happ provider missed first tunnel/physical intersection'
grep -Fxq '153.80.241.175' <<<"$happ_output" || die 'Happ provider missed second tunnel/physical intersection'
if grep -Fxq '8.8.8.8' <<<"$happ_output"; then
    die 'Happ provider treated sing-box DNS traffic as a VPN endpoint'
fi
if grep -Fxq '31.76.19.84' <<<"$happ_output"; then
    die 'Happ provider treated an unpaired physical direct socket as a VPN endpoint'
fi
if grep -Fxq '77.88.55.88' <<<"$happ_output"; then
    die 'Happ provider treated an unpaired Xray socket as a VPN endpoint'
fi
if grep -Fxq '172.18.0.2' <<<"$happ_output"; then
    die 'Happ provider exposed an internal TUN peer as an endpoint'
fi

custom_names_output="$(
    KIKIMORA_ENDPOINT_INTERFACE=tun0 \
    KIKIMORA_UNDERLAY4_DEVICE=wlp0s20f3 \
    KIKIMORA_IP="$tmp/bin/ip" \
    KIKIMORA_SS="$tmp/bin/ss" \
    bash "$PROVIDERS/happ" secondary 'xray,sing-box'
)"
[[ "$custom_names_output" == "$happ_output" ]] || die 'explicit default Happ process names changed discovery result'

printf 'Endpoint provider plugin tests: OK\n'
