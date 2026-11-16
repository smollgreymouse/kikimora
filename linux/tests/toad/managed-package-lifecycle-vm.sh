#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# managed-package-lifecycle-vm.sh — the versioned Linux package lifecycle
# gate (G5 fresh install / G6 upgrades, roadmap §15.5).
#
# Disposable-fixture contract (T1R, 2026-11-16 post-e7ec9c2 audit §7.3):
#   * fresh starts only on a VM where Kikimora is NOT installed; the harness
#     never purges an unexpected operator install to manufacture a fresh row;
#   * upgrade modes start only on a clean VM, then the harness itself
#     installs --previous and stages the requested old state;
#   * candidate/previous artifacts are structurally validated READ-ONLY
#     before any service/package mutation;
#   * a failed mode restores/removes only resources it created and writes
#     failure evidence into --work-dir before exiting with the original code;
#   * the host resets/reprovisions the disposable VM baseline between
#     independent modes; the harness does not claim in-script baseline
#     restoration.
#
# Usage (root, on the Ubuntu VM):
#   sudo bash managed-package-lifecycle-vm.sh \
#       --mode <fresh|upgrade-local|upgrade-schema1-empty|upgrade-schema2|upgrade-schema1-ambiguous> \
#       --candidate /abs/kikimora_<version>_<arch>.deb \
#       [--previous /abs/kikimora_<prev>_<arch>.deb] \
#       --work-dir /abs/persistent/evidence/dir
set -Eeuo pipefail

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
HARNESS_PATH="$(readlink -f "${BASH_SOURCE[0]}")"
HARNESS_SHA256="$(sha256sum "$HARNESS_PATH" | cut -d' ' -f1)"
SOURCE_SHA="$(git -C "$(dirname "$0")/../.." rev-parse HEAD 2>/dev/null || echo unknown)"

note() { echo "== $*"; }
pass() { echo "PASS: $*"; }

# One failure/cleanup path (T1R §7.4): evidence capture before cleanup, then
# the ORIGINAL exit code. die() returns 1 so the EXIT trap still runs.
die() {
    echo "FAIL: $*" >&2
    return 1
}

write_failure_summary() {
    {
        echo "mode=$MODE rc=$1"
        echo "source sha: $SOURCE_SHA"
        echo "harness sha256: $HARNESS_SHA256"
        echo "--- dpkg kikimora state ---"
        dpkg -l kikimora 2>&1 || true
        echo "--- service states ---"
        systemctl is-active kikimora-core.service leshy.service 2>&1 || true
        echo "--- installer log tail ---"
        tail -40 "$EVIDENCE/install.log" 2>/dev/null || true
        tail -40 "$EVIDENCE/install-candidate.log" 2>/dev/null || true
        tail -40 "$EVIDENCE/install-previous.log" 2>/dev/null || true
    } >"$EVIDENCE/failure.txt" 2>&1
}

on_exit() {
    rc=$?
    if [[ $rc -ne 0 ]]; then
        write_failure_summary "$rc"
        echo "failure evidence: $EVIDENCE/failure.txt" >&2
    fi
    exit "$rc"
}
trap on_exit EXIT

# T1.1 (roadmap §17): print the source, harness and package identities at
# start. This is a package gate, so the identity that matters is the
# candidate/previous .deb checksum, not an installed binary.
note "harness identities"
note "harness: $HARNESS_PATH"
note "harness sha256: $HARNESS_SHA256"
note "source sha: $SOURCE_SHA"
for pkg in "$CANDIDATE" "$PREVIOUS"; do
    if [[ -n "$pkg" ]]; then
        note "$(basename "$pkg") sha256: $(sha256sum "$pkg" | cut -d' ' -f1)"
    fi
done

# ---- read-only preflight (T1R §7.3) ----------------------------------------

# preflight_deb structurally validates a .deb BEFORE any systemctl/apt
# command runs. A candidate that is not a Kikimora package of this
# architecture must fail the preflight, never the mutation.
preflight_deb() { # preflight_deb <path> <kind: candidate|previous>
    local path="$1" kind="$2"
    dpkg-deb --info "$path" >/dev/null 2>&1 ||
        { echo "preflight($kind): dpkg-deb --info failed for $path" >&2; return 1; }
    local package version arch
    package="$(dpkg-deb --field "$path" Package 2>/dev/null || true)"
    version="$(dpkg-deb --field "$path" Version 2>/dev/null || true)"
    arch="$(dpkg-deb --field "$path" Architecture 2>/dev/null || true)"
    [[ "$package" == "kikimora" ]] ||
        { echo "preflight($kind): Package is '$package', want kikimora" >&2; return 1; }
    [[ -n "$version" ]] ||
        { echo "preflight($kind): Version is empty" >&2; return 1; }
    [[ "$arch" == "$(dpkg --print-architecture)" ]] ||
        { echo "preflight($kind): Architecture $arch != $(dpkg --print-architecture)" >&2; return 1; }
    local contents
    contents="$(dpkg-deb --contents "$path" 2>/dev/null || true)"
    for file in /usr/local/bin/kikimora-core /usr/local/bin/kikimora-toad /usr/local/bin/leshy; do
        grep -Fq " $file\$" <<<"$contents" ||
            { echo "preflight($kind): payload is missing $file" >&2; return 1; }
    done
    # An integrated candidate must also carry the UI payload; a headless
    # candidate is only accepted for a version whose Depends say so.
    if grep -Fq " /usr/bin/kikimora-ui\$" <<<"$contents"; then
        for pair in "libqt6core6" "libqt6gui6" "libqt6quick6" "libqt6widgets6"; do
            dpkg-deb --field "$path" Depends | grep -Fq "$pair" ||
                { echo "preflight($kind): integrated deb Depends is missing $pair" >&2; return 1; }
        done
    fi
    return 0
}

preflight_deb "$CANDIDATE" candidate ||
    { echo "candidate failed the read-only preflight: $CANDIDATE" >&2; exit 2; }
if [[ -n "$PREVIOUS" ]]; then
    preflight_deb "$PREVIOUS" previous ||
        { echo "previous failed the read-only preflight: $PREVIOUS" >&2; exit 2; }
fi
pass "read-only artifact preflight"

# require_clean_host refuses a host where Kikimora is already installed:
# fresh modes precondition on a clean disposable snapshot, and the harness
# never purges an operator install to manufacture a row (T1R §7.3).
require_clean_host() {
    if dpkg -l kikimora 2>/dev/null | grep -q '^ii'; then
        echo "Kikimora is already installed; $MODE requires a clean disposable VM" >&2
        echo "reset/reprovision the VM baseline instead of purging the install" >&2
        exit 2
    fi
}

# ---- helpers ---------------------------------------------------------------

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
    require_clean_host

    DEBIAN_FRONTEND=noninteractive apt-get install -y "$CANDIDATE" >"$EVIDENCE/install.log" 2>&1 ||
        die "fresh install failed"
    assert_installed
    assert_ui_payload
    pass "fresh install payload"

    managed_smoke

    # Reboot prepare/verify uses the G4 mechanism; on a package VM the verify
    # happens after the host reboots with the same mode/case contract. The
    # candidate stays installed for that follow-up row (disposable-fixture
    # contract §7.3: a successful mode may leave the candidate installed).
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
    require_clean_host
    previous_with_state "$shape"

    DEBIAN_FRONTEND=noninteractive apt-get install -y "$CANDIDATE" >"$EVIDENCE/install-candidate.log" 2>&1
    local rc=$?
    if [[ "$shape" == "ambiguous" ]]; then
        [[ "$rc" -ne 0 ]] || die "ambiguous upgrade must fail the upgrade transaction"
        dpkg -l kikimora 2>/dev/null | grep -q '^ii' || die "aborted upgrade must keep the previous install installed"
        [[ "$(installed_version "$PREVIOUS")" == "$(dpkg-query -W -f='${Version}' kikimora 2>/dev/null)" ]] ||
            die "aborted upgrade left a different version installed"
        pass "ambiguous upgrade aborted, previous install intact"
    else
        [[ "$rc" -eq 0 ]] || die "candidate install failed"
        assert_installed
        assert_ui_payload
        wait_core_ready 90 || die "upgraded core did not become ready"
        managed_smoke
        pass "upgrade shape=$shape accepted"
    fi
}

case "$MODE" in
    fresh) mode_fresh ;;
    upgrade-local) mode_upgrade local ;;
    upgrade-schema1-empty) mode_upgrade schema1-empty ;;
    upgrade-schema2) mode_upgrade schema2 ;;
    upgrade-schema1-ambiguous) mode_upgrade ambiguous ;;
esac

note "managed-package-lifecycle-vm: mode=$MODE OK"
