# 09G-W6 — Integrated release closure and production re-acceptance

Status: **EXECUTION PLAN**

Closes: release/process findings JRN-21/JRN-22 and restores production status
only after W0-W5 product work is proven.

Depends on: W0-W5 all green.

## Fixed release decision

The next integrated production candidate version is **1.5.0**.

There is exactly one candidate build for final acceptance:

~~~text
source SHA S (VERSION=1.5.0)
  -> hosted Build packages
  -> immutable candidate .deb/MSI/tar/zip + checksums
  -> every installed/VM/real-host acceptance runs those exact bytes
  -> tag 1.5.0 points to S
  -> release uploads those exact accepted bytes
~~~

Do not rebuild after final acceptance. A source change after candidate creation,
including a code/test/package change, invalidates the candidate and restarts W6.

Documentation-only evidence commits after acceptance are allowed only after the
accepted source/tag SHA is recorded explicitly; they do not become the release
source.

## Commit W6.1 — update acceptance wiring and authoritative roadmap contract

### Files

- `.github/workflows/toad.yml`
- `.github/workflows/desktop-ui.yml`
- `desktop/tests/run-required-suites.sh`
- `desktop/tests/run-required-suites.ps1`
- `desktop/tests/guest-run-suites.sh`
- `docs/roadmap.md`
- `docs/ui-coverage-matrix.md`.

### toad.yml acceptance-harness-contracts

Add to the Linux harness file list:

~~~text
linux/tests/toad/installed-desktop-journey-vm.sh
~~~

Require:
- file exists;
- bash -n;
- ShellCheck 0.10.0;
- harness/source identity block, same as existing VM harnesses.

If W0 Python journey has a standalone syntax/self-test mode, add:

~~~text
python3 -m py_compile desktop/tests/installed_desktop_journey.py
python3 desktop/tests/installed_desktop_journey.py --self-test
~~~

No graphical journey is claimed by hosted CI.

### desktop-ui required suites

Ensure `core-semantic-contract-test` from W0 is:

- a CTest target;
- run by Linux/macOS/Windows desktop UI workflow where supported;
- run by run-required-suites wrappers;
- zero skips.

For platform-specific rows that cannot execute RealCore on a platform, the
test binary must still run and explicitly execute the FakeCore/common rows;
Linux must execute both FakeCore and RealCore rows. Do not QSKIP the entire
test.

### Roadmap section 20 rewrite

Replace the old U8e definition. Split:

~~~text
U8c = installed-core Qt component compatibility
      desktop/tests/guest-run-suites.sh

U8e = installed Linux desktop journey
      linux/tests/toad/installed-desktop-journey-vm.sh
      + desktop/tests/installed_desktop_journey.py
      + real /usr/bin/kikimora-ui in GNOME/Wayland
~~~

State explicitly: U8c never substitutes for U8e.

Keep U9e as the existing Windows installed component/real-core lane until a
separate Windows real-shell first-run journey is designed; W1 is Linux-only.
However all W3-W5 cross-platform core/API semantics must pass U9e.

## Commit W6.2 — remove obsolete acceptance assertions from old G5/U8c

Update `linux/tests/toad/managed-package-lifecycle-vm.sh`:

- `assert_ui_payload` no longer requires
  `/etc/xdg/autostart/kikimora.desktop`;
- instead assert it is absent;
- assert `/usr/share/applications/kikimora.desktop`;
- assert helper, polkit policy, operator registry dir contract;
- assert fresh install still leaves the core network-inert/inactive;
- do **not** convert G5 into first-run desktop acceptance;
- managed_smoke continues to explicitly start core for its package/runtime purpose; its log must say that this preparation is G5-only and not U8e evidence.

Update guest-run-suites comments to U8c terminology.

This commit must not change product behavior.

## Commit W6.3 — exact source gate before version bump

Before changing VERSION, on the W0-W5 source head run:

### Go

~~~bash
cd toad
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./internal/control
go test -race -count=1 ./cmd/kikimora-core
~~~

`gofmt -l .` must print nothing.

### Desktop

Fresh build directory:

~~~bash
rm -rf build/w6-desktop
cmake -S desktop -B build/w6-desktop -DCMAKE_BUILD_TYPE=RelWithDebInfo
cmake --build build/w6-desktop --parallel 2
ctest --test-dir build/w6-desktop --output-on-failure
desktop/tests/run-required-suites.sh build/w6-desktop
~~~

Invoke exactly `desktop/tests/run-required-suites.sh build/w6-desktop`; do not replace the wrapper with a hand-picked subset.

Required:
- 0 failed;
- 0 mandatory skipped;
- 0 unexpected QML warnings;
- CoreSemanticContract FakeCore/RealCore rows green.

### Package contracts

~~~bash
bash linux/tests/test_release_packaging.sh
bash linux/tests/test_integrated_release_packaging.sh dist
bash -n linux/tests/toad/installed-desktop-journey-vm.sh
shellcheck --shell=bash --exclude=SC1091   linux/tests/toad/installed-desktop-journey-vm.sh
python3 -m py_compile desktop/tests/installed_desktop_journey.py
~~~

### Existing strong regression scopes

Run the existing targeted tests containing:

- bypass_handover_test;
- bypass_owner_test;
- profile_switch_epoch_test;
- profile_membership_test;
- startup_intent_test;
- committed_state_test;
- selected-route managed recovery tests;
- package lifecycle static contracts.

Do not delete old tests because a new higher-level journey exists.

Record command/output summary in a new execution log:
`docs/toad-steps/09g-execution-log.md`.

## Commit W6.4 — version 1.5.0 source freeze

Only after W6.3 is green:

1. change `VERSION` from 1.4.0 to **1.5.0**;
2. update version-specific package assertions/docs that are source contracts;
3. do not update historical evidence rows as if 1.5.0 had already passed;
4. run:
   - git diff --check;
   - version/package unit tests;
   - full required source gates affected by VERSION.

Commit message:

~~~text
release: prepare kikimora 1.5.0 candidate
~~~

Record exact source SHA `S = git rev-parse HEAD`.

From this point no source mutation is allowed without discarding the candidate.

## W6.5 — hosted CI on exact SHA S

Push S and require success for every path-triggered workflow:

- Desktop UI;
- Toad core;
- Build packages;
- ShellCheck;
- any endpoint/parking/profile workflow triggered by the changed source.

Record exact run IDs.

### Build packages artifacts

From the `Build packages` run on S download, without rebuilding:

~~~text
kikimora_1.5.0_amd64.deb
kikimora-1.5.0-linux-amd64.tar.gz
SHA256SUMS
kikimora-1.5.0-windows-amd64.msi
kikimora-1.5.0-windows-amd64.zip
SHA256SUMS-windows
~~~

Verify:
- hosted workflow metadata prints source S;
- VERSION 1.5.0;
- Leshy requested/resolved pin exact;
- SHA256SUMS matches downloaded bytes;
- extract /usr/share/kikimora/build-info.json from the downloaded .deb and
  require source_sha == S, version == 1.5.0, leshy_sha == the pinned resolved
  Leshy SHA;
- integrated package contract green.

Create a local immutable acceptance directory:

~~~text
artifacts/acceptance-1.5.0/
  <downloaded artifacts>
  source-sha.txt
  workflow-runs.txt
  sha256.txt
~~~

Do not edit artifact bytes.

## W6.6 — disposable Ubuntu package/runtime regressions on exact candidate

Use the documented VM transport only:

~~~text
python3 scripts/lab/vm.py put ubuntu <file> <remote>
python3 scripts/lab/vm.py run ubuntu "<command>"
python3 scripts/lab/vm.py run ubuntu --sudo "<command>"
~~~

Do not use hardcoded IP unless discovery fails; fallback remains documented in
`docs/lab-vms.md`.

Upload exact candidate + versioned harnesses.

### G5 fresh

Run existing:

~~~text
managed-package-lifecycle-vm.sh --mode fresh
~~~

Required:
- package payload correct;
- fresh service/network behavior remains inert;
- managed_smoke green when that harness explicitly starts core;
- no system XDG autostart entry;
- helper/polkit/registry package contract present.

### G6 upgrade rows

Run all currently required upgrade modes against the same 1.5.0 candidate and
the retained previous accepted baseline package where available.

No row may be removed because W1 changed postinst/service behavior.

### Core managed-routing/profile regression

Run the current accepted Ubuntu VM suites from roadmap G/K, including:

- managed selected routing;
- managed dual profile;
- mode transition crash/recovery;
- managed reboot;
- disconnect teardown;
- package lifecycle;
- profile/default switch with two roles.

The new W4 profile-switch journal needs failpoint/reboot execution on this VM,
not only unit tests.

## W6.7 — real installed GNOME/Wayland journey on exact candidate

Reset the disposable Ubuntu GUI VM to its clean snapshot.

Run:

~~~text
linux/tests/toad/installed-desktop-journey-vm.sh \
  --candidate <exact kikimora_1.5.0_amd64.deb> \
  --expected-version 1.5.0 \
  --expected-source-sha <40hex S> \
  --expected-deb-sha256 <64hex candidate digest> \
  --user greymouse \
  --artifact-dir <persistent evidence dir> \
  --phase fresh
~~~

Required journey, through accessible UI controls, not CLI shortcuts:

1. install candidate;
2. verify core initially inactive/no roles connected;
3. launch exact desktop entry with `gtk-launch kikimora`;
4. UI shows first-run Initialize, not API-incompatible;
5. authorize once;
6. same UI PID/session reaches Ready without logout/reboot;
7. create profile A;
8. import native AWG fixture with nontrivial obfuscation fields and explicit
   display/technical name;
9. role appears live/stopped in A;
10. connect and reach Ready;
11. set A default;
12. create profile B and a second role;
13. while B is inactive, store B's disconnected role as B's future default preference;
14. switch A->B and B->A in one UI action each; the switch stages/connects the target default and performs the protected handover;
15. edit connected role endpoint/display field using safe reload transaction;
16. force one rejected edit and prove old config remains working;
17. remove loaded stopped non-owner role live;
18. rename and remove a profile;
19. restart UI;
20. restart core;
21. verify authorized same account reconnects without re-enrollment;
22. reboot host and run `--phase post-reboot`;
23. verify active profile/desired/default/operator ACL state;
24. toggle autostart off, log out/in, assert no UI;
25. toggle on, log out/in, assert UI starts;
26. final DisconnectAll and verify no owned selected routes/interfaces remain.

The journey captures exact:
- source SHA;
- candidate SHA256;
- package version;
- UI executable path;
- UI PID;
- session type;
- each AT-SPI action/result;
- final core snapshot;
- journal;
- systemctl state.

Zero skipped steps.

## W6.8 — Windows regression on the same source SHA

Because W3-W5 modify shared Go/core and Qt APIs, require the 1.5.0 Windows
candidate from the same source SHA S to pass:

- package payload contract;
- FreshMsi;
- managed package lifecycle rows currently required by H;
- U9e real-core Qt component runner;
- profile/default/create/edit API tests supported on Windows;
- no new mandatory skips.

Linux polkit/operator ACL code is build-tagged/packaging-specific and must not
change Windows socket authorization.

Record MSI/zip hashes and test totals.

## W6.9 — operator workstation clean-install canary

Only after disposable VM and Windows gates are green, run the same accepted
Linux .deb on the operator-designated workstation.

This is the final proof for:
- actual GNOME/Wayland desktop identity/icon;
- graphical polkit prompt;
- same-session authorization;
- Wi-Fi/default-route environment;
- real existing VPN provider profile import;
- actual session login/autostart.

Use the migration/rollback procedure separately from product acceptance. Keep a
tested rollback until 1.5.0 passes the canary.

Minimum canary:

- first launch/init without terminal;
- native real provider config imported through UI;
- two VPN roles/profiles usable;
- profile A/B switching;
- disconnect/reconnect;
- core restart;
- host reboot;
- no default-route leak;
- no stale old Kikimora units/files;
- autostart setting truthful.

Do not mark production based only on ubuntu2604 VM again.

## W6.10 — final evidence and publication, no rebuild

Create/update `docs/toad-steps/09g-execution-log.md` with:

~~~text
source SHA S
VERSION 1.5.0
Leshy tag/SHA
Build packages run ID
Desktop UI run IDs
Toad/ShellCheck run IDs
deb SHA256
tar SHA256
MSI SHA256
zip SHA256
G5/G6 results
managed routing/profile crash/reboot results
U8c component totals
U8e real installed journey result + evidence path
U9e Windows totals
operator workstation canary result
all mandatory skips = 0
all unexpected QML warnings = 0
~~~

Update:
- `docs/roadmap.md`;
- `docs/ui-coverage-matrix.md`;
- desktop audit/test plan docs;
- `docs/toad-steps/08d1-execution-log.md` with a new historical append-only
  1.5.0 closure entry.

Do not rewrite the 1.4.0 GREEN history. State explicitly why it was insufficient
and which W0/W6 gate supersedes its installed-desktop claim.

### Tag/release

Only after all gates above:

1. tag `1.5.0` at exact SHA S;
2. push tag;
3. create GitHub release;
4. upload **the already accepted files** from
   `artifacts/acceptance-1.5.0/`;
5. verify uploaded asset digests equal the recorded acceptance digests;
6. no rebuild and no asset replacement after publication.

Production-ready status in roadmap changes from revoked to accepted only after
this digest verification.

## W6 DONE definition

W6 is complete only when one immutable 1.5.0 source/artifact identity has:

- old core/routing/profile safety gates green;
- FakeCore/RealCore semantic contract green;
- Linux package fresh/upgrade gates green;
- real installed GNOME/Wayland journey green;
- Windows shared-code/package regressions green;
- operator workstation canary green;
- published artifact hashes identical to accepted artifact hashes.

A green offscreen Qt suite by itself can never restore Linux desktop production
acceptance.
