# 09G — Linux desktop production recovery execution plans

Status: **EXECUTION-READY / NO ARCHITECTURE DECISIONS LEFT TO THE EXECUTOR**

Source audit:

- `docs/toad-steps/09f-linux-desktop-user-journey-audit-2026-11-27.md`

This packet turns the W0-W6 remediation sequence from 09f into concrete code
work. The executor is not expected to choose architectures, protocols, durable
formats, lock order, UI semantics or acceptance criteria. If implementation
discovers a contradiction with the current source, stop that sub-step and
record the contradiction; do not substitute a different design.

## Mandatory order

1. `09g-w0-acceptance-contract.md`
2. `09g-w1-linux-first-run-auth.md`
3. `09g-w2-ui-command-identity.md`
4. `09g-w3-live-role-lifecycle.md`
5. `09g-w4-profile-defaults-crud.md`
6. `09g-w5-role-edit-config-model.md`
7. `09g-w6-release-closure.md`

W0 is written first because the old installed gate accepted the wrong product
journey. W2 precedes W3 because live add/remove makes row-index role identity
unsafe. W4 depends on W3 because profile policy must operate on a live role
composition. W5 depends on the stable-id and command-result model from W2 and
the live role transaction from W3.


## Finding ownership

| Audit finding | Owning execution work |
|---|---|
| JRN-01 first-run authorization | W1.1-W1.5 |
| JRN-02 transport mislabeled API incompatibility | W1.4 |
| JRN-03 GNOME/Wayland app identity | W1.5 + W6 U8e |
| JRN-04 canonical import drift / AWG | W3.1, W3.4, W3.9 |
| JRN-05 explicit role name missing | W3.4, W3.9 |
| JRN-06 current-profile context dropped | W3.4, W3.9 |
| JRN-07 live create/remove absent | W3.2-W3.8 |
| JRN-08 secondary action gating | W2.7 |
| JRN-09 fresh core never initialized | W1.2-W1.5 |
| JRN-10 lifecycle inconsistency | W3.2-W3.10 |
| JRN-11 impossible OpenConnect URL flow | W3.4, W3.5, W3.9 |
| JRN-12 global default blocks normal profile switch | W4.1-W4.4, W4.8 |
| JRN-13 profile CRUD incomplete | W4.5, W4.6, W4.8 |
| JRN-14 role edit/rename incomplete | W5.1-W5.9 |
| JRN-15 unsaved membership dropped | W2.5, W2.8 |
| JRN-16 global/stale command feedback | W2.3, W2.4, W2.6 |
| JRN-17 Linux autostart lies | W1.6 |
| JRN-18 default combo offers impossible choice | W4.3, W4.8 |
| JRN-19 wrong empty-profile count | W4.8 |
| JRN-20 loaded-role removal UX/lifecycle | W3.7-W3.10 |
| JRN-21 installed UI acceptance did not run installed UI | W0 + W6 |
| JRN-22 FakeCore/RealCore semantic contradiction | W0.3 + W3.10 + W6 |
| JRN-23 ProfileDrawer inline role actions unwired | W2.5 |
| JRN-24 VLESS importer drift | W3.1, W3.4, W3.9 |
| JRN-25 configured vs runtime role truth | W5.2, W5.3, W5.9 |
| JRN-26 row-index identity unsafe under live CRUD | W2.1, W2.2 |
| JRN-27 RoleDrawer frozen snapshot | W2.5 |
| JRN-28 runtime state under /etc | W3.1 |
| JRN-29 advertised disconnect rejected for default owner | W4.7, W4.8 |

A finding is not closed merely because one owning commit landed. It is closed
only when every listed owning sub-step and its mandatory gate are green.

## Executor evidence discipline

Use the already-created `docs/toad-steps/09g-execution-log.md` from the first source commit onward.

After each commit boundary in W0-W5 append:

- exact source SHA;
- exact sub-step id, e.g. `W3.6`;
- files changed;
- commands actually run;
- pass/fail/skip counts;
- any failpoint/crash row run;
- explicit statement of which JRN finding(s) the commit advances;
- explicit statement of which S-01..S-06 regression contracts were rerun.

Do not edit a later plan to match an implementation shortcut. If source reality
contradicts a plan assumption, record the contradiction in the execution log
and stop that sub-step for plan correction.

## Global rules

- Preserve the fail-closed contracts listed as S-01..S-06 in 09f.
- Do not remove an existing safety rejection merely because the UI should stop
  presenting an impossible action.
- The Go core remains the durable source of truth. QML never becomes the
  authority for desired state, profile membership, default route or role config.
- Raw provider secrets/private keys must never be logged.
- The release remains **not production-ready** until W6.
- Every source commit runs `git diff --check`; Go commits additionally run
  gofmt/vet/unit/race scopes from the child plan; Qt commits run the required
  Layer A/B/C suites from the child plan.
- No child plan may be compressed into a single opaque rewrite. Use the commit
  boundaries listed in each file so regressions can be bisected.

## Required final state

The same candidate .deb must support:

```text
fresh install
-> launch real /usr/bin/kikimora-ui from kikimora.desktop
-> one polkit authorization, no logout/reboot
-> core Ready in the same graphical session, roles all-off
-> create profile
-> import native AWG/VLESS/Amnezia provider config with explicit role name
-> role appears live in that profile without core restart
-> connect/disconnect with truthful operation-local feedback
-> configure per-profile default and switch A->B atomically
-> edit role/credentials, remove loaded role, rename/remove profile
-> restart UI/core/host without losing durable intent
-> autostart toggle is truthful on the next login
```

Old managed-routing/profile/bypass crash gates remain mandatory alongside the
new installed-desktop journey.
