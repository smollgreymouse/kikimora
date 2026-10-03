#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
# Installed-console lifecycle acceptance for the disposable Ubuntu VM.
set -Eeuo pipefail

KK="${KIKIMORA_VM_KK:-/usr/local/bin/kk-next}"
UNIT="${KIKIMORA_VM_CORE_UNIT:-kikimora-core-next.service}"
OUT="${KIKIMORA_VM_OUT:-$HOME/kikimora-lab/08a2/runtime}"
READY_TIMEOUT="${KIKIMORA_VM_READY_TIMEOUT:-90}"
ROLES=(awg oc)

mkdir -p "$OUT"

snapshot() {
  "$KK" status --json
}

role_field() {
  local json="$1" role="$2" expr="$3"
  python3 - "$json" "$role" "$expr" <<'PY'
import json,sys
path, role, expr = sys.argv[1:]
s=json.load(open(path,encoding="utf-8"))
for r in s.get("roles",[]):
    if r.get("id")==role:
        print(eval(expr, {"__builtins__": {}}, {"r": r, "s": s}))
        raise SystemExit(0)
raise SystemExit(2)
PY
}

wait_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT)) json
  while (( SECONDS <= deadline )); do
    if json="$(snapshot 2>/dev/null)"; then
      printf '%s\n' "$json" >"$OUT/.wait-status.json"
      if python3 - "$OUT/.wait-status.json" "${ROLES[@]}" <<'PY'
import json,sys
roles=set(sys.argv[2:])
try:
    s=json.load(open(sys.argv[1],encoding="utf-8"))
except Exception:
    raise SystemExit(1)
epoch=int((s.get("underlay") or {}).get("epoch") or 0)
if epoch <= 0 or s.get("aggregate_state") != "Ready":
    raise SystemExit(1)
seen=set()
for r in s.get("roles",[]):
    if r.get("id") not in roles:
        continue
    seen.add(r["id"])
    ep=r.get("endpoint") or {}
    park=r.get("parking") or {}
    pub=r.get("publication") or {}
    if not r.get("desired_enabled"):
        raise SystemExit(1)
    if r.get("state") != "Ready" or not r.get("route_ready"):
        raise SystemExit(1)
    if int(r.get("validated_underlay_epoch") or 0) != epoch:
        raise SystemExit(1)
    if ep.get("state") != "ready" or int(ep.get("applied_underlay_epoch") or 0) != epoch:
        raise SystemExit(1)
    if park.get("active"):
        raise SystemExit(1)
    if not pub.get("published"):
        raise SystemExit(1)
if seen != roles:
    raise SystemExit(1)
PY
      then
        rm -f "$OUT/.wait-status.json"
        printf '%s\n' "$json"
        return 0
      fi
    fi
    sleep 1
  done
  echo "ERROR: roles did not reach Ready/current epoch" >&2
  snapshot >&2 || true
  return 1
}

record_state() {
  local name="$1"
  local dir="$OUT/$name"
  mkdir -p "$dir"
  snapshot >"$dir/status.json"
  ip -br link >"$dir/links.txt"
  ip -br addr >"$dir/addresses.txt"
  ip -4 rule show >"$dir/rules4.txt"
  ip -6 rule show >"$dir/rules6.txt"
  ip -4 route show table all >"$dir/routes4.txt"
  ip -6 route show table all >"$dir/routes6.txt"
  systemctl is-active "$UNIT" >"$dir/core-active.txt" || true
  systemctl show "$UNIT" -p MainPID -p ActiveState -p SubState >"$dir/core-unit.txt"
}

connect_roles() {
  "$KK" connect --role awg >/dev/null
  "$KK" connect --role oc >/dev/null
  wait_ready >"$OUT/ready.json"
  record_state baseline-ready
  echo "installed-console connect: PASS"
}

assert_ready() {
  wait_ready >"$OUT/ready-latest.json"
  echo "installed-console Ready/current-epoch: PASS"
}

core_restart() {
  local before="$OUT/core-restart-before.json" after="$OUT/core-restart-after.json"
  snapshot >"$before"
  timeout 120 "$KK" watch --json >"$OUT/core-restart-watch.jsonl" 2>"$OUT/core-restart-watch.err" &
  local watch_pid=$!
  sleep 1
  sudo systemctl restart "$UNIT"
  wait_ready >"$after"
  sleep 1
  kill "$watch_pid" 2>/dev/null || true
  wait "$watch_pid" 2>/dev/null || true

  python3 - "$before" "$after" <<'PY'
import json,sys
before=json.load(open(sys.argv[1])); after=json.load(open(sys.argv[2]))
bd={r["id"]:r for r in before["roles"]}; ad={r["id"]:r for r in after["roles"]}
for role in ("awg","oc"):
    assert bd[role]["desired_enabled"] is True
    assert ad[role]["desired_enabled"] is True
    assert ad[role]["state"]=="Ready" and ad[role]["route_ready"] is True
PY
  grep -Fq 'watch disconnected:' "$OUT/core-restart-watch.err"
  python3 - "$OUT/core-restart-watch.jsonl" <<'PY'
import json,sys
lines=[line for line in open(sys.argv[1],encoding="utf-8") if line.strip()]
assert len(lines) >= 2, len(lines)
for line in lines:
    json.loads(line)
PY
  record_state after-core-restart
  echo "installed-console core restart + watch reconnect: PASS"
}

kill_role() {
  local role="${1:?usage: kill-role ROLE}" other before after pid gen
  case "$role" in awg) other=oc ;; oc) other=awg ;; *) echo "invalid role: $role" >&2; exit 64 ;; esac
  before="$OUT/kill-$role-before.json"
  after="$OUT/kill-$role-after.json"
  snapshot >"$before"
  pid="$(role_field "$before" "$role" 'r.get("pid") or 0')"
  gen="$(role_field "$before" "$role" 'r.get("generation") or 0')"
  [[ "$pid" -gt 1 && "$gen" -gt 0 ]] || { echo "missing pid/generation for $role" >&2; exit 1; }
  sudo kill -KILL "$pid"

  local deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    if wait_ready >"$after" 2>/dev/null; then
      local new_pid new_gen
      new_pid="$(role_field "$after" "$role" 'r.get("pid") or 0')"
      new_gen="$(role_field "$after" "$role" 'r.get("generation") or 0')"
      if [[ "$new_pid" -ne "$pid" && "$new_gen" -ne "$gen" ]]; then
        break
      fi
    fi
    sleep 1
  done

  python3 - "$before" "$after" "$role" "$other" <<'PY'
import json,sys
before=json.load(open(sys.argv[1])); after=json.load(open(sys.argv[2]))
role,other=sys.argv[3:]
bd={r["id"]:r for r in before["roles"]}; ad={r["id"]:r for r in after["roles"]}
assert ad[role]["pid"] != bd[role]["pid"]
assert ad[role]["generation"] != bd[role]["generation"]
assert ad[role]["state"]=="Ready"
assert ad[other]["pid"] == bd[other]["pid"], (bd[other]["pid"],ad[other]["pid"])
assert ad[other]["generation"] == bd[other]["generation"]
assert ad[other]["interface"]["ifindex"] == bd[other]["interface"]["ifindex"]
PY
  record_state "after-kill-$role"
  echo "installed-console kill/recover $role: PASS"
}

nm_restart() {
  local before="$OUT/nm-restart-before.json" after="$OUT/nm-restart-after.json"
  snapshot >"$before"
  sudo systemctl restart NetworkManager.service
  wait_ready >"$after"
  python3 - "$before" "$after" <<'PY'
import json,sys
before=json.load(open(sys.argv[1])); after=json.load(open(sys.argv[2]))
bd={r["id"]:r for r in before["roles"]}; ad={r["id"]:r for r in after["roles"]}
for role in ("awg","oc"):
    assert ad[role]["state"]=="Ready"
    assert ad[role]["interface"]["ifindex"] == bd[role]["interface"]["ifindex"]
obs=after.get("observers") or {}
assert obs.get("networkmanager_healthy") is True, obs
PY
  record_state after-networkmanager-restart
  echo "installed-console NetworkManager restart: PASS"
}

case "${1:-}" in
  connect) connect_roles ;;
  assert-ready) assert_ready ;;
  snapshot) record_state "${2:-manual}" ;;
  core-restart) core_restart ;;
  kill-role) shift; kill_role "$@" ;;
  nm-restart) nm_restart ;;
  *)
    echo "usage: $0 <connect|assert-ready|snapshot [NAME]|core-restart|kill-role ROLE|nm-restart>" >&2
    exit 64
    ;;
esac
