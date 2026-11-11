#!/usr/bin/env python3
"""Canonical access helper for the disposable Kikimora lab VMs.

Examples:
    python scripts/lab/vm.py discover ubuntu
    python scripts/lab/vm.py run ubuntu "hostname"
    python scripts/lab/vm.py run ubuntu --sudo "systemctl status kikimora-core"
    python scripts/lab/vm.py put ubuntu ./gate.sh /tmp/gate.sh

    python scripts/lab/vm.py discover windows
    python scripts/lab/vm.py run windows "powershell -NoProfile -Command hostname"
    python scripts/lab/vm.py put windows ./gate.ps1 C:/Users/greymouse/Desktop/gate.ps1

Host resolution order:
    1. --host
    2. KIKIMORA_<TARGET>_VM_HOST
    3. VirtualBox Guest Additions guest-property discovery
    4. the documented last-known fallback address

Authentication:
    SSH key/agent first; LAB_PW is a password fallback and is never stored here.
"""

from __future__ import annotations

import argparse
import ipaddress
import os
from pathlib import Path
import re
import shlex
import shutil
import stat
import subprocess
import sys
from dataclasses import dataclass
from typing import Iterable, Optional, Sequence, Tuple


@dataclass(frozen=True)
class Target:
    name: str
    user: str
    fallback_host: str
    host_env: str
    vbox_names: Tuple[str, ...]
    preferred_net: Optional[int]
    upload_transport: str


TARGETS = {
    "ubuntu": Target(
        name="ubuntu",
        user="greymouse",
        fallback_host="192.168.3.74",
        host_env="KIKIMORA_UBUNTU_VM_HOST",
        # ubuntu2604 is the acceptance name. ubuntu-desktop-vm is the older
        # local VirtualBox name still present on the workstation.
        vbox_names=("ubuntu2604", "ubuntu-desktop-vm"),
        preferred_net=0,
        upload_transport="scp",
    ),
    "windows": Target(
        name="windows",
        user="greymouse",
        fallback_host="192.168.3.72",
        host_env="KIKIMORA_WINDOWS_VM_HOST",
        vbox_names=("win10",),
        preferred_net=1,
        upload_transport="sftp",
    ),
}

LAN = ipaddress.ip_network("192.168.3.0/24")
GUEST_IP_RE = re.compile(
    r"^/VirtualBox/GuestInfo/Net/(?P<net>\d+)/V4/IP\s*=\s*'(?P<ip>[^']+)'"
)


def _vboxmanage() -> Optional[str]:
    found = shutil.which("VBoxManage")
    if found:
        return found
    if os.name == "nt":
        candidate = Path(r"C:\Program Files\Oracle\VirtualBox\VBoxManage.exe")
        if candidate.exists():
            return str(candidate)
    return None


def _list_vms(vbox: str) -> set[str]:
    try:
        result = subprocess.run(
            [vbox, "list", "vms"],
            check=False,
            capture_output=True,
            text=True,
            timeout=10,
        )
    except (OSError, subprocess.TimeoutExpired):
        return set()
    names = set()
    for line in result.stdout.splitlines():
        match = re.match(r'^"([^"]+)"\s+\{', line)
        if match:
            names.add(match.group(1))
    return names


def parse_guest_ipv4(text: str, preferred_net: Optional[int]) -> Optional[str]:
    candidates: list[tuple[int, str]] = []
    for line in text.splitlines():
        match = GUEST_IP_RE.match(line.strip())
        if not match:
            continue
        value = match.group("ip")
        try:
            addr = ipaddress.ip_address(value)
        except ValueError:
            continue
        if addr.version != 4 or addr not in LAN:
            continue
        candidates.append((int(match.group("net")), value))

    if preferred_net is not None:
        for net, value in candidates:
            if net == preferred_net:
                return value
    return candidates[0][1] if candidates else None


def discover_vbox_host(target: Target) -> Optional[str]:
    vbox = _vboxmanage()
    if not vbox:
        return None
    present = _list_vms(vbox)
    for vm_name in target.vbox_names:
        if vm_name not in present:
            continue
        try:
            result = subprocess.run(
                [vbox, "guestproperty", "enumerate", vm_name],
                check=False,
                capture_output=True,
                text=True,
                timeout=10,
            )
        except (OSError, subprocess.TimeoutExpired):
            continue
        host = parse_guest_ipv4(result.stdout, target.preferred_net)
        if host:
            return host
    return None


def resolve_host(target: Target, explicit: Optional[str]) -> tuple[str, str]:
    if explicit:
        return explicit, "--host"
    env_host = os.environ.get(target.host_env, "").strip()
    if env_host:
        return env_host, target.host_env
    discovered = discover_vbox_host(target)
    if discovered:
        return discovered, "VirtualBox guest property"
    return target.fallback_host, "documented fallback"


def _load_paramiko():
    try:
        import paramiko  # type: ignore
    except ImportError as exc:
        raise RuntimeError(
            "paramiko is required for VM run/put operations; "
            "install it locally or use the normal SSH/scp tools"
        ) from exc
    return paramiko


def _candidate_key(explicit: Optional[str]) -> Optional[str]:
    if explicit:
        return str(Path(explicit).expanduser())
    default = Path("~/.ssh/id_ed25519").expanduser()
    return str(default) if default.exists() else None


def connect_ssh(
    host: str,
    user: str,
    timeout: int,
    key: Optional[str],
):
    paramiko = _load_paramiko()
    errors: list[str] = []

    def new_client():
        client = paramiko.SSHClient()
        try:
            client.load_system_host_keys()
        except OSError:
            pass
        # Lab guests are disposable and DHCP addresses can move. The helper
        # matches the historical lab behavior: trust a first-seen LAN key, but
        # never stores credentials in the repository.
        client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        return client

    key_file = _candidate_key(key)
    client = new_client()
    try:
        kwargs = dict(
            hostname=host,
            username=user,
            timeout=min(timeout, 15),
            allow_agent=True,
            look_for_keys=True,
        )
        if key_file:
            kwargs["key_filename"] = key_file
        client.connect(**kwargs)
        return client
    except Exception as exc:  # paramiko exposes several auth/socket subclasses
        errors.append(f"key/agent authentication failed: {exc}")
        client.close()

    password = os.environ.get("LAB_PW")
    if password:
        client = new_client()
        try:
            client.connect(
                hostname=host,
                username=user,
                password=password,
                timeout=min(timeout, 15),
                allow_agent=False,
                look_for_keys=False,
            )
            return client
        except Exception as exc:
            errors.append(f"LAB_PW authentication failed: {exc}")
            client.close()
    else:
        errors.append("LAB_PW is not set")

    raise RuntimeError(
        f"cannot authenticate to {user}@{host}: " + "; ".join(errors)
    )


def run_remote(
    target: Target,
    host: str,
    user: str,
    command: str,
    timeout: int,
    key: Optional[str],
    sudo: bool,
) -> int:
    if sudo and target.name != "ubuntu":
        raise RuntimeError("--sudo is supported only for the Ubuntu target")

    client = connect_ssh(host, user, timeout, key)
    password = os.environ.get("LAB_PW")
    if sudo:
        quoted = shlex.quote(command)
        if password:
            command = f"sudo -S -p '' bash -lc {quoted}"
        else:
            command = f"sudo -n bash -lc {quoted}"

    try:
        stdin, stdout, stderr = client.exec_command(command, timeout=timeout)
        if sudo and password:
            stdin.write(password + "\n")
            stdin.flush()
        out = stdout.read().decode(errors="replace")
        err = stderr.read().decode(errors="replace")
        rc = stdout.channel.recv_exit_status()
    finally:
        client.close()

    if out:
        print(out, end="" if out.endswith("\n") else "\n")
    if err:
        print(err, end="" if err.endswith("\n") else "\n", file=sys.stderr)
    return rc


def _read_ack(channel) -> None:
    code = channel.recv(1)
    if not code:
        raise RuntimeError("remote scp closed before acknowledgement")
    if code == b"\x00":
        return
    message = bytearray()
    while True:
        chunk = channel.recv(1)
        if not chunk or chunk == b"\n":
            break
        message.extend(chunk)
    raise RuntimeError(
        f"remote scp error {code[0]}: {message.decode(errors='replace')}"
    )


def scp_upload(client, local: Path, remote: str) -> None:
    size = local.stat().st_size
    mode = stat.S_IMODE(local.stat().st_mode) or 0o644
    channel = client.get_transport().open_session()
    try:
        channel.exec_command(f"scp -t -- {shlex.quote(remote)}")
        _read_ack(channel)
        # With an exact remote file target, the receiver still expects a
        # filename in the protocol. Use the destination basename.
        remote_name = Path(remote).name or local.name
        channel.sendall(f"C{mode:04o} {size} {remote_name}\n".encode())
        _read_ack(channel)
        with local.open("rb") as src:
            while True:
                data = src.read(1024 * 1024)
                if not data:
                    break
                channel.sendall(data)
        channel.sendall(b"\x00")
        _read_ack(channel)
    finally:
        channel.close()


def put_remote(
    target: Target,
    host: str,
    user: str,
    local_path: str,
    remote_path: str,
    timeout: int,
    key: Optional[str],
) -> None:
    local = Path(local_path)
    if not local.is_file():
        raise RuntimeError(f"local file does not exist: {local}")

    client = connect_ssh(host, user, timeout, key)
    try:
        if target.upload_transport == "scp":
            scp_upload(client, local, remote_path)
        else:
            sftp = client.open_sftp()
            try:
                sftp.put(str(local), remote_path)
            finally:
                sftp.close()
    finally:
        client.close()

    print(f"put {local} -> {target.name}:{remote_path}")


def add_connection_args(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--host", help="override the resolved guest address")
    parser.add_argument("--user", help="override the guest SSH user")
    parser.add_argument("--key", help="SSH private key; default ~/.ssh/id_ed25519")
    parser.add_argument("--timeout", type=int, default=120)


def parser() -> argparse.ArgumentParser:
    root = argparse.ArgumentParser(description=__doc__)
    sub = root.add_subparsers(dest="action", required=True)

    discover = sub.add_parser("discover", help="print the resolved guest IPv4")
    discover.add_argument("target", choices=TARGETS)
    discover.add_argument("--host", help="explicit host wins over discovery")
    discover.add_argument(
        "--source",
        action="store_true",
        help="print the resolution source after the address",
    )

    run = sub.add_parser("run", help="run one foreground command over SSH")
    run.add_argument("target", choices=TARGETS)
    add_connection_args(run)
    run.add_argument("--sudo", action="store_true", help="Ubuntu: run through sudo")
    run.add_argument("command")

    put = sub.add_parser("put", help="upload one file to a guest")
    put.add_argument("target", choices=TARGETS)
    add_connection_args(put)
    put.add_argument("local")
    put.add_argument("remote")

    return root


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = parser().parse_args(argv)
    target = TARGETS[args.target]
    host, source = resolve_host(target, getattr(args, "host", None))

    if args.action == "discover":
        if args.source:
            print(f"{host}\t{source}")
        else:
            print(host)
        return 0

    user = args.user or target.user
    print(
        f"[lab-vm] target={target.name} host={host} source={source} user={user}",
        file=sys.stderr,
    )
    try:
        if args.action == "run":
            return run_remote(
                target,
                host,
                user,
                args.command,
                args.timeout,
                args.key,
                args.sudo,
            )
        put_remote(
            target,
            host,
            user,
            args.local,
            args.remote,
            args.timeout,
            args.key,
        )
        return 0
    except RuntimeError as exc:
        print(f"lab-vm: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
