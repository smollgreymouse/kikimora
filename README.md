# Kikimora

> macOS orchestration is available as an experimental, separate bundle. See
> [docs/macos.md](docs/macos.md).

![Version](https://img.shields.io/badge/version-1.0.0-blue)
![Platform](https://img.shields.io/badge/platform-Linux-lightgrey)
![Shell](https://img.shields.io/badge/shell-Bash-4EAA25?logo=gnu-bash&logoColor=white)
![systemd](https://img.shields.io/badge/init-systemd-black?logo=linux)
![License](https://img.shields.io/badge/license-MIT-green)

**Kikimora** is a Linux routing and DNS orchestration layer built around
[Leshy](https://github.com/ftelnov/leshy) for systems that use multiple VPN connections.

It routes selected domains through a primary VPN, sends all remaining traffic
through a secondary VPN, integrates Leshy with `systemd-resolved`, and repairs
the DNS configuration automatically when another VPN client overwrites it.

> [!IMPORTANT]
> Kikimora expects the VPN clients to allow normal Linux policy routing.
> AmneziaVPN Kill Switch must be disabled because it installs nftables rules
> that can block traffic independently of the routes configured by Kikimora.

## Platform packages

Run the same installer from the repository root on each supported platform:

```bash
sudo ./install.sh [platform options]
```

The root installer selects the implementation for the current OS:

- `linux/` contains the systemd-based Linux package;
- `macos/` contains the experimental launchd-based macOS package.

The installed CLI remains `kikimora` with the `kk` alias on both platforms.

## Table of contents

- [Why Kikimora](#why-kikimora)
- [Features](#features)
- [Architecture](#architecture)
- [Traffic flow](#traffic-flow)
- [Requirements](#requirements)
- [Quick start](#quick-start)
- [Configuration](#configuration)
- [Commands](#commands)
- [Service behavior](#service-behavior)
- [Health recovery](#health-recovery)
- [Examples](#examples)
- [Troubleshooting](#troubleshooting)
- [Security](#security)
- [Contributing](#contributing)
- [License](#license)

## Why Kikimora

A conventional VPN setup usually assumes that one VPN owns the default route
and the system DNS configuration. This becomes fragile when two VPNs must be
active at the same time.

Kikimora separates the responsibilities:

- **Leshy** classifies domains and serves local DNS responses.
- **Kikimora routing logic** sends traffic through the correct VPN interface.
- **`leshy-dns`** owns the `systemd-resolved` integration.
- **Route Watch** reacts to VPN interface changes.
- **Health Watch** detects broken DNS integration and repairs it.

The result is a system that can survive VPN disconnects, reconnects, and DNS
configuration changes without requiring a Leshy restart.

## Features

- Domain-based split routing.
- Simultaneous primary and secondary VPN support.
- Configurable default routing zone.
- Dedicated `leshy-dns0` DNS interface.
- Native `systemd-resolved` integration.
- Automatic route recovery after VPN reconnects.
- Automatic DNS recovery after VPN clients rewrite resolver settings.
- Safe DNS suspension when Leshy stops responding.
- Public health-check interface through `leshy-dns check`.
- systemd-based startup, supervision, and logging.
- Human-readable status output through the `kk` command.
- No dependency on NetworkManager-specific routing logic.

## Architecture

```text
                              Linux applications
                                      |
                                      v
                              systemd-resolved
                                      |
                                      v
                          +-----------------------+
                          |      leshy-dns0       |
                          |  local DNS interface  |
                          +-----------+-----------+
                                      |
                                      v
                          +-----------------------+
                          |        Leshy          |
                          | domain classification |
                          +-----------+-----------+
                                      |
                        +-------------+-------------+
                        |                           |
                        v                           v
              primary domain set              default zone
                        |                           |
                        v                           v
                primary VPN amn0             secondary VPN vpn0
```

### Component ownership

```text
+--------------------+------------------------------------------------------+
| Component          | Responsibility                                       |
+--------------------+------------------------------------------------------+
| Leshy              | Local DNS service and domain classification          |
| leshy-dns          | systemd-resolved runtime integration                 |
| Route Watch        | VPN interface monitoring and route reconstruction    |
| Health Watch       | Leshy and DNS integration health recovery            |
| kk (dispatcher)    | Operator CLI — dispatches to lib modules:            |
|                    | common.sh, dns.sh, service.sh, status.sh,           |
|                    | domains.sh, config.sh, maintenance.sh, help.sh       |
+--------------------+------------------------------------------------------+
```

## Traffic flow

With the default project configuration:

```text
OpenAI / GitHub / configured primary domains
                    |
                    v
                  amn0

All other domains
                    |
                    v
                  vpn0
```

The default zone is:

```text
DEFAULT_ZONE=secondary
```

Domain classification happens through Leshy. Routing decisions are then
translated into Linux routes for the corresponding VPN interface.

## Requirements

Kikimora targets Linux systems with:

- Bash;
- systemd;
- `systemd-resolved`;
- `iproute2`;
- `dig`, normally provided by `dnsutils` or `bind-utils`;
- Leshy;
- at least one configured VPN interface;
- root privileges for installation and runtime network changes.

The reference deployment uses:

```text
Primary VPN interface:   amn0
Secondary VPN interface: vpn0
DNS interface:           leshy-dns0
```

Other interface names may be used if the project configuration supports them.

## Quick start

The exact installation commands depend on the repository layout and installer
used by your deployment. A typical setup follows this sequence:

```bash
git clone <repository-url> kikimora
cd kikimora

sudo ./install.sh --primary-interface tun0 --secondary-interface tun1
sudo kk enable --now
```

Verify the system:

```bash
kk status
sudo /usr/local/sbin/leshy-dns check
```

A healthy system should report both VPN interfaces as ready and
`leshy-dns0` as active.

Example:

```text
Primary     tun0         ready
Secondary   tun1         ready
DNS         leshy-dns0   active
```

### Specifying VPN interfaces

The `--primary-interface` and `--secondary-interface` flags tell Kikimora which
network interfaces belong to each VPN. Replace `tun0` and `tun1` with the actual
interface names used by your VPN clients.

You can set the interface names in two ways:

**1. During installation** — pass the flags to `install.sh` or `kk install`:

```bash
sudo ./install.sh --primary-interface tun0 --secondary-interface tun1
```

**2. After installation** — edit the configuration file directly:

```bash
sudo kk config edit
# or manually:
sudo editor /etc/kikimora/leshy/vpn.conf
```

The file `/etc/kikimora/leshy/vpn.conf` contains:

```bash
PRIMARY_INTERFACE="tun0"
SECONDARY_INTERFACE="tun1"
```

After changing the interfaces, restart the services:

```bash
sudo kk restart
```

Re-running `kk install` with the same flags also replaces the saved values.

## Configuration

The reference configuration directory is:

```text
/etc/kikimora/leshy/
```

Typical layout:

```text
/etc/kikimora/
└── leshy/
    ├── config.yml
    ├── primary.txt
    ├── secondary.txt
    └── bypass.txt
```

### `config.yml`

Contains the main Leshy and routing configuration.

### `primary.txt`

Domains that should use the primary VPN.

Example:

```text
openai.com
chatgpt.com
github.com
githubusercontent.com
```

### `secondary.txt`

Domains explicitly assigned to the secondary VPN.

This file may be optional when the default zone is already `secondary`.

### `bypass.txt`

Domains that should bypass the VPN routing policy, if bypass support is enabled
by the local configuration.

### Domain list rules

Recommended conventions:

- one domain per line;
- lowercase domain names;
- no URL schemes such as `https://`;
- no paths;
- comments only if the parser supports them;
- keep broad parent domains only when routing all subdomains is intended.

## Commands

### Main operator command

```bash
kk status
kk start
kk stop
kk restart
```

The exact available subcommands depend on the installed `kk` script.

### DNS integration command

```bash
sudo kk dns status
sudo /usr/local/sbin/leshy-dns check
sudo kk dns enable
sudo kk dns disable
sudo kk dns resume
sudo kk dns suspend
```

#### `status`

Displays the current DNS integration state.

#### `check`

Performs a silent runtime validation.

Exit status:

```text
0  DNS integration is healthy
1  DNS integration is incomplete or broken
```

This command is designed for scripts and service health checks.

#### `enable`

Creates or activates the DNS integration.

#### `disable`

Removes the DNS integration and its persistent state.

#### `resume`

Repairs or restores the runtime DNS configuration.

This command is safe to call when the interface already exists. It verifies the
actual runtime state instead of trusting marker files.

#### `suspend`

Temporarily releases the system from the Leshy DNS configuration so normal DNS
resolution can continue while Leshy is unavailable.

## Service behavior

### Route Watch

Route Watch observes the VPN interfaces.

When an interface disappears:

```text
VPN disconnect
      |
      v
interface removed
      |
      v
Route Watch notices the change
      |
      v
routing state is recalculated
```

When the interface returns:

```text
VPN reconnect
      |
      v
interface created
      |
      v
Route Watch rebuilds the required routes
```

### Health Watch

Health Watch validates two separate conditions:

1. Leshy must answer locally.
2. The operating system must still send DNS through Leshy.

The second condition is checked through:

```bash
leshy-dns check
```

This design keeps all `systemd-resolved` knowledge inside `leshy-dns`.

## Health recovery

### VPN client overwrites DNS

Some VPN clients reconfigure `systemd-resolved` during reconnect and assign the
global routing domain `~.` to their own interface.

Example broken state:

```text
Link (amn0)
    DNS Domain: ~.
    Default Route: yes

Link (leshy-dns0)
    DNS Server: 127.0.0.1:53053
    DNS Domain:
```

Kikimora recovers automatically:

```text
AmneziaVPN reconnect
          |
          v
systemd-resolved configuration overwritten
          |
          v
leshy-dns check returns failure
          |
          v
Health Watch calls leshy-dns resume
          |
          v
~. restored on leshy-dns0
          |
          v
DNS queries return to Leshy
```

### Leshy becomes unavailable

```text
Leshy stops answering
          |
          v
Health Watch detects failure
          |
          v
leshy-dns suspend
          |
          v
system resolver falls back to normal DNS
```

### Runtime marker files

Kikimora does not assume that a marker file means the resolver state is valid.

The runtime validation checks the actual state:

- `leshy-dns0` exists;
- the expected DNS server is assigned;
- routing domain `~.` is present;
- the correct default-route property is present.

If the interface exists but the resolver properties are incomplete,
`leshy-dns resume` repairs the existing interface instead of recreating the
entire service.

## Examples

### Check project status

```bash
kk status
```

### Check DNS integration from a script

```bash
if sudo /usr/local/sbin/leshy-dns check; then
    echo "Kikimora DNS integration is healthy"
else
    echo "Kikimora DNS integration is broken"
fi
```

### Inspect resolver ownership

```bash
resolvectl status
resolvectl status leshy-dns0
resolvectl domain leshy-dns0
```

Healthy output should include:

```text
Link (...) (leshy-dns0)
    DNS Server: 127.0.0.1:53053
    DNS Domain: ~.
```

### Test a DNS query

```bash
resolvectl query example.com
```

The selected resolver link should be `leshy-dns0`, not a VPN interface.

### Test Leshy directly

```bash
dig @127.0.0.1 -p 53053 example.com
```

### Inspect logs

```bash
journalctl -u leshy.service
journalctl -u leshy-route-watch.service
journalctl -u leshy-health-watch.service
```

Follow logs live:

```bash
journalctl -fu leshy-health-watch.service
```

## Troubleshooting

### DNS shows `down` after a VPN reconnect

Run:

```bash
sudo /usr/local/sbin/leshy-dns check
echo $?
```

If the exit code is `1`, repair manually:

```bash
sudo kk dns resume
```

Then verify:

```bash
resolvectl domain leshy-dns0
```

Expected:

```text
Link (...) (leshy-dns0): ~.
```

### DNS queries use `amn0` directly

Check:

```bash
resolvectl status
```

If `amn0` owns `~.`, the VPN client has overwritten the resolver routing
domain. Restart Health Watch or run:

```bash
sudo kk dns resume
```

### Routes look correct but HTTPS does not work

Inspect firewall and nftables rules:

```bash
sudo nft list ruleset
```

AmneziaVPN Kill Switch may block traffic even when Linux routing is correct.
Disable the Kill Switch and test again.

### `leshy-dns check` succeeds but name resolution fails

Test Leshy directly:

```bash
dig @127.0.0.1 -p 53053 example.com
```

Then inspect:

```bash
systemctl status leshy.service
journalctl -u leshy.service
```

### Primary VPN is missing

A missing primary interface is reported as:

```text
Primary     amn0         missing
```

Start or reconnect the primary VPN. Route Watch should rebuild the routes when
the interface returns.

## Security

Kikimora changes system routing and resolver configuration and therefore runs
privileged components.

Review scripts before installation and restrict write access to:

```text
/usr/local/sbin/
/etc/kikimora/
/etc/systemd/system/
```

Security issues should be reported according to [SECURITY.md](SECURITY.md).

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) before
opening a pull request.

Please use the GitHub issue templates for bug reports, feature requests, and
support questions.

## Changelog

See [CHANGELOG.md](CHANGELOG.md).

## License

Kikimora is released under the [MIT License](LICENSE).
