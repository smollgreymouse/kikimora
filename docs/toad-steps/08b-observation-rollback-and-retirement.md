# Toad step 08B — observation, rollback confidence and legacy retirement decision

Status: **FUTURE / OPERATOR-GATED**.

Execute only after 08A installed-host staging is complete and reviewed.

This packet separates “Go control plane works on the real host” from “legacy recovery assets may be retired”.

The executor must never run:

```bash
sudo kk orchestration retire-legacy --confirm
```

without an explicit operator instruction in the current session.

---

## Goal

Use an operator-selected observation window to prove that:

- Go ownership remains stable across ordinary host use;
- retries/recovery do not accumulate stale parking/routes/rules;
- desired state remains correct;
- diagnostics remain usable and redacted;
- rollback is still understood and available;
- only then present a retirement decision to the operator.

No fixed observation duration is hard-coded here. The operator chooses the window appropriate for the deployment.

---

# Phase 1 — observation-window baseline

At the start of the observation window record:

- current Git/package version;
- ownership triple;
- desired role state;
- current core revision;
- underlay epoch/interface/gateway/source;
- Toad PIDs/generations/ifindices;
- endpoint applied epochs;
- parking;
- publication;
- IPv4/IPv6 routes/rules/table 51890;
- NetworkManager managed state;
- core/Toad service health.

Create a redacted diagnostic archive.

---

# Phase 2 — periodic stability checks

During the operator-selected observation window, repeat the same bounded checks.

Look specifically for:

- monotonically exploding underlay epoch without real path changes;
- repeated full Toad restart loops;
- retry storms;
- parking left active after Ready;
- duplicate endpoint routes/rules;
- stale publication files;
- stale desired bits after operator actions;
- a Toad generation going backwards;
- validation against an old underlay epoch;
- NetworkManager re-owning `kk-*`;
- selected-route IPv4/IPv6 fallback to physical underlay;
- unbounded log growth from one repeating recovery error.

Do not turn this into an always-on telemetry project. The purpose is release acceptance.

---

# Phase 3 — rollback confidence review

Before retirement, verify the rollback path is still available.

Read-only requirements:

- legacy unit files and writers still exist;
- old ownership values are known;
- legacy config is still readable;
- `kk orchestration rollback` implementation still matches the installed package;
- backup/diagnostic archives are available.

If the operator explicitly requests a real rollback rehearsal, execute it as a separate controlled maintenance action and then, if requested, cut back to Go again through 08A predicates.

Do not perform a rollback rehearsal implicitly.

---

# Phase 4 — retirement preflight

Before offering retirement, generate a retirement report containing:

1. 07E automated proof evidence;
2. 08A installed-host evidence;
3. observation-window evidence;
4. any recovery incidents and root causes;
5. rollback availability;
6. current desired state;
7. current ownership and service state;
8. latest redacted diagnostic archive;
9. confirmation that no unresolved STOP/DESIGN packet exists.

If any acceptance is missing, legacy retirement remains blocked.

---

# Phase 5 — explicit retirement decision

Only the operator decides whether to retire legacy writers.

If authorized, first take a final backup and diagnostic snapshot.

Then run only the repository-supported command:

```bash
sudo kk orchestration retire-legacy --confirm
```

Do not manually delete legacy files in place of the command.

After retirement verify:

- Go ownership remains unchanged;
- core remains Ready/current epoch;
- all desired roles remain correct;
- no parking active for Ready roles;
- selected-route fail-closed behavior still holds;
- systemd has no references to removed legacy writer units;
- installer/verify/doctor commands treat retired legacy components according to the intended post-retirement state.

If retirement changes what `kk verify`, installer or backup expect, update those contracts before calling retirement complete.

---

# Phase 6 — post-retirement recovery plan

Retirement must leave a documented recovery path.

At minimum record:

- package/repo version retired from;
- backup archive path;
- ownership config;
- desired-state file path;
- steps to reinstall/restore a known-good package;
- diagnostics command;
- which legacy files were removed versus intentionally retained.

Do not claim rollback-to-legacy remains immediate after the legacy writer has been retired unless the files were deliberately preserved for that purpose.

---

# Completion state

08B is complete only when:

- observation evidence is reviewed;
- operator explicitly approved retirement;
- retirement command succeeded;
- post-retirement verification is green;
- recovery documentation exists.

If operator declines retirement, record:

`Go control plane accepted; legacy retirement intentionally deferred`

That is a valid final state.

---

# Executor report

```text
HEAD/package:
Observation window:
- operator-selected duration:
- checks performed:
- incidents:
- route/rule drift:
- parking drift:
- restart/retry loops:
- fail-closed evidence:
- diagnostic archives:

Rollback confidence:
- legacy files present before retirement:
- rehearsal performed: yes/no
- result:

Retirement:
- operator approval: yes/no
- retire-legacy command run: yes/no
- backup before retirement:
- result:

Post-retirement:
- ownership:
- core Ready/current epoch:
- desired roles:
- routes/rules:
- selected traffic:
- verify/doctor/install contract:
- recovery documentation:

Unresolved:
- ...
```
