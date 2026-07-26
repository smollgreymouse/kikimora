# Service management — start/stop/restart/enable/disable
#
# This file is sourced by the kikimora entrypoint.

cmd_service() {
  local action="$1"; shift || true; require_root
  case "$action" in
    start|stop|restart) [[ $# -eq 0 ]] || die "unexpected arguments"; systemctl "$action" "${MANAGED_UNITS[@]}" ;;
    enable|disable)
      local now=''; [[ "${1:-}" == --now ]] && { now=--now; shift; }; [[ $# -eq 0 ]] || die "usage: kk $action [--now]"; systemctl "$action" ${now:+$now} "${MANAGED_UNITS[@]}" ;;
  esac
}