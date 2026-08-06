#!/usr/bin/python3
"""Chrome Native Messaging bridge for Kikimora domain management."""

from __future__ import annotations

import json
import os
import re
import struct
import subprocess
import sys
from typing import BinaryIO, Any

HOST_NAME = "com.kikimora.domain_manager"
EXTENSION_ID = "amllchapajpfdibbngeghpjbbofemaif"
ALLOWED_ORIGIN = f"chrome-extension://{EXTENSION_ID}/"
KIKIMORA_BIN = "/usr/local/sbin/kikimora"
PKEXEC_BIN = "/usr/bin/pkexec"
MAX_REQUEST_BYTES = 64 * 1024
DOMAIN_RE = re.compile(
    r"^(?:\*\.)?(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$"
)


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
    if value not in {"primary", "secondary"}:
        raise RequestError("zone must be primary or secondary")
    return str(value)


def run_kikimora_add(domain: str, zone: str) -> dict[str, Any]:
    if not os.path.isfile(KIKIMORA_BIN) or not os.access(KIKIMORA_BIN, os.X_OK):
        raise RequestError(f"Kikimora is not installed at {KIKIMORA_BIN}")
    if not os.path.isfile(PKEXEC_BIN) or not os.access(PKEXEC_BIN, os.X_OK):
        raise RequestError("pkexec is required to update the root-owned Kikimora configuration")

    command = [
        PKEXEC_BIN,
        KIKIMORA_BIN,
        "domains",
        "add",
        domain,
        f"--{zone}",
    ]

    try:
        completed = subprocess.run(
            command,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=180,
            check=False,
        )
    except subprocess.TimeoutExpired as exc:
        raise RequestError("authorization or Kikimora command timed out") from exc

    stdout = completed.stdout.strip()
    stderr = completed.stderr.strip()
    if completed.returncode != 0:
        if completed.returncode in {126, 127}:
            raise RequestError("authorization was cancelled or denied")
        raise RequestError(stderr or stdout or f"Kikimora exited with code {completed.returncode}")

    return {
        "ok": True,
        "domain": domain,
        "zone": zone,
        "message": stdout or f"Added: {domain} ({zone})",
    }


def process_message(message: dict[str, Any]) -> dict[str, Any]:
    action = message.get("action")
    if action == "ping":
        return {"ok": True, "host": HOST_NAME, "extension_id": EXTENSION_ID}
    if action != "add-domain":
        raise RequestError("unsupported action")

    domain = normalize_domain(message.get("domain"))
    zone = normalize_zone(message.get("zone"))
    return run_kikimora_add(domain, zone)


def validate_origin(argv: list[str]) -> None:
    if os.environ.get("KIKIMORA_NATIVE_HOST_SKIP_ORIGIN_CHECK") == "1":
        return
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
