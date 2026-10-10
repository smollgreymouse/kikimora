#!/usr/bin/env python3
"""Static regression checks for embedded Python predicates in orchestration acceptance."""

from __future__ import annotations

import io
import re
import sys
import tokenize
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "linux/tests/toad/go-orchestration-acceptance.sh"

text = SCRIPT.read_text()
lines = text.splitlines()

blocks: list[tuple[int, str]] = []
i = 0
while i < len(lines):
    line = lines[i]
    if "mpf_wait_snapshot" not in line or not line.rstrip().endswith('"'):
        i += 1
        continue
    start = i + 1
    body: list[str] = []
    i += 1
    while i < len(lines):
        if lines[i].startswith('" || {'):
            break
        body.append(lines[i])
        i += 1
    else:
        raise SystemExit(f"unterminated mpf_wait_snapshot predicate starting at line {start}")
    blocks.append((start, "\n".join(body)))
    i += 1

if not blocks:
    raise SystemExit("no mpf_wait_snapshot predicates found")

errors: list[str] = []
shell_var = re.compile(r"\$[A-Za-z_][A-Za-z0-9_]*")
for line_no, raw in blocks:
    # Model what bash passes to python3 -c closely enough for syntax checking:
    # expand shell variables to a numeric sentinel and unescape quotes used
    # inside the outer shell double-quoted string.
    normalized = shell_var.sub("1", raw).replace(r'\"', '"')
    try:
        compile(normalized, f"{SCRIPT.name}:{line_no}", "exec")
    except SyntaxError as exc:
        errors.append(f"line {line_no}: embedded Python syntax error: {exc.msg}")
        continue

    try:
        tokens = tokenize.generate_tokens(io.StringIO(normalized).readline)
        for token in tokens:
            if token.type == tokenize.NAME and token.string in {"true", "false", "none"}:
                errors.append(
                    f"line {line_no + token.start[0] - 1}: lowercase Python constant "
                    f"{token.string!r} in snapshot predicate"
                )
    except tokenize.TokenError as exc:
        errors.append(f"line {line_no}: embedded Python tokenization error: {exc}")

if errors:
    print("\n".join(errors), file=sys.stderr)
    raise SystemExit(1)

print(f"orchestration snapshot predicates: PASS ({len(blocks)} blocks)")
