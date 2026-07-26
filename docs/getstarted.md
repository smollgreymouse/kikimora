# Getting started

This guide covers the first run of Kikimora after installation and clarifies what to do with VPN username/password credentials.

## Install

Run the installer with the Linux interface names used by your VPN clients:

```bash
sudo ./install.sh --primary-interface tun0 --secondary-interface tun1
```

Replace `tun0` and `tun1` with the actual interface names on your machine. For example, a reference deployment may use `amn0` for the primary VPN and `vpn0` for the secondary VPN.

## Username and password credentials

Kikimora does not log in to your VPN provider and does not need your VPN account username or password.

The `--primary-interface` and `--secondary-interface` installer options are interface names, not credentials. They tell Kikimora which already-configured Linux network interfaces should be treated as the primary and secondary VPN paths.

Configure VPN credentials inside the VPN client itself, for example in AmneziaVPN, WireGuard, OpenVPN, or another client you use. After the VPN client connects and creates an interface, Kikimora observes that interface and manages routing and DNS around it.

Do not put VPN usernames, passwords, tokens, private keys, or provider account credentials into:

```text
/etc/kikimora/leshy/vpn.conf
/etc/kikimora/leshy/routing.conf
/etc/kikimora/leshy/domains/*.txt
```

Those files should contain only interface names, routing mode, and domain lists.

If a VPN client asks for a username and password, use the credentials issued by that VPN provider or configured in that VPN client. Do not use your Linux user password, GitHub password, or any Kikimora-specific value: Kikimora has no separate user/password login.

## Start and enable services

For normal autostart after boot, run once:

```bash
sudo kk enable --now
```

This enables and starts the managed services. Kikimora also ensures that DNS integration is active after the services start.

For a one-shot start without enabling autostart:

```bash
sudo kk start
```

## Verify

Check the service and DNS state:

```bash
kk status
sudo /usr/local/sbin/leshy-dns check
```

A healthy setup should show the VPN interfaces as ready and `leshy-dns0` as active.

## Daily use

After a normal reboot, no manual DNS command is required if autostart was enabled with `sudo kk enable --now`.

Useful commands:

```bash
kk status
sudo kk restart
sudo kk stop
sudo kk start
```

`kk restart` is safe for DNS: Kikimora temporarily restores normal DNS first, restarts the managed services, and then restores or enables Leshy DNS integration only after the services are running again.
