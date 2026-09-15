# Real OpenConnect Toad tests

Use these tests in two stages: first a real-server isolated preflight with a diagnostic archive, then a persistent system-wide `up/status/down` controller with optional diagnostic recording.

Both use the same one-file local secret profile. The populated profile is ignored by Git.

## 1. Prepare the local profile once

From the repository root:

```bash
cp linux/tests/toad/real-vps-openconnect.example.toml \
   linux/tests/toad/real-vps-openconnect.secret
chmod 0600 linux/tests/toad/real-vps-openconnect.secret
```

Edit `linux/tests/toad/real-vps-openconnect.secret` and fill the real `username`, `password`, and `totp_secret` from the NetworkManager/OpenConnect profile. The committed template already uses the production gateway `https://ve.ad-tech.ru` and `vpn_protocol = "anyconnect"`.

Do not commit or attach the populated `.secret` file. The test materializes password and TOTP into private runtime files and passes only file paths to `kikimora-toad`; secret values are not put in argv or diagnostic metadata.

## 2. Stage A: isolated real-server diagnostic preflight

Run this first:

```bash
./linux/tests/toad/real-vps-openconnect-diag-test.sh
```

Optional explicit output path:

```bash
TOAD_DIAG_OUTPUT="$PWD/openconnect-preflight.tar.gz" \
./linux/tests/toad/real-vps-openconnect-diag-test.sh
```

This test creates a private network namespace with a `slirp4netns` uplink. It does **not** change the host default route or host DNS.

Inside the namespace it:

1. starts the real `kikimora-toad` against the production OpenConnect server;
2. waits for the managed `kk-oc0` and online state;
3. reads the DNS servers pushed by OpenConnect (or the explicit profile override, if configured);
4. routes only those DNS servers, Google, and `gitlab.sca.ad-tech.ru` through `kk-oc0`;
5. keeps the OpenConnect gateway on the isolated slirp underlay;
6. requires Google HTTP 200 with a substantial body;
7. requires the private GitLab to resolve through VPN DNS and return HTTP 2xx/3xx/401/403;
8. proves the two target flows crossed `kk-oc0` and did not leak onto the slirp underlay;
9. records ChatGPT as `SKIPPED_EXPECTED_UNAVAILABLE` and never uses it as an acceptance target;
10. tears down the namespace and writes one compact diagnostic archive.

The default archive is:

```text
toad-real-vps-openconnect-diag-YYYY-MM-DD-HHMMSS.tar.gz
```

Do not continue to Stage B until this preflight passes.

## 3. Stage B: persistent system-wide controller

Stop/disconnect the old Kikimora/VPN yourself first. The controller never stops or modifies it on your behalf and refuses to start if another VPN-like default route is still active.

Start OpenConnect system-wide and enable diagnostics:

```bash
./linux/tests/toad/real-vps-openconnect-system-wide-vpn.sh up --diag
```

Optional archive path:

```bash
./linux/tests/toad/real-vps-openconnect-system-wide-vpn.sh \
  up --diag --diag-output "$PWD/openconnect-system-wide.tar.gz"
```

Optional non-default profile path:

```bash
./linux/tests/toad/real-vps-openconnect-system-wide-vpn.sh \
  up --diag /path/to/profile.secret
```

`up` returns after the automatic acceptance gates pass and **leaves the VPN active**. You can then use the browser for as long as needed.

Check ownership/state without changing anything:

```bash
./linux/tests/toad/real-vps-openconnect-system-wide-vpn.sh status
```

When manual testing is finished:

```bash
./linux/tests/toad/real-vps-openconnect-system-wide-vpn.sh down
```

`down` removes the system-wide default route and DNS configuration owned by the test, stops the Toad, removes the endpoint pin, then closes and writes the diagnostic archive started by `up --diag`.

## System-wide acceptance targets

The OpenConnect targets intentionally differ from AWG/Xray:

- `https://www.google.com/` must return HTTP 200 through `kk-oc0`;
- `https://gitlab.sca.ad-tech.ru/` must resolve using OpenConnect DNS and return HTTP 2xx/3xx/401/403 through `kk-oc0`;
- ChatGPT is intentionally not probed because it is expected to be unavailable through this VPN;
- the OpenConnect endpoint remains pinned to the original physical underlay to prevent recursive routing;
- server-pushed OpenConnect DNS becomes the system resolver for the test (`~.` on `kk-oc0`), unless an explicit local `dns_servers` override is present.

The system-wide controller is IPv4-only for this acceptance run and refuses to cut over while an IPv6 default route exists, avoiding an untested IPv6 leak path.

## Diagnostics

The isolated test always records diagnostics. The persistent system-wide controller records them only when `up` is given `--diag` or `--diag-output`.

The bundle includes before/connected/active/before-down/after network, route, DNS and runtime snapshots; Toad state; server-pushed `openconnect-network.env`; Google and private-GitLab probe results; controller/kernel journal; and a compact summary. Password, TOTP seed, and username are redacted before packaging. Large text files are compacted and archives above roughly 4 MiB receive a second compaction pass.
