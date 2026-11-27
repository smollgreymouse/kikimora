# 09G execution log — Linux desktop production recovery

Status: **NOT STARTED**

Authoritative plans:

- `docs/toad-steps/09g-linux-desktop-execution-plans.md`
- `docs/toad-steps/09g-w0-acceptance-contract.md`
- `docs/toad-steps/09g-w1-linux-first-run-auth.md`
- `docs/toad-steps/09g-w2-ui-command-identity.md`
- `docs/toad-steps/09g-w3-live-role-lifecycle.md`
- `docs/toad-steps/09g-w4-profile-defaults-crud.md`
- `docs/toad-steps/09g-w5-role-edit-config-model.md`
- `docs/toad-steps/09g-w6-release-closure.md`

Source audit:

- `docs/toad-steps/09f-linux-desktop-user-journey-audit-2026-11-27.md`

## Rules for every entry

Append only. Do not rewrite a failed run into a green one.

Every sub-step entry contains:

~~~text
step:
source SHA before:
source SHA after/commit:
files changed:
JRN findings advanced:
S-01..S-06 contracts rerun:
commands:
results:
  passed:
  failed:
  skipped:
  QML warnings:
failpoint/crash rows:
artifact/package identity:
remaining blockers:
notes:
~~~

A sub-step is not DONE when a mandatory command was not run. Record
`NOT RUN` and the reason; do not silently omit it.

If implementation contradicts a plan assumption, add a
`PLAN CONTRADICTION` entry with exact source references and stop that sub-step
for plan correction. Do not choose a replacement architecture in the execution
log.

---

## W0

### W0.1 — reclassify installed component suite

Status: DONE — 2026-11-27

~~~text
step: W0.1
source SHA before: 4cad505 (pulled ff-only onto origin/master)
source SHA after/commit: 8cc1891 docs(09g-w0.1)
files changed:
    desktop/tests/guest-run-suites.sh (header reclassification only)
    docs/roadmap.md (INSTALLED ACCEPTANCE ownership note)
    docs/ui-coverage-matrix.md (Linux lane scope note)
    docs/desktop-ui-test-execution-and-coverage-plan.md (Layer D scope note)
JRN findings advanced: JRN-21 (the acceptance contract split: the old lane
    is explicitly component compatibility, not installed desktop E2E)
S-01..S-06 contracts rerun: NOT APPLICABLE — labels/docs only; the runner
    code path is untouched (the W0.1 gate is byte-for-behavior equivalence)
commands:
    bash -n desktop/tests/guest-run-suites.sh
    bash desktop/tests/guest-run-suites.sh --build-dir /nonexistent ...
results:
    passed: runner fail-closed path unchanged; syntax clean
    failed: 0
    skipped: 0
    QML warnings: n/a (no suites run in this step)
failpoint/crash rows: n/a
artifact/package identity: n/a (no package built in this step)
remaining blockers: none for W0.1
notes: the roadmap INSTALLED ACCEPTANCE block now states explicitly that
    guest-run-suites.sh does not satisfy installed-executable,
    graphical-session, first-run-authorization or XDG-autostart acceptance;
    those belong to 09G-W0.2 and W6.7.

### W0.2 — real installed desktop journey harness

Status: DONE — 2026-11-27

~~~text
step: W0.2
source SHA before: 55ab50a (origin/master ff-only; W0.1 already in)
commits (each a separate logical commit):
    2bd30d9 feat(09g-w0.2): artifact provenance — build-info.json in the
        integrated package (build-release.sh computes/validates
        KIKIMORA_SOURCE_SHA + KIKIMORA_LESHY_SHA; stage-release.sh writes
        /usr/share/kikimora/build-info.json schema {schema:1, version,
        source_sha, leshy_sha}, 0644, fixed key order)
    3fcb8db feat(09g-w0.2): the integrated packaging test asserts
        build-info provenance
    d312726 feat(09g-w0.2): the installed desktop journey harness (shell +
        pyatspi driver)
    ad16d8d fix(09g-w0.2): honor a caller-supplied KIKIMORA_LESHY_SHA
    e251c2c fix(09g-w0.2): the packaging test pins LESHY_SHA, not the
        LESHY_REF tag
    a568caf fix(09g-w0.2): session lookup matches the loginctl USER column
    7340c2c fix(09g-w0.2): select the user's active graphical session
    867cd44 fix(09g-w0.2): preflight the session toolkit-accessibility
        prerequisite
    ef9a005 fix(09g-w0.2): resolve press/activate aliases in invoke_action
    7f36635 fix(09g-w0.2): scan accessible names in the not-Ready assertion
files:
    packaging/linux/build-release.sh
    packaging/linux/stage-release.sh
    linux/tests/test_integrated_release_packaging.sh
    linux/tests/toad/installed-desktop-journey-vm.sh (new)
    desktop/tests/installed_desktop_journey.py (new)
JRN findings advanced:
    JRN-21 the journey now launches the REAL packaged /usr/bin/kikimora-ui
        through gtk-launch kikimora.desktop in the desktop user's REAL
        GNOME Wayland session (no offscreen, no synthesized DBus/display)
    JRN-09 the harness never pre-starts the core: after install
        kikimora-core.service is inactive+disabled in both phases; the
        harness stops the units before apt so prerm never leaves the
        upgrade restart marker
    JRN-01 the first-run defect is exposed verbatim, not prepared away:
        the fresh UI renders «Ядро несовместимо по API: команды
        заблокированы — core socket: QLocalSocket::connectToServer: Socket
        access error» (a transport failure misreported as API
        incompatibility — W1's typed transport state will own this), and
        the CONNECT control (AT-SPI Press) invoked by the journey does NOT
        make the UI Ready within 12s
S-01..S-06 contracts rerun: PRESERVED — fresh install never self-starts
    or self-connects (core inactive+disabled, empty core journal, no core
    socket, core-snapshot.json empty); no secrets in evidence (paths and
    fingerprints only); the VM-only polkit rule authorizes only
    io.github.smollgreymouse.kikimora.authorize-user for the validated
    --user and is removed on every exit (verified empty rules.d after run)
commands:
    bash -n linux/tests/toad/installed-desktop-journey-vm.sh
    VM release: KIKIMORA_REQUIRE_UI=1 KIKIMORA_UI_BIN=... KIKIMORA_LESHY_SRC=...
        KIKIMORA_LESHY_SHA=b87fea8d... bash packaging/linux/build-release.sh
        (Leshy v0.6.0 = b87fea8d compiled from the pinned revision)
    bash linux/tests/test_integrated_release_packaging.sh dist
    sudo bash linux/tests/toad/installed-desktop-journey-vm.sh --candidate
        dist/kikimora_1.4.0_amd64.deb --expected-version 1.4.0
        --expected-source-sha ad16d8d... --expected-deb-sha256 ca4ba938...
        --user greymouse --artifact-dir /var/tmp/kikimora-journey-fresh
        --phase fresh
    same with --artifact-dir /var/tmp/kikimora-journey-reboot
        --phase post-reboot (after a real VM reboot with GDM autologin)
results:
    integrated packaging test: GREEN (checksums, payload, Qt Depends set,
        build-info provenance source=ad16d8d leshy=b87fea8d)
    journey fresh: harness exit 3 = the pinned W0 verdict; steps
        ui-launches ok (frame 'Kikimora'), ui-not-ready ok (16 display
        strings scanned, 0 exact 'Ready' hits), init-action-scan ok (2
        init-claiming controls), init-invoke ok (CONNECT pressed via AT-SPI
        Press; the UI did NOT become Ready within 12s), verdict
        first_run_unusable
    journey post-reboot: same GREEN-pinned shape (session 1, Wayland,
        active; core still inactive after reboot; xdg autostart UI is
        pkill'ed and relaunched through gtk-launch by the harness)
    failed: 0 unexpected (every nonzero exit is the pinned verdict or a
        fail-closed harness gate)
    skipped: 0
QML warnings: n/a (no in-process QML suites in this step; the journey runs
    the installed binary)
failpoint/crash rows: n/a
artifact/package identity:
    kikimora_1.4.0_amd64.deb sha256
    ca4ba938429f02aef5db21d9abb71bf47f8e946c8c1cc7db672dd86acb20e948,
    source_sha ad16d8d958c4736750107ea3e346429ee42bb511,
    leshy_sha b87fea8d0145f9c5261b9854f8af2ac659fc0695 (companion pin
    v0.6.0/LESHY_SHA)
    evidence: /var/tmp/kikimora-journey-fresh/ and
    /var/tmp/kikimora-journey-reboot/ on ubuntu2604 (192.168.1.51):
    at-spi-steps.jsonl, ui-journey.log, gtk-launch.log, install.log,
    core-journal.log, core-status.log, core-snapshot.json, build-info.json,
    polkit-rule.used
disposable-VM setup prerequisites discovered (setup lane, not product):
    python3-pyatspi installed; gsettings
    org.gnome.desktop.interface toolkit-accessibility true (Qt registers
    its AT-SPI bridge only when set — the harness preflight now fails
    closed on this); GDM autologin for the journey user so a real
    Wayland session exists (also required for the post-reboot phase)
remaining blockers: none for W0.2
notes: the W0.2 gate intentionally ends nonzero — the fresh journey pins
    first_run_unusable until W1 implements first-run authorization; the
    SAME test is updated to require success when W1 lands (no bypass
    variant). The first two release attempts aborted at the new
    provenance gate (empty Leshy SHA in a non-git tree) — the gate
    caught a real caller-contract bug, fixed by ad16d8d.


### W0.3 — FakeCore/RealCore semantic contract

Status: DONE — 2026-11-27 (RealCore rows RED by design until W3)

~~~text
step: W0.3
source SHA before: 315fb1c (after the W0.2 log entry)
commits:
    ba84282 feat(09g-w0.3): the shared FakeCore/RealCore semantic contract
        test (adapter + row table + CMake target + all four runners)
    d02a850 fix(09g-w0.3): verify the loaded-role removal by stable id
        (the removeTunnel argument and the verification id carried
        separately: the fake's removeTunnel matches its role id, which
        today equals the display label)
files:
    desktop/tests/CoreSemanticContractTest.cpp (new)
    desktop/CMakeLists.txt (target core-semantic-contract-test, CTest
        core-semantic-contract, offscreen env)
    desktop/tests/run-required-suites.sh (+LAYER_C, +case branch)
    desktop/tests/run-required-suites.ps1 (+layerC)
    desktop/tests/guest-run-suites.sh (+selected branch, +default list)
    desktop/tests/guest-run-suites.ps1 (+Run-Suite, non-live like
        real-core-client-test)
JRN findings advanced:
    JRN-22 FakeCore/RealCore semantic contradiction is now a standing RED
        GATE: the same row table runs on both backends through the plan's
        test-only adapter (core + settle + seedImport), and the real core
        fails exactly the live-lifecycle rows until W3
S-01..S-06 contracts rerun: NOT APPLICABLE — test-only change; desktop/src
    untouched (no product behavior altered, per W0 DONE)
commands:
    VM (ubuntu2604): cmake -S desktop -B build/desktop-live -G Ninja
        && ninja -C build/desktop-live core-semantic-contract-test
        && go build kikimora-core + fake-toad from the same w0-src3 tree
    QT_QPA_PLATFORM=offscreen KIKIMORA_CORE_BINARY=...
        KIKIMORA_FAKE_TOAD_BINARY=... ./core-semantic-contract-test
results (QtTest 6.10.2):
    fakeCoreMatchesTargetContract: PASS — all target-contract rows green
        on the fake
    realCoreMatchesTargetContract: FAIL — the designed red gate, exactly
        two rows red:
        - import-role-visible-immediately: no role with id contract-role
          after the import + settle (real CreateTunnel is disk-only and
          loads at the next core restart)
        - remove-loaded-stopped-role-live: alpha survived removeTunnel +
          settle (real RemoveTunnel deletes config; the live snapshot
          keeps the role until restart)
        the remaining real rows (create-profile-visible-immediately,
        desired=false, membership live, identity by id, rejection
        completion) already hold on the current core
    Totals: 3 passed, 1 failed, 0 skipped — no skip, no weakening
QML warnings: n/a (no QML in this suite)
failpoint/crash rows: n/a
artifact/package identity: n/a (no package built in this step; suite run
    on the VM tree w0-src3 @ ba84282+d02a850 content)
remaining blockers: none for W0.3; the red gate STAYS until W3 flips it
    green (W6 must not close with it red)
notes: the plan's row list is implemented verbatim; FakeCore gets no
    alternate expectations. W0 is now DONE by its own contract: the old
    guest lane is explicitly lower-level (W0.1), the real installed
    journey exists and pins first_run_unusable (W0.2), and the
    FakeCore/RealCore divergence is a red gate (W0.3).


---

## W1

### W1.1 — operator registry and peer authorization

Status: DONE — 2026-11-27

~~~text
step: W1.1
source SHA before: 7331c30 (W0 DONE)
commits:
    6fcf154 feat(09g-w1.1): shared operator registry and peer authorization
    0e22433 fix(09g-w1.1): drive the peer authorization order through
        server-identity seams
    bdcd9ba fix(09g-w1.1): chown-skip the root-owned registry entry case
        without privileges
    e14803f fix(09g-w1.1): privilege-complementary registry test coverage
    c887d46 fix(09g-w1.1): set the group-writable mode explicitly in the
        rejection test
files:
    toad/internal/operatorauth/registry_linux.go (new)
    toad/internal/operatorauth/registry_linux_test.go (new)
    toad/internal/control/peer_linux.go (refactored onto
        authorizePeerWithRegistry)
    toad/internal/control/peer_linux_test.go (extended)
JRN findings advanced:
    JRN-01/09 coupling: the authorization authority moves to the root-owned
        registry (per-uid entries at /etc/kikimora/operators.d) that a
        same-session pkexec helper can extend; the kikimora supplementary
        group stops being the desktop authorization contract (the legacy
        server-GID path stays for CLI/admin compatibility)
S-01..S-06 contracts rerun: PRESERVED — fail closed: a malformed/insecure
    registry entry is an error, never a silent deny/allow; uid 0 is never a
    registry entry; SO_PEERCRED is the only uid source (no request-body
    uid); no secrets logged (paths and uids only)
commands:
    VM (ubuntu2604, w1-check @ c887d46):
    go test -count=1 ./internal/operatorauth ./internal/control
    sudo go test -count=1 ./internal/operatorauth   (root-owned positive paths)
    go test -race -count=1 ./internal/control
    go vet ./...
    gofmt -l (clean)
results:
    passed: operatorauth ok (both runs), control ok, race ok, vet ok
    failed: 0 (control's pre-existing privilege-sensitive DAC tests fail
        only under a sudo run of the whole package — they simulate
        unwritable files via chmod which root bypasses; they are not part
        of the W1.1 gate and are untouched)
    skipped: 0 in the canonical run; the privilege-complementary cases
        chown-skip/gid-skip where their prerequisite is absent (each covered
        by the complementary run)
failpoint/crash rows: n/a
artifact/package identity: n/a (no package built in this step)
remaining blockers: none for W1.1
notes: the registry validation contract: regular non-symlink file, owner
    root, not group/other writable, exactly one decimal line equal to the
    canonical decimal filename uid. Authorize writes exactly "<uid>\n"
    through fsutil.WriteAtomicFile (fsync payload + parent dir) at 0644
    into the 0755 dir. authorizePeerWithRegistry order: server uid/root ->
    registry (fail closed on error) -> legacy server-GID -> reject. The
    serverUID/serverEGID/readPeerGroups init seams exist so the plan's
    registry-path cases are real tests with a self-pair AF_UNIX socket;
    production values are unchanged.


### W1.2 — privileged helper and ACL reapplication

Status: DONE — 2026-11-27

~~~text
step: W1.2
source SHA before: baa9486 (W1.1 log entry)
commits:
    73daefe feat(09g-w1.2): the privileged kikimora-authorize-user helper
    5269896 fix(09g-w1.2): run the end-to-end authorize case as root
        against a non-root uid
files:
    toad/cmd/kikimora-authorize-user/main_linux.go (new)
    toad/cmd/kikimora-authorize-user/main_linux_test.go (new)
JRN findings advanced:
    JRN-01/09: the same-session activation transaction exists — one pkexec
        helper authorizes the caller, activates the core service and
        re-establishes the per-uid kernel reachability ACL; no logout,
        no reboot, no usermod
S-01..S-06 contracts rerun: PRESERVED — the helper trusts PKEXEC_UID only
    (canonical decimal, uid 0 refused, LookupId required; no uid/name argv
    exists at all); the registry stays the authorization authority and the
    ACL is only the reachability gate; per-uid named ACL entries only
    (--x on /run/kikimora, rw- on core.sock), never a default ACL and
    never toads/secrets/Leshy paths; the socket-wait timeout is a hard
    error with no ACL command after it
commands:
    VM (ubuntu2604, w1-check @ 5269896):
    go test -count=1 ./cmd/kikimora-authorize-user
    sudo go test -count=1 ./cmd/kikimora-authorize-user
    go vet ./...
results:
    passed: helper suite ok in the user run (rejection + exact-argv
        assertions) and in the root run (end-to-end authorize against
        uid 65534)
    failed: 0
    skipped: 0 in the root run; non-root runs skip only the euid-0
        end-to-end case
failpoint/crash rows: n/a
artifact/package identity: n/a (helper packaging lands in W1.3)
remaining blockers: none for W1.2
notes: install path fixed /usr/libexec/kikimora/kikimora-authorize-user;
    modes limited to --authorize-current-user and --apply-runtime-acl
    [--wait-ms N]; the exec/privilege seams assert the exact systemctl and
    setfacl argv with no shell.


### W1.3 — package/polkit/service hooks

Status: DONE — 2026-11-27

~~~text
step: W1.3
source SHA before: 76e72eb (W1.2 log entry)
commits:
    1fcf903 feat(09g-w1.3): package the helper, policy and service hooks
    06837d1 fix(09g-w1.3): extract the deb control files for the postinst
        assertions
    fa53a8a fix(09g-w1.3): match the getfacl named-user form in the ACL
        assertion
files:
    linux/files/io.github.smollgreymouse.kikimora.policy (new, exact plan body)
    linux/files/kikimora-core.service (exact ExecStartPost, no - prefix)
    packaging/linux/stage-release.sh (helper build+stage, policy stage,
        /usr/libexec/kikimora + /usr/share/polkit-1/actions dirs,
        pkexec+acl in the base Depends — the helper ships with every
        artifact because the service ExecStartPost needs its ACL mode;
        postinst creates /etc/kikimora/operators.d 0755 and the usermod
        remediation message is removed entirely; upgrade restart kept)
    linux/tests/test_integrated_release_packaging.sh (helper/policy/deps/
        postinst-no-usermod/ExecStartPost assertions)
    linux/tests/toad/managed-package-lifecycle-vm.sh (fresh-mode rows)
JRN findings advanced:
    JRN-01/02/09: the packaged product now carries the complete in-session
        activation path (polkit action -> helper -> registry -> enable-now
        -> ExecStartPost ACL); the fresh install stays network-inert and
        the group membership contract is gone from the package surface
S-01..S-06 contracts rerun: PRESERVED — fresh install never auto-starts
    the core (asserted in the harness before the helper runs); the
    ExecStartPost failure is a visible start failure; no default ACL, no
    toads/secrets/Leshy ACL targets; no secrets in evidence
commands:
    VM deb rebuild: KIKIMORA_REQUIRE_UI=1 KIKIMORA_UI_BIN=...
        KIKIMORA_LESHY_BIN=/usr/local/bin/leshy (the v0.6.0 binary from the
        pinned companion) KIKIMORA_LESHY_SHA=b87fea8d... bash
        packaging/linux/build-release.sh
    bash linux/tests/test_integrated_release_packaging.sh dist
    sudo bash linux/tests/toad/managed-package-lifecycle-vm.sh --mode fresh
        --candidate dist/kikimora_1.4.0_amd64.deb --work-dir
        /var/tmp/kikimora-lifecycle --phase prepare
    (reboot) --phase verify
results:
    integrated packaging test: GREEN — helper executable, policy action +
        exec-path annotation, pkexec+acl in Depends, postinst creates
        operators.d and has no usermod, service carries the exact
        ExecStartPost; provenance source=1fcf903 leshy=b87fea8d
    fresh lifecycle: prepare GREEN (payload; helper/policy/registry present
        with the core inactive; helper run with PKEXEC_UID=1000 activates
        the core, writes /etc/kikimora/operators.d/1000 and sets the
        user:1000:rw- socket ACL; the authorized uid reaches the socket
        with the kikimora group stripped and no new groups) + reboot verify
        GREEN
    artifact identity: kikimora_1.4.0_amd64.deb sha256
        c3d59648d97ead98e88c625cbdf11c5004d0e6d916d35ee9fcd615a7a752f9ba
remaining blockers: none for W1.3
notes: the first prepare run failed only on the harness grep (getfacl
    prints the resolved account name, not the numeric uid) — the helper
    itself worked on the first try; the harness check accepts both forms.


### W1.4 — typed transport problem state

Status: NOT STARTED

### W1.5 — async initialization and GNOME identity

Status: NOT STARTED

### W1.6 — per-user autostart

Status: NOT STARTED

---

## W2

### W2.1 — stable-id RoleListModel

Status: NOT STARTED

### W2.2 — stable-id CoreBackend mutations

Status: NOT STARTED

### W2.3 — request-scoped command completion

Status: NOT STARTED

### W2.4 — remove optimistic profile/membership state

Status: NOT STARTED

### W2.5 — canonical RoleDrawer/live binding

Status: NOT STARTED

### W2.6 — operation-local UI feedback

Status: NOT STARTED

### W2.7 — role-action safety gate

Status: NOT STARTED

### W2.8 — composite dirty close guard

Status: NOT STARTED

---

## W3

### W3.1 — role-state/secrets paths + full Config writer

Status: NOT STARTED

### W3.2 — dynamic Controller roles

Status: NOT STARTED

### W3.3 — dynamic SelectedRouteManager zones

Status: NOT STARTED

### W3.4 — canonical import Config builder

Status: NOT STARTED

### W3.5 — core-owned OpenConnect secrets

Status: NOT STARTED

### W3.6 — live add transaction

Status: NOT STARTED

### W3.7 — live remove transaction

Status: NOT STARTED

### W3.8 — API handlers/field errors

Status: NOT STARTED

### W3.9 — desktop canonical import UI

Status: NOT STARTED

### W3.10 — remove restart-only assumptions

Status: NOT STARTED

---

## W4

### W4.1 — profile-default durable store

Status: NOT STARTED

### W4.2 — defaults in profile composition transaction

Status: NOT STARTED

### W4.3 — profile-default API/bypass helper

Status: NOT STARTED

### W4.4 — crash-safe atomic profile switch

Status: NOT STARTED

### W4.5 — profile rename

Status: NOT STARTED

### W4.6 — profile remove

Status: NOT STARTED

### W4.7 — safe disconnect of default owner

Status: NOT STARTED

### W4.8 — desktop profile/default UI

Status: NOT STARTED

---

## W5

### W5.1 — stable display name

Status: NOT STARTED

### W5.2 — configured-role snapshot

Status: NOT STARTED

### W5.3 — Qt configured/runtime model

Status: NOT STARTED

### W5.4 — RolePatch API types

Status: NOT STARTED

### W5.5 — pure patch builder

Status: NOT STARTED

### W5.6 — forced config-reload recovery

Status: NOT STARTED

### W5.7 — transactional UpdateRole

Status: NOT STARTED

### W5.8 — UpdateRole capability/API

Status: NOT STARTED

### W5.9 — editable role UI

Status: NOT STARTED

---

## W6

### W6.1 — acceptance wiring/roadmap

Status: NOT STARTED

### W6.2 — update old G5/U8c assertions

Status: NOT STARTED

### W6.3 — exact source pre-release gates

Status: NOT STARTED

### W6.4 — VERSION 1.5.0 source freeze

Status: NOT STARTED

### W6.5 — hosted CI/artifact freeze

Status: NOT STARTED

### W6.6 — disposable Ubuntu regressions

Status: NOT STARTED

### W6.7 — real installed GNOME/Wayland journey

Status: NOT STARTED

### W6.8 — Windows shared-code regression

Status: NOT STARTED

### W6.9 — operator workstation canary

Status: NOT STARTED

### W6.10 — evidence/tag/release without rebuild

Status: NOT STARTED
