#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# installed-desktop-journey-vm.sh — the 09G-W0.2/W1 installed desktop journey
# (JRN-21/JRN-01/JRN-09): launches the real packaged /usr/bin/kikimora-ui
# through the desktop session machinery in the desktop user's REAL
# GNOME/Wayland session and drives it with AT-SPI (python3-pyatspi). This is
# NOT the offscreen component lane (guest-run-suites.sh).
#
# Phases:
#   fresh       — remove any existing package, clean product state, install
#                 the exact candidate, enable the per-user autostart entry,
#                 restart GDM so GNOME itself launches the UI inside the
#                 local active session, and run the full first-run
#                 authorization journey (W1 acceptance rows 1-9);
#   post-reboot — the rebooted session is observed for the autostart rows
#                 (--autostart off|on; W1 acceptance row 10).
#
# PLAN CONTRADICTION (resolved 2026-11-27, 09g-w1 acceptance): the W0
# contract's step 6 (sudo gtk-launch from the harness) cannot satisfy the
# W1 polkit rule's subject.local && subject.active requirements on an
# SSH-driven VM — a sudo-spawned process lands in a Remote=yes loginctl
# session (verified empirically), and polkit denies the enrollment action
# (pkexec "Not authorized", journal 20:55:52). The harness therefore
# launches the UI the way a real login does: GNOME's own xdg autostart of
# the per-user desktop entry (W1.6), which places the process in the local
# active session. The plan's launch intent (the real installed UI in the
# real desktop session, no offscreen/synthesized display) is preserved.
#
# Usage (root, on the disposable ubuntu VM):
#   sudo bash installed-desktop-journey-vm.sh \
#       --candidate /abs/kikimora_VERSION_ARCH.deb \
#       --expected-version 1.4.0 \
#       --expected-source-sha <40hex> \
#       --expected-deb-sha256 <64hex> \
#       --user greymouse \
#       --artifact-dir /abs/dir \
#       --phase fresh|post-reboot \
#       [--autostart off|on]
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
USER_AUTOSTART="/home/$JOURNEY_USER/.config/autostart/kikimora.desktop"
cleanup() {
    rm -f "$POLKIT_RULE" 2>/dev/null || true
    # Harness hygiene: the launched UI does not survive the harness run.
    # (xdg autostart only fires at session start, so nothing respawns it.)
    pkill -u "$JOURNEY_USER" -x kikimora-ui 2>/dev/null || true
}
trap cleanup EXIT

note "phase=$PHASE autostart=$AUTOSTART user=$JOURNEY_USER candidate=$CANDIDATE"

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

# ---- step 4: clean product state, install the exact candidate ----
# Stop first: a running core would make prerm set the upgrade restart marker
# and postinst would start the service again — that would defeat JRN-09.
systemctl stop kikimora-core.service leshy.service 2>/dev/null || true
# Fresh-phase hygiene only: a stale UI from a previous run must not be
# mistaken for the journey launch. The post-reboot rows OBSERVE the
# session, so killing the autostarted UI here would fake their result.
if [[ "$PHASE" == "fresh" ]]; then
    pkill -u "$JOURNEY_USER" -x kikimora-ui 2>/dev/null || true
fi
if [[ "$PHASE" == "fresh" ]]; then
    if [[ "$(dpkg -l kikimora 2>/dev/null | grep -c '^ii' || true)" -gt 0 ]]; then
        DEBIAN_FRONTEND=noninteractive apt-get remove -y kikimora >/dev/null 2>&1 || true
    fi
    rm -rf /etc/kikimora /var/lib/kikimora /run/kikimora 2>/dev/null || true
    rm -rf "/home/$JOURNEY_USER/.config/kikimora" "/home/$JOURNEY_USER/.local/share/kikimora" 2>/dev/null || true
    # The W1 journey must start from the pre-authorization state: the desktop
    # user is NOT in the kikimora socket group and no per-user autostart
    # entry exists (acceptance row 10a later proves the off state).
    deluser "$JOURNEY_USER" kikimora 2>/dev/null || true
    systemctl disable kikimora-core.service 2>/dev/null || true
    rm -f "$USER_AUTOSTART" 2>/dev/null || true
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

# ---- fresh phase: let GNOME itself launch the UI in the local active
# session (see the PLAN CONTRADICTION note in the header) ----
if [[ "$PHASE" == "fresh" ]]; then
    sudo -u "$JOURNEY_USER" mkdir -p "/home/$JOURNEY_USER/.config/autostart"
    sudo -u "$JOURNEY_USER" tee "$USER_AUTOSTART" >/dev/null <<'ENTRY'
[Desktop Entry]
Type=Application
Name=Kikimora
Exec=/usr/bin/kikimora-ui --background
Icon=kikimora
Terminal=false
X-GNOME-Autostart-enabled=true
ENTRY
    note "per-user autostart entry installed for the journey; restarting GDM"
    systemctl restart gdm3.service
fi

# ---- step 2: the user's active graphical session ----
# A desktop account typically owns several sessions (manager/user scopes);
# the journey must run inside the active Wayland/X11 one, not the first hit.
wait_graphical_session() {
    for _ in $(seq 1 60); do
        for sid in $(loginctl list-sessions --no-legend 2>/dev/null | awk -v u="$JOURNEY_USER" '{for (i = 2; i <= NF; i++) if ($i == u) print $1}'); do
            sid_type="$(loginctl show-session "$sid" -p Type --value 2>/dev/null || true)"
            sid_state="$(loginctl show-session "$sid" -p State --value 2>/dev/null || true)"
            if [[ "$sid_type" == "wayland" || "$sid_type" == "x11" ]] && [[ "$sid_state" == "active" ]]; then
                echo "$sid"
                return 0
            fi
        done
        sleep 2
    done
    return 1
}

SESSION_ID="$(wait_graphical_session)" || fail "no active graphical loginctl session for $JOURNEY_USER"
SESSION_TYPE="$(loginctl show-session "$SESSION_ID" -p Type --value)"
SESSION_STATE="$(loginctl show-session "$SESSION_ID" -p State --value)"
SESSION_REMOTE="$(loginctl show-session "$SESSION_ID" -p Remote --value)"
SESSION_DISPLAY="$(loginctl show-session "$SESSION_ID" -p Display --value)"
note "session $SESSION_ID: Type=$SESSION_TYPE State=$SESSION_STATE Remote=$SESSION_REMOTE Display=$SESSION_DISPLAY"

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

# ---- step 5: install assertions ----
if [[ "$PHASE" == "fresh" ]]; then
    if systemctl is-active --quiet kikimora-core.service; then
        fail "kikimora-core.service was pre-started after a fresh install (JRN-09)"
    fi
fi
note "core service state: $(systemctl is-active kikimora-core.service 2>/dev/null || echo unknown)/$(systemctl is-enabled kikimora-core.service 2>/dev/null || echo unknown)"
[[ -x /usr/bin/kikimora-ui ]] || fail "/usr/bin/kikimora-ui is not installed"
[[ -f /usr/share/applications/kikimora.desktop ]] || fail "kikimora.desktop is not installed"
# W1.6: the package stages no system-wide autostart entry.
[[ ! -f /etc/xdg/autostart/kikimora.desktop ]] \
    || fail "the package staged a system-wide /etc/xdg/autostart entry (W1.6 regression)"

# ---- steps 6-7: the UI process in the local active session ----
UI_PID=""
if [[ "$PHASE" == "fresh" ]]; then
    # GNOME's autostart launched the UI with the session; wait for it.
    for _ in $(seq 1 60); do
        UI_PID="$(pgrep -u "$JOURNEY_USER" -x kikimora-ui | head -1)" || true
        if [[ -n "$UI_PID" ]]; then break; fi
        sleep 1
    done
    [[ -n "$UI_PID" ]] || fail "the autostarted UI never appeared in the fresh session"
else
    # Post-reboot autostart rows: observe, do not launch.
    if [[ "$AUTOSTART" == "off" ]]; then
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
    else
        # pgrep can catch a process that just died (a zombie passes pgrep but
        # /proc/PID/exe no longer resolves), so require exe resolution too.
        ui_found=""
        for _ in $(seq 1 30); do
            UI_PID="$(pgrep -u "$JOURNEY_USER" -x kikimora-ui | head -1)" || true
            if [[ -n "$UI_PID" ]] && [[ "$(readlink -f "/proc/$UI_PID/exe" 2>/dev/null)" == "/usr/bin/kikimora-ui" ]]; then
                ui_found=1
                break
            fi
            sleep 1
        done
        if [[ -z "$ui_found" ]]; then
            fail "autostart on: kikimora-ui did not start at the next login"
        fi
        note "autostart on: the installed UI started at the next login (pid $UI_PID)"
        pkill -u "$JOURNEY_USER" -x kikimora-ui 2>/dev/null || true
    fi
fi

if [[ -n "$UI_PID" ]]; then
    UI_EXE="$(readlink -f "/proc/$UI_PID/exe")"
    [[ "$UI_EXE" == "/usr/bin/kikimora-ui" ]] \
        || fail "the UI process is not /usr/bin/kikimora-ui (got '${UI_EXE:-none}')"
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
    UI_SESSION="$(loginctl show-session "$(loginctl list-sessions --no-legend | awk -v u="$JOURNEY_USER" '{for (i = 2; i <= NF; i++) if ($i == u) print $1}' | while read -r sid; do echo "$sid"; done | head -1)" -p Id --value 2>/dev/null || true)"
    note "installed /usr/bin/kikimora-ui runs in the session (pid $UI_PID, detected session $SESSION_ID)"
fi

# ---- step 8: the AT-SPI journey as the same user with the same session ----
# The fresh phase runs the full first-run authorization journey (the same
# test that pinned first_run_unusable before W1 now REQUIRES success).
# The post-reboot phases verify the per-user autostart rows instead.
STEP_LOG="$ARTIFACT_DIR/at-spi-steps.jsonl"
JOURNEY_LOG="$ARTIFACT_DIR/ui-journey.log"
JOURNEY_ENV=(
    "XDG_RUNTIME_DIR=$XDG_RUNTIME_DIR"
    "DBUS_SESSION_BUS_ADDRESS=$DBUS_SESSION_BUS_ADDRESS"
    "XDG_SESSION_TYPE=$USER_XDG_SESSION_TYPE"
)
if [[ -n "$WAYLAND_DISPLAY" ]]; then JOURNEY_ENV+=("WAYLAND_DISPLAY=$WAYLAND_DISPLAY"); fi
if [[ -n "$USER_DISPLAY" ]]; then JOURNEY_ENV+=("DISPLAY=$USER_DISPLAY"); fi
if [[ -n "$USER_XDG_CURRENT_DESKTOP" ]]; then JOURNEY_ENV+=("XDG_CURRENT_DESKTOP=$USER_XDG_CURRENT_DESKTOP"); fi
if [[ "$PHASE" == "fresh" ]]; then
    JOURNEY_RC=0
    sudo -u "$JOURNEY_USER" env "${JOURNEY_ENV[@]}" python3 "$JOURNEY_PY" "$STEP_LOG" \
        >"$JOURNEY_LOG" 2>&1 || JOURNEY_RC=$?
else
    JOURNEY_RC=0
    : >"$JOURNEY_LOG"
    if [[ "$AUTOSTART" == "off" ]]; then
        # W1 acceptance row 10b preparation: enable the per-user autostart the
        # same way the UI's own toggle writes it, then reboot and rerun with
        # --autostart on.
        sudo -u "$JOURNEY_USER" mkdir -p "/home/$JOURNEY_USER/.config/autostart"
        sudo -u "$JOURNEY_USER" tee "$USER_AUTOSTART" >/dev/null <<'ENTRY'
[Desktop Entry]
Type=Application
Name=Kikimora
Exec=/usr/bin/kikimora-ui --background
Icon=kikimora
Terminal=false
X-GNOME-Autostart-enabled=true
ENTRY
        note "per-user autostart entry written; reboot and rerun with --autostart on"
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
    # W1 acceptance row 6: the SAME process survives and its own UI log
    # records a successful handshake AFTER the invoke timestamp (the driver
    # printed INVOKE_TS=<unix time> for this grep) — the in-session
    # reconnect proof.
    UI_LOG_DIR="/home/$JOURNEY_USER/.local/share/Kikimora/logs"
    invoke_ts="$(sed -n 's/.*INVOKE_TS=\([0-9.]*\).*/\1/p' "$JOURNEY_LOG" | tail -1)"
    [[ -n "$invoke_ts" ]] || fail "the journey driver printed no invoke timestamp"
    if ! grep -q "first_run_authorized" "$JOURNEY_LOG"; then
        fail "the journey verdict is not first_run_authorized"
    fi
    live_pid="$(pgrep -u "$JOURNEY_USER" -x kikimora-ui | head -1)" || true
    [[ -n "$live_pid" ]] || fail "the UI process died during the first-run journey"
    [[ "$live_pid" == "$UI_PID" ]]         || fail "the UI process changed identity during the journey ($UI_PID -> $live_pid)"
    ui_log="$UI_LOG_DIR/kikimora-ui-$(date +%Y%m%d).log"
    [[ -f "$ui_log" ]] || fail "the UI log is missing: $ui_log"
    # The pkexec -> helper -> systemctl enable --now -> ExecStartPost ACL
    # chain takes seconds; poll for the same-session reconnect instead of
    # racing it. The UI process stays alive the whole time (row 6).
    reconnect_ok=""
    for _ in $(seq 1 60); do
        if python3 - "$ui_log" "$invoke_ts" <<'PYRECONNECT'
import datetime
import sys

log_path, invoke_ts = sys.argv[1], float(sys.argv[2])
with open(log_path, encoding="utf-8", errors="replace") as handle:
    for line in handle:
        if '"id":"handshake"' not in line and '"id": "handshake"' not in line:
            continue
        stamp = line.split(" ", 1)[0].lstrip("[")
        try:
            when = datetime.datetime.fromisoformat(
                stamp.replace("Z", "+00:00")).timestamp()
        except Exception:
            continue
        if when >= invoke_ts and '"ok":true' in line.replace(" ", ""):
            print("PASS: a successful handshake landed after the invoke "
                  "(same-session reconnect)")
            sys.exit(0)
sys.exit(1)
PYRECONNECT
        then
            reconnect_ok=1
            break
        fi
        sleep 1
    done
    [[ -n "$reconnect_ok" ]]         || fail "no successful handshake after the Initialize invoke within 60s — the same-session reconnect did not happen"
    # The helper's enable --now may still be finishing; wait for the core it
    # activated before reading row 7's snapshot.
    core_up=""
    for _ in $(seq 1 30); do
        if systemctl is-active --quiet kikimora-core.service; then
            core_up=1
            break
        fi
        sleep 1
    done
    [[ -n "$core_up" ]] || fail "the core never activated after the authorized first run"
    /usr/local/bin/kikimora-core status --socket /run/kikimora/core.sock --json         >"$ARTIFACT_DIR/core-snapshot.json" 2>/dev/null || true
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
    # Leave the machine in the autostart-off state (acceptance row 10a).
    rm -f "$USER_AUTOSTART" 2>/dev/null || true
    note "journey authorized: the same-session Initialize reached a usable first run"
else
    [[ "$JOURNEY_RC" -eq 0 ]] || fail "the post-reboot autostart row failed (see $JOURNEY_LOG)"
fi
note "installed desktop journey: phase=$PHASE autostart=$AUTOSTART evidence in $ARTIFACT_DIR"
exit "$JOURNEY_RC"
