#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO="$SCRIPT_DIR"
GATES_DIR="$REPO/linux/tests/toad"
RESULT_DIR="$REPO/.gigacode"
cd "$REPO"
mkdir -p "$RESULT_DIR"
FAILED=()

cleanup_stale_test_fixtures() {
    echo "=== cleanup: stale Kikimora mpf test fixtures ==="
    sudo env REPO="$REPO" bash <<'ROOT_EOF'
set -euo pipefail

mapfile -t pids < <(
    ps -eo pid=,args= | awk -v repo="$REPO" '
        index($0, repo "/build/toad-smoke/") || index($0, "/tmp/mpf-") {
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

# A failed old fixture may already have lost its temp dir. Those ocserv-main
# processes are recognizable because this sudo-run harness orphaned them under
# the invoking user's systemd --user process rather than system PID 1.
if [[ "${SUDO_UID:-}" =~ ^[0-9]+$ ]]; then
    user_systemd_pid="$(ps -u "$SUDO_UID" -o pid=,args= | awk '$2 == "/usr/lib/systemd/systemd" && $3 == "--user" {print $1; exit}')"
    if [[ "$user_systemd_pid" =~ ^[1-9][0-9]*$ ]]; then
        while read -r pid ppid comm; do
            [[ "$ppid" == "$user_systemd_pid" && "$comm" == "ocserv-main" ]] && pids+=("$pid")
        done < <(ps -eo pid=,ppid=,comm=)
    fi
fi

# Include direct descendants (ocserv-sm/worker, Toad children) of selected roots.
for parent in "${pids[@]}"; do
    while read -r child; do
        [[ "$child" =~ ^[1-9][0-9]*$ ]] && pids+=("$child")
    done < <(ps -eo pid=,ppid= | awk -v p="$parent" '$2 == p {print $1}')
done

if ((${#pids[@]})); then
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
    echo
}

cleanup_stale_test_fixtures

echo "=== 1/4: route-parking ==="
sudo bash "$GATES_DIR/run-isolated.sh" route-parking 2>&1 | tee "$RESULT_DIR/gate-route-parking.log" || { echo "FAIL"; FAILED+=("route-parking"); }

echo ""
echo "=== 2/4: multi-toad ==="
sudo bash "$GATES_DIR/run-isolated.sh" multi-toad 2>&1 | tee "$RESULT_DIR/gate-multi-toad.log" || { echo "FAIL"; FAILED+=("multi-toad"); }

echo ""
echo "=== 3/4: orchestration-acceptance (the main fix) ==="
sudo bash "$GATES_DIR/run-isolated.sh" orchestration-acceptance 2>&1 | tee "$RESULT_DIR/gate-orchestration-acceptance.log" || { echo "FAIL"; FAILED+=("orchestration-acceptance"); }

echo ""
echo "=== 4/4: xray-interop ==="
sudo bash "$GATES_DIR/run-isolated.sh" xray-interop 2>&1 | tee "$RESULT_DIR/gate-xray-interop.log" || { echo "FAIL"; FAILED+=("xray-interop"); }

echo ""
echo "========== RESULTS =========="
if [ ${#FAILED[@]} -eq 0 ]; then
    echo "ALL 4 GATES PASSED"
else
    echo "FAILED: ${FAILED[*]}"
    echo "Check logs in $RESULT_DIR/gate-*.log"
fi
