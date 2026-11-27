#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""09G-W0.2/W1 installed desktop journey driver.

Runs INSIDE the real desktop user's graphical session (launched by
linux/tests/toad/installed-desktop-journey-vm.sh) and drives the real
installed kikimora-ui through the AT-SPI accessibility tree (pyatspi).
Controls are located by Accessible.name and accessible role only — never
by QML objectName or pixel coordinates.

W1 acceptance form (the W0 pin was here until W1 landed; the same test
was updated to require success, no bypass variant was created):

  1. the UI launches (the Kikimora application appears in AT-SPI);
  2. the fresh UI is Unavailable with the typed access-denied explanation
     and an Initialize action — never the API-incompatibility text;
  3. one polkit authorization: the journey presses the Initialize button;
  4. the SAME already-running UI process reaches a usable state in this
     session (the Unavailable banner leaves the accessibility tree);
  5. the driver exits 0 with the reason first_run_authorized.

Every step is appended as a JSONL record {"step":..., "ok":..., "detail":...}
to the step log passed as argv[1]; the final reason is always printed to
stdout so the VM harness can pin the verdict.
"""

import json
import re
import signal
import sys
import time

import pyatspi

APP_NAME = "Kikimora"
READY_TEXT = "Ready"
# How long one polkit authorization may take end to end
# (pkexec dialog -> helper -> systemctl enable --now -> ACL -> redial).
READY_TIMEOUT = 90.0
# A same-session initialization action is anything that claims to bring the
# VPN up or authorize this user session (the connect control names itself
# from action/state text, e.g. "Connect"/"Подключить").
INIT_NAME_RE = re.compile(
    r"(connect|подключ|активир|activat|enroll|authoriz|init)", re.IGNORECASE)
APP_TIMEOUT = 45.0
WINDOW_TIMEOUT = 15.0
INVOKE_TIMEOUT = 12.0
HARD_DEADLINE = 150.0

EXIT_PINNED_FAILURE = 3
EXIT_AUTHORIZED = 0

_step_log = sys.argv[1] if len(sys.argv) > 1 else None
_failures = []


def log_step(step, ok, detail):
    record = {"step": step, "ok": bool(ok), "detail": str(detail)}
    line = json.dumps(record, ensure_ascii=False)
    if _step_log:
        with open(_step_log, "a", encoding="utf-8") as handle:
            handle.write(line + "\n")
    print(("OK   " if ok else "BAD  ") + line, flush=True)
    if not ok:
        _failures.append(record)
    return record


# ---------------------------------------------------------------- helpers

def wait_until(predicate, timeout, interval=0.5):
    """Poll predicate until it returns a truthy value or the timeout passes."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = predicate()
        if result:
            return result
        time.sleep(interval)
    return None


def _children(node):
    try:
        count = node.childCount
    except Exception:
        return []
    result = []
    for index in range(min(count, 64)):
        try:
            child = node.getChildAtIndex(index)
        except Exception:
            continue
        if child is not None:
            result.append(child)
    return result


def walk(root, max_depth=25):
    """Depth-first walk of the accessible tree, cycle- and error-safe."""
    seen = set()
    stack = [(root, 0)]
    while stack:
        node, depth = stack.pop()
        if depth > max_depth or node is None:
            continue
        try:
            path = node.get_path()
        except Exception:
            path = None
        key = (id(type(node)), path) if path is not None else id(node)
        if key in seen:
            continue
        seen.add(key)
        yield node
        if depth < max_depth:
            for child in reversed(_children(node)):
                stack.append((child, depth + 1))


def wait_application(name, timeout):
    """Wait for an accessible application with the exact given name."""
    def scan():
        try:
            desktop = pyatspi.Registry.getDesktop(0)
        except Exception as error:
            log_step("at-spi-desktop", False, "getDesktop failed: %s" % error)
            return None
        for app in _children(desktop):
            try:
                if app.getRoleName() == "application" and app.name == name:
                    return app
            except Exception:
                continue
        return None
    return wait_until(scan, timeout)


def find_by_name(root, accessible_name_or_object_name, role=None):
    """First accessible node whose Accessible.name matches exactly."""
    for node in walk(root):
        try:
            if node.name != accessible_name_or_object_name:
                continue
        except Exception:
            continue
        if role is not None and node.getRoleName() != role:
            continue
        return node
    return None


def node_text(node):
    try:
        return node.queryText().getText(0, -1)
    except Exception:
        return None


def invoke_action(node, action_name="click"):
    """Invoke the named doAction (default click); returns True on success.

    Qt's AT-SPI bridge names the canonical press action "Press", so a click
    request also matches press/activate case-insensitively.
    """
    try:
        action = node.queryAction()
    except Exception as error:
        raise RuntimeError("%r exposes no action interface: %s" % (node, error))
    available = [action.getName(i) for i in range(action.nActions)]
    candidates = [action_name.lower()]
    if action_name.lower() == "click":
        candidates += ["press", "activate"]
    for index, name in enumerate(available):
        if name.lower() in candidates:
            action.doAction(index)
            return True
    raise RuntimeError("%r has no %r action (has %r)" % (node, action_name, available))


def set_text(node, text):
    """Set text content on an editable text node."""
    editable = node.queryEditableText()
    for setter in ("setTextContent", "setTextContents"):
        method = getattr(editable, setter, None)
        if method is not None:
            if method(text):
                return True
    raise RuntimeError("%r exposes no usable editable-text setter" % (node,))


def assert_text(node, expected_substring):
    """Assert node text contains expected_substring; returns the actual text."""
    text = node_text(node)
    if text is None:
        raise RuntimeError("%r exposes no text" % (node,))
    if expected_substring not in text:
        raise RuntimeError("%r text %r does not contain %r"
                           % (node, text, expected_substring))
    return text


# ------------------------------------------------------------ journey steps

def collect_display_strings(root):
    """Every string the UI exposes: accessible names plus text content.

    Qt Quick exposes a Text item's string as the accessible NAME (queryText
    is often unsupported), so a state chip renders "Ready" through the name
    channel; scanning only the text interface would be a vacuous pass.
    """
    strings = []
    for node in walk(root):
        try:
            name = node.name
        except Exception:
            name = None
        if name and len(name) < 80:
            strings.append(name)
        text = node_text(node)
        if text and len(text) < 80:
            strings.append(text)
    return strings


def find_window(app):
    for node in walk(app):
        if node.getRoleName() == "frame":
            return node
    return None




def window_attributes(window):
    try:
        return dict(item.split(":", 1) for item in window.getAttributes() if ":" in item)
    except Exception:
        return {}


def run_journey():
    # --- 1. the UI launches ---
    app = wait_application(APP_NAME, APP_TIMEOUT)
    if app is None:
        log_step("ui-launches", False,
                 "first_run_unusable: application %r never appeared in AT-SPI "
                 "within %.0fs" % (APP_NAME, APP_TIMEOUT))
        return
    window = wait_until(lambda: find_window(app), WINDOW_TIMEOUT)
    if window is None:
        log_step("ui-launches", False,
                 "first_run_unusable: %r appeared but exposes no frame window" % APP_NAME)
        return
    log_step("ui-launches", True,
             "application %r is up with frame %r; attributes %s"
             % (APP_NAME, window.name, window_attributes(window)))

    # --- 2. the fresh UI is Unavailable with the typed access-denied
    # explanation and an Initialize action (09G-W1.4/W1.5); never the
    # API-incompatibility wording ---
    unavailable_banner = wait_until(
        lambda: find_by_name(window, "coreUnavailableBanner"), WINDOW_TIMEOUT)
    if unavailable_banner is None:
        strings = sorted(set(collect_display_strings(window)))[:40]
        log_step("unavailable-explanation", False,
                 "first_run_unusable: the coreUnavailableBanner never appeared; "
                 "visible strings: %s" % (strings,))
        return
    strings = collect_display_strings(window)
    denied = any("авторизация" in s for s in strings)
    incompatible = any("несовместимо" in s for s in strings)
    log_step("unavailable-explanation", denied and not incompatible,
             "typed-transport state: access-denied explanation present=%s, "
             "API-incompatibility wording present=%s" % (denied, incompatible))

    button = wait_until(lambda: find_by_name(window, "initializeKikimoraButton"),
                        WINDOW_TIMEOUT)
    if button is None:
        log_step("initialize-action", False,
                 "first_run_unusable: initializeKikimoraButton never appeared")
        return

    # --- 3. one polkit authorization through the real helper ---
    try:
        invoked = invoke_action(button, "click")
    except RuntimeError as error:
        log_step("initialize-invoke", False,
                 "first_run_unusable: the Initialize action could not be "
                 "invoked: %s" % error)
        return
    log_step("initialize-invoke", True,
             "the Initialize action was invoked (pkexec -> helper -> registry "
             "-> enable --now -> ACL)")

    # --- 4. the same already-running UI leaves the Unavailable state in this
    # session: the banner disappears from the accessibility tree ---
    gone = wait_until(
        lambda: find_by_name(window, "coreUnavailableBanner") is None,
        READY_TIMEOUT, interval=1.0)
    log_step("same-session-ready", bool(gone),
             "the Unavailable banner left the accessibility tree of the SAME "
             "running process within %.0fs: %s" % (READY_TIMEOUT, bool(gone)))

    # A fresh install carries zero roles: no role/state text may say Ready
    # after the authorization.
    strings_after = collect_display_strings(window)
    ready_hits = [s for s in strings_after if s.strip() == READY_TEXT]
    log_step("no-role-ready", not ready_hits,
             "post-authorization role scan: %d display strings, %d exact "
             "'%s' role hits (a fresh install has no roles)" %
             (len(strings_after), len(ready_hits), READY_TEXT))

    held = not _failures
    log_step("verdict", held,
             "first_run_authorized: the same-session initialization completed "
             "(assertions %s)" % ("held" if held else
                                  "violated: %s" % [r["step"] for r in _failures]))


def _hard_timeout(_signum, _frame):
    raise TimeoutError("journey exceeded the %ss hard deadline" % HARD_DEADLINE)


def main():
    signal.signal(signal.SIGALRM, _hard_timeout)
    signal.alarm(int(HARD_DEADLINE))
    try:
        run_journey()
    except Exception as error:  # any driver failure is a pinned failure
        log_step("driver-exception", False,
                 "first_run_unusable: journey driver failed: %r" % (error,))
    finally:
        signal.alarm(0)
    if _failures:
        log_step("exit", False, "first_run_unusable")
        return EXIT_PINNED_FAILURE
    log_step("exit", True, "first_run_authorized")
    return EXIT_AUTHORIZED


if __name__ == "__main__":
    sys.exit(main())
