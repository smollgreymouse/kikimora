# Help and usage for Kikimora CLI
#
# This file is sourced by the kikimora entrypoint.

usage() {
  cat <<'HELP'
Kikimora 1.0.0 — Leshy installation, diagnostics and management

USAGE
  kikimora COMMAND [OPTIONS]
  kk COMMAND [OPTIONS]

MAIN COMMANDS
  install [--primary-interface NAME] [--secondary-interface NAME]
      Install or upgrade Kikimora and Leshy.
      --primary-interface NAME     Higher-priority VPN interface (e.g. tun0, amn0)
      --secondary-interface NAME   Lower-priority VPN interface (e.g. tun1, vpn0)
  upgrade PATH            Upgrade Kikimora from a local ZIP or directory
  uninstall [--purge]      Remove Kikimora; --purge removes its configuration
  verify                  Verify installation integrity and configuration
  doctor                  Gather extended system diagnostics
  debuglog                Write a full Linux debug log bundle to a file

LESHY MANAGEMENT
  start                    Start services and ensure DNS integration
  stop                     Suspend DNS integration and stop services
  restart                  Rebuild config, restart services and ensure DNS integration
  enable [--now]           Enable service autostart
  disable [--now]          Disable service autostart
  status [-v|--verbose]    Show quick diagnostics
  interfaces               Show interfaces, addresses and routes
  logs [OPTIONS]           Show or follow Leshy logs

DNS
  dns enable               Route system DNS through Leshy
  dns disable              Restore original DNS and forget saved state
  dns suspend              Temporarily restore original DNS
  dns resume               Resume DNS through Leshy if saved state exists
  dns status               Show current DNS state

CONFIGURATION
  config show              Show vpn.conf and config.toml
  config edit              Edit vpn.conf, rebuild and validate config
  config validate          Validate current configuration

DOMAINS
  domains                  Show list summary
  domains list [ZONE]      Show domains
  domains add DOMAIN       Add domain (default primary)
  domains remove DOMAIN    Remove domain
  domains edit ZONE        Open list in editor
  domains import FILE ZONE Import domains from file
  domains export ZONE      Print list to stdout
  domains default [MODE]   Show/change default route

STATIC ROUTES
  routes                   Show static route summary
  routes list [ZONE]       Show static IP/CIDR routes
  routes add CIDR          Add route (default secondary)
  routes remove CIDR       Remove route
  routes edit ZONE         Open route list in editor
  routes import FILE ZONE  Import routes from file
  routes export ZONE       Print route list to stdout

BACKUPS
  backup                   Create backup in /var/backups/kikimora/leshy
  restore [ARCHIVE]        Restore specified or latest backup

HELP AND VERSION
  help                     Show this help
  COMMAND --help           Show help for a specific command
  version, --version       Show Kikimora version

EXAMPLES
  sudo ./kikimora install
  sudo kk install --primary-interface amn0 --secondary-interface vpn0
  sudo kk start
  sudo kk domains add example.com
  sudo kk routes add 172.25.36.0/24 --secondary
  sudo kk dns status
  kk status
  sudo kk debuglog
  sudo kk verify
  sudo kk doctor
  kk logs -f
  sudo kk backup
  sudo kk upgrade ./kikimora-NEW.zip
  sudo kk uninstall
  sudo kk uninstall --purge

AUTOCOMPLETION
  Installed automatically for Bash, Zsh and Fish.
  After installation, open a new shell or reload its configuration.

DETAILED HELP
  kikimora install --help
  kikimora dns --help
  kikimora config --help
  kikimora domains --help
  kikimora routes --help
  kikimora logs --help
  kikimora debuglog --help
  kikimora restore --help
  kikimora upgrade --help
HELP
}

command_help() {
  local command="$1"
  case "$command" in
    install) local installer; installer="$(find_installer || true)"; [[ -n "$installer" ]] || die "installer not found"; exec "$installer" --help ;;
    verify) printf 'Usage: sudo kikimora verify\nCheck managed files, Leshy configuration and systemd units.\n' ;;
    doctor) printf 'Usage: sudo kikimora doctor\nShow services, interfaces, routes, DNS, logs and verify result.\n' ;;
    debuglog) cmd_debuglog --help ;;
    backup) printf 'Usage: sudo kikimora backup\nCreate a configuration archive in %s.\n' "$BACKUP_DIR" ;;
    restore) printf 'Usage: sudo kikimora restore [BACKUP.tar.gz]\nWithout a path, the latest backup is used.\n' ;;
    upgrade) printf 'Usage: sudo kikimora upgrade PATH [INSTALL OPTIONS]\nPATH — ZIP or directory of the new Kikimora release.\n' ;;
    uninstall) cmd_uninstall --help ;;
    start) printf 'Usage: sudo kikimora start\nStart services and ensure DNS integration.\n' ;;
    stop) printf 'Usage: sudo kikimora stop\nSuspend DNS integration and stop services.\n' ;;
    restart) printf 'Usage: sudo kikimora restart\nRebuild and validate config, suspend DNS, restart services and ensure DNS integration.\n' ;;
    enable|disable|status|interfaces) printf 'Usage: sudo kikimora %s\n' "$command" ;;
    logs) printf 'Usage: kikimora logs [-n N|--lines N] [--no-follow] [--all]\n' ;;
    dns) cmd_dns --help ;;
    config) cmd_config --help ;;
    domains) cmd_domains --help ;;
    routes) cmd_routes --help ;;
    completion) cmd_completion --help ;;
    version) printf 'Usage: kikimora version\n' ;;
    help) usage ;;
    *) die "unknown command: $command" ;;
  esac
}
