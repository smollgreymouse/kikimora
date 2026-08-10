#!/usr/bin/env bash
set -Eeuo pipefail

readonly SOURCE="${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/files/leshy-dns}"

die() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

[[ ${EUID} -eq 0 ]] || die 'test must run as root'
command -v flock >/dev/null 2>&1 || die 'flock is required'
command -v python3 >/dev/null 2>&1 || die 'python3 is required'

tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/mock"

stub_resolv="$(readlink -f /etc/resolv.conf)"
python3 - "$SOURCE" "$tmp/leshy-dns" "$tmp" "$stub_resolv" <<'PY'
from pathlib import Path
import sys

source = Path(sys.argv[1])
out = Path(sys.argv[2])
tmp = sys.argv[3]
stub = sys.argv[4]
text = source.read_text(encoding="utf-8")
replacements = {
    "readonly state_dir='/run/kikimora/leshy/dns'": f"readonly state_dir='{tmp}/state'",
    "readonly lock_file='/run/kikimora/leshy/dns.lock'": f"readonly lock_file='{tmp}/dns.lock'",
    "readonly persistent_dir='/var/lib/kikimora/leshy'": f"readonly persistent_dir='{tmp}/persistent'",
    "readonly config='/etc/kikimora/leshy/config.toml'": f"readonly config='{tmp}/config.toml'",
    "readonly stub_resolv='/run/systemd/resolve/stub-resolv.conf'": f"readonly stub_resolv='{stub}'",
}
for old, new in replacements.items():
    if old not in text:
        raise SystemExit(f"expected source line not found: {old}")
    text = text.replace(old, new, 1)
out.write_text(text, encoding="utf-8")
out.chmod(0o755)
PY

cat > "$tmp/config.toml" <<'EOF_CONFIG'
[server]
default_upstream = [
    "1.1.1.1:53",
]
EOF_CONFIG

cat > "$tmp/bin/systemctl" <<'EOF_SYSTEMCTL'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ ${1:-} == is-active ]] || exit 64
exit 0
EOF_SYSTEMCTL

cat > "$tmp/bin/dig" <<'EOF_DIG'
#!/usr/bin/env bash
set -Eeuo pipefail
cat <<'EOF_REPLY'
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 1
EOF_REPLY
EOF_DIG

cat > "$tmp/bin/ip" <<'EOF_IP'
#!/usr/bin/env bash
set -Eeuo pipefail

case "$*" in
    'link show dev leshy-dns0')
        [[ -e ${MOCK_STATE}/link ]]
        ;;
    'link show dev wlan0')
        exit 0
        ;;
    'link add leshy-dns0 type dummy')
        # Widen the unprotected race window. With the production flock, the
        # second caller cannot reach this point until the first has completed.
        sleep 0.25
        if mkdir "${MOCK_STATE}/link-create" 2>/dev/null; then
            : > "${MOCK_STATE}/link"
            printf 'link-add\n' >> "${MOCK_STATE}/ip.log"
            exit 0
        fi
        printf 'RTNETLINK answers: File exists\n' >&2
        exit 2
        ;;
    'link delete leshy-dns0')
        rm -rf -- "${MOCK_STATE}/link-create" "${MOCK_STATE}/link"
        ;;
    'address add 192.0.2.1/32 dev leshy-dns0'|'link set leshy-dns0 up')
        exit 0
        ;;
    *)
        printf 'unexpected ip invocation: %s\n' "$*" >&2
        exit 64
        ;;
esac
EOF_IP

cat > "$tmp/bin/resolvectl" <<'EOF_RESOLVECTL'
#!/usr/bin/env bash
set -Eeuo pipefail

case "${1:-}" in
    status)
        if (($# == 1)); then
            printf 'Link 2 (wlan0)\n'
        else
            exit 0
        fi
        ;;
    domain)
        if (($# == 2)); then
            case "$2" in
                wlan0) printf 'Link 2 (wlan0): ~.\n' ;;
                leshy-dns0) printf 'Link 9 (leshy-dns0): ~.\n' ;;
                *) exit 64 ;;
            esac
        fi
        ;;
    dns)
        if (($# == 2)); then
            printf 'Link 9 (leshy-dns0): 127.0.0.1:53053\n'
        fi
        ;;
    default-route)
        if (($# == 2)); then
            printf 'Link 9 (leshy-dns0): yes\n'
        fi
        ;;
    llmnr|mdns|flush-caches|query|revert)
        exit 0
        ;;
    *)
        printf 'unexpected resolvectl invocation: %s\n' "$*" >&2
        exit 64
        ;;
esac
EOF_RESOLVECTL

chmod +x "$tmp/bin/systemctl" "$tmp/bin/dig" "$tmp/bin/ip" "$tmp/bin/resolvectl"
export MOCK_STATE="$tmp/mock"
export PATH="$tmp/bin:$PATH"

"$tmp/leshy-dns" enable > "$tmp/enable.1.log" 2>&1 &
pid1=$!
"$tmp/leshy-dns" enable > "$tmp/enable.2.log" 2>&1 &
pid2=$!

status1=0
status2=0
wait "$pid1" || status1=$?
wait "$pid2" || status2=$?

if ((status1 != 0 || status2 != 0)); then
    cat "$tmp/enable.1.log" >&2
    cat "$tmp/enable.2.log" >&2
    die "concurrent enable failed: statuses ${status1}, ${status2}"
fi

[[ -e "$tmp/state/enabled" ]] || die 'enabled marker was not created'
[[ -e "$tmp/persistent/dns-enabled" ]] || die 'intent marker was not created'
[[ -e "$tmp/mock/link" ]] || die 'mock DNS link was not left active'

add_count=0
[[ ! -r "$tmp/mock/ip.log" ]] || add_count="$(grep -c '^link-add$' "$tmp/mock/ip.log" || true)"
[[ $add_count == 1 ]] || die "expected exactly one link creation, got ${add_count}"

if grep -Fq 'RTNETLINK answers: File exists' "$tmp/enable.1.log" "$tmp/enable.2.log"; then
    die 'concurrent caller reached duplicate link creation'
fi

already_count="$(grep -hF 'system DNS already switched to Leshy' "$tmp/enable.1.log" "$tmp/enable.2.log" | wc -l)"
[[ $already_count == 1 ]] || die 'waiting caller did not re-check the completed runtime state'

printf 'Leshy DNS mutation race test: OK\n'
