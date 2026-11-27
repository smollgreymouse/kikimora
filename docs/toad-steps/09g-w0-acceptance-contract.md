# 09G-W0 — Repair the desktop acceptance contract before product fixes

Status: **EXECUTION PLAN**

Closes/process findings: JRN-21, JRN-22 and the acceptance portions of
JRN-01/JRN-09. This step deliberately does not fix the product yet. Its purpose
is to create tests that fail for the current product for the correct reasons.

## Fixed decisions

- Keep `desktop/tests/guest-run-suites.sh`; rename/re-document its semantic role
  as **installed-core component compatibility**, not installed desktop E2E.
- Add a separate Linux installed desktop journey that launches the real packaged
  `/usr/bin/kikimora-ui` through `kikimora.desktop` in a real GNOME/Wayland
  user session.
- Drive that binary with AT-SPI. Use `python3-pyatspi`; do not add image/OCR
  automation.
- The journey starts with core stopped/disabled and no pre-added `kikimora`
  supplementary group. The runner must never run `systemctl start` before
  launching the UI.
- CI may install a narrow test-only polkit rule for the designated VM account;
  production packaging must still ship the real policy/helper implemented in W1.
- Add one cross-backend semantic contract for FakeCore vs RealCore. A behavior
  may not have different user-visible lifecycle semantics in those backends.

## Commit W0.1 — reclassify the existing installed suite

### Files

- `desktop/tests/guest-run-suites.sh`
- `docs/roadmap.md`
- `docs/ui-coverage-matrix.md`
- `docs/desktop-ui-test-execution-and-coverage-plan.md`

### Changes

1. Change the runner header/comment/name in documentation from "installed
   desktop acceptance" to "installed-core Qt component suites".
2. Keep its current mechanics:
   - `QT_QPA_PLATFORM=offscreen`;
   - test binaries from `--build-dir`;
   - ready installed core precondition.
3. Do **not** weaken its zero-skip/zero-warning behavior.
4. In the roadmap ownership matrix, state explicitly that this lane does not
   satisfy installed executable, graphical session, first-run authorization or
   XDG autostart acceptance.

### Gate

Existing runner must remain byte-for-behavior equivalent except labels/docs.

## Commit W0.2 — add the real installed desktop journey harness

### New files

- `linux/tests/toad/installed-desktop-journey-vm.sh`
- `desktop/tests/installed_desktop_journey.py`

### VM shell contract

`installed-desktop-journey-vm.sh` accepts:

```text
--candidate <absolute .deb>
--expected-version <VERSION>
--expected-source-sha <40hex git SHA>
--expected-deb-sha256 <64hex SHA-256>
--user <desktop-user>
--artifact-dir <dir>
--phase fresh|post-reboot
```

### Artifact provenance metadata required by the runner

Modify:

- `packaging/linux/build-release.sh`;
- `packaging/linux/stage-release.sh`;
- `linux/tests/test_integrated_release_packaging.sh`.

`build-release.sh` computes and validates:

```bash
KIKIMORA_SOURCE_SHA="$(git -C "$ROOT" rev-parse HEAD)"
KIKIMORA_LESHY_SHA="$(git -C "$KIKIMORA_LESHY_SRC" rev-parse HEAD)"
export KIKIMORA_SOURCE_SHA KIKIMORA_LESHY_SHA
```

For an integrated release both values are mandatory 40 lowercase hex.
stage-release writes deterministic, timestamp-free:

```text
/usr/share/kikimora/build-info.json
```

with schema:

```json
{
  "schema": 1,
  "version": "<VERSION>",
  "source_sha": "<40hex>",
  "leshy_sha": "<40hex>"
}
```

File mode 0644, root-owned in the package. JSON key order above is fixed so the
portable archive remains reproducible.

The integrated packaging test extracts the built .deb and requires exact
version/current source SHA/current pinned Leshy SHA.

The journey script must:

1. before any install/mutation:
   - require candidate SHA-256 exactly equals --expected-deb-sha256;
   - require dpkg-deb Version equals --expected-version;
   - extract build-info.json with dpkg-deb --fsys-tarfile/tar or dpkg-deb -x
     into its work dir;
   - require build-info version and source_sha equal the expected values;
   - record Leshy SHA from build-info for evidence;
2. Verify a graphical session for `--user` exists:
   - `loginctl list-sessions --no-legend`;
   - choose that user's active graphical session;
   - read `Type`, `Display`, `Remote`, `State`;
   - require Wayland or X11 and active state.
3. Resolve the existing graphical-session environment; do not synthesize a
   private DBus or display:
   - `XDG_RUNTIME_DIR=/run/user/<uid>`;
   - `DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/<uid>/bus`;
   - query the existing user manager with:
     `sudo -u <user> env XDG_RUNTIME_DIR=... DBUS_SESSION_BUS_ADDRESS=...
       systemctl --user show-environment`;
   - for a Wayland session require nonempty `WAYLAND_DISPLAY` and
     `XDG_SESSION_TYPE=wayland`;
   - for an X11 session require nonempty `DISPLAY` and
     `XDG_SESSION_TYPE=x11`;
   - preserve `XDG_CURRENT_DESKTOP` when present;
   - require the existing AT-SPI accessibility bus to be reachable from that
     session; do not launch a second accessibility bus.
4. Remove any existing Kikimora package only in the dedicated VM snapshot,
   clean product runtime/config state used by this test, then install the exact
   candidate.
5. Assert immediately after fresh install:
   - `kikimora-core.service` is not manually pre-started by the harness;
   - installed binary is `/usr/bin/kikimora-ui`;
   - desktop entry is `/usr/share/applications/kikimora.desktop`.
6. Build one environment array from step 3 and launch using the actual desktop
   entry as the existing user:
   `sudo -u <user> env <captured-session-env> gtk-launch kikimora`.
   Do not use `dbus-run-session`, `weston`, Xvfb or
   `QT_QPA_PLATFORM=offscreen`.
7. Assert the resulting process executable resolves to
   `/usr/bin/kikimora-ui` and its environment still carries the captured
   graphical session.
8. Run `installed_desktop_journey.py` as that same user with the same captured
   session environment.
9. Collect:
   - UI log;
   - `journalctl -u kikimora-core.service -b`;
   - package postinst output if available;
   - AT-SPI step log;
   - `systemctl status`;
   - final core JSON snapshot.
10. Exit nonzero on every missing accessibility object, timeout, unexpected
    dialog, failed assertion or skipped step.

### VM-only polkit automation

The disposable VM journey must exercise the real pkexec/helper path without
storing an administrator password.

After candidate install and before launching the UI, the root VM harness writes:

~~~text
/etc/polkit-1/rules.d/49-kikimora-installed-journey.rules
~~~

Validate `--user` against `^[a-z_][a-z0-9_-]*$` before embedding it. Rule
body for user `greymouse` is exactly:

~~~javascript
polkit.addRule(function(action, subject) {
    if (action.id == "io.github.smollgreymouse.kikimora.authorize-user" &&
        subject.user == "greymouse" &&
        subject.local &&
        subject.active) {
        return polkit.Result.YES;
    }
});
~~~

For another validated --user, substitute only the quoted username.

The harness installs a trap that removes this rule on every exit and then
restarts/reloads polkit only if the target distro requires it; on current
Ubuntu polkit monitors the rules directory and no restart is required.

This rule authorizes **only** the Kikimora enrollment action. It does not
authorize systemctl, arbitrary pkexec binaries or sudo.

The W6 operator-workstation canary does **not** install this rule. That canary
requires the real GNOME polkit authentication dialog.

### Harness dependency preflight

Before package mutation require:

~~~bash
command -v gtk-launch
command -v gdbus
python3 -c 'import pyatspi'
~~~

Check AT-SPI session bus with the captured user session environment:

~~~bash
gdbus call --session   --dest org.a11y.Bus   --object-path /org/a11y/bus   --method org.a11y.Bus.GetAddress
~~~

Missing pyatspi/AT-SPI is a harness failure. The disposable VM image/setup must
install `python3-pyatspi`; the product .deb must not depend on this test-only
package.

### Python AT-SPI contract

Use pyatspi and Qt Quick's AT-SPI surface. Locate controls by Accessible.name
and accessible role, never by QML objectName or pixel coordinates. Existing
KButton/KTextField/KComboBox/KSwitch controls already expose user-facing
Accessible.name values. When a journey control is ambiguous or unnamed, add a
unique user-facing accessibleText/Accessible.name in the owning W1-W6 QML
commit and add a Qt accessibility assertion for it.

Implement helpers:

```python
wait_application(name)
find_by_name(root, accessible_name_or_object_name)
invoke_action(node, "click")
set_text(node, text)
assert_text(node, expected_substring)
wait_until(predicate, timeout)
```

Store every step as JSONL in the artifact directory:
`{"step":..., "ok":..., "detail":...}`.

### Initial W0 journey assertions

Before W1 is implemented, the fresh journey is expected to fail at first-run.
Pin the failure, do not mark the test expected-fail in CI:

1. UI launches.
2. UI is not Ready.
3. It exposes no successful same-session initialization action yet.
4. The test exits nonzero with reason `first_run_unusable`.

Once W1 lands, update the same test to require success; do not create a second
test that bypasses the failure.

## Commit W0.3 — one shared FakeCore/RealCore behavior contract

### Files

- add `desktop/tests/CoreSemanticContractTest.cpp`
- `desktop/CMakeLists.txt`
- reuse `desktop/tests/support/LiveCoreFixture.*`
- reuse `desktop/src/core/FakeCoreBackend.*`

### Test adapter

Define a small test-only interface in the test file:

```cpp
struct SemanticBackend {
    CoreBackend *core;
    std::function<void()> settle;
    std::function<void(QString name, QString profile)> seed/import;
};
```

Run the same table against FakeCore and RealCore. The expected contract is the
**target** product contract, so current RealCore create/remove rows should fail
until W3.

Rows:

- create profile becomes visible immediately;
- create/import role becomes visible immediately without core restart;
- created role is desired=false;
- created role belongs to the requested profile;
- membership mutation is live;
- remove loaded stopped role removes it live;
- command rejection produces a completion result;
- role identity is stable by id, not list row.

Do not give FakeCore alternate expectations.

### CMake

Add target `core-semantic-contract-test`, CTest name
`core-semantic-contract`, and include it in:

- `desktop/tests/run-required-suites.sh`;
- `desktop/tests/run-required-suites.ps1`;
- `desktop/tests/guest-run-suites.sh`;
- Windows runner only where the RealCore fixture is supported by that lane.

## W0 DONE

W0 is complete when:

- old guest runner is explicitly lower-level;
- a real installed executable journey exists and currently exposes the real
  first-run defect rather than preparing it away;
- FakeCore/RealCore semantic divergence is a red gate;
- no product behavior has been altered yet.
