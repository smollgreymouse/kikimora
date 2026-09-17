# Go orchestration ownership and cutover commands.
#
# This file is sourced by the kikimora entrypoint. Legacy files and units stay
# installed until a separate post-parity retirement step.

readonly ORCH_OWNERSHIP_CONFIG="${KIKIMORA_ORCHESTRATION_OWNERSHIP_CONFIG:-/etc/kikimora/leshy/orchestration-ownership.conf}"
readonly ORCH_CONFIG_DIR="${KIKIMORA_ORCHESTRATION_CONFIG_DIR:-/etc/kikimora/toads}"
readonly ORCH_CORE_UNIT="${KIKIMORA_ORCHESTRATION_CORE_UNIT:-kikimora-core.service}"
readonly ORCH_CORE_BIN="${KIKIMORA_ORCHESTRATION_CORE_BIN:-/usr/local/bin/kikimora-core}"
readonly ORCH_SYSTEMCTL="${KIKIMORA_ORCHESTRATION_SYSTEMCTL:-systemctl}"
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
     "$ORCH_SYSTEMCTL" == /tmp/* ]] || die "run the command via sudo"
}

orch_read_owner() {
  local key="$1"
  [[ -r "$ORCH_OWNERSHIP_CONFIG" ]] || return 1
  awk -v key="$key" '$1 == key && $2 == "=" {gsub(/[\"[:space:]]/, "", $3); print $3; exit}' "$ORCH_OWNERSHIP_CONFIG"
}

orch_write_owner() {
  local routing="$1" tunnel="$2" endpoint="$3" tmp
  tmp="$(mktemp "${ORCH_OWNERSHIP_CONFIG}.tmp.XXXXXX")"
  cat >"$tmp" <<EOF_OWNERSHIP
# Single-writer migration guard. Valid values: legacy or go.
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
  [[ -x "$ORCH_CORE_BIN" ]] || die "Go core is not executable: $ORCH_CORE_BIN"
  [[ -r "$ORCH_OWNERSHIP_CONFIG" ]] || die "ownership config not found: $ORCH_OWNERSHIP_CONFIG"
  [[ -d "$ORCH_CONFIG_DIR" ]] || die "Go Toad config directory not found: $ORCH_CONFIG_DIR"
  compgen -G "$ORCH_CONFIG_DIR/*.toml" >/dev/null || die "no Go Toad TOML configs found in $ORCH_CONFIG_DIR"
}

orch_status() {
  local routing tunnel endpoint unit
  routing="$(orch_read_owner routing_owner || printf unknown)"
  tunnel="$(orch_read_owner tunnel_owner || printf unknown)"
  endpoint="$(orch_read_owner endpoint_owner || printf unknown)"
  printf 'ownership: routing=%s tunnel=%s endpoint=%s\n' "$routing" "$tunnel" "$endpoint"
  printf '  %-32s %s\n' "$ORCH_CORE_UNIT" "$("${ORCH_SYSTEMCTL}" is-active "$ORCH_CORE_UNIT" 2>/dev/null || true)"
  for unit in leshy-route-watch.service; do
    printf '  %-32s %s\n' "$unit" "$("${ORCH_SYSTEMCTL}" is-active "$unit" 2>/dev/null || true)"
  done
  if [[ "$routing" == go && "$tunnel" == go && "$endpoint" == go ]]; then
    printf 'cutover: go\n'
  else
    printf 'cutover: legacy\n'
  fi
}

orch_rollback_values() {
  local routing="$1" tunnel="$2" endpoint="$3"
  orch_write_owner "$routing" "$tunnel" "$endpoint"
  orch_legacy_start || true
}

orch_cutover_go() {
  local old_routing old_tunnel old_endpoint
  orch_require_root
  orch_preflight
  old_routing="$(orch_read_owner routing_owner || printf legacy)"
  old_tunnel="$(orch_read_owner tunnel_owner || printf external)"
  old_endpoint="$(orch_read_owner endpoint_owner || printf legacy)"

  if [[ "$old_routing" == go && "$old_tunnel" == go && "$old_endpoint" == go ]]; then
    orch_legacy_stop || return 1
    orch_legacy_disable || return 1
    if ! "${ORCH_SYSTEMCTL}" enable "$ORCH_CORE_UNIT" || ! "${ORCH_SYSTEMCTL}" start "$ORCH_CORE_UNIT" || ! orch_core_active; then
      orch_core_disable
      orch_core_stop
      orch_write_owner legacy external legacy
      orch_legacy_start || true
      die "Go core did not become active; ownership was rolled back to legacy"
    fi
    printf 'Go orchestration is already active.\n'
    return 0
  fi

  # Stop writers before publishing go ownership. Every failure after this
  # point restores the old ownership and starts the legacy writer set.
  orch_legacy_stop || return 1
  if ! orch_legacy_disable; then
    orch_rollback_values "$old_routing" "$old_tunnel" "$old_endpoint"
    return 1
  fi
  orch_write_owner go go go
  if ! "${ORCH_SYSTEMCTL}" daemon-reload || ! "${ORCH_SYSTEMCTL}" enable "$ORCH_CORE_UNIT" || ! "${ORCH_SYSTEMCTL}" start "$ORCH_CORE_UNIT" || ! orch_core_active; then
    printf 'Go core failed to start; ownership was rolled back to legacy.\n' >&2
    orch_core_disable
    orch_core_stop
    orch_rollback_values "$old_routing" "$old_tunnel" "$old_endpoint"
    return 1
  fi
  printf 'Go orchestration cutover complete. Legacy writer units are disabled.\n'
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
  printf 'Orchestration ownership rolled back to legacy.\n'
}

orch_retire_legacy() {
  orch_require_root
  orch_go_owns_all || die 'legacy retirement requires routing_owner=go, tunnel_owner=go and endpoint_owner=go'
  orch_core_active || die 'Go core must be active before legacy retirement'

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
    *) die "unknown orchestration action: $action (use status, cutover --go or rollback)" ;;
  esac
}
