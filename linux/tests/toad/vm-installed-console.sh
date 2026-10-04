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



core_kill() {
  local before="$OUT/core-kill-before.json" after="$OUT/core-kill-after.json" core_pid
  snapshot >"$before"
  core_pid="$(systemctl show "$UNIT" -p MainPID --value)"
  [[ "$core_pid" =~ ^[1-9][0-9]*$ ]] || { echo "invalid core pid: $core_pid" >&2; return 1; }

  timeout 120 "$KK" watch --json >"$OUT/core-kill-watch.jsonl" 2>"$OUT/core-kill-watch.err" &
  local watch_pid=$!
  sleep 1
  sudo kill -KILL "$core_pid"

  local deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    local new_pid
    new_pid="$(systemctl show "$UNIT" -p MainPID --value 2>/dev/null || true)"
    if [[ "$new_pid" =~ ^[1-9][0-9]*$ && "$new_pid" != "$core_pid" ]] && wait_ready >"$after" 2>/dev/null; then
      break
    fi
    sleep 1
  done

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
  grep -Fq 'watch disconnected:' "$OUT/core-kill-watch.err"
  record_state after-core-kill
  echo "installed-console hard core death/recovery: PASS"
}

physical_link_cycle() {
  local before="$OUT/link-cycle-before.json" during="$OUT/link-cycle-during.json" after="$OUT/link-cycle-after.json"
  local iface old_epoch
  snapshot >"$before"
  iface="$(python3 - "$before" <<'PY'
import json,sys
s=json.load(open(sys.argv[1]))
u=s.get("underlay") or {}
p=u.get("ipv4") or u.get("ipv6") or {}
print(p.get("interface") or "")
PY
)"
  old_epoch="$(python3 - "$before" <<'PY'
import json,sys
s=json.load(open(sys.argv[1]))
print(int((s.get("underlay") or {}).get("epoch") or 0))
PY
)"
  [[ "$iface" =~ ^[A-Za-z0-9_.:-]+$ && "$old_epoch" -gt 0 ]] || {
    echo "invalid underlay before link cycle: iface=$iface epoch=$old_epoch" >&2
    return 1
  }

  sudo ip link set dev "$iface" down
  sleep 4
  snapshot >"$during" || true
  sudo ip link set dev "$iface" up

  local deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    if wait_ready >"$after" 2>/dev/null; then
      local new_epoch
      new_epoch="$(python3 - "$after" <<'PY'
import json,sys
s=json.load(open(sys.argv[1]))
print(int((s.get("underlay") or {}).get("epoch") or 0))
PY
)"
      if (( new_epoch > old_epoch )); then
        break
      fi
    fi
    sleep 1
  done

  python3 - "$before" "$after" "$old_epoch" <<'PY'
import json,sys
before=json.load(open(sys.argv[1])); after=json.load(open(sys.argv[2])); old=int(sys.argv[3])
bd={r["id"]:r for r in before["roles"]}; ad={r["id"]:r for r in after["roles"]}
assert int((after.get("underlay") or {}).get("epoch") or 0) > old
for role in ("awg","oc"):
    assert bd[role]["desired_enabled"] is True
    assert ad[role]["desired_enabled"] is True
    assert ad[role]["state"]=="Ready" and ad[role]["route_ready"] is True
PY
  record_state after-link-cycle
  echo "installed-console physical link down/up recovery: PASS"
}

assert_no_accumulation() {
  local label="${1:-state}"
  local core_count toad_count awg_links oc_links dup4 dup6
  core_count="$(ps -eo comm= | awk '$1=="kikimora-core" {n++} END {print n+0}')"
  toad_count="$(ps -eo comm= | awk '$1=="kikimora-toad" {n++} END {print n+0}')"
  awg_links="$(ip -o link show | awk -F': ' '$2 ~ /^kk-awg0(@|$)/ {n++} END{print n+0}')"
  oc_links="$(ip -o link show | awk -F': ' '$2 ~ /^kk-oc0(@|$)/ {n++} END{print n+0}')"
  dup4="$(ip -4 route show table all | grep ' dev kk-' | sort | uniq -d || true)"
  dup6="$(ip -6 route show table all | grep ' dev kk-' | sort | uniq -d || true)"

  [[ "$core_count" -eq 1 ]] || { echo "$label: core process count=$core_count" >&2; return 1; }
  [[ "$toad_count" -eq 2 ]] || { echo "$label: Toad process count=$toad_count" >&2; return 1; }
  [[ "$awg_links" -eq 1 ]] || { echo "$label: kk-awg0 link count=$awg_links" >&2; return 1; }
  [[ "$oc_links" -eq 1 ]] || { echo "$label: kk-oc0 link count=$oc_links" >&2; return 1; }
  [[ -z "$dup4" ]] || { echo "$label: duplicate IPv4 managed routes:$dup4" >&2; return 1; }
  [[ -z "$dup6" ]] || { echo "$label: duplicate IPv6 managed routes:$dup6" >&2; return 1; }

  if [[ "$KK" == "/usr/local/bin/kk" ]]; then
    local staging_refs
    staging_refs="$(grep -H '/kikimora-next/' /etc/kikimora/toads/*.toml 2>/dev/null || true)"
    [[ -z "$staging_refs" ]] || {
      echo "$label: canonical profiles still reference staging paths:$staging_refs" >&2
      return 1
    }
  fi
}

bounded_soak() {
  local iterations="${1:-2}"
  [[ "$iterations" =~ ^[1-9][0-9]*$ && "$iterations" -le 10 ]] || {
    echo "invalid soak iteration count: $iterations" >&2
    return 64
  }
  local i
  for i in $(seq 1 "$iterations"); do
    echo "=== soak iteration $i/$iterations ==="
    core_restart
    assert_no_accumulation "after core restart $i"
    kill_role awg
    assert_no_accumulation "after AWG recovery $i"
    kill_role oc
    assert_no_accumulation "after OpenConnect recovery $i"
    nm_restart
    assert_no_accumulation "after NetworkManager restart $i"
    physical_link_cycle
    assert_no_accumulation "after physical link cycle $i"
    probe_apps
  done
  record_state after-bounded-soak
  echo "installed-console bounded lifecycle soak: PASS"
}

suspend_resume() {
  local before="$OUT/suspend-before.json" after="$OUT/suspend-after.json"
  snapshot >"$before"

  # This process continues only after the guest has actually resumed. Waking a
  # VirtualBox guest is intentionally a host-side responsibility; virtual RTC
  # alarms are not a reliable hypervisor resume mechanism.
  sudo systemctl suspend
  wait_ready >"$after"

  python3 - "$before" "$after" <<'PY'
import json,sys
before=json.load(open(sys.argv[1])); after=json.load(open(sys.argv[2]))
bd={r["id"]:r for r in before["roles"]}; ad={r["id"]:r for r in after["roles"]}
for role in ("awg","oc"):
    assert bd[role]["desired_enabled"] is True
    assert ad[role]["desired_enabled"] is True
    assert ad[role]["state"]=="Ready" and ad[role]["route_ready"] is True
obs=after.get("observers") or {}
assert obs.get("sleep_healthy") is True, obs
PY
  record_state after-suspend-resume
  echo "installed-console OS suspend/resume recovery: PASS"
}

prepare_reboot() {
  snapshot >"$OUT/reboot-before.json"
  cat /proc/sys/kernel/random/boot_id >"$OUT/reboot-before.boot-id"
  sync
  echo "installed-console reboot prepared; requesting guest reboot"
  sudo systemctl reboot
}

assert_after_reboot() {
  local before="$OUT/reboot-before.json" after="$OUT/reboot-after.json"
  [[ -s "$before" && -s "$OUT/reboot-before.boot-id" ]] || {
    echo "reboot pre-state is missing" >&2
    return 1
  }
  local old_boot new_boot
  old_boot="$(cat "$OUT/reboot-before.boot-id")"
  new_boot="$(cat /proc/sys/kernel/random/boot_id)"
  [[ "$old_boot" != "$new_boot" ]] || {
    echo "boot id did not change across reboot" >&2
    return 1
  }
  wait_ready >"$after"
  python3 - "$before" "$after" <<'PY'
import json,sys
before=json.load(open(sys.argv[1])); after=json.load(open(sys.argv[2]))
bd={r["id"]:r for r in before["roles"]}; ad={r["id"]:r for r in after["roles"]}
for role in ("awg","oc"):
    assert bd[role]["desired_enabled"] is True
    assert ad[role]["desired_enabled"] is True
    assert ad[role]["state"]=="Ready" and ad[role]["route_ready"] is True
obs=after.get("observers") or {}
assert all(obs.get(k) is True for k in (
    "netlink_healthy","underlay_converger_healthy","sleep_healthy","networkmanager_healthy"
)), obs
PY
  record_state after-reboot
  echo "installed-console reboot desired-state recovery: PASS"
}

prepare_poweroff() {
  snapshot >"$OUT/cold-boot-before.json"
  cat /proc/sys/kernel/random/boot_id >"$OUT/cold-boot-before.boot-id"
  sync
  echo "installed-console cold boot prepared; requesting guest poweroff"
  sudo systemctl poweroff
}

assert_after_cold_boot() {
  local before="$OUT/cold-boot-before.json" after="$OUT/cold-boot-after.json"
  [[ -s "$before" && -s "$OUT/cold-boot-before.boot-id" ]] || {
    echo "cold-boot pre-state is missing" >&2
    return 1
  }
  local old_boot new_boot
  old_boot="$(cat "$OUT/cold-boot-before.boot-id")"
  new_boot="$(cat /proc/sys/kernel/random/boot_id)"
  [[ "$old_boot" != "$new_boot" ]] || {
    echo "boot id did not change across cold boot" >&2
    return 1
  }
  wait_ready >"$after"
  python3 - "$before" "$after" <<'PY'
import json,sys
before=json.load(open(sys.argv[1])); after=json.load(open(sys.argv[2]))
bd={r["id"]:r for r in before["roles"]}; ad={r["id"]:r for r in after["roles"]}
for role in ("awg","oc"):
    assert bd[role]["desired_enabled"] is True
    assert ad[role]["desired_enabled"] is True
    assert ad[role]["state"]=="Ready" and ad[role]["route_ready"] is True
obs=after.get("observers") or {}
assert all(obs.get(k) is True for k in (
    "netlink_healthy","underlay_converger_healthy","sleep_healthy","networkmanager_healthy"
)), obs
PY
  record_state after-cold-boot
  echo "installed-console cold-boot desired-state recovery: PASS"
}


prepare_hypervisor_freeze() {
  local kind="${1:-}"
  [[ "$kind" == "pause" || "$kind" == "savestate" ]] || {
    echo "usage: $0 prepare-hypervisor-freeze <pause|savestate>" >&2
    return 64
  }
  snapshot >"$OUT/$kind-before.json"
  cat /proc/sys/kernel/random/boot_id >"$OUT/$kind-before.boot-id"
  date -Is >"$OUT/$kind-before.time"
  sync
  echo "installed-console $kind pre-state captured; perform the VirtualBox host action now"
}

assert_after_hypervisor_resume() {
  local kind="${1:-}"
  [[ "$kind" == "pause" || "$kind" == "savestate" ]] || {
    echo "usage: $0 assert-after-hypervisor-resume <pause|savestate>" >&2
    return 64
  }
  local before="$OUT/$kind-before.json" after="$OUT/$kind-after.json"
  [[ -s "$before" && -s "$OUT/$kind-before.boot-id" ]] || {
    echo "$kind pre-state is missing" >&2
    return 1
  }
  local old_boot new_boot
  old_boot="$(cat "$OUT/$kind-before.boot-id")"
  new_boot="$(cat /proc/sys/kernel/random/boot_id)"
  [[ "$old_boot" == "$new_boot" ]] || {
    echo "boot id changed across $kind/resume" >&2
    return 1
  }
  wait_ready >"$after"
  python3 - "$before" "$after" <<'PY'
import json,sys
before=json.load(open(sys.argv[1])); after=json.load(open(sys.argv[2]))
bd={r["id"]:r for r in before["roles"]}; ad={r["id"]:r for r in after["roles"]}
for role in ("awg","oc"):
    assert bd[role]["desired_enabled"] is True
    assert ad[role]["desired_enabled"] is True
    assert ad[role]["state"]=="Ready" and ad[role]["route_ready"] is True
obs=after.get("observers") or {}
assert all(obs.get(k) is True for k in (
    "netlink_healthy","underlay_converger_healthy","sleep_healthy","networkmanager_healthy"
)), obs
PY
  assert_no_accumulation "after $kind resume"
  record_state "after-$kind-resume"
  echo "installed-console $kind/resume state convergence: PASS"
}

prepare_hard_reset() {
  snapshot >"$OUT/hard-reset-before.json"
  cat /proc/sys/kernel/random/boot_id >"$OUT/hard-reset-before.boot-id"
  date -Is >"$OUT/hard-reset-before.time"
  sync
  echo "installed-console hard-reset pre-state captured; reset the VM from the VirtualBox host now"
}

assert_after_hard_reset() {
  local before="$OUT/hard-reset-before.json" after="$OUT/hard-reset-after.json"
  [[ -s "$before" && -s "$OUT/hard-reset-before.boot-id" ]] || {
    echo "hard-reset pre-state is missing" >&2
    return 1
  }
  local old_boot new_boot
  old_boot="$(cat "$OUT/hard-reset-before.boot-id")"
  new_boot="$(cat /proc/sys/kernel/random/boot_id)"
  [[ "$old_boot" != "$new_boot" ]] || {
    echo "boot id did not change across hard reset" >&2
    return 1
  }
  wait_ready >"$after"
  python3 - "$before" "$after" <<'PY'
import json,sys
before=json.load(open(sys.argv[1])); after=json.load(open(sys.argv[2]))
bd={r["id"]:r for r in before["roles"]}; ad={r["id"]:r for r in after["roles"]}
for role in ("awg","oc"):
    assert bd[role]["desired_enabled"] is True
    assert ad[role]["desired_enabled"] is True
    assert ad[role]["state"]=="Ready" and ad[role]["route_ready"] is True
obs=after.get("observers") or {}
assert all(obs.get(k) is True for k in (
    "netlink_healthy","underlay_converger_healthy","sleep_healthy","networkmanager_healthy"
)), obs
PY
  assert_no_accumulation "after hard reset"
  record_state after-hard-reset
  echo "installed-console hard-reset desired-state recovery: PASS"
}

probe_https_via() {
  local iface="$1" host="$2" path="$3" ip="$4" code_re="$5" label="$6"
  local existing response code remote
  existing="$(ip -4 route show exact "$ip/32" 2>/dev/null || true)"
  if [[ -n "$existing" ]]; then
    if grep -Eq "^$ip dev $iface( scope link)? metric 3$" <<<"$existing"; then
      # A previously interrupted probe may leave exactly our synthetic /32.
      # Remove only that known harness-owned route; never replace any other
      # pre-existing route to the target.
      sudo ip -4 route del "$ip/32" dev "$iface" metric 3
    else
      echo "refusing to replace existing probe route: $existing" >&2
      return 1
    fi
  fi
  sudo ip -4 route add "$ip/32" dev "$iface" metric 3
  trap 'sudo ip -4 route del "'"$ip"'/32" dev "'"$iface"'" metric 3 2>/dev/null || true' RETURN
  response="$(env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY     curl -4sS --noproxy '*' --connect-timeout 10 --max-time 30     --resolve "$host:443:$ip" -o /dev/null     -w 'http_code=%{http_code} size_download=%{size_download} remote_ip=%{remote_ip}'     "https://$host$path")"
  code="$(sed -n 's/.*http_code=\([^ ]*\).*/\1/p' <<<"$response")"
  remote="$(sed -n 's/.*remote_ip=\([^ ]*\).*/\1/p' <<<"$response")"
  [[ "$remote" == "$ip" && "$code" =~ $code_re ]] || {
    echo "$label probe failed: $response" >&2
    return 1
  }
  printf '%s=%s\n' "$label" "$response" | tee -a "$OUT/application-probes.txt"
  sudo ip -4 route del "$ip/32" dev "$iface" metric 3
  trap - RETURN
}

probe_apps() {
  wait_ready >/dev/null
  : >"$OUT/application-probes.txt"

  local ip
  ip="$(getent ahostsv4 telegram.org | awk 'NR==1 {print $1; exit}')"
  [[ -n "$ip" ]]
  probe_https_via kk-awg0 telegram.org / "$ip" '^200$' awg_telegram

  ip="$(getent ahostsv4 chatgpt.com | awk 'NR==1 {print $1; exit}')"
  [[ -n "$ip" ]]
  probe_https_via kk-awg0 chatgpt.com /cdn-cgi/trace "$ip" '^200$' awg_chatgpt

  ip="$(getent ahostsv4 api.openai.com | awk 'NR==1 {print $1; exit}')"
  [[ -n "$ip" ]]
  probe_https_via kk-awg0 api.openai.com /v1/models "$ip" '^401$' awg_openai_api

  local archive traffic
  archive="$(find "$HOME/kikimora-lab/real-vpn" -maxdepth 1 -type f -name 'openconnect-*-diag.tar.gz' -printf '%T@ %p\n' 2>/dev/null | sort -nr | head -1 | cut -d' ' -f2- || true)"
  [[ -n "$archive" ]] || { echo "OpenConnect standalone diagnostic archive not found" >&2; return 1; }
  traffic="$(tar -tzf "$archive" | grep '/traffic-test.txt$' | head -1)"
  ip="$(tar -xOzf "$archive" "$traffic" | sed -n 's/^internal_gitlab=.*remote_ip=\([^ ]*\).*/\1/p' | head -1)"
  [[ -n "$ip" ]] || { echo "internal GitLab IP missing from prior redacted diagnostic" >&2; return 1; }
  probe_https_via kk-oc0 gitlab.sca.ad-tech.ru / "$ip" '^(2|3)[0-9][0-9]$' oc_internal_gitlab

  echo "installed-console real application probes: PASS"
}

case "${1:-}" in
  connect) connect_roles ;;
  assert-ready) assert_ready ;;
  snapshot) record_state "${2:-manual}" ;;
  core-restart) core_restart ;;
  core-kill) core_kill ;;
  kill-role) shift; kill_role "$@" ;;
  nm-restart) nm_restart ;;
  link-cycle) physical_link_cycle ;;
  soak) bounded_soak "${2:-2}" ;;
  suspend-resume) suspend_resume ;;
  prepare-reboot) prepare_reboot ;;
  assert-after-reboot) assert_after_reboot ;;
  prepare-poweroff) prepare_poweroff ;;
  assert-after-cold-boot) assert_after_cold_boot ;;
  prepare-hypervisor-freeze) shift; prepare_hypervisor_freeze "$@" ;;
  assert-after-hypervisor-resume) shift; assert_after_hypervisor_resume "$@" ;;
  prepare-hard-reset) prepare_hard_reset ;;
  assert-after-hard-reset) assert_after_hard_reset ;;
  probe-apps) probe_apps ;;
  *)
    echo "usage: $0 <connect|assert-ready|snapshot [NAME]|core-restart|core-kill|kill-role ROLE|nm-restart|link-cycle|soak [ITERATIONS]|suspend-resume|prepare-reboot|assert-after-reboot|prepare-poweroff|assert-after-cold-boot|prepare-hypervisor-freeze <pause|savestate>|assert-after-hypervisor-resume <pause|savestate>|prepare-hard-reset|assert-after-hard-reset|probe-apps>" >&2
    exit 64
    ;;
esac
