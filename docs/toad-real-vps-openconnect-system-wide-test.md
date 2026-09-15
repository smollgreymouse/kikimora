# Real OpenConnect system-wide Toad test

This is the real-server acceptance test for the managed OpenConnect Toad (`kk-oc0`). It uses the production gateway and credentials from one local secret TOML file. The populated secret file is intentionally ignored by git.

## 1. Prepare the local profile

Copy:

```bash
cp linux/tests/toad/real-vps-openconnect-profile.example.toml \
   linux/tests/toad/real-vps-openconnect.secret
chmod 0600 linux/tests/toad/real-vps-openconnect.secret
```

Edit `linux/tests/toad/real-vps-openconnect.secret` and put the real values from the NetworkManager profile into:

- `username`
- `password`
- `totp_secret`

The committed example already contains the real gateway `https://ve.ad-tech.ru/` and AnyConnect/OpenConnect defaults used by the test. Do not commit the populated `.secret` file.

The test materializes the password and TOTP seed into private runtime files and gives `kikimora-toad` only file paths. Password/TOTP are not placed in process argv or the diagnostic bundle.

## 2. Before the system-wide test

Disconnect/stop the old Kikimora/VPN yourself. The test deliberately never stops or modifies legacy Kikimora for you. It refuses to start if another VPN-like default route or managed Toad is active.

The test is IPv4-only and refuses to perform the cutover while an IPv6 default route exists, to avoid an IPv6 leak during this acceptance run.

## 3. Run

From the repository root:

```bash
./linux/tests/toad/real-vps-openconnect-system-wide-test.sh
```

The default manual browser window is 120 seconds. Override it when needed:

```bash
TOAD_SYSTEM_WIDE_HOLD_SECONDS=180 \
./linux/tests/toad/real-vps-openconnect-system-wide-test.sh
```

## Acceptance targets

The test intentionally uses different targets from the public AWG/Xray acceptance tests:

1. `https://www.google.com/` must return HTTP 200 through `kk-oc0` with a substantial response body.
2. `https://gitlab.sca.ad-tech.ru/` must resolve using the VPN DNS and return an HTTP 2xx/3xx/4xx response through `kk-oc0`. This is the private reachability gate because the host is expected to be reachable only through this VPN.
3. ChatGPT is intentionally **not** probed and is recorded as `SKIPPED_EXPECTED_UNAVAILABLE`, because it is expected not to work through this VPN.
4. The OpenConnect gateway is pinned to the original physical underlay before the system default route is changed, preventing recursive routing through `kk-oc0`.
5. Server-pushed OpenConnect DNS is applied through `systemd-resolved`; a local `dns_servers` override exists only for a server that genuinely omits DNS.

After the automatic probes pass, the system-wide VPN remains active for the manual browser window. Press Enter to finish early or wait for the timeout.

## Automatic rollback and diagnostics

On normal completion, error, Ctrl+C, or timeout cleanup, the test restores the route/DNS state it owns, stops the temporary Toad unit, removes the endpoint pin, and collects before/active/after snapshots.

The output is one bounded archive similar to the other Toad system-wide diagnostics:

```text
toad-system-wide-openconnect-diag-YYYY-MM-DD-HHMMSS.tar.gz
```

The bundle contains `summary.txt`, route/interface/DNS/runtime snapshots, Toad state, server-pushed OpenConnect network metadata, bounded packet trace, Toad/kernel journals, probe results, and a two-second sampler from the manual window. Secret values are redacted and large text files are compacted; archives over about 4 MiB are compacted a second time.
