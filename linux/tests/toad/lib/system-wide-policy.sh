#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

# Policy overlay for system-wide-diag.sh.
# Source this after the base harness. It keeps the host-routing lifecycle intact,
# shortens the manual test window, and post-processes diagnostics before archiving.

SW_MANUAL_TIMEOUT="${TOAD_SYSTEM_WIDE_HOLD_SECONDS:-120}"
SW_DIAG_TARGET_BYTES="${TOAD_SYSTEM_WIDE_DIAG_TARGET_BYTES:-4194304}"
export SW_MANUAL_TIMEOUT SW_DIAG_TARGET_BYTES

sw_generate_compact_summary() {
    python3 - "$SW_DIAG_DIR" <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
out = root / "summary.txt"


def read_text(name):
    path = root / name
    if not path.exists():
        return ""
    try:
        return path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""


def read_state(name):
    path = root / name
    if not path.exists():
        return None
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None


def state_line(label, state):
    if not isinstance(state, dict):
        return f"{label}=unavailable"
    session = state.get("session") if isinstance(state.get("session"), dict) else {}
    bits = [
        f"state={state.get('state', 'unknown')}",
        f"reason={state.get('reason', 'unknown')}",
    ]
    for key in ("connected", "endpoint", "last_handshake_age_ms", "rx_bytes", "tx_bytes"):
        if key in session:
            bits.append(f"{key}={session.get(key)}")
    return f"{label}: " + " ".join(bits)


def interesting_sampler(text):
    if not text:
        return []
    blocks = re.split(r"(?=\n?===== )", text)
    rows = []
    previous = None
    for block in blocks:
        block = block.strip()
        if not block:
            continue
        values = {}
        for line in block.splitlines():
            if "=" in line:
                key, value = line.split("=", 1)
                if key in {"toad_alive", "route_endpoint", "route_google", "route_chatgpt", "rx_bytes", "tx_bytes"}:
                    values[key] = value.strip()
        signature = tuple(values.get(k, "") for k in ("toad_alive", "route_endpoint", "route_google", "route_chatgpt"))
        if previous is None or signature != previous:
            rows.append(block)
        previous = signature
    # Always retain the last sample even when routes did not change.
    last = blocks[-1].strip() if blocks else ""
    if last and (not rows or rows[-1] != last):
        rows.append(last)
    return rows[-8:]

metadata = read_text("metadata.txt").strip()
traffic = read_text("traffic-test.txt").strip()
warnings = read_text("warnings.txt").strip()
errors = read_text("errors.txt").strip()
command = read_text("command.log")
toad = read_text("toad.log")
sampler = read_text("active-sampler.txt")

states = [
    state_line("state-before-cutover", read_state("state-before-cutover.json")),
    state_line("state-active", read_state("state-active.json")),
    state_line("state-active-final", read_state("state-active-final.json")),
    state_line("state-after", read_state("state-after.json")),
]

trace_rows = []
for name in ("tun-trace.txt", "underlay-trace.txt"):
    text = read_text(name)
    packet_lines = [line for line in text.splitlines() if re.search(r"\b(IP6?|ARP)\b", line)]
    trace_rows.append(f"{name}: packet_lines={len(packet_lines)} bytes={len(text.encode('utf-8', errors='replace'))}")

phase_rows = []
for phase in ("before", "toad-up", "active", "active-final", "after"):
    route_text = read_text(f"{phase}/routes.txt")
    default_lines = [line.strip() for line in route_text.splitlines() if line.startswith("default ")]
    if default_lines:
        phase_rows.append(f"{phase}: " + " | ".join(default_lines[:6]))

with out.open("w", encoding="utf-8") as fh:
    fh.write("SYSTEM-WIDE TOAD DIAGNOSTIC SUMMARY\n")
    fh.write("===================================\n\n")
    if metadata:
        fh.write("[metadata]\n")
        fh.write(metadata + "\n\n")
    fh.write("[toad states]\n")
    fh.write("\n".join(states) + "\n\n")
    if phase_rows:
        fh.write("[default-route snapshots]\n")
        fh.write("\n".join(phase_rows) + "\n\n")
    if traffic:
        fh.write("[automatic traffic probes]\n")
        fh.write(traffic[-12000:] + "\n\n")
    fh.write("[packet traces]\n")
    fh.write("\n".join(trace_rows) + "\n\n")
    sampler_rows = interesting_sampler(sampler)
    if sampler_rows:
        fh.write("[manual-window route/state changes]\n")
        fh.write("\n---\n".join(sampler_rows) + "\n\n")
    if warnings:
        fh.write("[warnings]\n")
        fh.write(warnings[-12000:] + "\n\n")
    if errors:
        fh.write("[errors]\n")
        fh.write(errors[-12000:] + "\n\n")
    # Keep the tails because shutdown failures and core errors generally land there.
    if command:
        fh.write("[command.log tail]\n")
        fh.write("\n".join(command.splitlines()[-80:]) + "\n\n")
    if toad:
        fh.write("[toad.log tail]\n")
        fh.write("\n".join(toad.splitlines()[-120:]) + "\n")
PY
}

sw_compact_diagnostics() {
    python3 - "$SW_DIAG_DIR" "$SW_DIAG_TARGET_BYTES" <<'PY'
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
target = int(sys.argv[2])

# Preserve structured state and the generated summary verbatim. Large noisy text
# gets a bounded head+tail so startup and cleanup/failure context both survive.
limits = {
    "tun-trace.txt": 600_000,
    "underlay-trace.txt": 350_000,
    "system-journal.txt": 700_000,
    "active-sampler.txt": 220_000,
    "toad.log": 700_000,
    "command.log": 450_000,
}

def limit_for(path: pathlib.Path) -> int:
    if path.name in {"summary.txt", "metadata.txt", "traffic-test.txt", "warnings.txt", "errors.txt", "size-report.txt"}:
        return 0
    if path.name in limits:
        return limits[path.name]
    if path.name in {"runtime.txt", "firewall.txt", "network.txt", "routes.txt", "dns.txt", "network-manager.txt"}:
        return 300_000
    if path.suffix in {".txt", ".log"}:
        return 400_000
    return 0


def compact(path: pathlib.Path, limit: int) -> tuple[int, int, bool]:
    raw = path.read_bytes()
    before = len(raw)
    if limit <= 0 or before <= limit:
        return before, before, False
    head = min(96_000, limit // 4)
    marker = (
        b"\n\n--- diagnostic log compacted: middle removed; "
        + str(max(0, before - limit)).encode("ascii")
        + b"+ bytes omitted ---\n\n"
    )
    tail = max(0, limit - head - len(marker))
    kept = raw[:head] + marker + raw[-tail:]
    path.write_bytes(kept)
    return before, len(kept), True

rows = []
for path in sorted(root.rglob("*")):
    if not path.is_file():
        continue
    limit = limit_for(path)
    before, after, changed = compact(path, limit)
    rows.append((str(path.relative_to(root)), before, after, changed))

report = root / "size-report.txt"
with report.open("w", encoding="utf-8") as fh:
    fh.write(f"archive_target_bytes={target}\n")
    fh.write("path\tbytes_before\tbytes_after\tcompacted\n")
    for rel, before, after, changed in rows:
        fh.write(f"{rel}\t{before}\t{after}\t{str(changed).lower()}\n")
    fh.write(f"total_tree_bytes_after={sum(row[2] for row in rows)}\n")
PY
}

sw_recompact_for_archive_size() {
    python3 - "$SW_DIAG_DIR" <<'PY'
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
protected = {"summary.txt", "metadata.txt", "traffic-test.txt", "warnings.txt", "errors.txt", "size-report.txt"}
for path in root.rglob("*"):
    if not path.is_file() or path.name in protected:
        continue
    if path.suffix not in {".txt", ".log"}:
        continue
    raw = path.read_bytes()
    limit = 180_000
    if path.name in {"toad.log", "system-journal.txt", "tun-trace.txt"}:
        limit = 320_000
    if len(raw) <= limit:
        continue
    head = min(48_000, limit // 4)
    marker = b"\n\n--- second-pass compaction for chat upload size ---\n\n"
    tail = max(0, limit - head - len(marker))
    path.write_bytes(raw[:head] + marker + raw[-tail:])
PY
}

sw_finalize() {
    local status="$1"
    local archive_status=0 archive_size=0

    trap - ERR EXIT INT TERM
    set +e

    sw_stop_sampler
    if [[ -z "$SW_RX_AFTER" ]]; then
        SW_RX_AFTER="$(sw_read_counter rx_bytes)"
    fi
    if [[ -z "$SW_TX_AFTER" ]]; then
        SW_TX_AFTER="$(sw_read_counter tx_bytes)"
    fi
    sw_capture_state "$SW_DIAG_DIR/state-active-final.json"
    sw_collect_snapshot active-final

    # Restore ordinary host connectivity before stopping Toad itself.
    sw_disable_default_route
    sw_restore_dns
    sw_remove_endpoint_route
    sw_stop_trace
    sw_stop_toad

    sleep 0.2
    sw_collect_snapshot after
    sw_capture_state "$SW_DIAG_DIR/state-after.json"

    {
        printf '=== journal since test start ===\n'
        journalctl --no-pager --since "@$SW_START_EPOCH" -n 1200 2>/dev/null || true
        printf '\n=== kernel journal since test start ===\n'
        journalctl -k --no-pager --since "@$SW_START_EPOCH" -n 800 2>/dev/null || true
        printf '\n=== dmesg tail ===\n'
        dmesg --ctime 2>/dev/null | tail -n 350 || true
    } >"$SW_DIAG_DIR/system-journal.txt" 2>&1

    if [[ "$SW_RESULT" != "PASS" && "$status" -eq 0 ]]; then
        status=1
    fi
    sw_write_metadata "$status"
    sw_generate_compact_summary
    sw_compact_diagnostics
    sw_redact_tree
    rm -rf -- "$SW_PRIVATE_DIR"

    tar -czf "$SW_ARCHIVE" -C "$SW_WORK" diag
    archive_status=$?
    if (( archive_status == 0 )); then
        archive_size="$(stat -c %s "$SW_ARCHIVE" 2>/dev/null || printf '0')"
        if [[ "$archive_size" =~ ^[0-9]+$ ]] && (( archive_size > SW_DIAG_TARGET_BYTES )); then
            sw_warn "diagnostic archive was ${archive_size} bytes; applying second-pass compaction for chat upload"
            rm -f -- "$SW_ARCHIVE"
            sw_recompact_for_archive_size
            sw_generate_compact_summary
            sw_redact_tree
            tar -czf "$SW_ARCHIVE" -C "$SW_WORK" diag
            archive_status=$?
            archive_size="$(stat -c %s "$SW_ARCHIVE" 2>/dev/null || printf '0')"
        fi
    fi

    if (( archive_status == 0 )); then
        chmod 0600 "$SW_ARCHIVE" 2>/dev/null || true
        printf 'Diagnostic archive: %s (%s bytes)\n' "$SW_ARCHIVE" "$archive_size"
        rm -rf -- "$SW_WORK"
    else
        printf 'ERROR: failed to create diagnostic archive; sanitized directory remains at %s\n' "$SW_DIAG_DIR" >&2
        status="$archive_status"
    fi

    exit "$status"
}
