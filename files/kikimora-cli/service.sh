# Service management — start/stop/restart/enable/disable
#
# This file is sourced by the kikimora entrypoint.

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
      cmd_dns_suspend_if_available
      if ! systemctl restart "${MANAGED_UNITS[@]}"; then
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
