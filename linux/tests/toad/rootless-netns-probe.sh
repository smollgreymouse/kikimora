#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../../.." && pwd)"
TOAD_DIR="$REPO_ROOT/toad"
UNSUPPORTED_EXIT=77
TMP="$(mktemp -d "${TMPDIR:-/tmp}/kikimora-rootless-probe.XXXXXX")"
trap 'rm -rf -- "$TMP"' EXIT

fail() {
    echo "ERROR: rootless probe: $*" >&2
    exit 1
}

unsupported() {
    echo "UNSUPPORTED: rootless probe: $*" >&2
    exit "$UNSUPPORTED_EXIT"
}

for command in unshare nsenter ip go; do
    command -v "$command" >/dev/null 2>&1 || fail "required command not found: $command"
done

[[ -c /dev/net/tun ]] || unsupported "/dev/net/tun is absent"

HELPER="${TOAD_TUN_HELPER:-}"
if [[ -z "$HELPER" || ! -x "$HELPER" ]]; then
    HELPER="$TMP/toad-tun-test-helper"
    (
        cd "$TOAD_DIR"
        go build -o "$HELPER" ./internal/platform/testhelper
    ) || fail "failed to build existing TUN test helper"
fi

INNER="$TMP/inner.sh"
cat >"$INNER" <<'INNER_EOF'
#!/usr/bin/env bash
set -euo pipefail

UNSUPPORTED_EXIT=77
HELPER="$1"
WORK="$2"
NS="kkprobe-$$"
VETH_OUT="kkpo$$"
VETH_IN="kkpi$$"
HELPER_PID=""

unsupported() {
    echo "UNSUPPORTED_INNER: $*" >&2
    exit "$UNSUPPORTED_EXIT"
}

cleanup() {
    set +e
    if [[ -n "$HELPER_PID" ]] && kill -0 "$HELPER_PID" 2>/dev/null; then
        kill "$HELPER_PID" 2>/dev/null
        wait "$HELPER_PID" 2>/dev/null
    fi
    ip link delete "$VETH_OUT" 2>/dev/null
    ip netns delete "$NS" 2>/dev/null
}
trap cleanup EXIT

[[ "$(id -u)" == 0 ]] || unsupported "uid mapping did not produce uid 0 inside user namespace"

mount --make-rprivate / 2>"$WORK/mount-private.err" ||
    unsupported "private mount propagation failed: $(tr '\n' ' ' <"$WORK/mount-private.err")"
mount -t tmpfs -o mode=755 tmpfs /run 2>"$WORK/mount-run.err" ||
    unsupported "private tmpfs /run mount failed: $(tr '\n' ' ' <"$WORK/mount-run.err")"
mkdir -p /run/netns /run/amneziawg

nsenter --net=/proc/self/ns/net true 2>"$WORK/nsenter.err" ||
    unsupported "nsenter into owned network namespace failed: $(tr '\n' ' ' <"$WORK/nsenter.err")"

ip netns add "$NS" 2>"$WORK/netns-add.err" ||
    unsupported "named nested network namespace failed: $(tr '\n' ' ' <"$WORK/netns-add.err")"

ip link add "$VETH_OUT" type veth peer name "$VETH_IN" 2>"$WORK/veth-add.err" ||
    unsupported "CAP_NET_ADMIN/veth creation failed: $(tr '\n' ' ' <"$WORK/veth-add.err")"
ip link set "$VETH_IN" netns "$NS" 2>"$WORK/veth-move.err" ||
    unsupported "moving veth into nested namespace failed: $(tr '\n' ' ' <"$WORK/veth-move.err")"
ip addr add 198.18.0.1/30 dev "$VETH_OUT"
ip link set "$VETH_OUT" up
ip -n "$NS" addr add 198.18.0.2/30 dev "$VETH_IN"
ip -n "$NS" link set lo up
ip -n "$NS" link set "$VETH_IN" up

ip -n "$NS" route add 203.0.113.0/24 dev lo 2>"$WORK/route.err" ||
    unsupported "route add in owned namespace failed: $(tr '\n' ' ' <"$WORK/route.err")"
ip -n "$NS" rule add pref 32000 to 203.0.113.0/24 lookup main 2>"$WORK/rule.err" ||
    unsupported "policy rule add in owned namespace failed: $(tr '\n' ' ' <"$WORK/rule.err")"
ip -n "$NS" rule delete pref 32000
ip -n "$NS" route delete 203.0.113.0/24 dev lo

READY="$WORK/tun.ready"
RELEASE="$WORK/tun.release"
TUN_LOG="$WORK/tun.log"
ip netns exec "$NS" "$HELPER" "$READY" "$RELEASE" >"$TUN_LOG" 2>&1 &
HELPER_PID=$!

for ((i=0; i<200; ++i)); do
    if [[ -s "$READY" ]]; then
        break
    fi
    if ! kill -0 "$HELPER_PID" 2>/dev/null; then
        wait "$HELPER_PID" 2>/dev/null || true
        HELPER_PID=""
        unsupported "TUN helper exited before READY: $(tr '\n' ' ' <"$TUN_LOG")"
    fi
    sleep 0.05
done

[[ -s "$READY" ]] || unsupported "timed out creating TUN with existing helper"
ip -n "$NS" link show dev kk-toad0 >/dev/null 2>&1 ||
    unsupported "TUN helper reported READY but kk-toad0 is absent"

touch "$RELEASE"
if ! wait "$HELPER_PID"; then
    HELPER_PID=""
    unsupported "TUN helper failed during release: $(tr '\n' ' ' <"$TUN_LOG")"
fi
HELPER_PID=""

if ip -n "$NS" link show dev kk-toad0 >/dev/null 2>&1; then
    echo "ERROR_INNER: kk-toad0 survived final owner close" >&2
    exit 1
fi

echo "PASS_INNER"
INNER_EOF
chmod 0755 "$INNER"

set +e
OUTPUT="$(
    unshare --user --map-root-user --mount --net --pid --fork --mount-proc         bash "$INNER" "$HELPER" "$TMP" 2>&1
)"
STATUS=$?
set -e

if [[ "$STATUS" -eq 0 ]]; then
    echo "PASS: rootless userns+mount+netns+CAP_NET_ADMIN+veth+TUN+routes/rules"
    exit 0
fi

if [[ "$STATUS" -eq "$UNSUPPORTED_EXIT" ]]; then
    printf '%s\n' "$OUTPUT" >&2
    exit "$UNSUPPORTED_EXIT"
fi

if grep -Eqi 'uid_map|gid_map|Operation not permitted|Permission denied|user namespace|unprivileged' <<<"$OUTPUT"; then
    echo "UNSUPPORTED: rootless probe: mapped user namespace unavailable: $OUTPUT" >&2
    exit "$UNSUPPORTED_EXIT"
fi

printf '%s\n' "$OUTPUT" >&2
fail "unexpected probe regression (exit=$STATUS)"
