# Wintun redistributable drop-in

The Toad platform layer reaches the Wintun driver through the Go package
`golang.zx2c4.com/wintun`, which calls `LoadLibraryEx` with
`LOAD_LIBRARY_SEARCH_APPLICATION_DIR | LOAD_LIBRARY_SEARCH_SYSTEM32`. The DLL
must therefore sit next to `kikimora-core.exe` (or be installed machine-wide
into `System32`); `PATH` is never searched. That is why the Windows package has
to carry `wintun.dll` itself — without it no role can create its adapter, and
the failure only appears at runtime.

Drop the **64-bit** redistributable into this directory to make it the default
packaging input:

```text
packaging/windows/files/wintun/wintun.dll    (git-ignored, never committed)
packaging/windows/files/wintun/wintun.sha256 (optional single-line SHA-256 pin)
```

`stage.ps1` resolves the DLL in this order:

1. `-WintunDll <path>`;
2. `$Env:KIKIMORA_WINTUN_DLL`;
3. `wintun.dll` in this directory;
4. otherwise the release path **fails** — pass `-AllowMissingWintun` only for
   layout-only tests that do not ship a real package.

When `wintun.sha256` exists, the resolved DLL must match it; that is how a
reviewed vendor build stays pinned across machines. Obtain the DLL from
<https://www.wintun.net/>, place it here, and record the version plus hash in
the release notes.
