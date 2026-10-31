#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# vm-dualstack-regression.sh — 08A.3R Slice 4: installed VM dual-stack
# DNS/route-stability regression acceptance
#
# Runs on the disposable Ubuntu VM against the installed Kikimora package.
# Captures before/after route, rule, DNS and application-probe evidence.
# Verifies the dual-stack field regression contract from
# 08-dual-stack-dns-route-stability.md.

set -Eeuo pipefail

KK="${KK_BIN:-/usr/local/bin/kk}"
OUT_DIR="${VM_DUALSTACK_REGRESSION_OUT:-/tmp/vm-dualstack-regression-$$}"

mkdir -p "$OUT_DIR"
fail() { echo "ERROR: $*" >&2; exit 1; }
command -v python3 >/dev/null || fail "python3 required"
command -v timeout >/dev/null || fail "timeout required"
"$KK" status --json >"$OUT_DIR/preflight.json" || fail "core status unavailable"
python3 -m json.tool "$OUT_DIR/preflight.json" >/dev/null || fail "invalid core JSON"

echo "=== 08A.3R Slice 4: installed VM dual-stack regression ==="
echo "Output: $OUT_DIR"

# ---------------------------------------------------------------------------
# 1. Capture baseline (before)
# ---------------------------------------------------------------------------
echo "--- 1. Capturing baseline ---"

{
    echo "=== IPv4 routes ==="
    ip -4 route show
    echo "=== IPv6 routes ==="
    ip -6 route show
    echo "=== IPv4 rules ==="
    ip -4 rule show
    echo "=== IPv6 rules ==="
    ip -6 rule show
    echo "=== IPv4 route get AWG endpoint ==="
    ip -4 route get 72.56.119.116 2>/dev/null || echo "(unreachable)"
    echo "=== IPv4 route get OC endpoint ==="
    ip -4 route get 46.243.227.103 2>/dev/null || echo "(unreachable)"
    echo "=== Managed /1 prefix count ==="
    echo "v4 /1: $(ip -4 route show 0.0.0.0/1 2>/dev/null | wc -l) + $(ip -4 route show 128.0.0.0/1 2>/dev/null | wc -l)"
    echo "v6 /1: $(ip -6 route show ::/1 2>/dev/null | wc -l) + $(ip -6 route show 8000::/1 2>/dev/null | wc -l)"
    echo "=== Managed /32 /128 park count ==="
    echo "v4 parks: $(ip -4 route show table 254 | grep -c 'unreachable.*metric 42760' 2>/dev/null || echo 0)"
    echo "v6 parks: $(ip -6 route show table 254 | grep -c 'unreachable.*metric 42760' 2>/dev/null || echo 0)"
} >"$OUT_DIR/routes-before.txt"

{
    echo "=== resolvectl status ==="
    resolvectl status 2>/dev/null || echo "(unavailable)"
    echo "=== /etc/resolv.conf ==="
    cat /etc/resolv.conf 2>/dev/null
    echo "=== Leshy DNS state ==="
    ls -la /run/kikimora/leshy/dns/ 2>/dev/null || echo "(none)"
    echo "=== Leshy publication ==="
    ls -la /run/kikimora/leshy/vpn/ 2>/dev/null || echo "(none)"
} >"$OUT_DIR/dns-before.txt"

"$KK" status --json >"$OUT_DIR/snapshot-before.json" 2>/dev/null || true

# ---------------------------------------------------------------------------
# 2. A/AAAA resolution and per-family probe
# ---------------------------------------------------------------------------
echo "--- 2. A/AAAA resolution ---"

{
    echo "=== A record ==="
    dig @127.0.0.1 -p 53053 example.com A +short 2>/dev/null || echo "(failed)"
    echo "=== AAAA record ==="
    dig @127.0.0.1 -p 53053 example.com AAAA +short 2>/dev/null || echo "(failed)"
    echo "=== curl -4 (2s timeout) ==="
    curl -4 -so /dev/null -w '%{http_code} %{time_total}s' --connect-timeout 2 http://example.com 2>/dev/null || echo "FAIL"
    echo ""
    echo "=== curl -6 (2s timeout) ==="
    curl -6 -so /dev/null -w '%{http_code} %{time_total}s' --connect-timeout 2 http://example.com 2>/dev/null || echo "FAIL-v6"
    echo ""
} >"$OUT_DIR/dns-probes.txt"

echo "   DNS probes captured"
# Public HTTP is diagnostic-only: use a separately configured real role probe as the release gate.
if [[ -z "${AWG_PROBE_CMD:-}" || -z "${OC_PROBE_CMD:-}" ]]; then fail "AWG_PROBE_CMD and OC_PROBE_CMD must be provided for real data-plane acceptance"; fi

# ---------------------------------------------------------------------------
# 3. Real application probes (AWG + OC)
# ---------------------------------------------------------------------------
echo "--- 3. Application probes ---"

{
    echo "=== kikimora-core status ==="
    systemctl is-active kikimora-core 2>/dev/null || echo "(inactive)"
    echo "=== AWG Toad ==="
    pgrep -af 'kikimora-toad.*awg' | head -1 || echo "(not running)"
    echo "=== OC Toad ==="
    pgrep -af 'openconnect' | head -1 || echo "(not running)"
} >"$OUT_DIR/app-probes.txt"

echo "   Application probes captured"
for role in AWG OC; do
  var="${role}_PROBE_CMD"; cmd="${!var}"
  timeout 20s bash -c "$cmd" >"$OUT_DIR/probe-${role,,}-before.log" 2>&1 || fail "$role real application probe failed"
done

# ---------------------------------------------------------------------------
# 4. Zero-mutation soak (3 iterations, 2s apart)
# ---------------------------------------------------------------------------
echo "--- 4. Zero-mutation soak ---"

for i in 1 2 3; do
    sleep 2
    V4=$(ip -4 route show table 254 | wc -l)
    V6=$(ip -6 route show table 254 | wc -l)
    EP4=$(ip -4 route show table 51890 2>/dev/null | wc -l)
    EP6=$(ip -6 route show table 51890 2>/dev/null | wc -l)
    echo "iteration $i: v4=$V4 v6=$V6 ep-v4=$EP4 ep-v6=$EP6"
done >"$OUT_DIR/soak.txt"

metric_first() { head -1 "$OUT_DIR/soak.txt" | grep -oP "(^| )$1=\K[0-9]+"; }
metric_last()  { tail -1 "$OUT_DIR/soak.txt" | grep -oP "(^| )$1=\K[0-9]+"; }
# Assert every family/table is stable, including IPv6 main and both v4/v6
# endpoint table 51890 counts — not only the IPv4 main table.
for metric in v4 v6 ep-v4 ep-v6; do
    first=$(metric_first "$metric")
    last=$(metric_last "$metric")
    if [[ -z "$first" || "$first" != "$last" ]]; then
        fail "$metric route count drifted during soak: ${first:-?} -> ${last:-?}"
    fi
done
echo "PASS: route counts stable during soak (v4=$V4 v6=$V6 ep-v4=$EP4 ep-v6=$EP6)"

# ---------------------------------------------------------------------------
# 5. Recovery after core restart
# ---------------------------------------------------------------------------
echo "--- 5. Core restart recovery ---"

# The restart marker keeps the fail-closed parks through the stop: this test
# verifies transient-restart recovery, not a clean network release.
sudo mkdir -p /run/kikimora
sudo touch /run/kikimora/core-restart.pending
sudo systemctl restart kikimora-core
sleep 5

"$KK" status --json >"$OUT_DIR/snapshot-after-restart.json" 2>/dev/null || true
CORE_STATE=$(python3 -c "
import json
data = json.load(open('$OUT_DIR/snapshot-after-restart.json'))
print(data.get('core_state', 'unknown'))
" 2>/dev/null || echo "unknown")
echo "   Core state after restart: $CORE_STATE"
[[ "$CORE_STATE" != "unknown" ]] || fail "core state unavailable after restart"
for role in AWG OC; do
  var="${role}_PROBE_CMD"; cmd="${!var}"
  timeout 20s bash -c "$cmd" >"$OUT_DIR/probe-${role,,}-after.log" 2>&1 || fail "$role did not recover real application traffic"
done

# Re-check routes and DNS after recovery.
{
    echo "=== IPv4 routes (after restart) ==="
    ip -4 route show
    echo "=== IPv6 routes (after restart) ==="
    ip -6 route show
} >"$OUT_DIR/routes-after-restart.txt"

echo "   Routes captured after restart"

# ---------------------------------------------------------------------------
# 6. Final capture (after)
# ---------------------------------------------------------------------------
echo "--- 6. Final capture ---"

{
    echo "=== IPv4 routes ==="
    ip -4 route show
    echo "=== IPv6 routes ==="
    ip -6 route show
    echo "=== IPv4 rules ==="
    ip -4 rule show
    echo "=== IPv6 rules ==="
    ip -6 rule show
    echo "=== Managed /1 prefix count ==="
    echo "v4 /1: $(ip -4 route show 0.0.0.0/1 2>/dev/null | wc -l) + $(ip -4 route show 128.0.0.0/1 2>/dev/null | wc -l)"
    echo "v6 /1: $(ip -6 route show ::/1 2>/dev/null | wc -l) + $(ip -6 route show 8000::/1 2>/dev/null | wc -l)"
} >"$OUT_DIR/routes-after.txt"

"$KK" status --json >"$OUT_DIR/snapshot-after.json" 2>/dev/null || true

# ---------------------------------------------------------------------------
# 7. Verify no split-defaults accumulated
# ---------------------------------------------------------------------------
echo "--- 7. Split-default check ---"

SPLIT_V4=$(ip -4 route show 0.0.0.0/1 2>/dev/null | wc -l)
SPLIT_V4=$((SPLIT_V4 + $(ip -4 route show 128.0.0.0/1 2>/dev/null | wc -l)))
SPLIT_V6=$(ip -6 route show ::/1 2>/dev/null | wc -l)
SPLIT_V6=$((SPLIT_V6 + $(ip -6 route show 8000::/1 2>/dev/null | wc -l)))

if (( SPLIT_V4 > 0 )); then
    echo "ERROR: v4 split-default routes found ($SPLIT_V4)" >&2
    exit 1
fi
if (( SPLIT_V6 > 0 )); then
    echo "ERROR: v6 split-default routes found ($SPLIT_V6)" >&2
    exit 1
fi
echo "PASS: no split-default routes accumulated"
# Counts are not a zero-mutation proof; run the Leshy/netlink event gate separately.
echo "INFO: route-count soak is a bounded drift check, not netlink/DNS zero-write acceptance"

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
echo "============================================================"
echo "INSTALLED VM DUAL-STACK REGRESSION PASSED"
echo "Evidence: $OUT_DIR"
echo "============================================================"