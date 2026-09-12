# Toad naming convention

`Toad` is the project name for a Kikimora-managed VPN client/runtime instance.

This naming is part of the production architecture and should be used consistently by future implementation work.

## User-facing names

- standalone executable: `kikimora-toad`
- future Kikimora CLI group: `kk toad ...`
- per-instance service: `kikimora-toad@<name>.service`
- one running Toad = one independently managed VPN instance

Examples:

```text
kikimora-toad@awg-main.service
kikimora-toad@xray-main.service

kk toad list
kk toad status awg-main
kk toad start awg-main
kk toad stop awg-main
kk toad reconnect awg-main
```

The `kk toad` commands are roadmap/API names; do not implement commands ahead of the active execution packet.

## Repository/code names

The cross-platform runtime root is `toad/`.

Protocol backends keep technical names:

```text
toad/internal/backend/awg2/
toad/internal/backend/xray/
```

Platform-specific code belongs behind the common Toad runtime boundary, for example:

```text
toad/internal/platform/tun.go
toad/internal/platform/tun_linux.go
toad/internal/platform/tun_darwin.go
toad/internal/platform/tun_windows.go
```

## Names that do not change

Do not rename protocol terminology merely for theme consistency:

- `amneziawg2`, AWG2, AWG3
- VLESS, REALITY, Vision, Xray
- interface names such as `kk-awg0` and `kk-xray0`
- Leshy routing terminology
- external NetworkManager/OpenConnect interfaces such as `vpn0`

`Toad` describes the managed-client/runtime layer, not a new VPN protocol.

## Architectural meaning

A Toad reports protocol and interface facts. Kikimora orchestrates desired state. Leshy owns routing policy.

```text
Kikimora orchestrator
        |
        +--> Toad awg-main  --> official AmneziaWG core --> kk-awg0
        +--> Toad xray-main --> official Xray core       --> kk-xray0
        |
        v
      Leshy
```

The GUI planned for later controls Toads through the Kikimora control plane; it does not own their protocol lifecycle.
