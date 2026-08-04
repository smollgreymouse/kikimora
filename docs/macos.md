# macOS orchestration (experimental)

Leshy may run on macOS, but Kikimora's Linux installer and its `systemd`/
`systemd-resolved` integration do not. The `macos/` bundle provides a separate
root-managed orchestration layer built on `launchd`, `networksetup` and
`ifconfig`.

It runs three LaunchDaemons:

- `com.kikimora.leshy` — the Leshy process;
- `com.kikimora.route-watch` — publishes ready VPN interfaces to Leshy;
- `com.kikimora.health-watch` — verifies the local resolver and repairs DNS.

## Install

Install a working Leshy binary at `/usr/local/bin/leshy` first, then identify
the VPN interfaces and macOS network-service names:

```bash
ifconfig
networksetup -listallnetworkservices
```

Run the separate installer. Multiple `--dns-service` options are supported:

```bash
sudo ./install.sh \
  --primary-interface utun4 \
  --secondary-interface utun5 \
  --dns-service "Wi-Fi" \
  --leshy-config /path/to/config.toml \
  --start
```

Use `sudo kikimora` or its `sudo kk` alias with `status`, `start`, `stop`,
`restart`, or `dns {status|check|enable|disable|suspend|resume}` afterwards.

## DNS ownership and recovery

The macOS backend intentionally manages only the network services specified at
installation. Before it assigns `127.0.0.1`, it saves each service's current
DNS configuration under `/var/db/kikimora/leshy`. `stop`, `dns suspend`, and
DNS-health fallback restore those values; `dns disable` then removes the saved
state.

This keeps the recovery path bounded: do not add a VPN service to
`DNS_SERVICES` unless Kikimora should own and restore its DNS servers.

## Routing scope

Route Watch does not create macOS routes itself. It detects ready `utun*`
interfaces and writes the device files that Leshy consumes. Confirm that the
installed Leshy build supports your macOS route behavior before relying on it
for production traffic.

## Validation

After installation, verify:

```bash
sudo kikimora status
sudo kk dns check
dig @127.0.0.1 -p 53053 example.com
log show --last 10m --predicate 'process == "kikimora"'
```

Test a VPN reconnect and confirm that `kk dns check` returns zero
after Health Watch repairs resolver ownership.
