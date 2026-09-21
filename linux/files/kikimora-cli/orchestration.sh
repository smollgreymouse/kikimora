# Go orchestration ownership and cutover commands.
#
# This file is sourced by the kikimora entrypoint. Legacy files and units stay
# installed until a separate post-parity retirement step.

readonly ORCH_OWNERSHIP_CONFIG="${KIKIMORA_ORCHESTRATION_OWNERSHIP_CONFIG:-/etc/kikimora/leshy/orchestration-ownership.conf}"
readonly ORCH_CONFIG_DIR="${KIKIMORA_ORCHESTRATION_CONFIG_DIR:-/etc/kikimora/toads}"
readonly ORCH_CORE_UNIT="${KIKIMORA_ORCHESTRATION_CORE_UNIT:-kikimora-core.service}"
readonly ORCH_CORE_BIN="${KIKIMORA_ORCHESTRATION_CORE_BIN:-/usr/local/bin/kikimora-core}"
readonly ORCH_TOAD_BIN="${KIKIMORA_ORCHESTRATION_TOAD_BIN:-/usr/local/bin/kikimora-toad}"
readonly ORCH_CORE_SOCKET="${KIKIMORA_ORCHESTRATION_CORE_SOCKET:-/run/kikimora/core.sock}"
readonly ORCH_READY_TIMEOUT="${KIKIMORA_ORCHESTRATION_READY_TIMEOUT:-60}"
readonly ORCH_SYSTEMCTL="${KIKIMORA_ORCHESTRATION_SYSTEMCTL:-systemctl}"
readonly ORCH_NM_CONF="${KIKIMORA_ORCHESTRATION_NM_CONF:-/etc/NetworkManager/conf.d/90-kikimora-unmanaged.conf}"
readonly ORCH_LEGACY_LIBEXEC="${KIKIMORA_ORCHESTRATION_LEGACY_LIBEXEC:-/usr/local/libexec/kikimora/leshy}"
readonly ORCH_LEGACY_UNIT_DIR="${KIKIMORA_ORCHESTRATION_LEGACY_UNIT_DIR:-/etc/systemd/system}"
readonly ORCH_LEGACY_DROPIN_DIR="${KIKIMORA_ORCHESTRATION_LEGACY_DROPIN_DIR:-/etc/systemd/system/leshy.service.d}"
readonly ORCH_LEGACY_WRITER_UNITS=(leshy-route-watch.service)
readonly ORCH_LEGACY_UNITS=(leshy.service leshy-route-watch.service leshy-health-watch.service)

orch_require_root() {
  if [[ ${EUID} -eq 0 ]]; then
    return 0
  fi
  [[ ${KIKIMORA_ORCHESTRATION_TEST_MODE:-0} == 1 &&
     "$ORCH_OWNERSHIP_CONFIG" == /tmp/* &&
     "$ORCH_CONFIG_DIR" == /tmp/* &&
     "$ORCH_CORE_BIN" == /tmp/* &&
     "$ORCH_TOAD_BIN" == /tmp/* &&
     "$ORCH_SYSTEMCTL" == /tmp/* ]] || die "run the command via sudo"
}

orch_read_owner() {
  local key="$1"
  [[ -r "$ORCH_OWNERSHIP_CONFIG" ]] || return 1
  awk -v key="$key" '$1 == key && $2 == "=" {gsub(/["[:space:]]/, "", $3); print $3; exit}' "$ORCH_OWNERSHIP_CONFIG"
}

orch_write_owner() {
  local routing="$1" tunnel="$2" endpoint="$3" tmp
  tmp="$(mktemp "${ORCH_OWNERSHIP_CONFIG}.tmp.XXXXXX")"
  cat >"$tmp" <<EOF_OWNERSHIP
# Single-writer migration guard. Valid values: legacy, external, or go.
routing_owner = "$routing"
tunnel_owner = "$tunnel"
endpoint_owner = "$endpoint"
EOF_OWNERSHIP
  chmod 0644 "$tmp"
  mv -f -- "$tmp" "$ORCH_OWNERSHIP_CONFIG"
}

orch_legacy_stop() { "${ORCH_SYSTEMCTL}" stop "${ORCH_LEGACY_WRITER_UNITS[@]}"; }
orch_legacy_disable() { "${ORCH_SYSTEMCTL}" disable "${ORCH_LEGACY_WRITER_UNITS[@]}"; }
orch_legacy_start() { "${ORCH_SYSTEMCTL}" start "${ORCH_LEGACY_UNITS[@]}"; }
orch_core_active() { "${ORCH_SYSTEMCTL}" is-active --quiet "$ORCH_CORE_UNIT"; }
orch_core_disable() { "${ORCH_SYSTEMCTL}" disable "$ORCH_CORE_UNIT" || true; }
orch_core_stop() { "${ORCH_SYSTEMCTL}" stop "$ORCH_CORE_UNIT" || true; }
orch_go_owns_all() {
  [[ "$(orch_read_owner routing_owner || true)" == go &&
     "$(orch_read_owner tunnel_owner || true)" == go &&
     "$(orch_read_owner endpoint_owner || true)" == go ]]
}

orch_core_service_action() {
  local action="$1"
  shift || true
  case "$action" in
    start|stop|restart)
      [[ $# -eq 0 ]] || die "usage: sudo kk $action"
      "${ORCH_SYSTEMCTL}" "$action" "$ORCH_CORE_UNIT"
      ;;
    enable|disable)
      "${ORCH_SYSTEMCTL}" "$action" "$@" "$ORCH_CORE_UNIT"
      ;;
    *)
      return 64
      ;;
  esac
}

orch_preflight() {
  local config routing tunnel endpoint
  [[ -x "$ORCH_CORE_BIN" ]] || die "Go core is not executable: $ORCH_CORE_BIN"
  [[ -x "$ORCH_TOAD_BIN" ]] || die "Go Toad is not executable: $ORCH_TOAD_BIN"
  [[ -r "$ORCH_OWNERSHIP_CONFIG" ]] || die "ownership config not found: $ORCH_OWNERSHIP_CONFIG"
  [[ -d "$ORCH_CONFIG_DIR" ]] || die "Go Toad config directory not found: $ORCH_CONFIG_DIR"
  compgen -G "$ORCH_CONFIG_DIR/*.toml" >/dev/null || die "no Go Toad TOML configs found in $ORCH_CONFIG_DIR"

  for config in "$ORCH_CONFIG_DIR"/*.toml; do
    "$ORCH_TOAD_BIN" validate -config "$config" >/dev/null ||
      die "invalid Go Toad config: $config"
  done

  routing="$(orch_read_owner routing_owner || true)"
  tunnel="$(orch_read_owner tunnel_owner || true)"
  endpoint="$(orch_read_owner endpoint_owner || true)"
  if [[ "$routing/$tunnel/$endpoint" != "legacy/external/legacy" &&
        "$routing/$tunnel/$endpoint" != "go/go/go" ]]; then
    die "unsupported mixed orchestration ownership: routing=$routing tunnel=$tunnel endpoint=$endpoint"
  fi

  if "${ORCH_SYSTEMCTL}" is-active --quiet NetworkManager.service 2>/dev/null &&
     [[ ! -r "$ORCH_NM_CONF" ]]; then
    die "NetworkManager is active but Kikimora unmanaged-device config is missing: $ORCH_NM_CONF"
  fi
}

orch_status() {
  local routing tunnel endpoint
  routing="$(orch_read_owner routing_owner || printf unknown)"
  tunnel="$(orch_read_owner tunnel_owner || printf unknown)"
  endpoint="$(orch_read_owner endpoint_owner || printf unknown)"
  printf 'ownership: routing=%s tunnel=%s endpoint=%s\n' "$routing" "$tunnel" "$endpoint"
  printf '  %-32s %s\n' "$ORCH_CORE_UNIT" "$("${ORCH_SYSTEMCTL}" is-active "$ORCH_CORE_UNIT" 2>/dev/null || true)"
  printf '  %-32s %s\n' "leshy-route-watch.service" "$("${ORCH_SYSTEMCTL}" is-active leshy-route-watch.service 2>/dev/null || true)"
  if [[ "$routing" == go && "$tunnel" == go && "$endpoint" == go ]]; then
    printf 'cutover: go\n'
  else
    printf 'cutover: legacy\n'
  fi
}

orch_preflight_report() {
  orch_require_root
  orch_preflight
  orch_status
  printf 'configs:\n'
  local config
  for config in "$ORCH_CONFIG_DIR"/*.toml; do
    printf '  %s\n' "$config"
  done
  printf 'networkmanager_unmanaged_config: %s\n' "$([[ -r "$ORCH_NM_CONF" ]] && printf present || printf absent)"
  if "$ORCH_CORE_BIN" status --socket "$ORCH_CORE_SOCKET" --json >/dev/null 2>&1; then
    printf 'core_api: reachable\n'
  else
    printf 'core_api: unavailable\n'
  fi
}

orch_core_json() {
  "$ORCH_CORE_BIN" status --socket "$ORCH_CORE_SOCKET" --json
}

orch_wait_api() {
  local deadline=$((SECONDS + ORCH_READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    if orch_core_json >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

orch_json_ready() {
  python3 -c '
import json, sys
s = json.load(sys.stdin)
roles = s.get("roles") or []
desired = [r for r in roles if r.get("desired_enabled")]
if not desired:
    raise SystemExit(0 if s.get("aggregate_state") == "Stopped" else 1)
u = s.get("underlay") or {}
epoch = int(u.get("epoch") or 0)
if epoch <= 0 or not (u.get("ipv4") or u.get("ipv6")):
    raise SystemExit(1)
if s.get("aggregate_state") != "Ready":
    raise SystemExit(1)
for r in desired:
    if r.get("state") != "Ready" or not r.get("route_ready"):
        raise SystemExit(1)
    if int(r.get("validated_underlay_epoch") or 0) != epoch:
        raise SystemExit(1)
    if (r.get("parking") or {}).get("active"):
        raise SystemExit(1)
    ep = r.get("endpoint") or {}
    if ep.get("state") != "ready" or int(ep.get("applied_underlay_epoch") or 0) != epoch:
        raise SystemExit(1)
    pub = r.get("publication") or {}
    if not pub.get("published"):
        raise SystemExit(1)
raise SystemExit(0)
'
}

orch_wait_ready() {
  local deadline=$((SECONDS + ORCH_READY_TIMEOUT)) snapshot
  while (( SECONDS <= deadline )); do
    if snapshot="$(orch_core_json 2>/dev/null)" && printf '%s\n' "$snapshot" | orch_json_ready; then
      return 0
    fi
    sleep 1
  done
  return 1
}

orch_stop_go_roles() {
  if orch_core_json >/dev/null 2>&1; then
    "$ORCH_CORE_BIN" stop --socket "$ORCH_CORE_SOCKET" >/dev/null 2>&1 || true
  fi
}

orch_restore_legacy() {
  local routing="$1" tunnel="$2" endpoint="$3"
  orch_stop_go_roles
  orch_core_disable
  orch_core_stop
  orch_write_owner "$routing" "$tunnel" "$endpoint"
  "${ORCH_SYSTEMCTL}" daemon-reload || true
  "${ORCH_SYSTEMCTL}" enable "${ORCH_LEGACY_UNITS[@]}" || true
  orch_legacy_start || true
}

orch_cutover_go() {
  local old_routing old_tunnel old_endpoint mutated=0
  orch_require_root
  orch_preflight
  old_routing="$(orch_read_owner routing_owner || printf legacy)"
  old_tunnel="$(orch_read_owner tunnel_owner || printf external)"
  old_endpoint="$(orch_read_owner endpoint_owner || printf legacy)"

  if orch_go_owns_all; then
    orch_legacy_stop || return 1
    orch_legacy_disable || return 1
    "${ORCH_SYSTEMCTL}" enable "$ORCH_CORE_UNIT" || return 1
    "${ORCH_SYSTEMCTL}" start "$ORCH_CORE_UNIT" || return 1
    orch_core_active || return 1
    orch_wait_api || die "Go core service is active but API is unavailable"
    # Do not ConnectAll here: an already-Go installation may intentionally
    # have a subset of roles disabled in persisted desired state.
    orch_wait_ready || die "Go core API did not converge to its persisted desired state"
    printf 'Go orchestration is already active.\n'
    return 0
  fi

  # Stop writers before publishing Go ownership. From this point every failure
  # is routed through one rollback helper.
  if ! orch_legacy_stop || ! orch_legacy_disable; then
    return 1
  fi
  mutated=1
  orch_write_owner go go go
  if ! "${ORCH_SYSTEMCTL}" daemon-reload ||
     ! "${ORCH_SYSTEMCTL}" enable "$ORCH_CORE_UNIT" ||
     ! "${ORCH_SYSTEMCTL}" start "$ORCH_CORE_UNIT" ||
     ! orch_core_active ||
     ! orch_wait_api ||
     ! "$ORCH_CORE_BIN" start --socket "$ORCH_CORE_SOCKET" >/dev/null ||
     ! orch_wait_ready; then
    printf 'Go orchestration did not reach Ready; restoring previous ownership.\n' >&2
    if (( mutated )); then
      orch_restore_legacy "$old_routing" "$old_tunnel" "$old_endpoint"
    fi
    return 1
  fi
  printf 'Go orchestration cutover complete. Desired roles are Ready and legacy writer units are disabled.\n'
}

orch_rollback() {
  orch_require_root
  [[ -x "${ORCH_LEGACY_LIBEXEC}/reconcile" && -x "${ORCH_LEGACY_LIBEXEC}/route-lifecycle" &&
     -x "${ORCH_LEGACY_LIBEXEC}/route-watch" && -e "${ORCH_LEGACY_UNIT_DIR}/leshy-route-watch.service" ]] ||
    die 'legacy route writers were retired; reinstall the compatibility package before rollback'
  orch_stop_go_roles
  orch_core_disable
  orch_core_stop
  orch_write_owner legacy external legacy
  "${ORCH_SYSTEMCTL}" daemon-reload
  "${ORCH_SYSTEMCTL}" enable "${ORCH_LEGACY_UNITS[@]}"
  "${ORCH_SYSTEMCTL}" start "${ORCH_LEGACY_UNITS[@]}"
  "${ORCH_SYSTEMCTL}" is-active --quiet leshy-route-watch.service ||
    die 'legacy route writer did not become active after rollback'
  printf 'Orchestration ownership rolled back to legacy.\n'
}

orch_retire_legacy() {
  orch_require_root
  orch_go_owns_all || die 'legacy retirement requires routing_owner=go, tunnel_owner=go and endpoint_owner=go'
  orch_core_active || die 'Go core must be active before legacy retirement'
  orch_wait_api || die 'Go core API must be reachable before legacy retirement'
  orch_wait_ready || die 'Go desired roles must be Ready before legacy retirement'

  local unit
  for unit in "${ORCH_LEGACY_WRITER_UNITS[@]}"; do
    if "${ORCH_SYSTEMCTL}" is-active --quiet "$unit"; then
      die "legacy unit is still active: $unit"
    fi
    if "${ORCH_SYSTEMCTL}" is-enabled --quiet "$unit"; then
      die "legacy unit is still enabled: $unit"
    fi
  done

  rm -f -- \
    "${ORCH_LEGACY_LIBEXEC}/reconcile" \
    "${ORCH_LEGACY_LIBEXEC}/route-watch" \
    "${ORCH_LEGACY_LIBEXEC}/route-lifecycle" \
    "${ORCH_LEGACY_UNIT_DIR}/leshy-route-watch.service" \
    "${ORCH_LEGACY_DROPIN_DIR}/route-cleanup.conf"
  "${ORCH_SYSTEMCTL}" daemon-reload
  printf 'Legacy route writers retired. DNS health watcher remains installed.\n'
}

cmd_orchestration() {
  local action="${1:-status}"
  shift || true
  case "$action" in
    status)
      [[ $# -eq 0 ]] || die 'usage: kk orchestration status'
      orch_status
      ;;
    preflight)
      [[ $# -eq 0 ]] || die 'usage: sudo kk orchestration preflight'
      orch_preflight_report
      ;;
    cutover)
      [[ "${1:-}" == --go && $# -eq 1 ]] || die 'usage: sudo kk orchestration cutover --go'
      orch_cutover_go
      ;;
    rollback)
      [[ $# -eq 0 ]] || die 'usage: sudo kk orchestration rollback'
      orch_rollback
      ;;
    retire-legacy)
      [[ "${1:-}" == --confirm && $# -eq 1 ]] || die 'usage: sudo kk orchestration retire-legacy --confirm'
      orch_retire_legacy
      ;;
    *) die "unknown orchestration action: $action (use status, preflight, cutover --go, rollback or retire-legacy --confirm)" ;;
  esac
}
