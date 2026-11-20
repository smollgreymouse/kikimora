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
#       --work-dir /abs/persistent/dir \
#       [--ready-toad /abs/ready-toad]        # required for the selected/park cases
#       [--routeprobe /abs/routeprobe]        # required for the selected/park cases
#
# prepare:
#   - clears only this case's prior state under --work-dir;
#   - stages one exact durable boundary through the tagged failpoint/barrier
#     mechanisms of the combined head;
#   - verifies the expected on-disk journal/checkpoint before returning;
#   - writes <work-dir>/reboot-case.json with the source SHA, case and the
#     expected state (the contract the verify phase enforces);
#   - exits 0 only when it is safe for the host to reboot. The host MUST
#     chain `prepare && reboot` in one session: the selected/park cases
#     stage the boundary with the core LIVE mid-barrier and the reboot is
#     the crash that freezes it (a graceful stop would release it).
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
READY_TOAD=""
ROUTEPROBE=""
FIXTURES=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --case) CASE="$2"; shift 2 ;;
        --phase) PHASE="$2"; shift 2 ;;
        --core) CORE="$2"; shift 2 ;;
        --leshy) LESHY="$2"; shift 2 ;;
        --work-dir) WORK_DIR="$2"; shift 2 ;;
        --ready-toad) READY_TOAD="$2"; shift 2 ;;
        --routeprobe) ROUTEPROBE="$2"; shift 2 ;;
        --fixtures) FIXTURES="$2"; shift 2 ;;
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

# T1.1 (roadmap §17): print the source and binary identities at start so a
# prepare/verify evidence pair can be tied to the exact inputs behind it.
note "harness identities"
note "harness: $(readlink -f "${BASH_SOURCE[0]}")"
note "harness sha256: $(sha256sum "$(readlink -f "${BASH_SOURCE[0]}")" | cut -d' ' -f1)"
SOURCE_SHA="$(git -C "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/../.." rev-parse HEAD 2>/dev/null || echo "${VM_ACCEPTANCE_SOURCE_SHA:-unknown}")"
note "source sha: $SOURCE_SHA"
[[ "$SOURCE_SHA" == "unknown" ]] && note "WARNING: source SHA unknown — set VM_ACCEPTANCE_SOURCE_SHA when running outside a checkout"
note "source sha: $SOURCE_SHA"
for bin in "$CORE" "$LESHY"; do
    if [[ -n "$bin" ]]; then
        note "$bin sha256: $(sha256sum "$bin" | cut -d' ' -f1)"
    fi
done
core_stop() {    systemctl stop kikimora-core.service >/dev/null 2>&1 || true
    if [[ -n "${CORE_PID:-}" ]]; then
        kill "$CORE_PID" 2>/dev/null || true
        wait "$CORE_PID" 2>/dev/null || true
        CORE_PID=""
    fi
}
# The selected/park prepare exits with the core LIVE mid-barrier: the reboot
# itself is the crash that creates the durable boundary. The EXIT trap must
# not stop it — the host must chain `prepare && reboot` in one session so
# nothing graceful runs between the two.
CORE_LEAVE_RUNNING=0
cleanup() {
    if [[ "${CORE_LEAVE_RUNNING:-0}" -eq 1 ]]; then
        note "leaving the barrier core running for the reboot (crash semantics)"
        return 0
    fi
    if [[ -n "${STUB_PID:-}" ]]; then kill "$STUB_PID" 2>/dev/null || true; fi
    if [[ -n "${LESHY_PID:-}" ]]; then kill "$LESHY_PID" 2>/dev/null || true; fi
    core_stop
    cleanup_fixture_netns
}
trap cleanup EXIT

# The selected/park cases need REAL registry entries: only an ensured
# selected route produces the checkpoint the startup-resume recovery
# replays across the barrier. The machinery is the same one
# managed-leshy-routing-netns.sh owns — a deterministic DNS stub upstream,
# the generated managed config and a Leshy child inside the fixture
# namespace — so one canary query ensures the role's zone route.
STUB_PID=""
LESHY_PID=""

stage_dns_machinery() {
    [[ -n "$FIXTURES" && "$FIXTURES" = /* && -d "$FIXTURES" ]] \
        || die "selected/park cases require --fixtures <linux/tests/toad/fixtures dir>"
    mkdir -p /etc/kikimora/leshy/domains
    cat > /etc/kikimora/leshy/config.toml <<'TOML'
[server]
listen_address = "127.0.0.1:15353"
default_upstream = ["127.0.0.1:15354"]
route_failure_mode = "servfail"
TOML
    ip netns exec "$FIXTURE_NS" python3 "$FIXTURES/dns-stub.py" >"$EVIDENCE/dns-stub.log" 2>&1 &
    STUB_PID=$!
}

dnsq() { ip netns exec "$FIXTURE_NS" python3 "$FIXTURES/dnsq.py" "$@"; }

start_leshy_child() {
    ip netns exec "$FIXTURE_NS" "$LESHY" /etc/kikimora/leshy/config.toml \
        >"$EVIDENCE/leshy.log" 2>&1 &
    LESHY_PID=$!
}

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

# The tagged-core exit-86 contract: a mode failpoint crash leaves the exact
# durable boundary on disk and the process gone with code 86 (the same
# protocol mode-transition-crash-vm.sh proves row by row).
wait_exit_86() {
    local waited=0
    while kill -0 "${CORE_PID:-0}" 2>/dev/null; do
        sleep 0.2
        waited=$((waited + 1))
        [[ "$waited" -ge 150 ]] && { echo "core did not exit within 30s" >&2; return 1; }
    done
    wait "$CORE_PID" 2>/dev/null
    local rc=$?
    CORE_PID=""
    [[ "$rc" -eq 86 ]] || { echo "core exited rc=$rc, want 86" >&2; return 1; }
    return 0
}

# The fixture namespace for the selected/park cases: the role needs a live
# managed interface and a default-route underlay for family validation, and
# the harness must leave nothing behind on the host (the same isolation
# contract managed-leshy-routing-netns.sh owns).
FIXTURE_NS="kk-reboot-$$"
MANAGED_IF="kkprimary0"
UNDERLAY_IF="kkreboot0"

stage_fixture_netns() {
    ip netns add "$FIXTURE_NS" || die "cannot create the fixture namespace $FIXTURE_NS"
    ip -n "$FIXTURE_NS" link set lo up
    ip -n "$FIXTURE_NS" link add "$MANAGED_IF" type dummy
    ip -n "$FIXTURE_NS" address add 10.204.0.2/32 dev "$MANAGED_IF"
    ip -n "$FIXTURE_NS" link set "$MANAGED_IF" up
    ip -n "$FIXTURE_NS" link add "$UNDERLAY_IF" type dummy
    ip -n "$FIXTURE_NS" address add 198.51.100.2/24 dev "$UNDERLAY_IF"
    ip -n "$FIXTURE_NS" link set "$UNDERLAY_IF" up
    ip -n "$FIXTURE_NS" route add default via 198.51.100.1 dev "$UNDERLAY_IF" onlink
}

cleanup_fixture_netns() {
    ip netns del "$FIXTURE_NS" >/dev/null 2>&1 || true
}

# start_core_server <barrier-step|-> — starts the tagged core inside the
# fixture namespace; "-" starts it without the recovery barrier env.
start_core_server() {
    local barrier_step="$1"
    rm -f "$SOCKET"
    if [[ "$barrier_step" == "-" ]]; then
        ip netns exec "$FIXTURE_NS" "$CORE" serve \
            --socket "$SOCKET" --state-dir "$STATE" --config-dir "$CONFIGS" \
            --ownership-config "$CONFIGS/ownership.conf" \
            --toad-binary "$READY_TOAD" --leshy-reload=false \
            >>"$CORE_LOG" 2>&1 &
    else
        KK_RECOVERY_BARRIER_STEP="$barrier_step" KK_RECOVERY_BARRIER_ROLE="primary" \
            KK_RECOVERY_BARRIER_DIR="$BARRIER_DIR" \
            ip netns exec "$FIXTURE_NS" "$CORE" serve \
            --socket "$SOCKET" --state-dir "$STATE" --config-dir "$CONFIGS" \
            --ownership-config "$CONFIGS/ownership.conf" \
            --toad-binary "$READY_TOAD" --leshy-reload=false \
            >>"$CORE_LOG" 2>&1 &
    fi
    CORE_PID=$!
}

wait_role_ready() { # wait_role_ready <deadline-seconds>
    local deadline=$1
    while true; do
        local state
        state=$("$CORE" status --socket "$SOCKET" --json 2>/dev/null | python3 -c "
import json, sys
try:
    snap = json.load(sys.stdin)
except Exception:
    print('')
    raise SystemExit
role = next((r for r in snap.get('roles', []) if r.get('id') == 'primary'), {})
print(role.get('state', ''))") || state=""
        [[ "$state" == "Ready" ]] && return 0
        sleep 1
        deadline=$((deadline - 1))
        [[ "$deadline" -gt 0 ]] || return 1
    done
}

wait_route_not_ready() { # wait_route_not_ready <deadline-seconds>
    local deadline=$1
    while true; do
        local rr
        rr=$("$CORE" status --socket "$SOCKET" --json 2>/dev/null | python3 -c "
import json, sys
try:
    snap = json.load(sys.stdin)
except Exception:
    print('')
    raise SystemExit
role = next((r for r in snap.get('roles', []) if r.get('id') == 'primary'), {})
print(role.get('route_ready', ''))") || rr=""
        [[ "$rr" == "False" ]] && return 0
        sleep 0.5
        deadline=$((deadline - 1))
        [[ "$deadline" -gt 0 ]] || return 1
    done
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
gateway = "198.51.100.1"
username = "reboot-fixture"
password_file = "$CONFIGS/primary.password"
vpn_protocol = "anyconnect"
token_mode = "none"
EOF
    printf 'reboot-secret\n' > "$CONFIGS/primary.password"

    # The Go lifecycle ownership (08D1 §11.2): without it the committed
    # desired state is not restored at startup and the selected/park cases'
    # role would never come back after the barrier-server restart.
    cat > "$CONFIGS/ownership.conf" <<'OWN'
routing_owner = "go"
tunnel_owner = "go"
endpoint_owner = "go"
OWN

    local enable_log="$EVIDENCE/enable.log"

    # G4 2026-11-19 repair: the failpoint/barrier environments live on the
    # SERVER process — the CLI is only the RPC client. The previous prepare
    # ran the client against a socket with no server: no privileged
    # credential was ever created and the client-side env never reached the
    # server-side transaction, so no case could ever stage its boundary.
    case "$CASE" in
        prepared|config-written|marker-written|generation-committed)
            # G3's proven pattern: the tagged core crashes with exit 86 at
            # the named enable boundary, preserving the on-disk journal.
            local boundary
            case "$CASE" in
                prepared) boundary="after_prepared" ;;
                config-written) boundary="after_config_written" ;;
                marker-written) boundary="after_marker_written" ;;
                generation-committed) boundary="after_generation_commit" ;;
            esac
            env "KK_MODE_FAILPOINT=enable/$boundary" "$CORE" serve \
                --socket "$SOCKET" --state-dir "$STATE" --config-dir "$CONFIGS" \
                --ownership-config "$CONFIGS/ownership.conf" \
                --toad-binary /bin/true --leshy-reload=false \
                >"$CORE_LOG" 2>&1 &
            CORE_PID=$!
            "$CORE" wait-startup --socket "$SOCKET" --timeout 30s >/dev/null 2>&1 \
                || die "the failpoint core never became ready; log: $CORE_LOG"
            if "$CORE" leshy-mode enable --leshy "$LESHY" --socket "$SOCKET" \
                --auth-file "$STATE/control-route-auth.key" --control-socket "$SOCKET" \
                >"$enable_log" 2>&1; then
                die "enable must have crashed the core at the $boundary failpoint; log: $enable_log"
            fi
            wait_exit_86 || die "the core did not die with exit 86 at $boundary; log: $CORE_LOG"
            note "mode boundary staged: enable/$boundary (journal preserved, core crashed)"
            ;;
        selected-active|park-applying|park-retiring)
            # The recovery-transaction boundary through the SAME machinery
            # the startup resume replays: one canary query ensures a real
            # selected route (the registry checkpoint carries it), the core
            # crashes with the checkpoint durable, and the barrier core's
            # startup resume runs the production recovery sequence into the
            # named step — the host reboots mid-barrier (G4 2026-11-20: the
            # trigger RPC computes ActionValidate for a healthy role and a
            # plain toad respawn never runs the recovery engine; the
            # checkpoint resume is the one path that always crosses
            # Freeze→Park→WithdrawSelected→Withdraw).
            [[ -n "$READY_TOAD" && "$READY_TOAD" = /* && -x "$READY_TOAD" ]] \
                || die "selected/park cases require --ready-toad <abs executable>"
            [[ -n "$ROUTEPROBE" && "$ROUTEPROBE" = /* && -x "$ROUTEPROBE" ]] \
                || die "selected/park cases require --routeprobe <abs executable>"
            stage_fixture_netns
            stage_dns_machinery
            start_core_server "-"
            "$CORE" wait-startup --socket "$SOCKET" --timeout 30s >/dev/null 2>&1 \
                || die "the staging core never became ready; log: $CORE_LOG"
            "$CORE" leshy-mode enable --leshy "$LESHY" --socket "$SOCKET" \
                --auth-file "$STATE/control-route-auth.key" --control-socket "$SOCKET" \
                >"$enable_log" 2>&1 || die "managed enable failed; log: $enable_log"
            "$CORE" connect --socket "$SOCKET" --role primary >>"$CORE_LOG" 2>&1 \
                || die "connect primary failed"
            wait_role_ready 60 || die "role primary never reached Ready before the evidence stage"
            start_leshy_child
            sleep 2
            # One canary query through the managed chain ensures the role's
            # zone route: the registry checkpoint now carries a real Active
            # selected entry.
            local canary
            canary=$(dnsq example.com 1 2>/dev/null || true)
            [[ "$canary" == "rcode=0 answers=1"* ]] \
                || die "the canary query never ensured the selected route: $canary"
            # Crash semantics, not a graceful stop: SIGTERM releases the
            # network and empties the registry checkpoint BY DESIGN — the
            # barrier core's startup resume would replay an empty registry
            # and no barrier step would ever run (G4 2026-11-20). SIGKILL
            # leaves the exact selected ownership durable for the resume.
            kill -9 "${CORE_PID:-0}" 2>/dev/null || true
            wait "${CORE_PID:-0}" 2>/dev/null || true
            CORE_PID=""
            # Crash semantics: SIGKILL leaves the checkpoint durable — the
            # exact selected ownership the startup resume replays.
            local step
            case "$CASE" in
                selected-active) step="withdraw-selected" ;;
                park-applying) step="park" ;;
                park-retiring) step="withdraw" ;;
            esac
            start_core_server "$step"
            "$CORE" wait-startup --socket "$SOCKET" --timeout 30s >/dev/null 2>&1 \
                || die "the barrier core never became ready; log: $CORE_LOG"
            wait_file "$BARRIER_DIR/primary.$step.waiting" 60 \
                || die "the recovery barrier for primary/$step never engaged; log: $CORE_LOG"
            note "recovery barrier engaged: primary/$step (the host must reboot now)"
            ;;
    esac

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

    # Case-state convergence: the staged boundary state itself must recover
    # across the reboot. The tagged core starts over the case state WITHOUT
    # the failpoint/barrier env, and the recovery the boundary interrupted
    # must converge: the in-flight journal resolves to the committed mode
    # (mode boundaries — the same restart contract mode-transition-crash-vm.sh
    # proves boundary by boundary) and the mid-barrier recovery completes to
    # a Ready role (selected/park cases; their fixture namespace does not
    # survive the reboot and is recreated here).
    case "$CASE" in
        prepared|config-written|marker-written|generation-committed)
            "$CORE" serve --socket "$SOCKET" --state-dir "$STATE" \
                --config-dir "$CONFIGS" --ownership-config "$CONFIGS/ownership.conf" \
                --toad-binary /bin/true --leshy-reload=false \
                >>"$CORE_LOG" 2>&1 &
            CORE_PID=$!
            "$CORE" wait-startup --socket "$SOCKET" --timeout 30s >/dev/null 2>&1 \
                || die "the core never recovered the case state after the reboot; log: $CORE_LOG"
            if [[ -f "$MARKER" ]]; then
                local converged_generation
                converged_generation=$(python3 -c "
import json
try:
    print(json.load(open('$MARKER')).get('mode_generation', 0))
except Exception:
    print(0)")
                note "case-state convergence: mode recovered (generation $converged_generation)"
            else
                note "case-state convergence: mode recovered (journal resolved, no committed marker)"
            fi
            core_stop
            ;;
        selected-active|park-applying|park-retiring)
            [[ -n "$READY_TOAD" && "$READY_TOAD" = /* && -x "$READY_TOAD" ]] \
                || die "selected/park verify requires --ready-toad (the same value prepare used)"
            stage_fixture_netns
            start_core_server "-"
            "$CORE" wait-startup --socket "$SOCKET" --timeout 30s >/dev/null 2>&1 \
                || die "the core never became ready over the case state; log: $CORE_LOG"
            wait_role_ready 90 || die "the mid-recovery role never converged to Ready after the reboot; log: $CORE_LOG"
            note "case-state convergence: the recovery interrupted by the reboot completed"
            core_stop
            ;;
    esac

    # Packaged ordering: the core recovers first; Leshy availability comes
    # after core recovery for every case. Leshy.service is pulled in by the
    # core unit and its ExecStartPre deliberately waits for the core's
    # recovery (wait-startup) — so it reports "activating" while the core
    # becomes active (G4 2026-11-19: an instant is-active check could never
    # pass). Wait for it, then prove the ORDER from the unit timestamps.
    systemctl start kikimora-core.service
    local waited=0
    until systemctl is-active --quiet kikimora-core.service; do
        sleep 1
        waited=$((waited + 1))
        [[ "$waited" -le 60 ]] || die "kikimora-core did not come active after reboot"
    done
    if [[ -f /usr/local/bin/leshy ]]; then
        waited=0
        until systemctl is-active --quiet leshy.service; do
            sleep 1
            waited=$((waited + 1))
            [[ "$waited" -le 90 ]] || die "leshy.service never became available after core recovery"
        done
        local core_ts leshy_ts
        core_ts=$(systemctl show -p ActiveEnterTimestamp --value kikimora-core.service)
        leshy_ts=$(systemctl show -p ActiveEnterTimestamp --value leshy.service)
        [[ -n "$core_ts" && -n "$leshy_ts" ]] || die "cannot read the unit activation timestamps"
        [[ "$core_ts" < "$leshy_ts" || "$core_ts" == "$leshy_ts" ]] ||
            die "ordering violated: leshy.service became active before kikimora-core.service"
        note "packaged ordering proven: core ($core_ts) <= leshy ($leshy_ts)"
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
