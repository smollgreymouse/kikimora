# Changelog

All notable changes to Kikimora are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.2.1] - 2026-10-31

### Added

- Experimental macOS orchestration bundle based on `launchd`, `networksetup`,
  and `ifconfig`.
- Linux `kk debuglog` command for collecting service, journal, routing, DNS,
  runtime and configuration diagnostics into one file.

### Changed

### Fixed

- Bundled Leshy 0.5.2: a zone pinned to a managed device is IPv4-only by
  default, so an AAAA answer can no longer send a client around the tunnel
  over the physical link (Windows Edge could not open chatgpt.com while
  curl over the tunnel succeeded). A zone can opt back in with
  `ipv6 = true` when its device really carries IPv6.
- AWG2 health treated a 30 s handshake age as stale, but WireGuard rekeys at
  120 s (REKEY_AFTER_TIME) and persistent keepalives do not refresh the
  handshake. Every healthy idle tunnel was therefore classified as stale once
  per rekey cycle: the role entered Recovering, its live transport was
  restarted, and the fail-closed path withdrew the Leshy publication. The
  window now follows the protocol reject deadline (180 s), so only a session
  the protocol itself refuses to use is reported as reconnecting.

### Removed

### Security

## [1.0.0] - 2026-07-25

### Added

- First stable release.
- Domain-based routing through multiple VPN interfaces.
- Primary VPN support using `amn0`.
- Secondary VPN support using `vpn0`.
- Configurable default routing zone.
- Dedicated `leshy-dns0` resolver interface.
- `systemd-resolved` integration.
- Route Watch for VPN interface and route recovery.
- Health Watch for Leshy and DNS integration monitoring.
- Public `leshy-dns check` command.
- Runtime resolver validation.
- Safe `leshy-dns resume`.
- Safe `leshy-dns suspend`.
- Automatic DNS repair after VPN reconnects.
- Operator-facing status reporting through `kk status`.
- systemd service integration and journald logging.

### Changed

- Resolver integration knowledge is centralized in `leshy-dns`.
- Health Watch uses the public `check`, `resume`, and `suspend` interface.
- Runtime state is validated directly instead of trusting marker files.
- Existing DNS interfaces are repaired in place when possible.
- Conflicting resolver routing domains are cleared before restoring `~.`.

### Fixed

- DNS bypass after AmneziaVPN reconnect.
- Loss of routing domain `~.` from `leshy-dns0`.
- Stale resolver state remaining undetected while Leshy still answered locally.
- Failure to recover DNS without restarting Leshy.
- Duplicate resolver validation logic between components.
- Incorrect assumption that an enable marker guaranteed healthy runtime state.

### Known limitations

- AmneziaVPN Kill Switch is incompatible with Kikimora routing and must be
  disabled.
- Application-level DNS-over-HTTPS may bypass Leshy.
- IPv6 behavior depends on the local VPN and routing configuration.

[Unreleased]: https://github.com/smollgreymouse/kikimora/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/smollgreymouse/kikimora/releases/tag/v1.0.0
