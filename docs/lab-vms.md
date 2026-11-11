# Lab VMs for acceptance runs

Two disposable lab guests carry the Linux and Windows acceptance work. They are
test systems only; neither is production.

Canonical repository transport helper:

~~~text
scripts/lab/vm.py
~~~

Do not add VM passwords, private keys or product secrets to the repository.
The ignored `tmp/` helpers are compatibility/local scratch only and are not an
execution contract.

The lab LAN is `192.168.3.0/24`.

## Address resolution contract

Never hardcode a historical `192.168.1.x` address in a test or plan.

`scripts/lab/vm.py` resolves a guest address in this order:

~~~text
1. explicit --host
2. KIKIMORA_UBUNTU_VM_HOST / KIKIMORA_WINDOWS_VM_HOST
3. VirtualBox Guest Additions IPv4 in 192.168.3.0/24
4. documented last-known fallback
~~~

Current fallbacks:

~~~text
ubuntu  -> 192.168.3.74
windows -> 192.168.3.72
~~~

Check before a native acceptance run:

~~~bash
python3 scripts/lab/vm.py discover ubuntu --source
python3 scripts/lab/vm.py discover windows --source
~~~

The helper knows both `ubuntu2604` and the older local VirtualBox registration
name `ubuntu-desktop-vm` for the Ubuntu guest. The Windows VirtualBox name is
`win10`.

A fallback address is not proof that the VM is running. The executor must still
establish SSH/guest availability before starting a native gate.

## Authentication

The helper tries SSH key/agent authentication first. By default it considers
`~/.ssh/id_ed25519`; `--key` can override it.

If key/agent authentication fails and `LAB_PW` is set, it retries with that
password.

`LAB_PW` is a local environment variable only. Never commit it.

On the workstation audit of 2026-11-11:

~~~text
192.168.3.74:
    ICMP reachable
    TCP/22 reachable
    ~/.ssh/id_ed25519 was NOT accepted
    LAB_PW was not present in the audit shell

192.168.3.72:
    not reachable at audit time
    treat as the documented Windows fallback, not proof the guest is powered on
~~~

Therefore old documentation claiming unconditional "SSH key, no password
prompt" is superseded.

## Ubuntu disposable VM

| | |
|---|---|
| Acceptance name | `ubuntu2604` |
| Older local VBox alias | `ubuntu-desktop-vm` |
| Guest | Ubuntu 26.04, user `greymouse` |
| Last-known/fallback address | `192.168.3.74` |
| Run command | `python3 scripts/lab/vm.py run ubuntu "<command>"` |
| Run with sudo | `python3 scripts/lab/vm.py run ubuntu --sudo "<command>"` |
| Upload | `python3 scripts/lab/vm.py put ubuntu <local> <remote>` |

The Ubuntu upload path deliberately does **not** depend on the SFTP subsystem.
The helper uses the SCP protocol over the authenticated SSH connection because
the guest historically returned ENOENT from SFTP.

For acceptance harnesses, upload the complete versioned script and run it as one
foreground command. Do not stream a long generated script through SSH stdin.

Example:

~~~bash
python3 scripts/lab/vm.py put ubuntu \
    linux/tests/toad/local-leshy-routing-netns.sh \
    /tmp/local-leshy-routing-netns.sh

python3 scripts/lab/vm.py run ubuntu \
    "sudo bash /tmp/local-leshy-routing-netns.sh ..."
~~~

Quirks:

- build with low parallelism on the small guest; high parallelism can starve
  sshd;
- `/tmp` is wiped by reboot;
- long background/interactive acceptance flows do not belong in the transport
  helper; the versioned guest script owns its child processes and cleanup;
- Firefox DoH must be disabled for browser routing acceptance, otherwise it can
  bypass the system resolver.

What normally runs there:

- installed Kikimora `.deb`;
- `kikimora-core`;
- Leshy;
- `kikimora-leshy-dns`;
- lab roles including AWG/OpenConnect/Xray;
- `kk`/Kikimora CLI.

## Windows guest

| | |
|---|---|
| VirtualBox name | `win10` |
| Guest | Windows 10 19045, user `greymouse` (administrator) |
| Last-known/fallback address | `192.168.3.72` |
| Run command | `python3 scripts/lab/vm.py run windows "<command>"` |
| Upload | `python3 scripts/lab/vm.py put windows <local> <remote>` |
| Fallback without guest networking | `VBoxManage guestcontrol win10 ...` when the VM is registered on the current host |

Windows upload uses SFTP, which has worked on this guest.

Prefer a PowerShell file for quote-heavy commands:

~~~bash
python3 scripts/lab/vm.py put windows \
    packaging/windows/test_managed_cutover.ps1 \
    C:/Users/greymouse/Desktop/test_managed_cutover.ps1

python3 scripts/lab/vm.py run windows \
    "powershell -NoProfile -ExecutionPolicy Bypass -File C:\\Users\\greymouse\\Desktop\\test_managed_cutover.ps1 ..."
~~~

Avoid large inline PowerShell expressions and pipelines through `cmd.exe`;
upload the versioned script instead.

What normally runs there:

- Kikimora MSI;
- `KikimoraCore` service;
- `kikimora-toad.exe`;
- `kikimora-ui.exe`;
- `wintun.dll`;
- Windows Leshy runtime/service;
- generated configuration under `C:\ProgramData`.

## One AWG peer across both VMs

The historical lab configuration uses the same AWG peer/address on both guests.
Do not keep the AWG role connected on both VMs simultaneously unless the lab has
been provisioned with separate peers.

## Direct VirtualBox discovery

When the relevant VM is registered on the current VirtualBox host:

~~~bash
VBoxManage guestproperty enumerate ubuntu2604
VBoxManage guestproperty enumerate ubuntu-desktop-vm
VBoxManage guestproperty enumerate win10
~~~

The helper selects only an IPv4 address in `192.168.3.0/24`.

For the historical layout:

~~~text
Ubuntu LAN: Net/0
Windows LAN: Net/1
~~~

Tunnel, Docker and Leshy test addresses such as `10.8.1.3`, `172.17.0.1`
and `192.0.2.1` are never accepted as the lab SSH address.

## Health checks

~~~bash
# Resolve first.
python3 scripts/lab/vm.py discover ubuntu --source
python3 scripts/lab/vm.py discover windows --source

# Ubuntu services.
python3 scripts/lab/vm.py run ubuntu \
    "systemctl is-active kikimora-core.service leshy.service kikimora-leshy-dns.service"

# Ubuntu core.
python3 scripts/lab/vm.py run ubuntu \
    "sudo kikimora-core status --json | head -c 200"

# Windows service.
python3 scripts/lab/vm.py run windows \
    "powershell -NoProfile -Command (Get-Service KikimoraCore).Status"
~~~

## Credentials and repository hygiene

This repository is public. Credentials are not repository data.

Rules:

- use SSH keys/agent when they are actually accepted by the guest;
- use `LAB_PW` only as a local password fallback;
- never force-add ignored `tmp/` credentials/helpers as a substitute for the
  tracked `scripts/lab/vm.py`;
- never commit GuestControl passwords;
- never commit VPN role secrets or product `*.secret` files;
- product role/config secrets remain only on the disposable guests.

## Transport helper self-test

The pure host-resolution/parser tests require no VM and no Paramiko connection:

~~~bash
python3 -m unittest scripts/lab/test_vm.py -v
~~~

A real SSH acceptance run additionally requires `paramiko` in the local
Python environment.
