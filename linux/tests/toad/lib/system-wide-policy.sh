#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

# Policy overlay for system-wide-diag.sh.
# Source this after the base harness. It keeps the host-routing lifecycle intact,
# shortens the manual test window, and bounds diagnostic volume before archiving.

SW_MANUAL_TIMEOUT="${TOAD_SYSTEM_WIDE_HOLD_SECONDS:-120}"
SW_DIAG_TARGET_BYTES="${TOAD_SYSTEM_WIDE_DIAG_TARGET_BYTES:-4194304}"

sw_compact_diagnostics() {
    python3 - "$SW_DIAG_DIR" "$SW_DIAG_TARGET_BYTES" <<'PY'
import os
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
target = int(sys.argv[2])

# Preserve small structured evidence verbatim. Large noisy text gets a bounded
# head+tail so startup context and the final failure/cleanup context both remain.
limits = {
    "tun-trace.txt": 600_000,
    "underlay-trace.txt": 350_000,
    "system-journal.txt": 700_000,
    "active-sampler.txt": 300_000,
    "toad.log": 700_000,
    "command.log": 500_000,
}
def limit_for(path: pathlib.Path) -> int:
    if path.name in limits:
        return limits[path.name]
    if path.name in {"runtime.txt", "firewall.txt", "network.txt", "routes.txt", "dns.txt", "network-manager.txt"}:
        return 350_000
    if path.suffix in {".txt", ".log"}:
        return 450_000
    return 0

def compact(path: pathlib.Path, limit: int) -> tuple[int, int, bool]:
    raw = path.read_bytes()
    before = len(raw)
    if limit <= 0 or before <= limit:
        return before, before, False
    head = min(96_000, limit // 4)
    tail = max(0, limit - head - 256)
    marker = (
        b"\n\n--- diagnostic log compacted: middle removed; "
        + str(before - head - tail).encode("ascii")
        + b" bytes omitted ---\n\n"
    )
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
    fh.write(f"total_text_tree_bytes_after={sum(row[2] for row in rows)}\n")
PY
}

sw_recompact_for_archive_size() {
    local archive_size="$1"
    if (( archive_size <= SW_DIAG_TARGET_BYTES )); then
        return 0
    fi

    # Second pass for unexpectedly noisy hosts. Keep all structured state files,
    # but tighten only text/log evidence. The marker preserves that compaction
    # happened instead of silently truncating data.
    python3 - "$SW_DIAG_DIR" <<'PY'
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
for path in root.rglob("*"):
    if not path.is_file() or path.name == "size-report.txt":
        continue
    if path.suffix not in {".txt", ".log"}:
        continue
    raw = path.read_bytes()
    limit = 220_000
    if path.name in {"toad.log", "system-journal.txt", "tun-trace.txt"}:
        limit = 400_000
    if len(raw) <= limit:
        continue
    head = min(64_000, limit // 4)
    tail = max(0, limit - head - 256)
    marker = b"\n\n--- second-pass compaction for upload size ---\n\n"
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
            sw_recompact_for_archive_size "$archive_size"
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
