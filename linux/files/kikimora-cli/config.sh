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
      printf '\n%s\n' '== VPN endpoints forced through physical underlay =='
      local endpoint_file
      for endpoint_file in \
        /etc/kikimora/leshy/endpoints/primary.txt \
        /etc/kikimora/leshy/endpoints/secondary.txt; do
        printf '%s\n' "--- ${endpoint_file} ---"
        if [[ -r "$endpoint_file" ]]; then
          cat "$endpoint_file"
        else
          printf '(not created yet; route-watch creates it on first run)\n'
        fi
      done
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
  show       Show vpn.conf, exact VPN endpoint lists and generated config.toml
  edit       Edit vpn.conf, rebuild and validate config.toml
  validate   Validate current config.toml

Exact VPN endpoint hostnames/IPs live in:
  /etc/kikimora/leshy/endpoints/primary.txt
  /etc/kikimora/leshy/endpoints/secondary.txt

Endpoint entries are exact only: no wildcard or parent-domain matching.
Configured endpoint IPs are forced through the physical underlay before normal
primary/secondary/default routing.
HELP
      ;;
    *) die "unknown config command: $subcommand" ;;
  esac
}
