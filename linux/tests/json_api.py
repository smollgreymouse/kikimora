#!/usr/bin/env python3
"""Regression checks for the machine-readable Kikimora CLI API."""

from __future__ import annotations

import json
import os
import shlex
import stat
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
COMMON = ROOT / "files" / "kikimora-cli" / "common.sh"
ENTRYPOINT = ROOT / "kikimora"


def write_executable(path: Path, content: str) -> None:
    path.write_text(content, encoding="utf-8")
    path.chmod(path.stat().st_mode | stat.S_IXUSR)


def main() -> None:
    with tempfile.TemporaryDirectory(prefix="kikimora-json-api-") as raw_tmp:
        tmp = Path(raw_tmp)
        bin_dir = tmp / "bin"
        bin_dir.mkdir()

        write_executable(
            bin_dir / "systemctl",
            """#!/usr/bin/env bash
set -u
verb="${1:-}"
shift || true
[[ "${1:-}" == "--quiet" ]] && shift || true
unit="${1:-}"
case "$verb" in
  is-active)
    case "$unit" in
      leshy.service|leshy-route-watch.service|leshy-health-watch.service) exit 0 ;;
      *) exit 3 ;;
    esac
    ;;
  is-failed) exit 1 ;;
  is-enabled) [[ "$unit" == leshy.service ]] && exit 0 || exit 1 ;;
  try-restart) exit 0 ;;
  *) exit 1 ;;
esac
""",
        )
        write_executable(
            bin_dir / "journalctl",
            """#!/usr/bin/env bash
printf '%s\\n' \\
  '{"MESSAGE":"Leshy started","_SYSTEMD_UNIT":"leshy.service","__REALTIME_TIMESTAMP":"1000000"}' \\
  '{"MESSAGE":"Route watcher ready","_SYSTEMD_UNIT":"leshy-route-watch.service","__REALTIME_TIMESTAMP":"2000000"}'
""",
        )

        env = os.environ.copy()
        env["PATH"] = f"{bin_dir}:{env['PATH']}"

        common = shlex.quote(str(COMMON))
        setup = f"""
set -Eeuo pipefail
SELF_DIR={shlex.quote(str(ROOT))}
source {common}
load_interfaces() {{ PRIMARY_INTERFACE=amn0; SECONDARY_INTERFACE=tun0; }}
vpn_role_state() {{
  case "$1" in primary|secondary) printf ready ;; *) printf missing ;; esac
}}
interface_state() {{
  if [[ "$2" == dns ]]; then printf active; else printf ready; fi
}}
get_default_zone() {{ printf 'direct\\n'; }}
read_current_primary_interface() {{ printf 'amn0\\n'; }}
read_current_secondary_interface() {{ printf 'tun0\\n'; }}
read_current_primary_provider() {{ printf 'static\\n'; }}
read_current_secondary_provider() {{ printf 'command\\n'; }}
read_current_primary_provider_args() {{ printf '\\n'; }}
read_current_secondary_provider_args() {{ printf '/usr/local/libexec/provider\\n'; }}
load_vpn_profiles() {{
  declare -gA VPN_PROFILE_PRIMARY=([default]=amn0 [office]=amn0)
  declare -gA VPN_PROFILE_SECONDARY=([default]=vpn0 [office]=tun0)
  declare -gA VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER=([default]=static [office]=static)
  declare -gA VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER=([default]=static [office]=command)
  declare -gA VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS=([default]='' [office]='')
  declare -gA VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS=([default]='' [office]='/usr/local/libexec/provider')
}}
profile_for_state() {{ printf 'office\\n'; }}
json_string_file_array() {{
  case "$1" in
    */primary.txt) printf '["203.0.113.10","vpn.example.net"]' ;;
    */secondary.txt) printf '[]' ;;
    *) printf '[]' ;;
  esac
}}
endpoint_installed_addresses() {{
  case "$1" in
    primary) printf '203.0.113.10\\n' ;;
    secondary) printf '198.51.100.40\\n198.51.100.41\\n' ;;
  esac
}}
"""

        def invoke(expression: str) -> dict:
            result = subprocess.run(
                ["bash", "-c", setup + "\n" + expression],
                check=True,
                capture_output=True,
                text=True,
                env=env,
            )
            try:
                return json.loads(result.stdout)
            except json.JSONDecodeError as exc:
                raise AssertionError(
                    f"invalid JSON from {expression!r}: {result.stdout!r}\nstderr: {result.stderr}"
                ) from exc

        status_payload = invoke("json_status")
        assert status_payload["schema_version"] == 1
        assert status_payload["service"] == "running"
        assert status_payload["profiles"]["active"] == "office"
        assert status_payload["interfaces"]["primary"] == {"name": "amn0", "state": "ready"}
        assert status_payload["interfaces"]["secondary"] == {"name": "tun0", "state": "ready"}
        assert status_payload["dns"]["provider"] == "leshy"
        assert status_payload["startup"]["enabled"] is True

        profiles_payload = invoke("json_profiles")
        assert profiles_payload["active"] == "office"
        profiles = {item["name"]: item for item in profiles_payload["profiles"]}
        assert profiles["office"]["active"] is True
        assert profiles["office"]["secondary"]["interface"] == "tun0"
        assert profiles["office"]["secondary"]["endpoint_provider"] == "command"

        endpoints_payload = invoke("json_endpoints")
        primary = endpoints_payload["roles"]["primary"]
        secondary = endpoints_payload["roles"]["secondary"]
        assert primary["provider"] == "static"
        assert primary["configured"] == ["203.0.113.10", "vpn.example.net"]
        assert primary["installed"] == ["203.0.113.10"]
        assert primary["actions"] == {"rediscover": True, "invalidate": False}
        assert secondary["provider"] == "command"
        assert secondary["provider_args"] == "/usr/local/libexec/provider"
        assert secondary["configured"] == []
        assert secondary["installed"] == ["198.51.100.40", "198.51.100.41"]
        assert secondary["actions"] == {"rediscover": True, "invalidate": False}
        assert "cache" not in secondary
        assert "current" not in secondary
        assert "candidates" not in secondary

        dns_payload = invoke("json_dns")
        assert dns_payload["provider"] == "leshy"
        assert dns_payload["interface"]["state"] == "active"

        logs_payload = invoke("json_logs --all --lines 2")
        assert logs_payload["limit"] == 2
        assert logs_payload["units"] == [
            "leshy.service",
            "leshy-route-watch.service",
            "leshy-health-watch.service",
        ]
        assert [entry["MESSAGE"] for entry in logs_payload["entries"]] == [
            "Leshy started",
            "Route watcher ready",
        ]

        entrypoint_text = ENTRYPOINT.read_text(encoding="utf-8")
        assert 'if [[ "${1:-}" == "--json" ]]' in entrypoint_text
        assert 'json_dispatch "$command" "$@"' in entrypoint_text
        assert 'endpoints) cmd_endpoints "$@"' in entrypoint_text

        combined = COMMON.read_text(encoding="utf-8") + entrypoint_text
        forbidden = ["happ-", "xray", "sing-box", "KIKIMORA_HAPP_STATE_DIR"]
        for token in forbidden:
            assert token not in combined, f"provider-specific token leaked into generic CLI API: {token}"

    print("CLI JSON API regression tests: OK")


if __name__ == "__main__":
    main()
