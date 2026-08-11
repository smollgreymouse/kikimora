# Service management — start/stop/restart/enable/disable
#
# This file is sourced by the kikimora entrypoint.

readonly ENDPOINT_UNDERLAY_CTL="${KIKIMORA_ENDPOINT_UNDERLAY_CTL:-/usr/local/libexec/kikimora/leshy/route-watch}"

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

endpoint_underlay_apply() {
  [[ -x "$ENDPOINT_UNDERLAY_CTL" ]] || {
    printf 'Endpoint underlay controller not found: %s\n' "$ENDPOINT_UNDERLAY_CTL" >&2
    return 1
  }
  "$ENDPOINT_UNDERLAY_CTL" endpoint apply
}

endpoint_underlay_preflight_clear() {
  [[ -x "$ENDPOINT_UNDERLAY_CTL" ]] || return 0
  "$ENDPOINT_UNDERLAY_CTL" endpoint check-clear
}

endpoint_underlay_clear() {
  local force="${1:-}"
  [[ -x "$ENDPOINT_UNDERLAY_CTL" ]] || return 0
  if [[ "$force" == --force ]]; then
    "$ENDPOINT_UNDERLAY_CTL" endpoint clear --force
  else
    "$ENDPOINT_UNDERLAY_CTL" endpoint clear
  fi
}

parse_force_only() {
  SERVICE_FORCE=''
  while (($#)); do
    case "$1" in
      --force) SERVICE_FORCE=--force ;;
      *) die "unexpected argument: $1" ;;
    esac
    shift
  done
}

parse_now_force() {
  SERVICE_NOW=''
  SERVICE_FORCE=''
  while (($#)); do
    case "$1" in
      --now) SERVICE_NOW=--now ;;
      --force) SERVICE_FORCE=--force ;;
      *) die "unexpected argument: $1" ;;
    esac
    shift
  done
}

cmd_service() {
  local action="$1"
  shift || true
  require_root

  case "$action" in
    start)
      [[ $# -eq 0 ]] || die "unexpected arguments"
      # Establish endpoint policy before starting Leshy/route-watch. If a VPN is
      # already UP on a conflicting path, the controller defers that role rather
      # than changing a live tunnel underneath the VPN client.
      endpoint_underlay_apply || return 1
      systemctl start "${MANAGED_UNITS[@]}"
      cmd_dns_ensure_enabled
      ;;

    stop)
      parse_force_only "$@"
      if [[ -z "$SERVICE_FORCE" ]]; then
        endpoint_underlay_preflight_clear || {
          printf 'Kikimora was not stopped. Disconnect managed VPN interfaces first, or retry with: sudo kk stop --force\n' >&2
          return 1
        }
      fi
      cmd_dns_suspend_if_available
      systemctl stop "${MANAGED_UNITS[@]}"
      endpoint_underlay_clear "$SERVICE_FORCE"
      ;;

    restart)
      [[ $# -eq 0 ]] || die "unexpected arguments"
      rebuild_service_config || return 1
      # Keep endpoint policy in place throughout an internal Kikimora restart;
      # removing it while VPN clients are active could move their underlay.
      endpoint_underlay_apply || return 1
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
      parse_now_force "$@"
      local -a systemctl_args=("$action")

      if [[ "$action" == enable && -n "$SERVICE_FORCE" ]]; then
        die '--force is only valid with stop or disable --now'
      fi
      if [[ "$action" == disable && -n "$SERVICE_FORCE" && -z "$SERVICE_NOW" ]]; then
        die '--force requires disable --now'
      fi

      if [[ "$action" == enable && -n "$SERVICE_NOW" ]]; then
        endpoint_underlay_apply || return 1
      fi

      if [[ "$action" == disable && -n "$SERVICE_NOW" ]]; then
        if [[ -z "$SERVICE_FORCE" ]]; then
          endpoint_underlay_preflight_clear || {
            printf 'Kikimora was not disabled --now. Disconnect managed VPN interfaces first, or retry with: sudo kk disable --now --force\n' >&2
            return 1
          }
        fi
        cmd_dns_suspend_if_available
      fi

      [[ -n "$SERVICE_NOW" ]] && systemctl_args+=("$SERVICE_NOW")
      systemctl "${systemctl_args[@]}" "${MANAGED_UNITS[@]}"

      if [[ "$action" == enable && -n "$SERVICE_NOW" ]]; then
        cmd_dns_ensure_enabled
      elif [[ "$action" == disable && -n "$SERVICE_NOW" ]]; then
        endpoint_underlay_clear "$SERVICE_FORCE"
      fi
      ;;
  esac
}
