# Kikimora managed VPN runtime

This directory contains the production managed-VPN wrapper built around official protocol cores.

Current status: **skeleton only**. Configuration, validation, state schema and backend boundary are implemented. Protocol backends and Linux TUN integration are intentionally not wired yet; follow the current packets in `../../docs/vpn-native-core-steps/`.

Commands:

```bash
go run ./cmd/kikimora-vpn validate -config /path/to/client.toml
go run ./cmd/kikimora-vpn run -config /path/to/client.toml
```

`run` deliberately fails until a protocol backend is implemented. Do not replace that failure with a fake/mock production backend.

Architecture source of truth:

1. `../../docs/vpn-native-core-roadmap.md`
2. `../../docs/vpn-native-core-stage0.md`
3. `../../docs/vpn-native-core-steps/`
