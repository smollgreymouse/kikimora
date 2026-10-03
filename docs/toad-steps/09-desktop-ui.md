# Toad step 09 — desktop/UI over the accepted console control plane

Status: **FUTURE; START ONLY AFTER CHAPTER 08 CONSOLE/RUNTIME PACKAGE ACCEPTANCE**.

Chapter 09 adds a user interface to an already accepted product. It must not become a second orchestration implementation.

## Entry contract from chapter 08

Before UI work is release-relevant, chapter 08 must provide:

- installed `kikimora-core` and independently supervised Toads;
- stable `kk` console operations;
- versioned local API with Handshake/capabilities;
- canonical revisioned Snapshot;
- revisioned Subscribe stream;
- structured reason/error codes;
- reconnect/resubscribe behavior across core restart;
- Linux console/runtime `.deb`;
- real VM lifecycle acceptance.

The UI consumes this contract.

## UI architecture boundary

The desktop application must:

- connect to the same local core socket/API as the CLI;
- use Snapshot/Subscribe as its authoritative state;
- issue the same control methods as `kk`;
- never infer VPN truth from process lists, TUN presence or NetworkManager state independently;
- never parse human-readable CLI output;
- tolerate API reconnects and resume from a newer revision;
- present degraded/recovering/fail-closed states without inventing new state semantics.

FakeCore remains useful only for isolated UI tests.

## Packaging direction

Preferred Linux split:

```text
kikimora        core + Toads + kk + system integration
kikimora-ui     Qt UI + desktop assets, depends on compatible kikimora
```

A later decision may merge the packages for distribution convenience, but the console/runtime package must remain independently installable and testable.

Detailed UI/UX work, desktop lifecycle integration, tray behavior and visual debugging belong to this chapter, not chapter 08.
