# Static CIDR route list management
#
# This file is sourced by the kikimora entrypoint.
# Uses ROUTES_DIR, PRIMARY_ROUTES, SECONDARY_ROUTES, resolve_route_zone(),
# normalize_cidr() and count_list_file() from common.sh.

apply_route_change() {
  local target="$1" candidate="$2" backup
  require_root
  install -d -m 0755 "$ROUTES_DIR"
  [[ -e "$target" ]] || install -m 0644 /dev/null "$target"

  backup="$(mktemp /tmp/kikimora-routes.XXXXXX)"
  cp -a -- "$target" "$backup"
  install -m 0644 "$candidate" "$target"

  if ! /usr/local/libexec/kikimora/leshy/build-config ||
     ! /usr/local/libexec/kikimora/leshy/check-config /usr/local/bin/leshy /etc/kikimora/leshy/config.toml; then
    install -m 0644 "$backup" "$target"
    /usr/local/libexec/kikimora/leshy/build-config >/dev/null 2>&1 || true
    rm -f -- "$backup"
    die "route list change rolled back: configuration validation failed"
  fi

  rm -f -- "$backup"
  if systemctl is-active --quiet leshy.service; then
    systemctl restart leshy.service
    printf 'Leshy restarted. Static routes will be applied during startup.\n'
  else
    printf 'Leshy not running; static routes will apply on next start.\n'
  fi
}

cmd_routes() {
  local subcommand="${1:-status}"
  [[ $# -eq 0 ]] || shift
  case "$subcommand" in
    status)
      [[ $# -eq 0 ]] || die "unexpected arguments for kikimora routes"
      printf 'Primary:   %s routes (%s)\n' "$(count_list_file "$PRIMARY_ROUTES")" "$PRIMARY_ROUTES"
      printf 'Secondary: %s routes (%s)\n' "$(count_list_file "$SECONDARY_ROUTES")" "$SECONDARY_ROUTES"
      ;;

    list)
      if [[ $# -eq 0 ]]; then
        local zone file
        for zone in primary secondary; do
          file="$(resolve_route_zone "$zone")"
          printf '== %s ==\n' "$zone"
          [[ -r "$file" ]] && grep -Ev '^[[:space:]]*(#|$)' "$file" || true
          printf '\n'
        done
      else
        [[ $# -eq 1 ]] || die "usage: kikimora routes list [primary|secondary]"
        local file
        file="$(resolve_route_zone "$1")"
        [[ -r "$file" ]] && grep -Ev '^[[:space:]]*(#|$)' "$file" || true
      fi
      ;;

    add|remove)
      [[ $# -ge 1 && $# -le 2 ]] || die "usage: sudo kikimora routes $subcommand CIDR [--primary|--secondary]"
      local cidr zone target tmp
      cidr="$(normalize_cidr "$1")"
      zone="${2:---secondary}"
      target="$(resolve_route_zone "$zone")"
      install -d -m 0755 "$ROUTES_DIR"
      [[ -e "$target" ]] || install -m 0644 /dev/null "$target"
      tmp="$(mktemp /tmp/kikimora-routes-new.XXXXXX)"
      if [[ "$subcommand" == add ]]; then
        { cat "$target"; printf '%s\n' "$cidr"; } | awk '{sub(/#.*/,""); gsub(/^[[:space:]]+|[[:space:]]+$/ ,""); if(length) print tolower($0)}' | LC_ALL=C sort -u > "$tmp"
        if grep -Fqx "$cidr" "$target"; then rm -f "$tmp"; printf 'Route already exists: %s\n' "$cidr"; return; fi
      else
        awk -v d="$cidr" 'tolower($0) != d {print}' "$target" > "$tmp"
        if ! grep -Fiqx "$cidr" "$target"; then rm -f "$tmp"; die "route not found in selected zone: $cidr"; fi
      fi
      apply_route_change "$target" "$tmp"
      rm -f -- "$tmp"
      printf '%s: %s (%s)\n' "$([[ "$subcommand" == add ]] && echo Added || echo Removed)" "$cidr" "${zone#--}"
      ;;

    edit)
      [[ $# -eq 1 ]] || die "usage: sudo kikimora routes edit primary|secondary"
      require_root
      local target tmp editor
      target="$(resolve_route_zone "$1")"
      install -d -m 0755 "$ROUTES_DIR"
      [[ -e "$target" ]] || install -m 0644 /dev/null "$target"
      tmp="$(mktemp /tmp/kikimora-routes-edit.XXXXXX)"
      cp -a "$target" "$tmp"
      editor="${SUDO_EDITOR:-${EDITOR:-vi}}"
      "$editor" "$tmp"
      apply_route_change "$target" "$tmp"
      rm -f "$tmp"
      ;;

    import)
      [[ $# -eq 2 ]] || die "usage: sudo kikimora routes import FILE primary|secondary"
      [[ -r "$1" ]] || die "file not accessible: $1"
      local target tmp line
      target="$(resolve_route_zone "$2")"
      tmp="$(mktemp /tmp/kikimora-routes-import.XXXXXX)"
      while IFS= read -r line || [[ -n "$line" ]]; do
        line="${line%%#*}"
        line="${line#"${line%%[![:space:]]*}"}"
        line="${line%"${line##*[![:space:]]}"}"
        [[ -n "$line" ]] || continue
        normalize_cidr "$line"
      done < "$1" | LC_ALL=C sort -u > "$tmp"
      apply_route_change "$target" "$tmp"
      rm -f "$tmp"
      ;;

    export)
      [[ $# -eq 1 ]] || die "usage: kikimora routes export primary|secondary"
      local file
      file="$(resolve_route_zone "$1")"
      [[ -r "$file" ]] && cat "$file" || true
      ;;

    help|-h|--help)
      cat <<'HELP'
Usage: kikimora routes COMMAND

Commands:
  routes                              Show static route counts
  routes list [ZONE]                  Show one or all route lists
  routes add CIDR [--primary|--secondary]
  routes remove CIDR [--primary|--secondary]
  routes edit primary|secondary
  routes import FILE primary|secondary
  routes export primary|secondary

CIDR examples:
  172.25.36.0/24
  172.25.36.237

By default add/remove operate on secondary.
HELP
      ;;

    *) die "unknown routes command: $subcommand" ;;
  esac
}
