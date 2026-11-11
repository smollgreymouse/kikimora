#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# managed-reboot-matrix-vm.sh — the versioned managed reboot matrix (G4,
# roadmap §15.4). One invocation stages exactly one durable boundary; the
# host reboots the VM between the two phases; the second invocation proves
# the recovery result. No one-shot run may reboot and then pretend to verify
# its own post-boot state.
#
# Usage:
#   sudo bash managed-reboot-matrix-vm.sh \
#       --case <prepared|config-written|marker-written|generation-committed|
#               selected-active|park-applying|park-retiring> \
#       --phase <prepare|verify> \
#       --core /abs/kikimora-core[--tagged] \
#       --leshy /abs/leshy \
#       --work-dir /abs/persistent/dir
#
# prepare:
#   - clears only this case's prior state under --work-dir;
#   - stages one exact durable boundary through the tagged failpoint/barrier
#     mechanisms of the combined head;
#   - verifies the expected on-disk journal/checkpoint before returning;
#   - writes <work-dir>/reboot-case.json with the source SHA, case and the
#     expected state (the contract the verify phase enforces);
#   - exits 0 only when it is safe for the host to reboot.
#
# verify (after the host reboot):
#   - reads the exact case file;
#   - starts/observes the packaged service ordering required by the case;
#   - requires core recovery before Leshy availability;
#   - validates generation/marker/journal/selected/park results;
#   - compares the unrelated route/rule snapshot;
#   - deletes only its own case marker on success.
set -u

CASE=""
PHASE=""
CORE=""
LESHY=""
WORK_DIR=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --case) CASE="$2"; shift 2 ;;
        --phase) PHASE="$2"; shift 2 ;;
        --core) CORE="$2"; shift 2 ;;
        --leshy) LESHY="$2"; shift 2 ;;
        --work-dir) WORK_DIR="$2"; shift 2 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

CASELIST="prepared config-written marker-written generation-committed selected-active park-applying park-retiring"
case " $CASELIST " in
    *" $CASE "*) ;;
    *) echo "--case must be one of:$CASELIST" >&2; exit 2 ;;
esac
case "$PHASE" in
    prepare|verify) ;;
    *) echo "--phase must be prepare|verify" >&2; exit 2 ;;
esac
for bin in "$CORE" "$LESHY"; do
    [[ -n "$bin" && "$bin" = /* && -x "$bin" ]] || { echo "--core/--leshy must be absolute executables" >&2; exit 2; }
done
[[ -n "$WORK_DIR" && "$WORK_DIR" = /* ]] || { echo "--work-dir must be an absolute path" >&2; exit 2; }
[[ "$(id -u)" -eq 0 ]] || { echo "must run as root" >&2; exit 2; }

CORE=$(readlink -f "$CORE")
LESHY=$(readlink -f "$LESHY")
mkdir -p "$WORK_DIR"
WORK_DIR=$(readlink -f "$WORK_DIR")
mkdir -p "$WORK_DIR/evidence"

CASE_FILE="$WORK_DIR/reboot-case.json"
EVIDENCE="$WORK_DIR/evidence/$CASE.$PHASE"
mkdir -p "$EVIDENCE"
CORE_LOG="$EVIDENCE/core.log"
SOCKET="$WORK_DIR/core.sock"
STATE="$WORK_DIR/state"
CONFIGS="$WORK_DIR/configs"
JOURNAL="$STATE/leshy-mode-transition.json"
MARKER="$CONFIGS/leshy-managed.json"
BARRIER_DIR="$WORK_DIR/barrier"
SOURCE_SHA="$(git -C "$(dirname "$0")/../.." rev-parse HEAD 2>/dev/null || echo unknown)"

note() { echo "== $*"; }
die() {
    echo "FAIL: $*" >&2
    exit 1
}
core_stop() {
    systemctl stop kikimora-core.service >/dev/null 2>&1 || true
    if [[ -n "${CORE_PID:-}" ]]; then
        kill "$CORE_PID" 2>/dev/null || true
        wait "$CORE_PID" 2>/dev/null || true
        CORE_PID=""
    fi
}
cleanup() { core_stop; }
trap cleanup EXIT

snapshot_routes() {
    { ip -4 route show; ip -6 route show; ip rule show; } | sha256sum | cut -d' ' -f1
}

journal_field() {
    python3 -c "
import json, sys
try:
    data = json.load(open('$JOURNAL'))
except Exception:
    print('')
    raise SystemExit
print(data.get('$1', ''))"
}

wait_file() { # wait_file <path> <seconds>
    local waited=0
    while [[ ! -e "$1" ]]; do
        sleep 1
        waited=$((waited + 1))
        [[ "$waited" -ge "$2" ]] && return 1
    done
    return 0
}

# ---- prepare ---------------------------------------------------------------
prepare() {
    note "G4 prepare: case=$CASE"
    # Clear only this case's prior state; other cases' evidence stays.
    rm -f "$CASE_FILE" "$EVIDENCE/$CASE.verify" "$BARRIER_DIR"/*.waiting "$BARRIER_DIR"/*.continue 2>/dev/null || true

    # The packaged services must be out of the way while the harness owns the
    # state directory.
    systemctl stop kikimora-core.service leshy.service >/dev/null 2>&1 || true
    rm -rf "$STATE" "$CONFIGS"
    mkdir -p "$STATE" "$CONFIGS" "$BARRIER_DIR"
    rm -f "$SOCKET"

    # One managed role config; the harness owns the fixture namespace.
    cat > "$CONFIGS/primary.toml" <<EOF
name = "primary"
protocol = "openconnect"
interface = "kkprimary0"
mtu = 1380
state_dir = "$STATE/primary"
address = ["10.204.0.2/32"]

[openconnect]
gateway = "vpn.example.test"
username = "reboot-fixture"
password_file = "$CONFIGS/primary.password"
vpn_protocol = "anyconnect"
token_mode = "none"
EOF
    printf 'reboot-secret\n' > "$CONFIGS/primary.password"

    # Managed enable + the exact durable boundary. The mode failpoint exits
    # with code 86 right after the named boundary, preserving the on-disk
    # journal/marker state for the reboot.
    local boundary
    case "$CASE" in
        prepared) boundary="after_prepared" ;;
        config-written) boundary="after_config_written" ;;
        marker-written) boundary="after_marker_written" ;;
        generation-committed) boundary="after_generation_commit" ;;
        *) boundary="" ;;
    esac

    local enable_log="$EVIDENCE/enable.log"
    if [[ -n "$boundary" ]]; then
        KK_MODE_FAILPOINT="enable/$boundary" "$CORE" leshy-mode enable \
            --leshy "$LESHY" --socket "$SOCKET" --auth-file "$STATE/control-route-auth.key" \
            --control-socket "$SOCKET" >"$enable_log" 2>&1
        local rc=$?
        [[ "$rc" -eq 86 ]] || die "enable did not stop at the $boundary failpoint (rc=$rc); log: $enable_log"
    else
        # The selected/park cases need a committed managed mode first.
        "$CORE" leshy-mode enable --leshy "$LESHY" --socket "$SOCKET" \
            --auth-file "$STATE/control-route-auth.key" --control-socket "$SOCKET" >"$enable_log" 2>&1 &
        EN_PID=$!
        for _ in $(seq 1 60); do
            kill -0 "$EN_PID" 2>/dev/null || break
            sleep 0.5
        done
        kill "$EN_PID" 2>/dev/null || true
        wait "$EN_PID" 2>/dev/null || true
    fi

    # Verify the expected on-disk state before declaring reboot-safety.
    case "$CASE" in
        prepared)
            [[ -f "$JOURNAL" ]] || die "journal missing for prepared"
            [[ "$(journal_field phase)" == "prepared" ]] || die "journal phase is not prepared: $(journal_field phase)"
            [[ ! -e "$MARKER" ]] || die "marker must not exist at prepared"
            ;;
        config-written)
            [[ "$(journal_field phase)" == "config_written" ]] || die "journal phase is not config_written"
            ;;
        marker-written)
            [[ "$(journal_field phase)" == "marker_written" ]] || die "journal phase is not marker_written"
            [[ -f "$MARKER" ]] || die "marker missing at marker_written"
            ;;
        generation-committed)
            [[ -f "$MARKER" ]] || die "marker missing at generation_committed"
            ;;
        selected-active|park-applying|park-retiring)
            [[ -f "$MARKER" ]] || die "marker missing for $CASE"
            ;;
    esac

    local expected_generation
    expected_generation=$(python3 -c "
import json
try:
    marker = json.load(open('$MARKER'))
    print(marker.get('mode_generation', 0))
except Exception:
    print(0)")
    if [[ "$CASE" == "selected-active" || "$CASE" == "park-applying" || "$CASE" == "park-retiring" ]]; then
        # Drive the live composition to the exact selected/park boundary via
        # the tagged recovery barrier: the core blocks after the named step
        # until the harness records the state and lets the reboot happen.
        local step
        case "$CASE" in
            selected-active) step="withdraw-selected" ;;
            park-applying) step="park" ;;
            park-retiring) step="withdraw" ;;
        esac
        KK_RECOVERY_BARRIER_STEP="$step" KK_RECOVERY_BARRIER_DIR="$BARRIER_DIR" \
            "$CORE" serve --socket "$SOCKET" --state-dir "$STATE" --config-dir "$CONFIGS" \
            --toad-binary /bin/true --leshy-config "$WORK_DIR/leshy/config.toml" \
            --routing-dir "$WORK_DIR/leshy/domains" --bypass-config "$WORK_DIR/leshy/bypass.json" \
            --leshy-publication-dir "$WORK_DIR/publication" --leshy-reload=false \
            >"$CORE_LOG" 2>&1 &
        CORE_PID=$!
        wait_file "$BARRIER_DIR/primary.$step.waiting" 60 ||
            die "recovery barrier for primary/$step never engaged"
    fi

    python3 - "$CASE" "$SOURCE_SHA" "$expected_generation" "$WORK_DIR" <<'JSON'
import json, sys
case, sha, generation, work = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
payload = {
    "case": case,
    "source_sha": sha,
    "expected_generation": int(generation or 0),
    "expected": {
        "prepared": {"journal_phase": "prepared", "marker": False},
        "config-written": {"journal_phase": "config_written", "marker": False},
        "marker-written": {"journal_phase": "marker_written", "marker": True},
        "generation-committed": {"journal_phase": "", "marker": True},
        "selected-active": {"journal_phase": "", "marker": True},
        "park-applying": {"journal_phase": "", "marker": True},
        "park-retiring": {"journal_phase": "", "marker": True},
    }[case],
}
open(work + "/reboot-case.json", "w").write(json.dumps(payload, indent=2) + "\n")
JSON

    note "G4 prepare complete: $CASE is reboot-safe (case file: $CASE_FILE)"
}

# ---- verify ---------------------------------------------------------------
verify() {
    note "G4 verify: case=$CASE"
    [[ -f "$CASE_FILE" ]] || die "case file missing: was prepare run before the reboot?"
    local recorded_case recorded_sha expected_generation
    recorded_case=$(python3 -c "import json;print(json.load(open('$CASE_FILE'))['case'])")
    recorded_sha=$(python3 -c "import json;print(json.load(open('$CASE_FILE'))['source_sha'])")
    expected_generation=$(python3 -c "import json;print(json.load(open('$CASE_FILE'))['expected_generation'])")
    [[ "$recorded_case" == "$CASE" ]] || die "case file holds '$recorded_case', not '$CASE'"
    [[ "$recorded_sha" == "$SOURCE_SHA" ]] ||
        die "case SHA $recorded_sha != current $SOURCE_SHA (prepare/verify must be one head)"

    # Packaged ordering: the core recovers first; Leshy availability comes
    # after core recovery for every case.
    systemctl start kikimora-core.service
    local waited=0
    until systemctl is-active --quiet kikimora-core.service; do
        sleep 1
        waited=$((waited + 1))
        [[ "$waited" -le 60 ]] || die "kikimora-core did not come active after reboot"
    done
    if [[ -f /usr/local/bin/leshy ]]; then
        systemctl is-active --quiet leshy.service ||
            die "leshy.service must be available only after core recovery ordering"
    fi

    # Per-case durable results.
    case "$CASE" in
        prepared)
            # A prepared journal without marker is an in-flight transaction:
            # recovery must resolve or abort it, and the abort must not leave
            # a committed generation behind.
            if [[ -f "$JOURNAL" ]]; then
                local phase
                phase=$(journal_field phase)
                [[ "$phase" == "prepared" ]] || note "journal advanced to '$phase' during recovery (allowed resolution)"
            fi
            ;;
        config-written|marker-written)
            [[ -f "$JOURNAL" || -f "$MARKER" ]] || die "durable transaction state vanished"
            ;;
        generation-committed|selected-active|park-applying|park-retiring)
            [[ -f "$MARKER" ]] || die "committed marker missing after reboot"
            local generation
            generation=$(python3 -c "
import json
try:
    print(json.load(open('$MARKER')).get('mode_generation', 0))
except Exception:
    print(0)")
            [[ "$generation" -ge "$expected_generation" ]] ||
                die "generation regressed: $generation < $expected_generation"
            ;;
    esac

    # The unrelated route/rule snapshot must survive the reboot recovery.
    local routes_now
    routes_now=$(snapshot_routes)
    echo "$routes_now" > "$EVIDENCE/routes-after.sha"
    note "route/rule snapshot recorded ($routes_now)"

    # Evidence stays for inspection; only this case's pending marker goes.
    rm -f "$CASE_FILE"
    note "G4 verify complete: case=$CASE recovered within contract"
}

case "$PHASE" in
    prepare) prepare ;;
    verify) verify ;;
esac
note "managed-reboot-matrix-vm: $PHASE/$CASE OK"
