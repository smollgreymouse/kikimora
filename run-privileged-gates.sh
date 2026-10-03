#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO="$SCRIPT_DIR"
GATES_DIR="$REPO/linux/tests/toad"
RESULT_DIR="$REPO/.gigacode"
BUILD_DIR="${KIKIMORA_TOAD_BUILD_DIR:-$REPO/build/toad-privileged-user-$(id -u)}"
STATE_DIR=""
FAILED=()

cd "$REPO"
mkdir -p "$RESULT_DIR" "$BUILD_DIR"

cleanup_stale_test_fixtures() {
    echo "=== cleanup: stale Kikimora test fixtures ==="
    sudo env REPO="$REPO" bash <<'ROOT_EOF'
set -euo pipefail

mapfile -t pids < <(
    ps -eo pid=,args= | awk -v repo="$REPO" '
        index($0, repo "/build/toad-") || index($0, "/tmp/mpf-") {
            print $1
        }
    ' | sort -u
)

for d in /tmp/mpf-*; do
    [[ -d "$d" ]] || continue
    if [[ -s "$d/ocserv.pid" ]]; then
        pid="$(cat "$d/ocserv.pid" 2>/dev/null || true)"
        [[ "$pid" =~ ^[1-9][0-9]*$ ]] && pids+=("$pid")
    fi
done

if [[ "${SUDO_UID:-}" =~ ^[0-9]+$ ]]; then
    user_systemd_pid="$(ps -u "$SUDO_UID" -o pid=,args= | awk '$2 == "/usr/lib/systemd/systemd" && $3 == "--user" {print $1; exit}')"
    if [[ "$user_systemd_pid" =~ ^[1-9][0-9]*$ ]]; then
        while read -r pid ppid comm; do
            [[ "$ppid" == "$user_systemd_pid" && "$comm" == "ocserv-main" ]] && pids+=("$pid")
        done < <(ps -eo pid=,ppid=,comm=)
    fi
fi

for parent in "${pids[@]}"; do
    while read -r child; do
        [[ "$child" =~ ^[1-9][0-9]*$ ]] && pids+=("$child")
    done < <(ps -eo pid=,ppid= | awk -v p="$parent" '$2 == p {print $1}')
done

if (("${#pids[@]}")); then
    mapfile -t pids < <(printf '%s\n' "${pids[@]}" | sort -nu)
    echo "stale fixture pids: ${pids[*]}"
    for pid in "${pids[@]}"; do
        [[ -d "/proc/$pid" ]] || continue
        kill -TERM "$pid" 2>/dev/null || true
    done
    sleep 1
    for pid in "${pids[@]}"; do
        [[ -d "/proc/$pid" ]] || continue
        kill -KILL "$pid" 2>/dev/null || true
    done
else
    echo "stale fixture pids: none"
fi

while read -r ns _; do
    case "$ns" in
        mpf-*) ip netns delete "$ns" 2>/dev/null || true ;;
    esac
done < <(ip netns list 2>/dev/null || true)

rm -rf -- /tmp/mpf-*
rm -f -- /var/run/amneziawg/amgor-* /var/run/amneziawg/amgoc-* 2>/dev/null || true

echo "fixture cleanup complete"
ROOT_EOF
}

snapshot_host() {
    local target="$1"
    {
        printf 'netns=%s\n' "$(readlink /proc/self/ns/net)"
        printf '%s\n' '-- links --'
        ip -o link show | awk -F': ' '{print $2}' | sed 's/@.*//' | sort
        printf '%s\n' '-- ipv4-default --'
        ip -4 route show default | sort
        printf '%s\n' '-- ipv6-default --'
        ip -6 route show default | sort
        printf '%s\n' '-- ipv4-rules --'
        ip -4 rule show | sort
        printf '%s\n' '-- ipv6-rules --'
        ip -6 rule show | sort
        printf '%s\n' '-- named-netns --'
        ip netns list 2>/dev/null | sort || true
    } >"$target"
}

finalize() {
    local status=$?
    trap - EXIT
    set +e

    cleanup_stale_test_fixtures
    local cleanup_status=$?

    local host_state_status=0
    if [[ -n "$STATE_DIR" && -f "$STATE_DIR/before" ]]; then
        snapshot_host "$STATE_DIR/after"
        if cmp -s "$STATE_DIR/before" "$STATE_DIR/after"; then
            echo "host state unchanged: PASS"
        else
            echo "ERROR: host network state changed across privileged gate suite" >&2
            diff -u "$STATE_DIR/before" "$STATE_DIR/after" >&2 || true
            host_state_status=1
        fi
    fi

    [[ -z "$STATE_DIR" ]] || rm -rf -- "$STATE_DIR"

    if (( status != 0 )); then
        exit "$status"
    fi
    if (( cleanup_status != 0 || host_state_status != 0 )); then
        exit 1
    fi
    exit 0
}
trap finalize EXIT

run_gate() {
    local index="$1" label="$2" mode="$3"
    local log="$RESULT_DIR/gate-$label.log"

    echo
    echo "=== $index: $label ==="
    cleanup_stale_test_fixtures

    if sudo env KIKIMORA_TOAD_BUILD_DIR="$BUILD_DIR"         bash "$GATES_DIR/run-isolated.sh" "$mode" 2>&1 | tee "$log"; then
        echo "PASS: $label"
    else
        echo "FAIL: $label"
        FAILED+=("$label")
    fi
}

echo "=== prebuild: current HEAD binaries as ordinary user ==="
KIKIMORA_TOAD_BUILD_DIR="$BUILD_DIR"     bash "$GATES_DIR/run-isolated.sh" build-only 2>&1 | tee "$RESULT_DIR/gate-build.log"

echo
echo "=== privilege acquisition ==="
sudo -v

cleanup_stale_test_fixtures
STATE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/kikimora-privileged-state.XXXXXX")"
snapshot_host "$STATE_DIR/before"

run_gate "1/4" "route-parking" "route-parking"
run_gate "2/4" "multi-toad" "multi-toad"
run_gate "3/4" "orchestration-acceptance" "orchestration-acceptance"
run_gate "4/4" "xray-interop" "xray-interop"

echo
echo "========== RESULTS =========="
if (("${#FAILED[@]}" == 0)); then
    echo "ALL 4 PRIVILEGED GATES PASSED"
else
    echo "FAILED: ${FAILED[*]}"
    echo "Check logs in $RESULT_DIR/gate-*.log"
    exit 1
fi
