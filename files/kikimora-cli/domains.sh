# Domain list management
#
# This file is sourced by the kikimora entrypoint.
# Uses DOMAINS_DIR, PRIMARY_DOMAINS, SECONDARY_DOMAINS, BYPASS_DOMAINS,
# ROUTING_CONFIG from common.sh and resolve_domain_zone(), normalize_domain()
# from common.sh.

# ── Domain change helpers ───────────────────────────────────────────────────

apply_domain_change() {
  local target="$1" candidate="$2" backup
  require_root
  [[ -d "$DOMAINS_DIR" ]] || die "directory not found: $DOMAINS_DIR"
  backup="$(mktemp /tmp/kikimora-domains.XXXXXX)"
  cp -a -- "$target" "$backup"
  install -m 0644 "$candidate" "$target"
  if ! /usr/local/libexec/kikimora/leshy/build-config ||
     ! /usr/local/libexec/kikimora/leshy/check-config /usr/local/bin/leshy /etc/kikimora/leshy/config.toml; then
    install -m 0644 "$backup" "$target"
    /usr/local/libexec/kikimora/leshy/build-config >/dev/null 2>&1 || true
    rm -f -- "$backup"
    die "list change rolled back: configuration validation failed"
  fi
  rm -f -- "$backup"
  if systemctl is-active --quiet leshy.service; then
    systemctl restart leshy.service
    printf 'Leshy restarted.\n'
  else
    printf 'Leshy not running; new configuration will apply on next start.\n'
  fi
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
      printf 'Primary:   %s domains (%s)\n' "$(awk '!/^[[:space:]]*(#|$)/{n++} END{print n+0}' "$PRIMARY_DOMAINS")" "$PRIMARY_DOMAINS"
      printf 'Secondary: %s domains (%s)\n' "$(awk '!/^[[:space:]]*(#|$)/{n++} END{print n+0}' "$SECONDARY_DOMAINS")" "$SECONDARY_DOMAINS"
      printf 'Bypass:    %s domains (%s)\n' "$(awk '!/^[[:space:]]*(#|$)/{n++} END{print n+0}' "$BYPASS_DOMAINS")" "$BYPASS_DOMAINS"
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
      awk '{sub(/#.*/,""); gsub(/^[[:space:]]+|[[:space:]]+$/,""); if(length) print tolower($0)}' "$1" | LC_ALL=C sort -u > "$tmp"
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