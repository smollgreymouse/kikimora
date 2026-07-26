# Getting started

This guide covers the first run of Kikimora after installation and the normal operator path after reboot.

## Install

Run the installer with the Linux interface names used by your VPN clients:

```bash
sudo ./install.sh --primary-interface tun0 --secondary-interface tun1
```

Replace `tun0` and `tun1` with the actual interface names on your machine. For example, a reference deployment may use `amn0` for the primary VPN and `vpn0` for the secondary VPN.

## User path after installation

Enable autostart and start everything once:

```bash
sudo kk enable --now
```

After this, a normal reboot should not require any manual DNS command. During boot, systemd starts the managed services:

```text
leshy.service
leshy-route-watch.service
leshy-health-watch.service
```

The Leshy service drop-in also protects cold boot DNS setup. It first tries to resume the saved DNS integration, then checks whether DNS is actually healthy, and enables DNS integration if needed:

```text
leshy-dns resume
leshy-dns check
leshy-dns enable   # only when check is not healthy
```

The DNS hooks are non-fatal for `leshy.service`, so a temporary DNS repair failure does not kill the already-started Leshy process.

## After turning on the computer

For the normal autostart path, just check status:

```bash
kk status
sudo /usr/local/sbin/leshy-dns check
```

A healthy setup should show the VPN interfaces as ready and `leshy-dns0` as active. The DNS check command should exit with status `0`.

## Manual start path

If autostart was not enabled, start Kikimora manually after boot:

```bash
sudo kk start
```

`kk start` starts the managed services and then ensures DNS integration using this sequence:

```text
systemctl start leshy.service leshy-route-watch.service leshy-health-watch.service
leshy-dns resume
leshy-dns check
leshy-dns enable   # only when check is not healthy
```

A separate `sudo kk dns enable` command is not required for a cold manual start.

## Practical daily commands

One-time setup after installation:

```bash
sudo kk enable --now
```

After a normal reboot:

```bash
kk status
```

If something did not come up correctly, or a VPN client reconnected in an unusual way:

```bash
sudo kk restart
kk status
```

`kk restart` temporarily restores normal system DNS first, restarts the managed services, and only then restores or enables DNS through Leshy. If the services fail to restart, DNS remains restored to the normal system state instead of pointing at an unavailable local Leshy listener.

Temporarily stop Kikimora:

```bash
sudo kk stop
```

`kk stop` suspends Leshy DNS integration first, then stops the managed services.

Disable autostart and stop everything now:

```bash
sudo kk disable --now
```

`kk disable --now` also suspends DNS before stopping/disabling the services.

## Change interfaces later

To change the VPN interfaces, edit the configuration:

```bash
sudo kk config edit
```

Then apply the change:

```bash
sudo kk restart
```
