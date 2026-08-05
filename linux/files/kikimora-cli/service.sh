# Service management — start/stop/restart/enable/disable
#
# This file is sourced by the kikimora entrypoint.

rebuild_service_config() {
  local candidate

  # Older installations may only have static routes in the generated config.
  # Materialize them before rebuilding so restart cannot silently drop them.
  seed_route_file_from_config primary "$PRIMARY_ROUTES"
  seed_route_file_from_config secondary "$SECONDARY_ROUTES"

  candidate="$(mktemp /etc/kikimora/leshy/.config.toml.restart.XXXXXX)"

  if ! /usr/local/libexec/kikimora/leshy/build-config \
      "$DOMAINS_DIR" "$candidate" "$ROUTING_CONFIG" "$ROUTES_DIR" ||
     ! /usr/local/libexec/kikimora/leshy/check-config \
      /usr/local/bin/leshy "$candidate"; then
    rm -f -- "$candidate"
    printf 'Configuration rebuild failed; services were not stopped.\n' >&2
    return 1
  fi

  chmod 0644 "$candidate"
  mv -f -- "$candidate" /etc/kikimora/leshy/config.toml
  printf 'Configuration rebuilt and validated.\n'
}

cmd_service() {
  local action="$1"
  shift || true
  require_root

  case "$action" in
    start)
      [[ $# -eq 0 ]] || die "unexpected arguments"
      systemctl start "${MANAGED_UNITS[@]}"
      cmd_dns_ensure_enabled
      ;;

    stop)
      [[ $# -eq 0 ]] || die "unexpected arguments"
      cmd_dns_suspend_if_available
      systemctl stop "${MANAGED_UNITS[@]}"
      ;;

    restart)
      [[ $# -eq 0 ]] || die "unexpected arguments"
      rebuild_service_config || return 1
      cmd_dns_suspend_if_available
      if ! systemctl stop "${MANAGED_UNITS[@]}"; then
        printf 'Services failed to stop; system DNS remains restored.\n' >&2
        return 1
      fi
      if ! systemctl start "${MANAGED_UNITS[@]}"; then
        printf 'Services failed to restart; system DNS remains restored.\n' >&2
        return 1
      fi
      cmd_dns_ensure_enabled
      ;;

    enable|disable)
      local now=''
      local -a systemctl_args=("$action")
      [[ "${1:-}" == --now ]] && { now=--now; shift; }
      [[ $# -eq 0 ]] || die "usage: kk $action [--now]"

      if [[ "$action" == disable && -n "$now" ]]; then
        cmd_dns_suspend_if_available
      fi

      [[ -n "$now" ]] && systemctl_args+=("$now")
      systemctl "${systemctl_args[@]}" "${MANAGED_UNITS[@]}"

      if [[ "$action" == enable && -n "$now" ]]; then
        cmd_dns_ensure_enabled
      fi
      ;;
  esac
}
