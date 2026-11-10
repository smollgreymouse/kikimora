# Lab VMs for acceptance runs

Two disposable VirtualBox VMs carry the Linux and Windows acceptance work. Both
are reachable from the workstation only; neither is production.

The lab LAN sits on `192.168.3.0/24` since 2026-11-10 and the pair is kept in
place from now on - treat the addresses below as **fixed**. If a VM ever moves
again, re-discover its address as shown in
[Re-discovering the addresses](#re-discovering-the-addresses-if-a-vm-ever-moves-again).

## Ubuntu disposable VM

| | |
|---|---|
| VirtualBox name | `ubuntu2604` |
| Guest | Ubuntu 26.04, user `greymouse` |
| Address | `192.168.3.71` - fixed (2026-11-10; earlier stops: `192.168.1.236`, `192.168.1.51`, `192.168.3.53`) |
| Auth | SSH key `%USERPROFILE%\.ssh\id_ed25519`, no password prompt; the host key for `192.168.3.71` is pinned in the station's `known_hosts` (2026-11-10), so scp connects without extra flags |
| Run a command | `python tmp/vm_run.py "<command>"` |
| Run a script | `python tmp/vm_put.py tmp/<file>.sh /tmp/<file>.sh` then `python tmp/vm_run.py --script tmp/<file>.sh` |
| Upload a payload | `scp -i ~/.ssh/id_ed25519 <local> greymouse@192.168.3.71:<remote>` from Git Bash (verified 2026-11-10). `python tmp/vm_put.py` is kept for reference only: the guest's SFTP subsystem fails with ENOENT on any file (2026-10-27, a guest-side issue - the lab network is a clean local LAN, no DPI); an 8 MB core binary went over scp intact, checksums matched |
| Quirks | 2 vCPU - build with `-j2`, a `-j8` build starves sshd; `/tmp` is wiped on reboot; the station's own `ssh.exe` returns no output, which is why the paramiko helpers exist; long exec stdin streams die mid-write (channel closes) - move payloads with scp | Firefox keeps its own DNS-over-HTTPS on: it resolves chatgpt.com itself and reaches it over the guest IPv6 past Leshy, so a browser acceptance run needs `network.trr.mode=5` (a fresh profile with `user.js` is enough); with DoH off the same page loads through the tunnel. Edge on the Windows guest uses the system resolver (NRPT to Leshy) and needs no such change.

What runs there: the `.deb` built from the merged branch, the `kikimora-core`,
`leshy` and `kikimora-leshy-dns` units, the roles `awg` / `oc` / `xray-nl`, and
the `kk` CLI (`/usr/local/sbin/kikimora`, alias `/usr/local/bin/kk`).

## Windows guest

| | |
|---|---|
| VirtualBox name | `win10` |
| Guest | Windows 10 19045, user `greymouse` (administrator) |
| VirtualBox CLI | `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe` |
| Addressing | LAN IPv4 `192.168.3.72` - reachable from the station, fixed (2026-11-10) |
| Elevated channel | `python tmp/win_ssh.py "<command>"` - SSH over LAN IPv4; the default host in the helper is `192.168.3.72`, no `--host` needed. The station key is deployed through `C:\ProgramData\ssh\administrators_authorized_keys`, so the session carries an elevated token. The old global EUI-64 IPv6 (`2a00:1370:...`) is dead: the lab router now hands out the ULA prefix `fd04:c1d8:e630:d300::/64` and the guest's temporary SLAAC addresses rotate daily, so IPv4 is the stable channel |
| Run a command | `python tmp/win_ssh.py "<command>"` |
| Run a script | `python tmp/win_put.py tmp/<file>.ps1 C:/Users/greymouse/Desktop/<file>.ps1` then `python tmp/win_ssh.py "powershell -NoProfile -ExecutionPolicy Bypass -File C:\Users\greymouse\Desktop\<file>.ps1"` |
| Fallback channel | `VBoxManage guestcontrol win10 run --exe C:\Windows\System32\cmd.exe --username <user> --password <pw> -- cmd /c ver` works without the guest network, because Guest Additions are installed |
| Quirks | quote-heavy inline PowerShell and even a bare `|` pipe get mangled by the SSH layer (cmd eats them) - drive the guest with script files or pipe-free one-liners like `(Get-Service KikimoraCore).Status`; the guest's IPv4 stack once had to be reset (`netsh winsock reset`, `netsh int ipv4 reset`, reboot) before Wintun routes could be configured |

What runs there: the MSI (`kikimora-core.exe` service, `kikimora-toad.exe`,
`kikimora-ui.exe`, `wintun.dll`) plus the portable Leshy Windows build in
`C:\leshy` with its config at `C:\ProgramData\leshy\config.toml`; the core
publishes its zone device files to `C:\ProgramData\Kikimora\leshy\vpn`.

## Один AWG-peer на две ВМ

Обе ВМ используют один и тот же AWG `private_key`, `peer_public_key` и адрес
`10.8.1.3/32`, то есть один peer на сервере: клиенты вытесняют друг друга при
каждом handshake. Держите `awg`/`awg-main` подключённым только на одной ВМ;
для одновременной работы заведите второй peer (свой ключ и, например,
`10.8.1.4/32`).

## Re-discovering the addresses (if a VM ever moves again)

Both guests report their interfaces to VirtualBox Guest Additions, so the
station can ask without logging in:

```bash
"/c/Program Files/Oracle/VirtualBox/VBoxManage.exe" guestproperty enumerate ubuntu2604 | grep "V4/IP"
"/c/Program Files/Oracle/VirtualBox/VBoxManage.exe" guestproperty enumerate win10 | grep "V4/IP"
```

* `ubuntu2604` - the LAN IP is the `Net/0` entry; ignore `10.8.1.3` (AWG),
  `192.0.2.1` and `172.17.0.1` (docker);
* `win10` - the LAN IP is the `Net/1` entry (`Net/0` is the AWG tunnel).

After moving to a fresh address, pin the new host keys once:
`ssh-keyscan -T 5 <ip> >> ~/.ssh/known_hosts`.

## Health checks

```powershell
# Ubuntu: all three units must be active
python tmp/vm_run.py "systemctl is-active kikimora-core.service leshy.service kikimora-leshy-dns.service"

# Windows: the service is auto-start and must be Running (pipe-free, see quirks)
python tmp/win_ssh.py "powershell -NoProfile -Command (Get-Service KikimoraCore).Status"

# Either side: role readiness
python tmp/vm_run.py "sudo kikimora-core status --json | head -c 200"
```

## Credentials

**Passwords are deliberately not in this repository: it is public**
(`github.com/smollgreymouse/kikimora`, visibility `public`). They live in
`tmp/lab-credentials.md` on the workstation - the whole `tmp/` tree is gitignored
(`git check-ignore -v tmp/vm_run.py` shows the rule), together with the
`VBoxManage guestcontrol` password.

Rules that keep the pair safe:

* SSH keys are the normal channel; passwords exist only for the console and
  `guestcontrol`;
* rotate the guest password before sharing a dump of this repo with anyone;
* never force-add `tmp/`: the credentials must stay untracked;
* the VPN endpoints, role configs and `*.secret` files of the product live on the
  VMs under `/etc/kikimora` (Linux) and `C:\ProgramData\Kikimora\toads`
  (Windows) and are not part of this repository either.
