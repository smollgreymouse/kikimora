# Configuration management — show/edit/validate
#
# This file is sourced by the kikimora entrypoint.

cmd_config() {
  local subcommand="${1:-show}"
  shift || true
  case "$subcommand" in
    show)
      [[ $# -eq 0 ]] || die "unexpected arguments for kikimora config show"
      printf '%s\n' '== /etc/kikimora/leshy/vpn.conf =='
      cat /etc/kikimora/leshy/vpn.conf
      printf '\n%s\n' '== /etc/kikimora/leshy/config.toml =='
      cat /etc/kikimora/leshy/config.toml
      ;;
    edit)
      [[ $# -eq 0 ]] || die "unexpected arguments for kikimora config edit"
      require_root
      local editor="${SUDO_EDITOR:-${EDITOR:-vi}}"
      "$editor" /etc/kikimora/leshy/vpn.conf
      /usr/local/libexec/kikimora/leshy/build-config
      /usr/local/libexec/kikimora/leshy/check-config /usr/local/bin/leshy /etc/kikimora/leshy/config.toml
      printf 'Configuration updated and validated. To apply: sudo kikimora restart\n'
      ;;
    validate)
      [[ $# -eq 0 ]] || die "unexpected arguments for kikimora config validate"
      /usr/local/libexec/kikimora/leshy/check-config /usr/local/bin/leshy /etc/kikimora/leshy/config.toml
      ;;
    help|-h|--help)
      cat <<'HELP'
Usage: kikimora config COMMAND

Commands:
  show       Show vpn.conf and generated config.toml
  edit       Edit vpn.conf, rebuild and validate config.toml
  validate   Validate current config.toml
HELP
      ;;
    *) die "unknown config command: $subcommand" ;;
  esac
}