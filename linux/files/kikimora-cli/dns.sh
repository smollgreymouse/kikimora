# DNS management — wraps /usr/local/sbin/leshy-dns
#
# This file is sourced by the kikimora entrypoint.

require_leshy_dns() {
  [[ -x /usr/local/sbin/leshy-dns ]] || die "/usr/local/sbin/leshy-dns not found"
}

cmd_dns_ensure_enabled() {
  require_leshy_dns

  /usr/local/sbin/leshy-dns resume || true
  if /usr/local/sbin/leshy-dns check; then
    return 0
  fi

  printf 'DNS integration is not active; enabling it.\n' >&2
  /usr/local/sbin/leshy-dns enable
}

cmd_dns_suspend_if_available() {
  [[ -x /usr/local/sbin/leshy-dns ]] || return 0
  /usr/local/sbin/leshy-dns suspend || true
}

cmd_dns() {
  local subcommand="${1:-status}"
  shift || true
  case "$subcommand" in
    status|enable|disable|suspend|resume)
      [[ $# -eq 0 ]] || die "unexpected arguments for kikimora dns $subcommand"
      require_leshy_dns
      exec /usr/local/sbin/leshy-dns "$subcommand"
      ;;
    help|-h|--help)
      cat <<'HELP'
Usage: sudo kikimora dns COMMAND

Commands:
  status    Show system DNS state
  enable    Switch system DNS to Leshy and remember the mode
  disable   Restore original DNS routing and forget saved mode
  suspend   Temporarily restore original DNS while preserving intent
  resume    Restore DNS through Leshy if mode was saved
HELP
      ;;
    *) die "unknown DNS command: $subcommand" ;;
  esac
}
