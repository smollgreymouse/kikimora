# Toad step 01 — platform boundary and Linux-owned TUN

## Goal

Implement the first real platform layer for Toad without integrating any VPN protocol core yet.

At the end of this step, Linux Toad code must be able to create one real L3 TUN, configure its address/MTU, keep ownership of the TUN fd, duplicate that fd, and prove in an isolated network namespace that closing the duplicate does not recreate or remove the TUN.

Do not implement AWG2 or Xray in this step.

## Read first

1. `docs/vpn-native-core-roadmap.md`
2. `docs/vpn-native-core-stage0.md`
3. `docs/toad-naming.md`
4. `toad/README.md`
5. current `toad/internal/config`, `state`, and `backend` packages

## Hard constraints

- `toad/` remains cross-platform.
- Linux-only imports must appear only in files guarded by `//go:build linux`.
- macOS and Windows jobs in the existing Toad CI must continue compiling/testing.
- Do not invoke `ip`, `ifconfig`, `nmcli`, NetworkManager, or shell commands from production Go code.
- Do not create routes. Leshy/Kikimora routing policy is outside this step.
- Do not create a default route in tests.
- Do not change config/state schema unless required by a concrete compile/test failure; if required, stop and report instead of redesigning it.

## Required code shape

Create `toad/internal/platform/tun.go` with the common platform-neutral contract:

```go
type TunnelSpec struct {
    Name      string
    MTU       int
    Addresses []netip.Prefix
}

type Tunnel interface {
    Name() string
    IfIndex() int
    MTU() int
    Close() error
}

func CreateTunnel(spec TunnelSpec) (Tunnel, error)
```

The exact implementation of `CreateTunnel` must be selected through build-tagged files.

Create Linux implementation in `toad/internal/platform/tun_linux.go` with `//go:build linux`.

The Linux concrete tunnel type must additionally expose:

```go
DuplicateFile() (*os.File, error)
```

Do not add `DuplicateFile` to the common `Tunnel` interface. It is a Linux attachment detail used by the next AWG step.

Create an unsupported implementation for non-Linux builds, e.g. `tun_unsupported.go` guarded by `//go:build !linux`, which returns a clear `unsupported platform` error from `CreateTunnel` but allows the package and CLI to compile on macOS/Windows.

## Linux implementation requirements

Use `/dev/net/tun` directly for the owner fd.

Use Linux `TUNSETIFF` with:

- `IFF_TUN`
- `IFF_NO_PI`

The owner fd must stay open until `Tunnel.Close()`.

Validate Linux interface-name length here, not in generic config. Linux names must fit `IFNAMSIZ-1`.

Use Go APIs/libraries, not shell commands, to:

- discover ifindex;
- set MTU;
- add all configured addresses;
- set the interface up.

Preferred Linux dependencies:

- `golang.org/x/sys/unix` for TUN/ioctl/fd duplication primitives;
- `github.com/vishvananda/netlink` for link/address configuration.

If a chosen API signature differs from the installed dependency, inspect that dependency and adapt. Do not invent an API name.

`DuplicateFile()` must duplicate the owner fd with close-on-exec semantics. Closing the returned duplicate must not close the owner's fd.

`Close()` must be idempotent. For a non-persistent Linux TUN, closing the final owner fd should be sufficient to remove the interface; do not call `LinkDel` as part of ordinary protocol reconnect behavior.

## Unit tests

Add non-privileged tests for:

- invalid/empty name;
- invalid MTU;
- Linux overlong name in a Linux-only test;
- non-Linux `CreateTunnel` returns unsupported when applicable.

Do not mock protocol traffic.

## Privileged isolated test

Add a Linux test script under `linux/tests/toad/tun-owner-netns.sh`.

It must:

1. create a disposable network namespace;
2. ensure the namespace has no default route;
3. launch a tiny Go test/helper using the real `platform.CreateTunnel` inside that namespace;
4. create `kk-toad0` with a private test address and MTU;
5. record its ifindex;
6. obtain a duplicated fd via `DuplicateFile()`;
7. close only the duplicate;
8. assert `kk-toad0` still exists and has the same ifindex;
9. close the owner tunnel;
10. assert `kk-toad0` disappears;
11. assert `kk-toad0` never appeared in the runner root namespace.

No NAT and no public/default route may be added.

Add a Linux-only CI job for this privileged test; keep the existing cross-platform unit matrix.

## Acceptance criteria

This step is complete only when all are true:

- `go test ./...` passes in `toad/`;
- `go vet ./...` passes;
- `gofmt -l .` is empty;
- Linux, macOS and Windows Toad unit jobs are green;
- the real Linux namespace TUN test is green;
- duplicate-fd close preserves the same TUN ifindex;
- owner close removes the TUN;
- no protocol dependency has been added yet.

## Forbidden shortcuts

Do not:

- use `water`, `wireguard-go`, Xray, or AmneziaWG yet;
- use a mock TUN as the acceptance test;
- run privileged TUN tests in the host/root namespace;
- weaken the macOS/Windows compile gate;
- move the runtime back under `linux/`.

## Executor report

Return a short report containing:

1. commit SHA(s);
2. files changed;
3. exact dependencies added;
4. CI run/result;
5. output of the namespace TUN test;
6. confirmation that duplicate close preserved ifindex;
7. any unresolved issue.

If any required API or privilege behavior cannot be established, do not guess. Stop with the exact compile/runtime error and the relevant code/API inspected.
