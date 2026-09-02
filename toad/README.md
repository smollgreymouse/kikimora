# Kikimora Toad managed VPN runtime

`toad/` is the cross-platform production managed-VPN runtime built around official protocol cores.

A **Toad** is one independently managed VPN client instance. Kikimora orchestrates Toads; Leshy owns routing policy.

Current status: **skeleton only**. Configuration, validation, state schema and backend boundary are implemented. Protocol backends and platform-specific TUN integration are intentionally not wired yet.

Cross-platform rule:

- protocol/runtime/config/state code lives under `toad/`;
- OS-specific TUN, privilege, service-manager and packaging code must live behind platform-specific files/packages;
- Linux-specific deployment/integration may use the existing top-level `linux/` tree, but the Toad runtime itself is not Linux-only.

Commands:

```bash
go run ./cmd/kikimora-toad validate -config /path/to/client.toml
go run ./cmd/kikimora-toad run -config /path/to/client.toml
```

Future user-facing Kikimora commands live under the `kk toad ...` command group. Do not implement commands ahead of the active execution packet.

`run` deliberately fails until a protocol backend is implemented. Do not replace that failure with a fake/mock production backend.

Architecture source of truth:

1. `../docs/toad-roadmap.md`
2. `../docs/toad-stage0.md`
3. `../docs/toad-naming.md`
4. `../docs/toad-steps/`
