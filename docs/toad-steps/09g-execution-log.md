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

Status: NOT STARTED

### W0.3 — FakeCore/RealCore semantic contract

Status: NOT STARTED

---

## W1

### W1.1 — operator registry and peer authorization

Status: NOT STARTED

### W1.2 — privileged helper and ACL reapplication

Status: NOT STARTED

### W1.3 — package/polkit/service hooks

Status: NOT STARTED

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
