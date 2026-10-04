# Toad step 08A.4a — Windows native networking substrate

Status: **PLANNED / IMPLEMENT BEFORE 08A.4 VM ACCEPTANCE**.

This packet exists because `08a4-windows-vm-console-lifecycle-acceptance.md`
is an acceptance packet, not an implementation plan. The current Windows target
is still Qt/FakeCore only and cannot be sent through the Linux-equivalent VM
lifecycle tests until a real native networking substrate exists.

## Goal

Bring the accepted Linux control-plane contracts to Windows without inventing a
second orchestration model.

The Windows implementation must preserve the same product semantics:

- one installed core service;
- independently supervised Toad processes;
- persistent desired state;
- revisioned Snapshot/Subscribe API;
- explicit underlay epochs;
- endpoint exception routing before transport startup;
- fail-closed selected traffic;
- per-role stable interface identity;
- route readiness distinct from process/session health;
- recovery states including WaitingForUnderlay/Recovering;
- no frontend ownership of orchestration.

## 1. Windows service/runtime shell

Implement production Windows service hosting for `kikimora-core.exe`.

Required:

- Service Control Manager registration and automatic startup;
- recovery policy after unexpected core death;
- graceful stop with bounded Toad shutdown;
- crash-safe desired state under `C:\ProgramData\Kikimora`;
- structured logs suitable for Event Log or an explicitly documented file sink;
- no secrets in Event Log;
- no desktop-login dependency.

The service identity and ACL model must be documented before package activation.

## 2. Local control IPC and authorization

Replace the current unsupported-platform allow-all peer authorization.

Required properties:

- local-only transport;
- authenticated/authorized local clients;
- ACL that permits the intended administrator/operator group and rejects
  unrelated local users;
- same Handshake/capabilities/Snapshot/Subscribe/control schema as Linux;
- reconnect/resubscribe after core restart;
- `kk.exe` uses the real local API, never FakeCore.

Preferred transport may be a Windows named pipe or a protected Windows AF_UNIX
socket, but the security contract is mandatory regardless of transport choice.

## 3. Native Windows underlay observer

Implement a Windows underlay/default-path observer that emits the same semantic
inputs as the accepted Linux observer.

It must detect:

- physical default-route changes;
- interface up/down;
- DHCP address replacement;
- gateway replacement;
- IPv4 and IPv6 availability changes;
- suspend/resume return;
- adapter disable/enable;
- hypervisor cable disconnect/reconnect.

Material changes increment the canonical underlay epoch exactly as on Linux.

Do not poll PowerShell commands as the production observer. Use native Windows
network-change facilities and expose deterministic adapters for tests.

## 4. Windows managed TUN ownership

Implement Windows TUN ownership behind the existing platform abstraction.

Required:

- deterministic create/open/close lifecycle;
- stable role-owned interface identity;
- address/MTU configuration;
- idempotent cleanup after core/Toad crash;
- no stale duplicate adapters after repeated recovery;
- reboot/cold-boot cleanup semantics;
- integration tests using a fake adapter layer plus privileged VM tests using the
  real backend.

The backend may use Wintun or another explicitly accepted driver. Driver
installation/versioning must be owned by the Windows package contract.

## 5. Native Windows route manager

Implement the accepted route-manager semantics on Windows.

Mandatory capabilities:

- physical endpoint exception route before transport process startup;
- selected-route ownership;
- fail-closed parking when selected transport is unavailable;
- route rebind when underlay interface/gateway/address changes;
- IPv4 and IPv6 handling;
- exact ownership tracking so cleanup never removes unrelated administrator
  routes;
- no duplicate routes after repeated recovery;
- route state exposed through the canonical snapshot.

Use native Windows route/IP Helper APIs or an equivalent supported native API.
PowerShell/netsh command execution is acceptable only as a diagnostic/test
helper, not as the production ownership mechanism.

## 6. Windows DNS ownership

Implement explicit DNS ownership and recovery semantics.

Required:

- role/system-wide DNS state is observable;
- desired DNS configuration is reapplied after interface recreation or resume;
- stale DNS configuration is removed when ownership ends;
- no silent physical-DNS fallback when the selected fail-closed policy forbids
  it;
- DNS state is included in diagnostics.

The design must document whether Windows system DNS, per-interface DNS, NRPT or
another supported mechanism owns each use case.

## 7. Real Windows protocol backends

AWG and OpenConnect are mandatory for the first Windows real-networking gate.

Each backend must satisfy the same Toad contract as Linux:

- independently supervised process/session;
- stable managed interface identity;
- explicit session health;
- no ambient route ownership inside the protocol process;
- no protocol core may install uncontrolled default/split-default routes;
- restart/recovery does not disturb unrelated roles.

Xray remains a separate lane and may be enabled after the mandatory AWG +
OpenConnect Windows gate, provided the common routing/fail-closed contracts are
already satisfied.

## 8. Windows recovery ordering regressions

Port regression coverage for bugs already found on Linux:

1. endpoint exception route must exist before transport startup;
2. missing endpoint address family is transient, not terminal;
3. DNS unavailable during staged underlay return is transient;
4. a non-ready Toad snapshot must not erase an active recovery transaction;
5. WaitingForUnderlay must move back to Recovering when physical underlay
   returns;
6. repeated core/Toad/link recovery must not accumulate stale process/interface/
   route state.

These are product-contract tests, not Linux-specific tests.

## 9. Windows installer

Replace the scaffold with a real installer.

The installer must own:

- core/Toad/kk binaries;
- service registration;
- driver/runtime dependencies;
- ProgramData directories and ACLs;
- upgrade;
- ordinary uninstall/reinstall;
- explicit purge semantics;
- package version and artifact hashes.

Fresh install must not silently set any VPN role desired=true.

The exact lifecycle acceptance is defined in
`08a4-windows-vm-console-lifecycle-acceptance.md`.

## 10. Deterministic and cross-build gates

Before privileged VM testing:

- Windows amd64 build passes;
- unit tests for underlay adapter pass;
- route-manager model tests pass;
- TUN lifecycle model tests pass;
- local IPC authorization tests pass;
- endpoint-ordering/recovery regression tests pass;
- package static tests pass;
- Linux behavior remains unchanged.

Do not weaken Linux tests to make Windows compile.

## 11. VM handoff gate

08A.4a is complete only when a disposable Windows VM can:

- install the real package;
- start KikimoraCore before desktop login;
- expose real `kk status --json`;
- connect real AWG and OpenConnect;
- produce real managed interfaces and routes;
- pass one baseline real-application probe per mandatory role.

Only then execute
`docs/toad-steps/08a4-windows-vm-console-lifecycle-acceptance.md`.

## Completion state

A Windows UI/FakeCore build does not satisfy this packet.

Expected handoff:

`Windows native runtime substrate implemented; proceed to 08A.4 VM lifecycle parity acceptance.`
