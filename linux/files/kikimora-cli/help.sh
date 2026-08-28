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
  install [OPTIONS]        Install or reinstall Kikimora and Leshy
      --primary-interface NAME     Higher-priority VPN interface (e.g. tun0, amn0)
      --secondary-interface NAME   Lower-priority VPN interface (e.g. tun1, vpn0)
      --leshy-binary PATH           Use a specific Leshy binary when none is installed
      --non-interactive             Do not ask interactive questions
      --stop-services               Allow stopping active Leshy services
      -y, --yes                     Agree to the required service stop without asking

  upgrade PATH [INSTALL OPTIONS]
                           Upgrade from a local ZIP or directory and pass remaining
                           options through to the new package installer
  uninstall [--purge] [-y|--yes]
                           Remove Kikimora; --purge also removes configuration/state/backups
  verify                   Verify installation integrity and configuration
  doctor                   Gather extended system diagnostics
  debuglog [OPTIONS]       Write a full Linux debug log bundle to a file
  diag [DOMAIN]            Capture focused secondary-VPN diagnostics

LESHY MANAGEMENT
  start                    Apply endpoint DIRECT policy, then start services and DNS integration
  stop [--force]           Stop services and remove endpoint policy; refuses live VPNs unless forced
  restart                  Rebuild and restart while keeping endpoint DIRECT policy in place
  enable [--now]           Enable service autostart; --now applies endpoint policy before starting
  disable [--now] [--force]
                           Disable autostart; with --now also stops and clears endpoint policy
  status [-v|--verbose]    Show quick diagnostics
  interfaces               Show interfaces, addresses and routes
  logs [OPTIONS]           Show or follow Leshy logs

VPN PROFILES
  profiles                 Same as profiles list
  profiles list            List named primary/secondary interface pairs and endpoint providers
  profiles status          Alias of profiles list
  profiles add NAME PRIMARY [SECONDARY] [OPTIONS]
                           Add a profile; omitted SECONDARY keeps the current secondary role
      --primary-provider NAME
      --secondary-provider NAME
      --primary-provider-args ARG
      --secondary-provider-args ARG
  profiles use NAME        Atomically switch both interfaces and endpoint providers
  profiles remove NAME     Remove an inactive profile

  Built-in endpoint providers: static, happ, command.
  A changed role is withdrawn immediately and must pass the normal readiness
  streak on its new interface. Leshy-owned destinations from a withdrawn VPN
  remain fail-closed through route parking until replacement routes return.

VPN endpoint safety
  Endpoint lists are exact hostname/IP entries in:
    /etc/kikimora/leshy/endpoints/primary.txt
    /etc/kikimora/leshy/endpoints/secondary.txt
  While Kikimora is running they are routed through the physical underlay.
  If Kikimora starts while a VPN is already UP on a conflicting endpoint path,
  that role is marked underlay-pending instead of changing the live tunnel.
  Disconnect that VPN once; route-watch applies DIRECT while it is down.
  Endpoint lists are shared by profiles, so include every VPN server endpoint
  that may serve the corresponding role in any profile.

DNS
  dns                      Same as dns status
  dns status               Show current DNS state
  dns enable               Route system DNS through Leshy
  dns disable              Restore original DNS and forget saved state
  dns suspend              Temporarily restore original DNS
  dns resume               Resume DNS through Leshy if saved state exists

CONFIGURATION
  config                   Same as config show
  config show              Show profiles.conf, vpn.conf, endpoint lists and config.toml
  config edit              Edit vpn.conf, rebuild and validate config
  config validate          Validate current configuration

DOMAINS
  domains                  Same as domains status
  domains status           Show domain-list counts
  domains list [ZONE]      Show one or all domain lists
  domains add DOMAIN [--primary|--secondary|--bypass]
                           Add domain; default zone is primary
  domains remove DOMAIN [--primary|--secondary|--bypass]
                           Remove domain from the selected zone
  domains edit ZONE        Open primary, secondary or bypass list in an editor
  domains import FILE ZONE Import domains from a file
  domains export ZONE      Print a domain list to stdout
  domains default [MODE]   Show/change unmatched-domain mode: direct, primary, secondary or none

STATIC ROUTES
  routes                   Same as routes status
  routes status            Show static-route counts
  routes list [ZONE]       Show one or all static IP/CIDR route lists
  routes add CIDR [--primary|--secondary]
                           Add route; default zone is secondary
  routes remove CIDR [--primary|--secondary]
                           Remove route from the selected zone
  routes edit ZONE         Open primary or secondary route list in an editor
  routes import FILE ZONE  Import routes from a file
  routes export ZONE       Print a route list to stdout

BACKUPS
  backup                   Create backup in /var/backups/kikimora/leshy
  restore [ARCHIVE]        Restore specified or latest backup

SHELL COMPLETION
  completion bash          Print installed Bash completion
  completion zsh           Print installed Zsh completion
  completion fish          Print installed Fish completion

HELP AND VERSION
  help, -h, --help         Show this help
  COMMAND --help           Show help for a specific command
  COMMAND -h               Same as COMMAND --help
  version, --version, -V   Show Kikimora version

EXAMPLES
  sudo ./kikimora install
  sudo kk install --primary-interface amn0 --secondary-interface vpn0
  sudo kk install --leshy-binary ./leshy
  sudo kk profiles add office amn1 vpn0
  sudo kk profiles add backup - vpn1
  sudo kk profiles use office
  sudo kk enable --now
  sudo kk start
  sudo kk stop --force
  sudo kk domains add example.com
  sudo kk routes add 172.25.36.0/24 --secondary
  sudo kk dns status
  kk status --verbose
  sudo kk diag
  sudo kk debuglog
  sudo kk verify
  sudo kk doctor
  kk logs -f
  sudo kk backup
  sudo kk upgrade ./kikimora-NEW.zip --non-interactive
  sudo kk uninstall
  sudo kk uninstall --purge --yes

AUTOCOMPLETION
  Installed automatically for Bash, Zsh and Fish.
  After installation, open a new shell or reload its configuration.

DETAILED HELP
  kikimora COMMAND --help
  kk COMMAND -h
HELP
}

command_help() {
  local command="$1"
  case "$command" in
    install)
      local installer
      installer="$(find_installer || true)"
      [[ -n "$installer" ]] || die "installer not found"
      exec "$installer" --help
      ;;
    verify)
      printf 'Usage: sudo kikimora verify\nCheck managed files, Leshy configuration and systemd units.\n'
      ;;
    doctor)
      printf 'Usage: sudo kikimora doctor\nShow services, interfaces, routes, DNS, logs and verify result.\n'
      ;;
    debuglog) cmd_debuglog --help ;;
    diag) cmd_diag --help ;;
    backup)
      printf 'Usage: sudo kikimora backup\nCreate a configuration archive in %s.\n' "$BACKUP_DIR"
      ;;
    restore)
      printf 'Usage: sudo kikimora restore [BACKUP.tar.gz]\nWithout a path, the latest backup is used.\n'
      ;;
    upgrade)
      printf 'Usage: sudo kikimora upgrade PATH [INSTALL OPTIONS]\nPATH — ZIP or directory of the new Kikimora release.\nINSTALL OPTIONS are the same options accepted by kikimora install.\n'
      ;;
    uninstall) cmd_uninstall --help ;;
    start)
      printf 'Usage: sudo kikimora start\nApply VPN endpoint DIRECT policy first, then start services and ensure DNS integration.\n'
      ;;
    stop)
      printf 'Usage: sudo kikimora stop [--force]\nStop services and clear endpoint policy. Without --force, live managed VPN interfaces make the command fail before anything is stopped.\n'
      ;;
    restart)
      printf 'Usage: sudo kikimora restart\nRebuild and validate config, keep endpoint DIRECT policy active, restart services and ensure DNS integration.\n'
      ;;
    enable)
      printf 'Usage: sudo kikimora enable [--now]\nEnable autostart; --now applies endpoint policy before starting services.\n'
      ;;
    disable)
      printf 'Usage: sudo kikimora disable [--now] [--force]\nWith --now, stop services and clear endpoint policy. --force is valid only with --now.\n'
      ;;
    status)
      printf 'Usage: kikimora status [-v|--verbose]\nShow service, interface, DNS-zone and startup state; verbose mode also prints systemd details.\n'
      ;;
    interfaces)
      printf 'Usage: kikimora interfaces\nShow managed VPN/DNS interfaces, addresses, routes and domain counts.\n'
      ;;
    logs)
      printf 'Usage: kikimora logs [-n N|--lines N] [-f|--follow] [--no-follow] [--all]\n'
      ;;
    dns) cmd_dns --help ;;
    config) cmd_config --help ;;
    profiles)
      cmd_profiles --help
      printf '\nAliases: profiles list and profiles status both show the profile list.\n'
      ;;
    domains)
      cmd_domains --help
      printf '\nAlias: domains status is the explicit form of bare domains.\n'
      ;;
    routes)
      cmd_routes --help
      printf '\nAlias: routes status is the explicit form of bare routes.\n'
      ;;
    completion) cmd_completion --help ;;
    version)
      printf 'Usage: kikimora version\nAliases: --version, -V\n'
      ;;
    help) usage ;;
    *) die "unknown command: $command" ;;
  esac
}
