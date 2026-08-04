# Changelog

All notable changes to Kikimora are documented in this file.

The format is based on Keep a Changelog 1.1.0, and the project follows Semantic Versioning.

## [Unreleased]

### Added

- Experimental macOS orchestration bundle based on `launchd`, `networksetup`,
  and `ifconfig`.
- Experimental Windows orchestration bundle based on PowerShell Scheduled Tasks,
  `Get-NetAdapter`, and Windows DNS client cmdlets.

### Changed

### Fixed

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
