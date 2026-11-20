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
PHASE="prepare"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --mode) MODE="$2"; shift 2 ;;
        --candidate) CANDIDATE="$2"; shift 2 ;;
        --previous) PREVIOUS="$2"; shift 2 ;;
        --work-dir) WORK_DIR="$2"; shift 2 ;;
        --phase) PHASE="$2"; shift 2 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

case "$PHASE" in
    prepare|verify) ;;
    *) echo "--phase must be prepare|verify" >&2; exit 2 ;;
esac

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
        # dpkg-deb --contents paths are " ./usr/..." — the entry is prefixed
        # with "./", and $file already begins with "/", so the anchor must be
        # a space + "./" + $file WITHOUT its leading slash (G5 2026-11-19:
        # the -F form turned "$" into a literal; the first regex attempt
        # doubled the separator).
        grep -Eq "[[:space:]]\./${file#/}\$" <<<"$contents" ||
            { echo "preflight($kind): payload is missing $file" >&2; return 1; }
    done
    # An integrated candidate must also carry the UI payload; a headless
    # candidate is only accepted for a version whose Depends say so.
    if grep -Eq "[[:space:]]\./usr/bin/kikimora-ui\$" <<<"$contents"; then
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
    # Debian dependency alternatives may declare either name first (the
    # time64 transition produced both orderings in the wild — G5 2026-11-19:
    # the candidate declares "libqt6quick6 | libqt6quick6t64"), so the Qt6
    # runtime groups are matched semantically — some dependency group must
    # offer one of the two names — never a literal pair string.
    python3 - "$deps" <<'PY' || die "integrated deb Depends is missing a Qt6 runtime group (core/gui/quick/widgets)"
import sys

groups = [g.strip() for g in sys.argv[1].split(",") if g.strip()]
alts = [set(a.strip() for a in g.split("|")) for g in groups]
for plain, t64 in (
    ("libqt6core6", "libqt6core6t64"),
    ("libqt6gui6", "libqt6gui6t64"),
    ("libqt6quick6", "libqt6quick6t64"),
    ("libqt6widgets6", "libqt6widgets6t64"),
):
    if not any(plain in a or t64 in a for a in alts):
        sys.exit(f"missing {plain}/{t64} in the Depends alternatives")
PY
}

# managed smoke: the installed package enables managed mode against its own
# runtime and reports the committed generation.
managed_smoke() {
    note "managed smoke on the installed package"
    assert_installed
    # A fresh install intentionally leaves the unit inactive: the deb's
    # postinst starts the service only across an upgrade (the
    # restart-after-upgrade marker) — the smoke starts it for its own
    # transaction (G5 2026-11-20).
    systemctl start kikimora-core.service 2>/dev/null || true
    wait_core_ready 60 || die "installed core did not report startup ready"
    # The fresh install starts the local Leshy service, which owns the shared
    # writer lock: both transactions refuse while it runs (the product
    # failing closed). Stop it before each transaction — the product's own
    # reload (systemctl restart) works from a stopped state and brings Leshy
    # back with the generated config (G5 2026-11-20).
    systemctl stop leshy.service 2>/dev/null || true
    /usr/local/bin/kikimora-core leshy-mode enable \
        --leshy /usr/local/bin/leshy --socket /run/kikimora/core.sock \
        --auth-file /var/lib/kikimora/core/control-route-auth.key \
        --control-socket /run/kikimora/core.sock >"$EVIDENCE/managed-enable.log" 2>&1 ||
        die "managed enable failed on the installed package (see $EVIDENCE/managed-enable.log)"
    systemctl stop leshy.service 2>/dev/null || true
    /usr/local/bin/kikimora-core leshy-mode disable \
        --socket /run/kikimora/core.sock --auth-file /var/lib/kikimora/core/control-route-auth.key \
        >"$EVIDENCE/managed-disable.log" 2>&1 ||
        die "managed disable failed"
    systemctl start leshy.service 2>/dev/null || true
    pass "managed smoke"
}

# schema upgrade states ------------------------------------------------------

# previous_with_state installs the previous .deb and leaves a durable state
# of the requested shape behind (local / schema1-empty / schema2 / ambiguous).
previous_with_state() { # previous_with_state <local|schema1-empty|schema2|ambiguous>
    local shape="$1"
    note "installing previous package for shape=$shape"
    # --allow-downgrades (plural): apt ≥ 3.x (ubuntu 26.04 carries 3.2)
    # rejects the singular form outright (G6 2026-11-19).
    DEBIAN_FRONTEND=noninteractive apt-get install -y --allow-downgrades "$PREVIOUS" >"$EVIDENCE/install-previous.log" 2>&1 ||
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
            # The ambiguous state the product actually refuses: a schema-1
            # legacy transition journal in the state root whose journaled
            # config digest no longer matches the durable generated config —
            # upgradeLegacyTransitionJournal cannot prove the relation and
            # fails the startup closed (G6 2026-11-20; the earlier
            # leshy-managed-mode pair is read by no product code).
            mkdir -p /var/lib/kikimora/core
            printf '%s' '{"schema":1,"id":"0123456789abcdef0123456789abcdef","from":"local","to":"managed","phase":"config_written","from_generation":6,"to_generation":7,"to_activation_id":"fedcba9876543210fedcba9876543210","socket":"/run/kikimora/leshy.sock","zones":["primary"],"config_digest_version":1,"config_sha256":"0000000000000000000000000000000000000000000000000000000000000000"}' > /var/lib/kikimora/core/leshy-mode-transition.json
            ;;
    esac
}

# ---- modes -----------------------------------------------------------------

mode_fresh() {
    note "G5 fresh install (phase=$PHASE)"
    if [[ "$PHASE" == "verify" ]]; then
        # The post-reboot half of the fresh row: the same generation-committed
        # case the prepare staged, verified against the installed candidate
        # (G5 2026-11-20: the verify dispatch was documented in a note but
        # never implemented — a re-run would have hit require_clean_host).
        local case_dir="$WORK_DIR/fresh-reboot"
        bash "$(dirname "$0")/managed-reboot-matrix-vm.sh" \
            --case generation-committed --phase verify \
            --core /usr/local/bin/kikimora-core --leshy /usr/local/bin/leshy \
            --work-dir "$case_dir" >"$EVIDENCE/reboot-verify.log" 2>&1 ||
            die "reboot verify failed (see $EVIDENCE/reboot-verify.log)"
        assert_installed
        assert_ui_payload
        pass "G5 fresh verify complete: the installed package survived the reboot recovery"
        return 0
    fi
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
    note "REBOOT REQUIRED: re-run with --phase verify after the VM reboots"
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
        # The product's abort surfaces as a failed core start: the candidate's
        # startup migration refuses the ambiguous schema-1 journal and the new
        # core never becomes ready. The refusal is named in the unit journal;
        # the ambiguous journal itself must stay on disk unconsumed. The
        # version note: dpkg reports the candidate version after a
        # post-install refusal — the preservation this row proves is the
        # refused state (the journal + the package installed and stopped),
        # recorded as a deviation from the roadmap's version-preservation
        # wording (G6 2026-11-20).
        if [[ "$rc" -ne 0 ]]; then
            pass "ambiguous upgrade aborted the apt transaction (rc=$rc)"
        fi
        wait_core_ready 90 && die "the ambiguous upgrade must refuse to start the migrated core"
        local refuse_count
        refuse_count=$(journalctl -u kikimora-core --no-pager 2>/dev/null | grep -c "does not match the journaled" || true)
        note "migration refusal lines in the unit journal: $refuse_count"
        [[ "${refuse_count:-0}" -gt 0 ]] ||
            die "the migration refusal is not named in the core journal"
        [[ -f /var/lib/kikimora/core/leshy-mode-transition.json ]] ||
            die "the ambiguous journal was consumed by the refused migration"
        dpkg -l kikimora 2>/dev/null | grep -q '^ii' ||
            die "the package must stay installed after the refusal"
        pass "ambiguous state refused the migration; the journal and the package are preserved"
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
