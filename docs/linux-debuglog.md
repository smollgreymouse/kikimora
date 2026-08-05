# Linux debug log bundle

`kk debuglog` collects the most useful Linux Kikimora state into one file for troubleshooting.

It is intended for cases where a VPN interface disappeared, DNS ownership changed, Leshy stopped answering, or the service lifecycle did not behave as expected.

## Usage

```bash
sudo kk debuglog
```

By default the command writes a file in the current directory:

```text
./kikimora-debug-YYYYmmdd-HHMMSS.log
```

Use a custom path:

```bash
sudo kk debuglog -o kikimora-debug.log
```

Limit journal output to recent entries:

```bash
sudo kk debuglog --since "30 minutes ago"
sudo kk debuglog -n 500
```

## What it collects

The bundle includes:

- OS and kernel metadata;
- Kikimora and Leshy versions;
- Kikimora installation state;
- `kk status --verbose`;
- `systemctl status` for the three managed units;
- `systemctl list-jobs`;
- `systemctl cat` for the managed units;
- `journalctl` logs for:
  - `leshy.service`,
  - `leshy-route-watch.service`,
  - `leshy-health-watch.service`;
- interfaces, addresses, routes and routing rules;
- Kikimora config files and domain/route lists;
- runtime device, readiness, route-watch, route-lifecycle and DNS state under
  `/run/kikimora/leshy`;
- `resolvectl` DNS state;
- `leshy-dns status` and `leshy-dns check`;
- a direct `dig @127.0.0.1 -p 53053 . NS` probe when `dig` exists;
- `kk verify` output.

## Privacy note

The debug bundle may include domain lists, static route lists, interface names, local routes, DNS servers, and parts of `config.toml`. Review the file before sharing it publicly.

## Examples

Collect a full current-boot bundle:

```bash
sudo kk debuglog
```

Collect only recent logs:

```bash
sudo kk debuglog --since "15 minutes ago" -o kikimora-reconnect.log
```

Collect the last 300 matching journal records:

```bash
sudo kk debuglog -n 300
```
