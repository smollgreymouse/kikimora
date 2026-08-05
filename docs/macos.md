# macOS orchestration

The macOS package implements the same operating model as the Linux package
with native Darwin facilities:

- `launchd` supervises Leshy, Route Watch, and Health Watch;
- `networksetup` switches selected network services to the local resolver;
- `ifconfig` supplies VPN interface readiness;
- BSD `netstat` and `route` provide scoped lifecycle cleanup;
- `dscacheutil` and `mDNSResponder` cache flushing replaces `resolvectl`.

Linux files and services are not installed or modified on macOS.

## Important DNS difference

`networksetup` accepts DNS addresses but cannot specify a non-standard port.
The generated macOS Leshy configuration therefore listens on
`127.0.0.1:53`, not Linux's `127.0.0.1:53053`. Leshy runs as root under a
LaunchDaemon and can bind the privileged port.

## Installation

Identify the VPN interface names and the macOS network services whose DNS
Kikimora should manage:

```bash
ifconfig
networksetup -listallnetworkservices
scutil --nc list
```

Install from the repository root:

```bash
sudo ./install.sh \
  --primary-vpn-service "Primary VPN" \
  --secondary-vpn-service "Corporate VPN" \
  --dns-service "Wi-Fi" \
  --leshy-binary /path/to/leshy
```

Repeat `--dns-service` to manage more than one service. On reinstall, omitted
interface and DNS-service options are read from the installed configuration.
Domain lists, static route lists, and `routing.conf` are preserved.

macOS may assign a different `utunN` number after reconnect or reboot. For VPNs
registered in SystemConfiguration, prefer `--primary-vpn-service` and
`--secondary-vpn-service`; Route Watch resolves the current interface through
`scutil` on every cycle. Literal `--primary-interface` and
`--secondary-interface` remain available for clients with stable device names.

The installer stages, generates, and validates the complete configuration
before installing it. It does not start services, enable autostart, change DNS,
or connect VPNs unless `--start` is passed.

```bash
sudo kk start          # start now and enable DNS integration
sudo kk enable         # persist launchd autostart, do not start now
sudo kk enable --now   # persist autostart and start now
```

The installer keeps master plist files under `/usr/local/libexec/kikimora/macos`.
`kk start` bootstraps those files for the current boot only. `kk enable` copies
them into `/Library/LaunchDaemons`; `kk disable` removes the persistent copies.

## VPN readiness and route lifecycle

Each configured VPN must remain `UP` with an IPv4 address for three consecutive
one-second checks before its role file is published. Route Watch then records a
new baseline and flushes macOS DNS caches. When an interface disappears, only
IPv4 host routes created after the baseline are removed; routes that predated
Kikimora are preserved.

Runtime role files are stored under:

```text
/var/run/kikimora/leshy/vpn/primary.dev
/var/run/kikimora/leshy/vpn/secondary.dev
```

## DNS ownership and recovery

`sudo kk dns enable` snapshots the DNS servers for every configured network
service, points those services at `127.0.0.1`, and records persistent enabled
intent. `suspend` restores the snapshots but keeps intent; `resume` reapplies
Leshy DNS only when intent exists; `disable` restores DNS and forgets intent.

Health Watch checks Leshy every two seconds. After three consecutive failures
it restores the original DNS configuration. When Leshy responds again, DNS is
repaired automatically. A VPN client overwriting service DNS is also detected
and repaired with a cooldown.

## Configuration and CLI

macOS uses the same list-based configuration model:

```text
/usr/local/etc/kikimora/leshy/domains/{primary,secondary,bypass}.txt
/usr/local/etc/kikimora/leshy/routes/{primary,secondary}.txt
/usr/local/etc/kikimora/leshy/routing.conf
```

Useful commands:

```bash
kk status
kk interfaces
sudo kk domains add example.com --primary
sudo kk routes add 172.25.36.0/24 --secondary
sudo kk domains default secondary
sudo kk restart
sudo kk verify
sudo kk doctor
kk logs --no-follow -n 100
```

## Logs

LaunchDaemon output is written to:

```text
/var/log/kikimora/leshy.log
/var/log/kikimora/route-watch.log
/var/log/kikimora/health-watch.log
```

Use `kk logs` to follow all three files.
