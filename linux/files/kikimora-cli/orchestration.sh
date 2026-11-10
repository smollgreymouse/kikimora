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
# The managed pair (Phase E): the core state directory owns the privileged
# credential, and the shared writer lock fences Leshy against the core.
readonly ORCH_STATE_DIR="${KIKIMORA_ORCHESTRATION_STATE_DIR:-/var/lib/kikimora/core}"
readonly ORCH_AUTH_KEY="${KIKIMORA_ORCHESTRATION_AUTH_KEY:-${ORCH_STATE_DIR}/control-route-auth.key}"
readonly ORCH_LESHY_BIN="${KIKIMORA_ORCHESTRATION_LESHY_BIN:-/usr/local/bin/leshy}"
readonly ORCH_LESHY_CONFIG="${KIKIMORA_ORCHESTRATION_LESHY_CONFIG:-/etc/kikimora/leshy/config.toml}"
readonly ORCH_WRITER_LOCK="${KIKIMORA_ORCHESTRATION_WRITER_LOCK:-/run/kikimora/leshy-writer.lock}"
readonly ORCH_LESHY_UNIT="leshy.service"

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
orch_legacy_writers_present() {
  [[ -x "${ORCH_LEGACY_LIBEXEC}/route-watch" || -e "${ORCH_LEGACY_UNIT_DIR}/leshy-route-watch.service" ]]
}
orch_core_active() { "${ORCH_SYSTEMCTL}" is-active --quiet "$ORCH_CORE_UNIT"; }
orch_core_disable() { "${ORCH_SYSTEMCTL}" disable "$ORCH_CORE_UNIT" || true; }
orch_core_stop() { "${ORCH_SYSTEMCTL}" stop "$ORCH_CORE_UNIT" || true; }
orch_go_owns_all() {
  [[ "$(orch_read_owner routing_owner || true)" == go &&
     "$(orch_read_owner tunnel_owner || true)" == go &&
     "$(orch_read_owner endpoint_owner || true)" == go ]]
}

orch_mode_field() { # orch_mode_field <field>
  "$ORCH_CORE_BIN" leshy-mode status --socket "$ORCH_CORE_SOCKET" \
    --auth-file "$ORCH_AUTH_KEY" --json 2>/dev/null | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    print('')
    raise SystemExit
print(data.get('$1', ''))"
}

orch_writer_lock_free() {
  flock -n "$ORCH_WRITER_LOCK" -c true
}

orch_leshy_caps_ok() {
  local caps
  caps="$("$ORCH_LESHY_BIN" capabilities --json 2>/dev/null || true)"
  local required
  for required in af_unix_control managed_selected_routes_v2 privileged_control_auth_v1; do
    grep -q "\"$required\"" <<<"$caps" || return 1
  done
  return 0
}

# orch_start_managed_pair is the product one-writer cutover (Phase E): start
# the core, wait for its startup readiness, and bring the pair to managed
# mode through the real schema2 transaction when the committed mode is
# Local. An already-managed install only verifies and returns. The marker on
# disk is the authority: nothing here invents a new activation for a restart
# and nothing silently disables a committed managed mode.
orch_start_managed_pair() {
  "${ORCH_SYSTEMCTL}" start "$ORCH_CORE_UNIT" || return $?
  orch_wait_ready || die "kikimora-core did not report startup readiness within ${ORCH_READY_TIMEOUT}s"

  local mode
  mode="$(orch_mode_field mode)"
  case "$mode" in
    managed)
      if ! "${ORCH_SYSTEMCTL}" is-active --quiet "$ORCH_LESHY_UNIT"; then
        die "managed mode is committed but $ORCH_LESHY_UNIT is not active; inspect with: sudo $ORCH_CORE_BIN leshy-mode status --socket $ORCH_CORE_SOCKET --auth-file $ORCH_AUTH_KEY (do NOT start local Leshy while the marker says managed)"
      fi
      local generation fingerprint config_generation config_activation
      generation="$(orch_mode_field generation)"
      fingerprint="$(orch_mode_field activation_id_fingerprint)"
      config_generation="$(grep -E '^kikimora_mode_generation = ' "$ORCH_LESHY_CONFIG" 2>/dev/null | grep -oE '[0-9]+' || true)"
      config_activation="$(grep -E '^kikimora_activation_id = ' "$ORCH_LESHY_CONFIG" 2>/dev/null | sed -E 's/^kikimora_activation_id = "([0-9a-f]{32})"$/\1/' | head -n 1 || true)"
      if [[ -z "$config_generation" || "$config_generation" != "$generation" ]]; then
        die "the generated Leshy config generation ($config_generation) does not match the committed generation ($generation)"
      fi
      if [[ -z "$config_activation" ]]; then
        die "the generated Leshy config carries no activation id"
      fi
      config_fingerprint="$(printf '%s' "$config_activation" | sha256sum | cut -c1-12)"
      if [[ "$config_fingerprint" != "$fingerprint" ]]; then
        die "the generated Leshy config activation fingerprint ($config_fingerprint) does not match the committed fingerprint ($fingerprint)"
      fi
      printf 'managed pair: already managed at generation %s\n' "$generation"
      return 0
      ;;
    local) ;;
    '')
      die "leshymode status is unavailable; the core may not own a state directory"
      ;;
    *)
      die "unknown committed mode: $mode"
      ;;
  esac

  # Local mode: the capability preflight runs BEFORE anything is stopped.
  orch_leshy_caps_ok || die "the installed Leshy does not advertise the managed capabilities (af_unix_control, managed_selected_routes_v2, privileged_control_auth_v1); refusing the cutover"

  if ! "${ORCH_SYSTEMCTL}" is-active --quiet "$ORCH_LESHY_UNIT"; then
    "${ORCH_SYSTEMCTL}" start "$ORCH_LESHY_UNIT" || die "$ORCH_LESHY_UNIT failed to start"
  fi
  "${ORCH_SYSTEMCTL}" stop "$ORCH_LESHY_UNIT" || die "$ORCH_LESHY_UNIT failed to stop gracefully"
  local waited=0
  while ! orch_writer_lock_free; do
    (( waited >= 15 )) && die "the shared writer lock is still held after $ORCH_LESHY_UNIT stopped"
    sleep 1
    waited=$((waited + 1))
  done

  if ! "$ORCH_CORE_BIN" leshy-mode enable --leshy "$ORCH_LESHY_BIN" \
      --socket "$ORCH_CORE_SOCKET" --auth-file "$ORCH_AUTH_KEY" \
      --control-socket "$ORCH_CORE_SOCKET" >"$ORCH_STATE_DIR/pair-enable.log" 2>&1; then
    # Fail closed exactly where the transaction left the disk: an existing
    # marker means the commit (or a mid-transaction crash) happened - the
    # live system stays fail-closed and recovery owns the next step. Without
    # a marker the attempt never committed: local Leshy comes back.
    if [[ -e "${KIKIMORA_ORCHESTRATION_MARKER:-/etc/kikimora/toads/leshy-managed.json}" ]]; then
      die "the enable transaction failed after committing managed state; the system stays fail-closed. Inspect with: sudo $ORCH_CORE_BIN leshy-mode status --socket $ORCH_CORE_SOCKET --auth-file $ORCH_AUTH_KEY (do NOT start local Leshy while the marker says managed)"
    fi
    printf 'the enable transaction failed before committing; restarting local Leshy\n' >&2
    "${ORCH_SYSTEMCTL}" start "$ORCH_LESHY_UNIT" || true
    die "the schema2 enable transaction failed: see $ORCH_STATE_DIR/pair-enable.log"
  fi

  if ! "${ORCH_SYSTEMCTL}" start "$ORCH_LESHY_UNIT"; then
    die "managed Leshy failed to start after the commit; the system stays fail-closed (do NOT start local Leshy while the marker says managed)"
  fi
  local waited2=0
  until "${ORCH_SYSTEMCTL}" is-active --quiet "$ORCH_LESHY_UNIT"; do
    (( waited2 >= 30 )) && die "managed Leshy did not reach active; the system stays fail-closed"
    sleep 1
    waited2=$((waited2 + 1))
  done
  if [[ "$(orch_mode_field mode)" != managed ]]; then
    die "the core does not report managed mode after the transaction"
  fi
  if [[ "$(orch_mode_field transition)" != none && -n "$(orch_mode_field transition)" ]]; then
    die "a mode transition is still in flight after the commit"
  fi
  printf 'managed pair: enabled at generation %s\n' "$(orch_mode_field generation)"
}

orch_core_service_action() {
  local action="$1"
  shift || true
  case "$action" in
    start)
      [[ $# -eq 0 ]] || die "usage: sudo kk start"
      orch_start_managed_pair
      ;;
    stop)
      [[ $# -eq 0 ]] || die "usage: sudo kk stop"
      # Stopping the core stops Leshy with it (the packaged unit dependency);
      # a committed managed mode stays committed for the next start.
      "${ORCH_SYSTEMCTL}" stop "$ORCH_CORE_UNIT"
      ;;
    restart)
      [[ $# -eq 0 ]] || die "usage: sudo kk restart"
      # The committed mode survives the restart: the marker on disk is the
      # authority and no new activation is invented.
      "${ORCH_SYSTEMCTL}" restart "$ORCH_CORE_UNIT"
      ;;
    enable|disable)
      local now=""
      if [[ "${1:-}" == "--now" ]]; then
        now=1
        shift
      fi
      "${ORCH_SYSTEMCTL}" "$action" "$@" "$ORCH_CORE_UNIT" || return $?
      if [[ "$action" == enable && -n "$now" ]]; then
        orch_start_managed_pair
      fi
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

orch_restore_previous_ownership() {
  local routing="$1" tunnel="$2" endpoint="$3" had_legacy="$4"
  orch_core_disable
  orch_core_stop
  orch_write_owner "$routing" "$tunnel" "$endpoint"
  "${ORCH_SYSTEMCTL}" daemon-reload || true
  if (( had_legacy )); then
    "${ORCH_SYSTEMCTL}" enable "${ORCH_LEGACY_UNITS[@]}" || true
    orch_legacy_start || true
  fi
}

orch_cutover_go() {
  local old_routing old_tunnel old_endpoint mutated=0 had_legacy=0
  orch_require_root
  orch_preflight
  old_routing="$(orch_read_owner routing_owner || printf legacy)"
  old_tunnel="$(orch_read_owner tunnel_owner || printf external)"
  old_endpoint="$(orch_read_owner endpoint_owner || printf legacy)"
  if orch_legacy_writers_present; then had_legacy=1; fi

  if orch_go_owns_all; then
    if (( had_legacy )); then
      orch_legacy_stop || return 1
      orch_legacy_disable || return 1
    fi
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

  # On a migration host, stop legacy writers before publishing Go ownership.
  # On a fresh installation there are no legacy writers to stop; the same
  # command becomes the initial activation path for the packaged Go runtime.
  if (( had_legacy )); then
    if ! orch_legacy_stop || ! orch_legacy_disable; then
      return 1
    fi
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
      orch_restore_previous_ownership "$old_routing" "$old_tunnel" "$old_endpoint" "$had_legacy"
    fi
    return 1
  fi
  if (( had_legacy )); then
    printf 'Go orchestration cutover complete. Desired roles are Ready and legacy writer units are disabled.\n'
  else
    printf 'Go orchestration activation complete. Desired roles are Ready.\n'
  fi
}

orch_rollback() {
  orch_require_root
  [[ -x "${ORCH_LEGACY_LIBEXEC}/reconcile" && -x "${ORCH_LEGACY_LIBEXEC}/route-lifecycle" &&
     -x "${ORCH_LEGACY_LIBEXEC}/route-watch" && -e "${ORCH_LEGACY_UNIT_DIR}/leshy-route-watch.service" ]] ||
    die 'legacy route writers were retired; reinstall the compatibility package before rollback'
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


orch_console_require_go() {
  orch_go_owns_all || die 'Go orchestration is not the active lifecycle owner'
}

orch_console_status() {
  orch_console_require_go
  case "${1:-}" in
    "")
      "$ORCH_CORE_BIN" status --socket "$ORCH_CORE_SOCKET"
      ;;
    --json)
      shift
      [[ $# -eq 0 ]] || die 'usage: kk status --json'
      "$ORCH_CORE_BIN" status --socket "$ORCH_CORE_SOCKET" --json
      ;;
    *)
      die 'usage: kk status [--json]'
      ;;
  esac
}

orch_console_watch() {
  orch_console_require_go
  case "${1:-}" in
    "")
      exec "$ORCH_CORE_BIN" watch --socket "$ORCH_CORE_SOCKET"
      ;;
    --json)
      shift
      [[ $# -eq 0 ]] || die 'usage: kk watch --json'
      exec "$ORCH_CORE_BIN" watch --socket "$ORCH_CORE_SOCKET" --json
      ;;
    *)
      die 'usage: kk watch [--json]'
      ;;
  esac
}

orch_console_role_arg() {
  local role=""
  if [[ "${1:-}" == --role ]]; then
    [[ -n "${2:-}" && $# -eq 2 ]] || die 'expected --role NAME'
    role="$2"
  elif [[ $# -ne 0 ]]; then
    die 'expected optional --role NAME'
  fi
  printf '%s' "$role"
}

orch_console_connect() {
  orch_console_require_go
  # The product one-writer path (Phase E): a bare `kk start` brings the
  # managed pair up first - including the schema2 cutover when the committed
  # mode is Local - before connecting the roles. A per-role connect assumes
  # the pair is already up.
  local role
  role="$(orch_console_role_arg "$@")"
  if [[ -n "$role" ]]; then
    "$ORCH_CORE_BIN" connect --socket "$ORCH_CORE_SOCKET" --role "$role"
  else
    orch_start_managed_pair
    "$ORCH_CORE_BIN" start --socket "$ORCH_CORE_SOCKET"
  fi
}

orch_console_disconnect() {
  orch_console_require_go
  local role
  role="$(orch_console_role_arg "$@")"
  if [[ -n "$role" ]]; then
    "$ORCH_CORE_BIN" disconnect --socket "$ORCH_CORE_SOCKET" --role "$role"
  else
    "$ORCH_CORE_BIN" stop --socket "$ORCH_CORE_SOCKET"
  fi
}

orch_console_retry() {
  orch_console_require_go
  [[ "${1:-}" == --role && -n "${2:-}" && $# -eq 2 ]] || die 'usage: kk retry --role NAME'
  "$ORCH_CORE_BIN" retry --socket "$ORCH_CORE_SOCKET" --role "$2"
}

orch_console_restart() {
  orch_console_require_go
  if [[ $# -eq 0 || ( $# -eq 1 && "${1:-}" == --all ) ]]; then
    "$ORCH_CORE_BIN" restart --socket "$ORCH_CORE_SOCKET"
    return
  fi
  [[ "${1:-}" == --role && -n "${2:-}" && $# -eq 2 ]] || die 'usage: kk restart [--role NAME|--all]'
  "$ORCH_CORE_BIN" disconnect --socket "$ORCH_CORE_SOCKET" --role "$2" >/dev/null
  "$ORCH_CORE_BIN" connect --socket "$ORCH_CORE_SOCKET" --role "$2"
}

orch_console_profiles() {
  orch_console_require_go
  "$ORCH_CORE_BIN" profiles --socket "$ORCH_CORE_SOCKET" "$@"
}

orch_console_interfaces() {
  orch_console_require_go
  "$ORCH_CORE_BIN" interfaces --socket "$ORCH_CORE_SOCKET" "$@"
}

# Return 64 when the current installation is not Go-owned or the command is not
# part of the core console surface, so the legacy dispatcher can handle it.
orch_console_try_dispatch() {
  local command="$1"
  shift || true
  orch_go_owns_all || return 64
  case "$command" in
    status) orch_console_status "$@" ;;
    watch) orch_console_watch "$@" ;;
    start|connect) orch_console_connect "$@" ;;
    stop|disconnect) orch_console_disconnect "$@" ;;
    retry) orch_console_retry "$@" ;;
    restart) orch_console_restart "$@" ;;
    profiles) orch_console_profiles "$@" ;;
    interfaces) orch_console_interfaces "$@" ;;
    *) return 64 ;;
  esac
}

orch_console_json_dispatch() {
  local command="$1"
  shift || true
  orch_go_owns_all || return 64
  case "$command" in
    status)
      [[ $# -eq 0 ]] || die 'usage: kk status --json'
      orch_console_status --json
      ;;
    watch)
      [[ $# -eq 0 ]] || die 'usage: kk watch --json'
      orch_console_watch --json
      ;;
    profiles)
      [[ $# -eq 0 ]] || die 'usage: kk profiles --json'
      orch_console_profiles --json
      ;;
    interfaces)
      [[ $# -eq 0 ]] || die 'usage: kk interfaces --json'
      orch_console_interfaces --json
      ;;
    *) return 64 ;;
  esac
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
