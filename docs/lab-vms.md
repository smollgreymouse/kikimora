# Lab VMs for acceptance runs

Two disposable VirtualBox VMs carry the Linux and Windows acceptance work. Both
are reachable from the workstation only; neither is production.

## Ubuntu disposable VM

| | |
|---|---|
| VirtualBox name | `ubuntu2604` |
| Guest | Ubuntu 26.04, user `greymouse` |
| Address | `192.168.1.51` (DHCP; it once moved from `192.168.1.236`) |
| Auth | SSH key `%USERPROFILE%\.ssh\id_ed25519`, no password prompt |
| Run a command | `python tmp/vm_run.py "<command>"` |
| Run a script | `python tmp/vm_put.py tmp/<file>.sh /tmp/<file>.sh` then `python tmp/vm_run.py --script tmp/<file>.sh` |
| Upload a payload | `python tmp/vm_put.py <local> <remote>` (`--binary` for archives and binaries); since 2026-10-27 SFTP is broken by the provider's DPI - use `scp -i ~/.ssh/id_ed25519 <local> greymouse@192.168.1.51:<remote>` from Git Bash instead (an 8 MB core binary transferred intact, checksums matched) and keep exec stdin payloads small |
| Quirks | 2 vCPU - build with `-j2`, a `-j8` build starves sshd; `/tmp` is wiped on reboot; the station's own `ssh.exe` returns no output, which is why the paramiko helpers exist |

What runs there: the `.deb` built from the merged branch, the `kikimora-core`,
`leshy` and `kikimora-leshy-dns` units, the roles `awg` / `oc` / `xray-nl`, and
the `kk` CLI (`/usr/local/sbin/kikimora`, alias `/usr/local/bin/kk`).

## Windows guest

| | |
|---|---|
| VirtualBox name | `win10` |
| Guest | Windows 10 19045, user `greymouse` (administrator) |
| VirtualBox CLI | `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe` |
| Addressing | the guest's LAN IPv4 `192.168.1.246` is **not** reachable from the station - the lab network hands the guest IPv6 only |
| Elevated channel | SSH over the **stable** EUI-64 address `2a00:1370:818c:9de:ae67:e1f8:fca4:47aa`, derived from MAC `08-00-27-6C-FD-2D`, so it survives reboots; temporary SLAAC addresses rotate every day |
| Run a command | `python tmp/win_ssh.py --host <addr> "<command>"` |
| Run a script | `python tmp/win_put.py tmp/<file>.ps1 C:/Users/greymouse/Desktop/<file>.ps1` then `python tmp/win_ssh.py --host <addr> "powershell -NoProfile -ExecutionPolicy Bypass -File C:\Users\greymouse\Desktop\<file>.ps1"` |
| Fallback channel | `VBoxManage guestcontrol win10 run --exe C:\Windows\System32\cmd.exe --username <user> --password <pw> -- cmd /c ver` works without the guest network, because Guest Additions are installed |
| Quirks | quote-heavy inline PowerShell is mangled by the SSH layer - always drive the guest with script files; the guest's IPv4 stack once had to be reset (`netsh winsock reset`, `netsh int ipv4 reset`, reboot) before Wintun routes could be configured |

What runs there: the MSI (`kikimora-core.exe` service, `kikimora-toad.exe`,
`kikimora-ui.exe`, `wintun.dll`) plus the portable Leshy Windows build in
`C:\leshy` with its config at `C:\ProgramData\leshy\config.toml`; the core
publishes its zone device files to `C:\ProgramData\Kikimora\leshy\vpn`.

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

## Health checks

```powershell
# Ubuntu: all three units must be active
python tmp/vm_run.py "systemctl is-active kikimora-core.service leshy.service kikimora-leshy-dns.service"

# Windows: the service is auto-start and must be Running
python tmp/win_ssh.py --host 2a00:1370:818c:9de:ae67:e1f8:fca4:47aa "powershell -NoProfile -Command Get-Service KikimoraCore"

# Either side: role readiness
python tmp/vm_run.py "sudo kikimora-core status --json | head -c 200"
```
