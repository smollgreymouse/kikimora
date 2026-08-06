#!/usr/bin/python3
"""Chrome Native Messaging bridge for Kikimora domain management."""

from __future__ import annotations

import json
import os
import re
import struct
import subprocess
import sys
from typing import Any, BinaryIO

HOST_NAME = "com.kikimora.domain_manager"
EXTENSION_ID = "amllchapajpfdibbngeghpjbbofemaif"
ALLOWED_ORIGIN = f"chrome-extension://{EXTENSION_ID}/"
KIKIMORA_BIN = "/usr/local/sbin/kikimora"
PKEXEC_BIN = "/usr/bin/pkexec"
MAX_REQUEST_BYTES = 64 * 1024
DOMAIN_RE = re.compile(
    r"^(?:\*\.)?(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$"
)
ZONES = ("primary", "secondary")


class RequestError(ValueError):
    """A safe validation error that can be returned to the extension."""


def read_exact(stream: BinaryIO, size: int) -> bytes:
    chunks: list[bytes] = []
    remaining = size
    while remaining:
        chunk = stream.read(remaining)
        if not chunk:
            raise EOFError("unexpected end of native message")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def read_message(stream: BinaryIO) -> dict[str, Any] | None:
    header = stream.read(4)
    if not header:
        return None
    if len(header) != 4:
        raise RequestError("invalid native message header")

    (length,) = struct.unpack("=I", header)
    if length == 0 or length > MAX_REQUEST_BYTES:
        raise RequestError("invalid native message length")

    payload = read_exact(stream, length)
    try:
        message = json.loads(payload.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise RequestError("invalid JSON request") from exc
    if not isinstance(message, dict):
        raise RequestError("request must be a JSON object")
    return message


def write_message(stream: BinaryIO, message: dict[str, Any]) -> None:
    payload = json.dumps(message, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    stream.write(struct.pack("=I", len(payload)))
    stream.write(payload)
    stream.flush()


def normalize_domain(value: Any) -> str:
    if not isinstance(value, str):
        raise RequestError("domain must be a string")
    domain = value.strip().lower().rstrip(".")
    if not DOMAIN_RE.fullmatch(domain):
        raise RequestError("invalid domain name")
    return domain


def normalize_zone(value: Any) -> str:
    if value not in ZONES:
        raise RequestError("zone must be primary or secondary")
    return str(value)


def ensure_executable(path: str, description: str) -> None:
    if not os.path.isfile(path) or not os.access(path, os.X_OK):
        raise RequestError(description)


def run_command(command: list[str], timeout: int) -> subprocess.CompletedProcess[str]:
    try:
        return subprocess.run(
            command,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired as exc:
        raise RequestError("Kikimora command timed out") from exc


def command_error(completed: subprocess.CompletedProcess[str]) -> RequestError:
    stdout = completed.stdout.strip()
    stderr = completed.stderr.strip()
    if completed.returncode in {126, 127}:
        return RequestError("authorization was cancelled or denied")
    return RequestError(stderr or stdout or f"Kikimora exited with code {completed.returncode}")


def parse_domain_list(output: str) -> list[str]:
    domains: set[str] = set()
    for raw_line in output.splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        try:
            domains.add(normalize_domain(line))
        except RequestError as exc:
            raise RequestError("Kikimora returned an invalid domain list") from exc
    return sorted(domains)


def run_kikimora_list(zone: str) -> list[str]:
    ensure_executable(KIKIMORA_BIN, f"Kikimora is not installed at {KIKIMORA_BIN}")
    completed = run_command(
        [KIKIMORA_BIN, "domains", "list", zone],
        timeout=30,
    )
    if completed.returncode != 0:
        raise command_error(completed)
    return parse_domain_list(completed.stdout)


def run_kikimora_change(action: str, domain: str, zone: str) -> dict[str, Any]:
    if action not in {"add", "remove"}:
        raise RequestError("unsupported domain change")

    ensure_executable(KIKIMORA_BIN, f"Kikimora is not installed at {KIKIMORA_BIN}")
    ensure_executable(
        PKEXEC_BIN,
        "pkexec is required to update the root-owned Kikimora configuration",
    )

    completed = run_command(
        [
            PKEXEC_BIN,
            KIKIMORA_BIN,
            "domains",
            action,
            domain,
            f"--{zone}",
        ],
        timeout=180,
    )
    if completed.returncode != 0:
        raise command_error(completed)

    verb = "Added" if action == "add" else "Removed"
    return {
        "ok": True,
        "action": action,
        "domain": domain,
        "zone": zone,
        "message": completed.stdout.strip() or f"{verb}: {domain} ({zone})",
    }


def process_message(message: dict[str, Any]) -> dict[str, Any]:
    action = message.get("action")
    if action == "ping":
        return {"ok": True, "host": HOST_NAME, "extension_id": EXTENSION_ID}

    if action == "list-domains":
        zones = {zone: run_kikimora_list(zone) for zone in ZONES}
        return {
            "ok": True,
            "zones": zones,
            "counts": {zone: len(domains) for zone, domains in zones.items()},
        }

    if action not in {"add-domain", "remove-domain"}:
        raise RequestError("unsupported action")

    domain = normalize_domain(message.get("domain"))
    zone = normalize_zone(message.get("zone"))
    change = "add" if action == "add-domain" else "remove"
    return run_kikimora_change(change, domain, zone)


def validate_origin(argv: list[str]) -> None:
    if len(argv) < 2 or argv[1].rstrip("/") != ALLOWED_ORIGIN.rstrip("/"):
        raise RequestError("caller origin is not allowed")


def main(argv: list[str] | None = None) -> int:
    argv = argv or sys.argv
    try:
        validate_origin(argv)
        request = read_message(sys.stdin.buffer)
        if request is None:
            return 0
        response = process_message(request)
    except RequestError as exc:
        response = {"ok": False, "error": str(exc)}
    except Exception as exc:  # Keep protocol output valid even on unexpected failure.
        print(f"kikimora-native-host: {exc}", file=sys.stderr)
        response = {"ok": False, "error": "unexpected native host error"}

    write_message(sys.stdout.buffer, response)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
