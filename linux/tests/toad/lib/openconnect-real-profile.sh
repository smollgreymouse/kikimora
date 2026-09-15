#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

oc_profile_require_secret_file() {
    local path="$1"
    local owner mode
    [[ -f "$path" && -r "$path" ]] || {
        printf 'ERROR: OpenConnect secret profile is missing or unreadable: %s\n' "$path" >&2
        return 1
    }
    owner="$(stat -c '%u' "$path")"
    mode="$(stat -c '%a' "$path")"
    [[ "$owner" == "$(id -u)" ]] || {
        printf 'ERROR: OpenConnect secret profile must be owned by the current user: %s\n' "$path" >&2
        return 1
    }
    if (( 8#$mode & 077 )); then
        printf 'ERROR: OpenConnect secret profile permissions are too broad (%s); run chmod 0600 %s\n' "$mode" "$path" >&2
        return 1
    fi
}

# Materialize one user-owned secret TOML into the normalized Toad config plus
# root-readable runtime secret files. Secret values never cross argv/stdout.
oc_profile_materialize() {
    local source="$1"
    local output_config="$2"
    local state_dir="$3"
    local password_file="$4"
    local token_file="$5"
    local dns_override_file="$6"
    local name="$7"
    local interface="$8"

    oc_profile_require_secret_file "$source"
    python3 - "$source" "$output_config" "$state_dir" "$password_file" "$token_file" "$dns_override_file" "$name" "$interface" <<'PY'
import ipaddress
import json
import os
import pathlib
import sys
import tomllib
from urllib.parse import urlsplit

(
    source,
    output_config,
    state_dir,
    password_file,
    token_file,
    dns_override_file,
    name,
    interface,
) = sys.argv[1:]

raw = tomllib.loads(pathlib.Path(source).read_text(encoding="utf-8"))

def require_string(key):
    value = raw.get(key)
    if not isinstance(value, str) or not value.strip():
        raise SystemExit(f"real OpenConnect profile field {key!r} is required")
    return value

gateway = require_string("gateway").strip()
username = require_string("username")
password = require_string("password")
totp_secret = require_string("totp_secret").replace(" ", "").strip()
vpn_protocol = str(raw.get("vpn_protocol", "anyconnect") or "anyconnect")
if vpn_protocol != "anyconnect":
    raise SystemExit(f"only vpn_protocol=anyconnect is supported, got {vpn_protocol!r}")
parts = urlsplit(gateway)
if parts.scheme != "https" or not parts.hostname:
    raise SystemExit("gateway must be an https:// URL with a hostname")
if parts.username or parts.password:
    raise SystemExit("gateway URL must not contain credentials")
port = parts.port or 443
if not (1 <= port <= 65535):
    raise SystemExit("gateway port is invalid")

mtu = int(raw.get("mtu", 1380))
if not 576 <= mtu <= 9000:
    raise SystemExit("mtu must be in range 576..9000")
reconnect_timeout = int(raw.get("reconnect_timeout", 300))
if not 0 <= reconnect_timeout <= 86400:
    raise SystemExit("reconnect_timeout must be in range 0..86400")
disable_udp = bool(raw.get("disable_udp", False))
disable_ipv6 = bool(raw.get("disable_ipv6", True))
server_cert = str(raw.get("server_cert", "") or "")
user_agent = str(raw.get("user_agent", "") or "")

dns_servers = raw.get("dns_servers", [])
if not isinstance(dns_servers, list):
    raise SystemExit("dns_servers must be an array")
normalized_dns = []
for value in dns_servers:
    if not isinstance(value, str):
        raise SystemExit("dns_servers entries must be strings")
    ip = ipaddress.ip_address(value)
    if ip.version != 4:
        raise SystemExit("this IPv4-only system-wide test accepts only IPv4 dns_servers")
    normalized_dns.append(str(ip))

for directory in {pathlib.Path(output_config).parent, pathlib.Path(password_file).parent, pathlib.Path(token_file).parent, pathlib.Path(dns_override_file).parent}:
    directory.mkdir(parents=True, exist_ok=True)
    os.chmod(directory, 0o700)

pathlib.Path(password_file).write_text(password + "\n", encoding="utf-8")
os.chmod(password_file, 0o600)
pathlib.Path(token_file).write_text(totp_secret, encoding="utf-8")
os.chmod(token_file, 0o600)
pathlib.Path(dns_override_file).write_text(" ".join(normalized_dns) + "\n", encoding="utf-8")
os.chmod(dns_override_file, 0o600)

q = lambda value: json.dumps(str(value), ensure_ascii=False)
lines = [
    f"name = {q(name)}",
    'protocol = "openconnect"',
    f"interface = {q(interface)}",
    f"mtu = {mtu}",
    f"state_dir = {q(state_dir)}",
    "",
    "[openconnect]",
    f"gateway = {q(gateway)}",
    f"vpn_protocol = {q(vpn_protocol)}",
    f"username = {q(username)}",
    f"password_file = {q(password_file)}",
    'token_mode = "totp"',
    f"token_secret_file = {q(token_file)}",
    f"server_cert = {q(server_cert)}",
    f"user_agent = {q(user_agent)}",
    f"disable_udp = {'true' if disable_udp else 'false'}",
    f"disable_ipv6 = {'true' if disable_ipv6 else 'false'}",
    f"reconnect_timeout = {reconnect_timeout}",
]
pathlib.Path(output_config).write_text("\n".join(lines) + "\n", encoding="utf-8")
os.chmod(output_config, 0o600)
PY
}

oc_profile_public_fields() {
    local config="$1"
    python3 - "$config" <<'PY'
import pathlib
import sys
import tomllib
from urllib.parse import urlsplit

cfg = tomllib.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
oc = cfg.get("openconnect") or {}
gateway = str(oc.get("gateway", ""))
parts = urlsplit(gateway)
port = parts.port or 443
print(f"gateway={gateway}")
print(f"gateway_host={parts.hostname or ''}")
print(f"gateway_port={port}")
print(f"interface={cfg.get('interface', '')}")
print(f"mtu={cfg.get('mtu', '')}")
PY
}
