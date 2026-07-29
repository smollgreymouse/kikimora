# Domain and static route list management
#
# This file is sourced by the kikimora entrypoint.
# Uses DOMAINS_DIR, ROUTES_DIR, PRIMARY_DOMAINS, SECONDARY_DOMAINS,
# BYPASS_DOMAINS, PRIMARY_ROUTES, SECONDARY_ROUTES, ROUTING_CONFIG,
# resolve_domain_zone(), resolve_route_zone(), normalize_domain(),
# normalize_cidr() and count_list_file() from common.sh.

# ── Shared validated list change helpers ────────────────────────────────────

apply_validated_list_change() {
  local target="$1" candidate="$2" kind="$3" backup
  require_root
  install -d -m 0755 "$(dirname -- "$target")"
  [[ -e "$target" ]] || install -m 0644 /dev/null "$target"

  backup="$(mktemp /tmp/kikimora-${kind}.XXXXXX)"
  cp -a -- "$target" "$backup"
  install -m 0644 "$candidate" "$target"

  if ! /usr/local/libexec/kikimora/leshy/build-config ||
     ! /usr/local/libexec/kikimora/leshy/check-config /usr/local/bin/leshy /etc/kikimora/leshy/config.toml; then
    install -m 0644 "$backup" "$target"
    /usr/local/libexec/kikimora/leshy/build-config >/dev/null 2>&1 || true
    rm -f -- "$backup"
    die "$kind list change rolled back: configuration validation failed"
  fi

  rm -f -- "$backup"
  if systemctl is-active --quiet leshy.service; then
    systemctl restart leshy.service
    printf 'Leshy restarted.\n'
  else
    printf 'Leshy not running; new configuration will apply on next start.\n'
  fi
}

apply_domain_change() {
  local target="$1" candidate="$2"
  [[ -d "$DOMAINS_DIR" ]] || die "directory not found: $DOMAINS_DIR"
  apply_validated_list_change "$target" "$candidate" domains
}

extract_zone_static_routes() {
  local zone="$1"
  [[ -r /etc/kikimora/leshy/config.toml ]] || return 0
  awk -v wanted="$zone" '
    function trim(s) { gsub(/^[[:space:]]+|[[:space:]]+$/, "", s); return s }
    /^\[\[zones\]\]/ { in_zone=0; in_static=0; next }
    /^[[:space:]]*name[[:space:]]*=/ {
      line=$0
      sub(/^[^=]*=/, "", line)
      line=trim(line)
      gsub(/^"|"$/, "", line)
      in_zone=(line == wanted)
      in_static=0
      next
    }
    in_zone && /^[[:space:]]*static_routes[[:space:]]*=/ { in_static=1; next }
    in_zone && in_static && /^[[:space:]]*]/ { exit }
    in_zone && in_static {
      line=$0
      sub(/#.*/, "", line)
      gsub(/[",]/, "", line)
      line=trim(line)
      if (line != "") print tolower(line)
    }
  ' /etc/kikimora/leshy/config.toml | LC_ALL=C sort -u
}

seed_route_file_from_config() {
  local zone="$1" target="$2" tmp
  [[ -e "$target" ]] && return 0
  install -d -m 0755 "$ROUTES_DIR"
  tmp="$(mktemp /tmp/kikimora-routes-seed.XXXXXX)"
  extract_zone_static_routes "$zone" > "$tmp"
  install -m 0644 "$tmp" "$target"
  rm -f -- "$tmp"
}

ensure_route_file() {
  local zone="$1" target
  target="$(resolve_route_zone "$zone")"
  seed_route_file_from_config "${zone#--}" "$target"
  printf '%s\n' "$target"
}

apply_route_change() {
  local target="$1" candidate="$2"
  install -d -m 0755 "$ROUTES_DIR"
  apply_validated_list_change "$target" "$candidate" routes
}

get_default_zone(){ [[ -r "$ROUTING_CONFIG" ]] || { printf 'direct\n'; return; }; bash -c 'source "$1"; printf "%s\n" "${DEFAULT_ZONE:-direct}"' _ "$ROUTING_CONFIG"; }

apply_default_change(){
  local mode="$1" old tmpdir was_active=0
  case "$mode" in direct|primary|secondary|none) ;; *) die "unknown default mode: $mode; allowed: direct, primary, secondary, none";; esac
  require_root; old="$(get_default_zone)"; [[ "$old" != "$mode" ]] || { printf 'Default route unchanged: %s\n' "$mode"; return; }
  tmpdir="$(mktemp -d /tmp/kikimora-default.XXXXXX)"
  cp -a "$ROUTING_CONFIG" "$tmpdir/routing.conf.old" 2>/dev/null || printf 'DEFAULT_ZONE=direct\n' >"$tmpdir/routing.conf.old"
  printf 'DEFAULT_ZONE=%s\n' "$mode" >"$tmpdir/routing.conf.new"
  systemctl is-active --quiet leshy.service && was_active=1 || true
  install -m 0644 "$tmpdir/routing.conf.new" "$ROUTING_CONFIG"
  if ! /usr/local/libexec/kikimora/leshy/build-config || ! /usr/local/libexec/kikimora/leshy/check-config /usr/local/bin/leshy /etc/kikimora/leshy/config.toml; then
    install -m 0644 "$tmpdir/routing.conf.old" "$ROUTING_CONFIG"; /usr/local/libexec/kikimora/leshy/build-config >/dev/null 2>&1 || true
    rm -rf -- "$tmpdir"
    die 'configuration validation failed; default not changed'
  fi
  printf 'Default route changed: %s → %s\nConfiguration regenerated and valid.\n' "$old" "$mode"
  if ((was_active)); then systemctl restart leshy.service; printf 'Leshy restarted.\n'; fi
  rm -rf -- "$tmpdir"
}

# ── cmd_domains ─────────────────────────────────────────────────────────────

cmd_domains() {
  local subcommand="${1:-status}"
  [[ $# -eq 0 ]] || shift
  case "$subcommand" in
    status)
      [[ $# -eq 0 ]] || die "unexpected arguments for kikimora domains"
      printf 'Primary:   %s domains (%s)\n' "$(count_list_file "$PRIMARY_DOMAINS")" "$PRIMARY_DOMAINS"
      printf 'Secondary: %s domains (%s)\n' "$(count_list_file "$SECONDARY_DOMAINS")" "$SECONDARY_DOMAINS"
      printf 'Bypass:    %s domains (%s)\n' "$(count_list_file "$BYPASS_DOMAINS")" "$BYPASS_DOMAINS"
      ;;
    list)
      if [[ $# -eq 0 ]]; then
        local zone file
        for zone in primary secondary bypass; do
          file="$(resolve_domain_zone "$zone")"
          printf '== %s ==\n' "$zone"
          grep -Ev '^[[:space:]]*(#|$)' "$file" || true
          printf '\n'
        done
      else
        [[ $# -eq 1 ]] || die "usage: kikimora domains list [primary|secondary|bypass]"
        grep -Ev '^[[:space:]]*(#|$)' "$(resolve_domain_zone "$1")" || true
      fi
      ;;
    add|remove)
      [[ $# -ge 1 && $# -le 2 ]] || die "usage: sudo kikimora domains $subcommand DOMAIN [--primary|--secondary|--bypass]"
      local domain zone target tmp
      domain="$(normalize_domain "$1")"
      zone="${2:---primary}"
      target="$(resolve_domain_zone "$zone")"
      tmp="$(mktemp /tmp/kikimora-domains-new.XXXXXX)"
      if [[ "$subcommand" == add ]]; then
        { cat "$target"; printf '%s\n' "$domain"; } | grep -Ev '^[[:space:]]*(#|$)' | LC_ALL=C sort -u > "$tmp"
        if grep -Fqx "$domain" "$target"; then rm -f "$tmp"; printf 'Domain already exists: %s\n' "$domain"; return; fi
      else
        awk -v d="$domain" 'tolower($0) != d {print}' "$target" > "$tmp"
        if ! grep -Fiqx "$domain" "$target"; then rm -f "$tmp"; die "domain not found in selected zone: $domain"; fi
      fi
      apply_domain_change "$target" "$tmp"
      rm -f -- "$tmp"
      printf '%s: %s (%s)\n' "$([[ "$subcommand" == add ]] && echo Added || echo Removed)" "$domain" "${zone#--}"
      ;;
    edit)
      [[ $# -eq 1 ]] || die "usage: sudo kikimora domains edit primary|secondary|bypass"
      require_root
      local target tmp editor
      target="$(resolve_domain_zone "$1")"; tmp="$(mktemp /tmp/kikimora-domains-edit.XXXXXX)"
      cp -a "$target" "$tmp"; editor="${SUDO_EDITOR:-${EDITOR:-vi}}"; "$editor" "$tmp"
      apply_domain_change "$target" "$tmp"; rm -f "$tmp"
      ;;
    import)
      [[ $# -eq 2 ]] || die "usage: sudo kikimora domains import FILE primary|secondary|bypass"
      [[ -r "$1" ]] || die "file not accessible: $1"
      local target tmp
      target="$(resolve_domain_zone "$2")"; tmp="$(mktemp /tmp/kikimora-domains-import.XXXXXX)"
      awk '{sub(/#.*/,""); gsub(/^[[:space:]]+|[[:space:]]+$/ ,""); if(length) print tolower($0)}' "$1" | LC_ALL=C sort -u > "$tmp"
      apply_domain_change "$target" "$tmp"; rm -f "$tmp"
      ;;
    export)
      [[ $# -eq 1 ]] || die "usage: kikimora domains export primary|secondary|bypass"
      cat "$(resolve_domain_zone "$1")"
      ;;
    default)
      if [[ $# -eq 0 ]]; then printf 'Default route for unmatched domains: %s\n' "$(get_default_zone)"; elif [[ $# -eq 1 ]]; then apply_default_change "$1"; else die 'usage: kk domains default [direct|primary|secondary|none]'; fi
      ;;
    help|-h|--help)
      cat <<'HELP'
Usage: kikimora domains COMMAND

Commands:
  domains                              Show domain counts
  domains list [ZONE]                  Show one or all lists
  domains add DOMAIN [--primary|--secondary|--bypass]
  domains remove DOMAIN [--primary|--secondary|--bypass]
  domains edit primary|secondary|bypass
  domains import FILE primary|secondary|bypass
  domains export primary|secondary|bypass
  domains default [direct|primary|secondary|none]

By default add/remove operate on primary.
HELP
      ;;
    *) die "unknown domains command: $subcommand" ;;
  esac
}

# ── cmd_routes ──────────────────────────────────────────────────────────────

cmd_routes() {
  local subcommand="${1:-status}"
  [[ $# -eq 0 ]] || shift
  case "$subcommand" in
    status)
      [[ $# -eq 0 ]] || die "unexpected arguments for kikimora routes"
      seed_route_file_from_config primary "$PRIMARY_ROUTES"
      seed_route_file_from_config secondary "$SECONDARY_ROUTES"
      printf 'Primary:   %s routes (%s)\n' "$(count_list_file "$PRIMARY_ROUTES")" "$PRIMARY_ROUTES"
      printf 'Secondary: %s routes (%s)\n' "$(count_list_file "$SECONDARY_ROUTES")" "$SECONDARY_ROUTES"
      ;;
    list)
      if [[ $# -eq 0 ]]; then
        local zone file
        for zone in primary secondary; do
          file="$(ensure_route_file "$zone")"
          printf '== %s ==\n' "$zone"
          [[ -r "$file" ]] && grep -Ev '^[[:space:]]*(#|$)' "$file" || true
          printf '\n'
        done
      else
        [[ $# -eq 1 ]] || die "usage: kikimora routes list [primary|secondary]"
        local file
        file="$(ensure_route_file "$1")"
        [[ -r "$file" ]] && grep -Ev '^[[:space:]]*(#|$)' "$file" || true
      fi
      ;;
    add|remove)
      [[ $# -ge 1 && $# -le 2 ]] || die "usage: sudo kikimora routes $subcommand CIDR [--primary|--secondary]"
      local cidr zone target tmp
      cidr="$(normalize_cidr "$1")"
      zone="${2:---secondary}"
      target="$(ensure_route_file "$zone")"
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
      target="$(ensure_route_file "$1")"
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
      target="$(ensure_route_file "$2")"
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
      file="$(ensure_route_file "$1")"
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
