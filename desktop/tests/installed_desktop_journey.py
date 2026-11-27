#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""09G-W0.2 installed desktop journey driver.

Runs INSIDE the real desktop user's graphical session (launched by
linux/tests/toad/installed-desktop-journey-vm.sh) and drives the real
installed kikimora-ui through the AT-SPI accessibility tree (pyatspi).
Controls are located by Accessible.name and accessible role only — never
by QML objectName or pixel coordinates.

W0 pin: a fresh install must be unusable at first run. The driver asserts

  1. the UI launches (the Kikimora application appears in AT-SPI);
  2. the UI is not Ready (no role/state text renders "Ready");
  3. it exposes no successful same-session initialization action yet;
  4. the driver exits nonzero with the pinned reason first_run_unusable.

Every step is appended as a JSONL record {"step":..., "ok":..., "detail":...}
to the step log passed as argv[1]; the pinned reason is always printed to
stdout so the VM harness can pin the verdict. Once W1 lands, this same test
is updated to require first-run success; no bypass variant may be created.
"""

import json
import re
import signal
import sys
import time

import pyatspi

APP_NAME = "Kikimora"
READY_TEXT = "Ready"
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


def collect_init_actions(root):
    """Actionable nodes whose name or action names claim initialization."""
    found = []
    for node in walk(root):
        try:
            names = [node.name]
        except Exception:
            continue
        try:
            action = node.queryAction()
        except Exception:
            continue
        try:
            names.extend(action.getName(i) for i in range(action.nActions))
        except Exception:
            pass
        if any(name and INIT_NAME_RE.search(name) for name in names):
            found.append((node, names))
    return found


def find_window(app):
    for node in walk(app):
        if node.getRoleName() == "frame":
            return node
    return None


def run_journey():
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
             "application %r is up with frame %r" % (APP_NAME, window.name))

    # --- assertion 2: the fresh UI is not Ready ---
    strings = collect_display_strings(window)
    ready_hits = [s for s in strings if s.strip() == READY_TEXT]
    sample = sorted(set(strings))[:40]
    log_step("ui-not-ready", not ready_hits,
             "first_run_unusable-ready-scan: %d display strings scanned, %d "
             "exact '%s' hits; visible strings: %s"
             % (len(strings), len(ready_hits), READY_TEXT, sample))

    # --- assertion 3: no successful same-session initialization action ---
    init_actions = collect_init_actions(window)
    names = [names for _, names in init_actions]
    log_step("init-action-scan", True,
             "first_run_unusable-init-scan: %d init-claiming controls: %s"
             % (len(init_actions), names))
    if not init_actions:
        log_step("no-init-success", True,
                 "first_run_unusable: the fresh UI exposes no same-session "
                 "initialization action at all")
    else:
        for node, action_names in init_actions:
            try:
                invoke_action(node, "click")
            except RuntimeError as error:
                log_step("init-invoke", True,
                         "first_run_unusable: %r could not even be invoked: %s"
                         % (action_names, error))
                continue
            ready_after = wait_until(
                lambda: any(s.strip() == READY_TEXT for s in collect_display_strings(window)),
                INVOKE_TIMEOUT, interval=1.0)
            log_step("init-invoke", ready_after is None,
                     "first_run_unusable: invoked %r; the UI did%s become "
                     "Ready within %.0fs"
                     % (action_names, " NOT" if ready_after is None else "",
                        INVOKE_TIMEOUT))

    # --- verdict: pinned W0 failure ---
    held = "held" if not _failures else "violated: %s" % [
        record["step"] for record in _failures]
    log_step("verdict", False,
             "first_run_unusable: fresh install exposes no usable first-run "
             "path before W1 (assertions %s)" % held)


def _hard_timeout(_signum, _frame):
    raise TimeoutError("journey exceeded the %ss hard deadline" % HARD_DEADLINE)


def main():
    signal.signal(signal.SIGALRM, _hard_timeout)
    signal.alarm(int(HARD_DEADLINE))
    try:
        run_journey()
    except Exception as error:  # any driver failure is still the W0 pin
        log_step("driver-exception", False,
                 "first_run_unusable: journey driver failed: %r" % (error,))
    finally:
        signal.alarm(0)
    log_step("exit", False, "first_run_unusable")
    return EXIT_PINNED_FAILURE


if __name__ == "__main__":
    sys.exit(main())
