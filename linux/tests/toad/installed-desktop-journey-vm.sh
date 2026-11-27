#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# installed-desktop-journey-vm.sh — the 09G-W0.2 installed desktop journey
# (JRN-21/JRN-01/JRN-09): launches the real packaged /usr/bin/kikimora-ui
# through kikimora.desktop in the desktop user's REAL GNOME/Wayland session
# and drives it with AT-SPI (python3-pyatspi). This is NOT the offscreen
# component lane (guest-run-suites.sh).
#
# The journey starts with the core stopped and no pre-added kikimora
# supplementary group; the harness never runs systemctl start before
# launching the UI (JRN-09). A VM-only polkit rule authorizes the kikimora
# enrollment action for the designated account and is removed on every exit
# (production ships the real policy/helper in W1).
#
# Phases:
#   fresh       — remove any existing package, clean product state, install
#                 the exact candidate, run the journey;
#   post-reboot — the same candidate is reinstalled in place (the VM has
#                 rebooted after the fresh phase) and the journey reruns
#                 against the rebooted session.
#
# Both phases expect the W0 verdict: the journey exits nonzero with the
# pinned reason first_run_unusable until W1 implements first-run auth.
#
# Usage (root, on the disposable ubuntu VM):
#   sudo bash installed-desktop-journey-vm.sh \
#       --candidate /abs/kikimora_VERSION_ARCH.deb \
#       --expected-version 1.3.1 \
#       --expected-source-sha <40hex> \
#       --expected-deb-sha256 <64hex> \
#       --user greymouse \
#       --artifact-dir /abs/dir \
#       --phase fresh|post-reboot
set -Eeuo pipefail

CANDIDATE=""
EXPECTED_VERSION=""
EXPECTED_SOURCE_SHA=""
EXPECTED_DEB_SHA256=""
JOURNEY_USER=""
ARTIFACT_DIR=""
PHASE="fresh"
AUTOSTART="off"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --candidate) CANDIDATE="$2"; shift 2 ;;
        --expected-version) EXPECTED_VERSION="$2"; shift 2 ;;
        --expected-source-sha) EXPECTED_SOURCE_SHA="$2"; shift 2 ;;
        --expected-deb-sha256) EXPECTED_DEB_SHA256="$2"; shift 2 ;;
        --user) JOURNEY_USER="$2"; shift 2 ;;
        --artifact-dir) ARTIFACT_DIR="$2"; shift 2 ;;
        --phase) PHASE="$2"; shift 2 ;;
        --autostart) AUTOSTART="$2"; shift 2 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

case "$PHASE" in fresh|post-reboot) ;; *) echo "--phase must be fresh|post-reboot" >&2; exit 2 ;; esac
case "$AUTOSTART" in off|on) ;; *) echo "--autostart must be off|on" >&2; exit 2 ;; esac
[[ -n "$EXPECTED_VERSION" ]] || { echo "--expected-version is required" >&2; exit 2; }
case "$JOURNEY_USER" in
    ''|[^a-z_]*|*[!a-z_0-9-]*) echo "--user must match ^[a-z_][a-z0-9_-]*\$" >&2; exit 2 ;;
esac
case "$EXPECTED_SOURCE_SHA" in
    ''|*[!0-9a-f]*) echo "--expected-source-sha must be 40 lowercase hex" >&2; exit 2 ;;
esac
[[ "${#EXPECTED_SOURCE_SHA}" -eq 40 ]] || { echo "--expected-source-sha must be 40 hex" >&2; exit 2; }
[[ "${#EXPECTED_DEB_SHA256}" -eq 64 ]] || { echo "--expected-deb-sha256 must be 64 hex" >&2; exit 2; }
case "$EXPECTED_DEB_SHA256" in *[!0-9a-f]*) echo "--expected-deb-sha256 must be 64 hex" >&2; exit 2 ;; esac
[[ -f "$CANDIDATE" && "$CANDIDATE" = /* ]] || { echo "--candidate must be an absolute file path" >&2; exit 2; }
[[ -d "$ARTIFACT_DIR" && "$ARTIFACT_DIR" = /* ]] || { echo "--artifact-dir must be an absolute directory" >&2; exit 2; }
[[ "$(id -u)" -eq 0 ]] || { echo "must run as root" >&2; exit 2; }

CANDIDATE="$(readlink -f "$CANDIDATE")"
ARTIFACT_DIR="$(readlink -f "$ARTIFACT_DIR")"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
JOURNEY_PY="$SCRIPT_DIR/../../../desktop/tests/installed_desktop_journey.py"
[[ -f "$JOURNEY_PY" ]] || { echo "journey driver missing: $JOURNEY_PY" >&2; exit 2; }

note() { echo "== $*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

JOURNEY_USER_UID="$(id -u "$JOURNEY_USER")" || fail "user $JOURNEY_USER does not exist"
RUNTIME_DIR="/run/user/$JOURNEY_USER_UID"

POLKIT_RULE="/etc/polkit-1/rules.d/49-kikimora-installed-journey.rules"
cleanup() {
    rm -f "$POLKIT_RULE" 2>/dev/null || true
    # Harness hygiene: the launched UI does not survive the harness run.
    # (xdg autostart only fires at session start, so nothing respawns it.)
    pkill -u "$JOURNEY_USER" -x kikimora-ui 2>/dev/null || true
}
trap cleanup EXIT

note "phase=$PHASE user=$JOURNEY_USER candidate=$CANDIDATE"

# ---- step 1: artifact provenance before any install/mutation ----
actual_deb_sha="$(sha256sum "$CANDIDATE" | cut -d' ' -f1)"
[[ "$actual_deb_sha" == "$EXPECTED_DEB_SHA256" ]] \
    || fail "candidate sha256 $actual_deb_sha != expected $EXPECTED_DEB_SHA256"
deb_version="$(dpkg-deb --field "$CANDIDATE" Version)"
[[ "$deb_version" == "$EXPECTED_VERSION" ]] \
    || fail "candidate Version $deb_version != expected $EXPECTED_VERSION"

PROV="$ARTIFACT_DIR/build-info.json"
dpkg-deb -x "$CANDIDATE" "$ARTIFACT_DIR/prov-extract"
cp "$ARTIFACT_DIR/prov-extract/usr/share/kikimora/build-info.json" "$PROV" \
    || fail "the candidate carries no usr/share/kikimora/build-info.json (build with 09G-W0.2 provenance)"
bi_source="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["source_sha"])' "$PROV")"
bi_leshy="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["leshy_sha"])' "$PROV")"
bi_version="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$PROV")"
[[ "$bi_version" == "$EXPECTED_VERSION" ]] \
    || fail "build-info version $bi_version != expected $EXPECTED_VERSION"
[[ "$bi_source" == "$EXPECTED_SOURCE_SHA" ]] \
    || fail "build-info source_sha $bi_source != expected $EXPECTED_SOURCE_SHA"
note "artifact provenance OK: version=$deb_version source=$bi_source leshy=$bi_leshy"

# ---- step 2: the user's active graphical session ----
# A desktop account typically owns several sessions (manager/user scopes);
# the journey must run inside the active Wayland/X11 one, not the first hit.
SESSION_ID=""
for sid in $(loginctl list-sessions --no-legend | awk -v u="$JOURNEY_USER" '{for (i = 2; i <= NF; i++) if ($i == u) {print $1}}'); do
    sid_type="$(loginctl show-session "$sid" -p Type --value)"
    sid_state="$(loginctl show-session "$sid" -p State --value)"
    if [[ "$sid_type" == "wayland" || "$sid_type" == "x11" ]] && [[ "$sid_state" == "active" ]]; then
        SESSION_ID="$sid"
        break
    fi
done
[[ -n "$SESSION_ID" ]] || fail "no active graphical loginctl session for $JOURNEY_USER"
SESSION_TYPE="$(loginctl show-session "$SESSION_ID" -p Type --value)"
SESSION_STATE="$(loginctl show-session "$SESSION_ID" -p State --value)"
SESSION_REMOTE="$(loginctl show-session "$SESSION_ID" -p Remote --value)"
SESSION_DISPLAY="$(loginctl show-session "$SESSION_ID" -p Display --value)"
note "session $SESSION_ID: Type=$SESSION_TYPE State=$SESSION_STATE Remote=$SESSION_REMOTE Display=$SESSION_DISPLAY"
[[ "$SESSION_STATE" == "active" ]] || fail "session $SESSION_ID is not active ($SESSION_STATE)"
[[ "$SESSION_REMOTE" == "no" ]] || fail "session $SESSION_ID is remote"
[[ "$SESSION_TYPE" == "wayland" || "$SESSION_TYPE" == "x11" ]] \
    || fail "session $SESSION_ID is neither Wayland nor X11 ($SESSION_TYPE)"

# ---- step 3: the existing graphical-session environment ----
XDG_RUNTIME_DIR="$RUNTIME_DIR"
DBUS_SESSION_BUS_ADDRESS="unix:path=$RUNTIME_DIR/bus"
USER_ENV="$(sudo -u "$JOURNEY_USER" env XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR" \
    DBUS_SESSION_BUS_ADDRESS="$DBUS_SESSION_BUS_ADDRESS" \
    systemctl --user show-environment)" || fail "systemctl --user show-environment failed"
WAYLAND_DISPLAY="$(sed -n 's/^WAYLAND_DISPLAY=//p' <<<"$USER_ENV")"
USER_XDG_SESSION_TYPE="$(sed -n 's/^XDG_SESSION_TYPE=//p' <<<"$USER_ENV")"
USER_DISPLAY="$(sed -n 's/^DISPLAY=//p' <<<"$USER_ENV")"
USER_XDG_CURRENT_DESKTOP="$(sed -n 's/^XDG_CURRENT_DESKTOP=//p' <<<"$USER_ENV")"
if [[ "$SESSION_TYPE" == "wayland" ]]; then
    [[ -n "$WAYLAND_DISPLAY" ]] || fail "the Wayland session has no WAYLAND_DISPLAY in the user environment"
    [[ "$USER_XDG_SESSION_TYPE" == "wayland" ]] || fail "the user environment is not wayland"
else
    [[ -n "$USER_DISPLAY" ]] || fail "the X11 session has no DISPLAY in the user environment"
    [[ "$USER_XDG_SESSION_TYPE" == "x11" ]] || fail "the user environment is not x11"
fi
note "graphical session env captured (type=$USER_XDG_SESSION_TYPE desktop=${USER_XDG_CURRENT_DESKTOP:-unset})"

# AT-SPI accessibility bus reachable from that session; never launch a second bus.
sudo -u "$JOURNEY_USER" env XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR" \
    DBUS_SESSION_BUS_ADDRESS="$DBUS_SESSION_BUS_ADDRESS" \
    gdbus call --session --dest org.a11y.Bus --object-path /org/a11y/bus \
    --method org.a11y.Bus.GetAddress >/dev/null \
    || fail "the session AT-SPI accessibility bus is not reachable"

# Qt registers its AT-SPI bridge only when the session's toolkit
# accessibility is enabled. This is a disposable-VM setup prerequisite
# (same lane as the python3-pyatspi package), never a product dependency:
# the harness fails closed instead of mutating the user's settings.
A11Y_TOOLKIT="$(sudo -u "$JOURNEY_USER" env XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR" \
    DBUS_SESSION_BUS_ADDRESS="$DBUS_SESSION_BUS_ADDRESS" \
    gsettings get org.gnome.desktop.interface toolkit-accessibility 2>/dev/null || echo unavailable)"
[[ "$A11Y_TOOLKIT" == "true" ]] \
    || fail "the session has toolkit-accessibility=$A11Y_TOOLKIT; enable it in the disposable VM setup (gsettings set org.gnome.desktop.interface toolkit-accessibility true)"

# ---- dependency preflight ----
command -v gtk-launch >/dev/null || fail "gtk-launch missing (install desktop-file-utils)"
command -v gdbus >/dev/null || fail "gdbus missing"
sudo -u "$JOURNEY_USER" env XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR" \
    DBUS_SESSION_BUS_ADDRESS="$DBUS_SESSION_BUS_ADDRESS" \
    python3 -c 'import pyatspi' || fail "python3-pyatspi missing (harness preflight)"

# ---- step 4: clean product state, install the exact candidate ----
# Stop first: a running core would make prerm set the upgrade restart marker
# and postinst would start the service again — that would defeat JRN-09.
systemctl stop kikimora-core.service leshy.service 2>/dev/null || true
pkill -u "$JOURNEY_USER" -x kikimora-ui 2>/dev/null || true
if [[ "$PHASE" == "fresh" ]]; then
    if [[ "$(dpkg -l kikimora 2>/dev/null | grep -c '^ii' || true)" -gt 0 ]]; then
        DEBIAN_FRONTEND=noninteractive apt-get remove -y kikimora >/dev/null 2>&1 || true
    fi
    rm -rf /etc/kikimora /var/lib/kikimora /run/kikimora 2>/dev/null || true
    rm -rf "/home/$JOURNEY_USER/.config/kikimora" "/home/$JOURNEY_USER/.local/share/kikimora" 2>/dev/null || true
    # The W0 journey must start from the pre-W1 auth defect state: the desktop
    # user is NOT in the kikimora socket group (nothing re-adds it).
    deluser "$JOURNEY_USER" kikimora 2>/dev/null || true
    systemctl disable kikimora-core.service 2>/dev/null || true
fi
DEBIAN_FRONTEND=noninteractive apt-get install -y --reinstall "$CANDIDATE" \
    >"$ARTIFACT_DIR/install.log" 2>&1 || fail "candidate install failed (see $ARTIFACT_DIR/install.log)"

# ---- the VM-only polkit rule (authorized enrollment only) ----
cat > "$POLKIT_RULE" <<POLKIT
polkit.addRule(function(action, subject) {
    if (action.id == "io.github.smollgreymouse.kikimora.authorize-user" &&
        subject.user == "$JOURNEY_USER" &&
        subject.local &&
        subject.active) {
        return polkit.Result.YES;
    }
});
POLKIT
chmod 0644 "$POLKIT_RULE"
note "polkit enrollment rule installed for $JOURNEY_USER (removed on every exit)"

# ---- step 5: fresh-install assertions ----
if [[ "$PHASE" == "fresh" ]]; then
    if systemctl is-active --quiet kikimora-core.service; then
        fail "kikimora-core.service was pre-started after a fresh install (JRN-09)"
    fi
fi
note "core service state after install: $(systemctl is-active kikimora-core.service 2>/dev/null || echo unknown)/$(systemctl is-enabled kikimora-core.service 2>/dev/null || echo unknown)"
[[ -x /usr/bin/kikimora-ui ]] || fail "/usr/bin/kikimora-ui is not installed"
[[ -f /usr/share/applications/kikimora.desktop ]] || fail "kikimora.desktop is not installed"

# ---- steps 6-7: launch through the real desktop entry as the user ----
JOURNEY_ENV=(
    "XDG_RUNTIME_DIR=$XDG_RUNTIME_DIR"
    "DBUS_SESSION_BUS_ADDRESS=$DBUS_SESSION_BUS_ADDRESS"
    "XDG_SESSION_TYPE=$USER_XDG_SESSION_TYPE"
)
if [[ -n "$WAYLAND_DISPLAY" ]]; then JOURNEY_ENV+=("WAYLAND_DISPLAY=$WAYLAND_DISPLAY"); fi
if [[ -n "$USER_DISPLAY" ]]; then JOURNEY_ENV+=("DISPLAY=$USER_DISPLAY"); fi
if [[ -n "$USER_XDG_CURRENT_DESKTOP" ]]; then JOURNEY_ENV+=("XDG_CURRENT_DESKTOP=$USER_XDG_CURRENT_DESKTOP"); fi

GTKLAUNCH_LOG="$ARTIFACT_DIR/gtk-launch.log"
sudo -u "$JOURNEY_USER" env "${JOURNEY_ENV[@]}" gtk-launch kikimora \
    >"$GTKLAUNCH_LOG" 2>&1 &

UI_PID=""
for _ in $(seq 1 30); do
    UI_PID="$(pgrep -u "$JOURNEY_USER" -x kikimora-ui | head -1)" || true
    if [[ -n "$UI_PID" ]]; then break; fi
    sleep 1
done
if [[ -z "$UI_PID" ]]; then
    fail "no kikimora-ui process appeared in the session within 30s (see $GTKLAUNCH_LOG)"
fi
UI_EXE="$(readlink -f "/proc/$UI_PID/exe")"
[[ "$UI_EXE" == "/usr/bin/kikimora-ui" ]] \
    || fail "the launched UI process is not /usr/bin/kikimora-ui (got '${UI_EXE:-none}')"
PROC_ENV="$(tr '\0' '\n' < "/proc/$UI_PID/environ")"
grep -Fxq "DBUS_SESSION_BUS_ADDRESS=$DBUS_SESSION_BUS_ADDRESS" <<<"$PROC_ENV" \
    || fail "the UI process lost the captured session bus"
if [[ "$SESSION_TYPE" == "wayland" ]]; then
    grep -Fxq "WAYLAND_DISPLAY=$WAYLAND_DISPLAY" <<<"$PROC_ENV" \
        || fail "the UI process lost WAYLAND_DISPLAY"
else
    grep -Fxq "DISPLAY=$USER_DISPLAY" <<<"$PROC_ENV" \
        || fail "the UI process lost DISPLAY"
fi
note "installed /usr/bin/kikimora-ui launched in the graphical session (pid $UI_PID)"

# ---- step 8: the AT-SPI journey as the same user with the same session ----
# The fresh phase runs the full first-run authorization journey (the same
# test that pinned first_run_unusable before W1 now REQUIRES success).
# The post-reboot phases verify the per-user autostart rows instead.
STEP_LOG="$ARTIFACT_DIR/at-spi-steps.jsonl"
JOURNEY_LOG="$ARTIFACT_DIR/ui-journey.log"
if [[ "$PHASE" == "fresh" ]]; then
    JOURNEY_RC=0
    sudo -u "$JOURNEY_USER" env "${JOURNEY_ENV[@]}" python3 "$JOURNEY_PY" "$STEP_LOG" \
        >"$JOURNEY_LOG" 2>&1 || JOURNEY_RC=$?
else
    JOURNEY_RC=0
    : >"$JOURNEY_LOG"
    if [[ "$AUTOSTART" == "off" ]]; then
        # W1 acceptance row 10a: autostart off -> next login has no UI. The
        # package no longer stages any system-wide entry and the per-user
        # entry does not exist yet. Give the session 30s to prove the absence.
        ui_found=""
        for _ in $(seq 1 30); do
            if pgrep -u "$JOURNEY_USER" -x kikimora-ui >/dev/null 2>&1; then
                ui_found=1
                break
            fi
            sleep 1
        done
        if [[ -n "$ui_found" ]]; then
            fail "autostart off: kikimora-ui is running at the next login (a per-user entry exists?)"
        fi
        note "autostart off: no UI at the next login (30s observed)"
        # W1 acceptance row 10b preparation: enable the per-user autostart the
        # same way the UI's own toggle writes it, then reboot and rerun with
        # --autostart on.
        sudo -u "$JOURNEY_USER" mkdir -p "/home/$JOURNEY_USER/.config/autostart"
        sudo -u "$JOURNEY_USER" tee "/home/$JOURNEY_USER/.config/autostart/kikimora.desktop" >/dev/null <<'ENTRY'
[Desktop Entry]
Type=Application
Name=Kikimora
Exec=/usr/bin/kikimora-ui --background
Icon=kikimora
Terminal=false
X-GNOME-Autostart-enabled=true
ENTRY
        note "per-user autostart entry written; reboot and rerun with --autostart on"
    else
        # W1 acceptance row 10b: autostart on -> next login has the UI.
        ui_found=""
        for _ in $(seq 1 30); do
            UI_PID="$(pgrep -u "$JOURNEY_USER" -x kikimora-ui | head -1)" || true
            if [[ -n "$UI_PID" ]]; then
                ui_found=1
                break
            fi
            sleep 1
        done
        [[ -n "$ui_found" ]] || fail "autostart on: kikimora-ui did not start at the next login"
        ui_exe="$(readlink -f "/proc/$UI_PID/exe")"
        [[ "$ui_exe" == "/usr/bin/kikimora-ui" ]] \
            || fail "the autostarted UI is not /usr/bin/kikimora-ui (got '$ui_exe')"
        note "autostart on: the installed UI started at the next login (pid $UI_PID)"
        pkill -u "$JOURNEY_USER" -x kikimora-ui 2>/dev/null || true
    fi
fi

# ---- step 9: collect evidence ----
journalctl -u kikimora-core.service -b >"$ARTIFACT_DIR/core-journal.log" 2>&1 || true
systemctl status kikimora-core.service --no-pager >"$ARTIFACT_DIR/core-status.log" 2>&1 || true
/usr/local/bin/kikimora-core status --socket /run/kikimora/core.sock --json \
    >"$ARTIFACT_DIR/core-snapshot.json" 2>/dev/null || true
cp "$POLKIT_RULE" "$ARTIFACT_DIR/polkit-rule.used" 2>/dev/null || true

# ---- step 10: verdict ----
if [[ "$PHASE" == "fresh" ]]; then
    if [[ "$JOURNEY_RC" -ne 0 ]]; then
        fail "the journey did not authorize the first run (exit $JOURNEY_RC, see $JOURNEY_LOG)"
    fi
    grep -q "first_run_authorized" "$JOURNEY_LOG" \
        || fail "the journey success is not the pinned first_run_authorized reason (see $JOURNEY_LOG)"
    # W1 acceptance row 8: the authorized user needed NO new supplementary group.
    if id -nG "$JOURNEY_USER" | grep -qw kikimora; then
        fail "the journey user acquired the kikimora supplementary group (the old authorization contract)"
    fi
    # W1 acceptance row 7: no VPN role is desired or connected after the first run.
    python3 - "$ARTIFACT_DIR/core-snapshot.json" <<'PYVERIFY'
import json
import sys

try:
    snapshot = json.load(open(sys.argv[1]))
except Exception as error:
    sys.exit("FAIL: the core snapshot is unreadable after the authorized first run: %s" % error)
roles = snapshot.get("roles", [])
if roles:
    sys.exit("FAIL: the fresh install carries roles after the first run: %s"
             % [role.get("id") for role in roles])
if not snapshot.get("startup_ready"):
    sys.exit("FAIL: the core is not startup_ready after the authorized first run")
print("PASS: core startup_ready with zero roles after the authorized first run")
PYVERIFY
    note "journey authorized: the same-session Initialize reached a usable first run"
else
    [[ "$JOURNEY_RC" -eq 0 ]] || fail "the post-reboot autostart row failed (see $JOURNEY_LOG)"
fi
note "installed desktop journey: phase=$PHASE autostart=$AUTOSTART evidence in $ARTIFACT_DIR"
exit "$JOURNEY_RC"
