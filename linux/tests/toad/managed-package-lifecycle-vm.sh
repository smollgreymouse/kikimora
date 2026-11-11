#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# managed-package-lifecycle-vm.sh — the versioned Linux package lifecycle
# gate (G5 fresh install / G6 upgrades, roadmap §15.5). Every mode is an
# independent run that records and restores its own baseline; nothing here is
# accepted from hand-entered apt/dpkg sequences.
#
# Usage (root, on the Ubuntu VM):
#   sudo bash managed-package-lifecycle-vm.sh \
#       --mode <fresh|upgrade-local|upgrade-schema1-empty|upgrade-schema2|upgrade-schema1-ambiguous> \
#       --candidate /abs/kikimora_<version>_<arch>.deb \
#       [--previous /abs/kikimora_<prev>_<arch>.deb] \
#       --work-dir /abs/persistent/evidence/dir
set -u

MODE=""
CANDIDATE=""
PREVIOUS=""
WORK_DIR=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --mode) MODE="$2"; shift 2 ;;
        --candidate) CANDIDATE="$2"; shift 2 ;;
        --previous) PREVIOUS="$2"; shift 2 ;;
        --work-dir) WORK_DIR="$2"; shift 2 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

case "$MODE" in
    fresh|upgrade-local|upgrade-schema1-empty|upgrade-schema2|upgrade-schema1-ambiguous) ;;
    *) echo "--mode must be fresh|upgrade-local|upgrade-schema1-empty|upgrade-schema2|upgrade-schema1-ambiguous" >&2; exit 2 ;;
esac
[[ -n "$CANDIDATE" && "$CANDIDATE" = /* && -f "$CANDIDATE" ]] || { echo "--candidate must be an absolute .deb path" >&2; exit 2; }
[[ "$(id -u)" -eq 0 ]] || { echo "must run as root" >&2; exit 2; }
[[ "$MODE" == "fresh" || ( -n "$PREVIOUS" && "$PREVIOUS" = /* && -f "$PREVIOUS" ) ]] ||
    { echo "--previous is required for upgrade modes" >&2; exit 2; }
[[ -n "$WORK_DIR" && "$WORK_DIR" = /* ]] || { echo "--work-dir must be an absolute path" >&2; exit 2; }

CANDIDATE=$(readlink -f "$CANDIDATE")
[[ -n "$PREVIOUS" ]] && PREVIOUS=$(readlink -f "$PREVIOUS")
mkdir -p "$WORK_DIR"
WORK_DIR=$(readlink -f "$WORK_DIR")
EVIDENCE="$WORK_DIR/$MODE"
mkdir -p "$EVIDENCE"
SOURCE_SHA="$(git -C "$(dirname "$0")/../.." rev-parse HEAD 2>/dev/null || echo unknown)"

note() { echo "== $*"; }
pass() { echo "PASS: $*"; }
die() {
    echo "FAIL: $*" >&2
    exit 1
}

# ---- helpers ---------------------------------------------------------------

# record_baseline snapshots the installed state so every mode restores it.
record_baseline() {
    {
        echo "sha=$SOURCE_SHA"
        dpkg -l kikimora 2>/dev/null | tail -1 || echo "kikimora not installed"
        systemctl is-active kikimora-core.service leshy.service 2>&1 || true
    } >"$EVIDENCE/baseline.txt" 2>&1
}

restore_baseline() {
    note "restoring baseline"
    if dpkg -l kikimora 2>/dev/null | grep -q '^ii'; then
        :
    else
        apt-get remove -y kikimora >/dev/null 2>&1 || true
        apt-get purge -y kikimora >/dev/null 2>&1 || true
    fi
    systemctl start kikimora-core.service >/dev/null 2>&1 || true
    systemctl start leshy.service >/dev/null 2>&1 || true
}

wait_core_ready() { # wait_core_ready <seconds>
    local waited=0
    while [[ "$waited" -lt "$1" ]]; do
        if systemctl is-active --quiet kikimora-core.service &&
           /usr/local/bin/kikimora-core wait-startup --socket /run/kikimora/core.sock --timeout 2s >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done
    return 1
}

installed_version() {
    dpkg-deb --field "$1" Version 2>/dev/null || echo ""
}

assert_installed() {
    dpkg -l kikimora 2>/dev/null | grep -q '^ii' || die "kikimora is not installed"
    [[ -x /usr/local/bin/kikimora-core ]] || die "kikimora-core binary missing"
    [[ -x /usr/local/bin/kikimora-toad ]] || die "kikimora-toad binary missing"
    [[ -x /usr/local/bin/leshy ]] || die "leshy binary missing"
}

assert_ui_payload() {
    [[ -x /usr/bin/kikimora-ui ]] || die "integrated package lost /usr/bin/kikimora-ui"
    [[ -f /usr/share/applications/kikimora.desktop ]] || die "desktop entry missing"
    [[ -f /etc/xdg/autostart/kikimora.desktop ]] || die "XDG autostart entry missing"
    [[ -f /usr/share/icons/hicolor/256x256/apps/kikimora.png ]] || die "icon missing"
    local deps
    deps="$(dpkg-deb --field "$CANDIDATE" Depends)"
    for pair in "libqt6core6t64 | libqt6core6" "libqt6gui6t64 | libqt6gui6" \
                "libqt6quick6t64 | libqt6quick6" "libqt6widgets6t64 | libqt6widgets6"; do
        grep -Fq "$pair" <<<"$deps" || die "integrated deb Depends missing '$pair'"
    done
}

# managed smoke: the installed package enables managed mode against its own
# runtime and reports the committed generation.
managed_smoke() {
    note "managed smoke on the installed package"
    assert_installed
    wait_core_ready 60 || die "installed core did not report startup ready"
    /usr/local/bin/kikimora-core leshy-mode enable \
        --leshy /usr/local/bin/leshy --socket /run/kikimora/core.sock \
        --auth-file /var/lib/kikimora/state/control-route-auth.key \
        --control-socket /run/kikimora/core.sock >"$EVIDENCE/managed-enable.log" 2>&1 ||
        die "managed enable failed on the installed package (see $EVIDENCE/managed-enable.log)"
    /usr/local/bin/kikimora-core leshy-mode disable \
        --socket /run/kikimora/core.sock --auth-file /var/lib/kikimora/state/control-route-auth.key \
        >"$EVIDENCE/managed-disable.log" 2>&1 ||
        die "managed disable failed"
    pass "managed smoke"
}

# schema upgrade states ------------------------------------------------------

# previous_with_state installs the previous .deb and leaves a durable state
# of the requested shape behind (local / schema1-empty / schema2 / ambiguous).
previous_with_state() { # previous_with_state <local|schema1-empty|schema2|ambiguous>
    local shape="$1"
    note "installing previous package for shape=$shape"
    DEBIAN_FRONTEND=noninteractive apt-get install -y --allow-downgrade "$PREVIOUS" >"$EVIDENCE/install-previous.log" 2>&1 ||
        die "previous package install failed"
    systemctl start kikimora-core.service >/dev/null 2>&1 || true
    wait_core_ready 60 || die "previous core did not become ready"

    case "$shape" in
        local)
            # A local-mode install: owned files exist, no managed marker.
            rm -f /var/lib/kikimora/state/leshy-managed-mode.json
            ;;
        schema1-empty)
            mkdir -p /var/lib/kikimora/state
            printf '{"schema":1,"mode_generation":0}\n' > /var/lib/kikimora/state/leshy-managed-mode.json
            ;;
        schema2)
            mkdir -p /var/lib/kikimora/state
            printf '{"schema":2,"mode_generation":4,"mode":"managed"}\n' > /var/lib/kikimora/state/leshy-managed-mode.json
            ;;
        ambiguous)
            # Two schema1 generations with no clear winner: the upgrade must
            # abort and keep the previous install intact.
            mkdir -p /var/lib/kikimora/state
            printf '{"schema":1,"mode_generation":7}\n' > /var/lib/kikimora/state/leshy-managed-mode.json
            printf '{"schema":1,"mode_generation":7}\n' > /var/lib/kikimora/state/leshy-managed-mode.conflict.json
            ;;
    esac
}

# ---- modes -----------------------------------------------------------------

mode_fresh() {
    note "G5 fresh install"
    # A clean slate for the package only; the VM's own files stay untouched
    # outside the package's owned resources.
    systemctl stop kikimora-core.service leshy.service >/dev/null 2>&1 || true
    apt-get remove -y kikimora >/dev/null 2>&1 || true
    apt-get purge -y kikimora >/dev/null 2>&1 || true

    DEBIAN_FRONTEND=noninteractive apt-get install -y "$CANDIDATE" >"$EVIDENCE/install.log" 2>&1 ||
        die "fresh install failed"
    assert_installed
    assert_ui_payload
    pass "fresh install payload"

    managed_smoke

    # Reboot prepare/verify uses the G4 mechanism; on a package VM the verify
    # happens after the host reboots with the same mode/case contract.
    local case_dir="$WORK_DIR/fresh-reboot"
    bash "$(dirname "$0")/managed-reboot-matrix-vm.sh" \
        --case generation-committed --phase prepare \
        --core /usr/local/bin/kikimora-core --leshy /usr/local/bin/leshy \
        --work-dir "$case_dir" >"$EVIDENCE/reboot-prepare.log" 2>&1 ||
        die "reboot prepare failed (see $EVIDENCE/reboot-prepare.log)"
    note "REBOOT REQUIRED: run the same command with --phase verify after the VM reboots"
    return 0
}

mode_upgrade() { # mode_upgrade <shape>
    local shape="$1"
    note "G6 upgrade mode=$MODE shape=$shape"
    record_baseline
    previous_with_state "$shape"

    DEBIAN_FRONTEND=noninteractive apt-get install -y "$CANDIDATE" >"$EVIDENCE/install-candidate.log" 2>&1
    local rc=$?
    if [[ "$shape" == "ambiguous" ]]; then
        [[ "$rc" -ne 0 ]] || die "ambiguous upgrade must fail the upgrade transaction"
        dpkg -l kikimora 2>/dev/null | grep -q '^ii' || die "aborted upgrade must keep the previous install installed"
        [[ "$(installed_version "$PREVIOUS")" == "$(dpkg-query -W -f='${Version}' kikimora 2>/dev/null)" ]] ||
            die "aborted upgrade left a different version installed"
        pass "ambiguous upgrade aborted, previous install restored"
    else
        [[ "$rc" -eq 0 ]] || die "candidate install failed"
        assert_installed
        assert_ui_payload
        wait_core_ready 90 || die "upgraded core did not become ready"
        managed_smoke
        pass "upgrade shape=$shape accepted"
    fi
    restore_baseline
}

case "$MODE" in
    fresh) mode_fresh ;;
    upgrade-local) mode_upgrade local ;;
    upgrade-schema1-empty) mode_upgrade schema1-empty ;;
    upgrade-schema2) mode_upgrade schema2 ;;
    upgrade-schema1-ambiguous) mode_upgrade ambiguous ;;
esac

note "managed-package-lifecycle-vm: mode=$MODE OK"
