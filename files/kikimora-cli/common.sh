# Common constants and utilities for Kikimora CLI
#
# This file is sourced by the kikimora entrypoint.
# Expects SELF_DIR to be set by the entrypoint.

# ── Path constants ──────────────────────────────────────────────────────────

readonly BACKUP_DIR="/var/backups/kikimora/leshy"
readonly DOMAINS_DIR="/etc/kikimora/leshy/domains"
readonly ROUTES_DIR="/etc/kikimora/leshy/routes"
readonly PRIMARY_DOMAINS="${DOMAINS_DIR}/primary.txt"
readonly SECONDARY_DOMAINS="${DOMAINS_DIR}/secondary.txt"
readonly BYPASS_DOMAINS="${DOMAINS_DIR}/bypass.txt"
readonly PRIMARY_ROUTES="${ROUTES_DIR}/primary.txt"
readonly SECONDARY_ROUTES="${ROUTES_DIR}/secondary.txt"
readonly ROUTING_CONFIG="/etc/kikimora/leshy/routing.conf"
readonly MANAGED_UNITS=(leshy.service leshy-route-watch.service leshy-health-watch.service)

# ── Helpers ─────────────────────────────────────────────────────────────────

die() { printf 'Error: %s\n' "$*" >&2; exit 1; }
require_root() { [[ $EUID -eq 0 ]] || die "run the command via sudo"; }

find_installer() {
  [[ -x "$SELF_DIR/install.sh" ]] || return 1
  printf '%s\n' "$SELF_DIR/install.sh"
}

count_list_file() {
  local file="$1"
  [[ -r "$file" ]] || { printf '0\n'; return; }
  awk '!/^[[:space:]]*(#|$)/{n++} END{print n+0}' "$file"
}

# ── Interface helpers ───────────────────────────────────────────────────────

interface_state(){
  local iface="$1" role="$2"
  [[ -d "/sys/class/net/$iface" ]] || { printf 'missing'; return; }
  if [[ "$role" == dns ]]; then
    local dns_line domain_line
    dns_line="$(resolvectl dns "$iface" 2>/dev/null || true)"
    domain_line="$(resolvectl domain "$iface" 2>/dev/null || true)"
    if grep -Fq '127.0.0.1:53053' <<<"$dns_line" && grep -Eq '(^|[[:space:]])~\.($|[[:space:]])' <<<"$domain_line"; then
      printf active
    else
      printf down
    fi
    return
  fi
  ip link show dev "$iface" 2>/dev/null | grep -qE '<[^>]*UP[^>]*>' || { printf down; return; }
  ip -o addr show dev "$iface" scope global 2>/dev/null | grep -q . && printf ready || printf down
}

load_interfaces(){ source /etc/kikimora/leshy/vpn.conf; PRIMARY_INTERFACE="${PRIMARY_INTERFACE:-${AMN_IFACE:-primary0}}"; SECONDARY_INTERFACE="${SECONDARY_INTERFACE:-${VPN_IFACE:-secondary0}}"; }

service_word(){ local u="$1"; if systemctl is-active --quiet "$u"; then printf '✓ running'; elif systemctl is-failed --quiet "$u"; then printf '✗ failed'; else printf '○ down'; fi; }

# ── Domain helpers ──────────────────────────────────────────────────────────

resolve_domain_zone() {
  local zone="${1:-primary}"
  case "$zone" in
    primary|--primary) printf '%s\n' "$PRIMARY_DOMAINS" ;;
    secondary|--secondary) printf '%s\n' "$SECONDARY_DOMAINS" ;;
    bypass|--bypass) printf '%s\n' "$BYPASS_DOMAINS" ;;
    *) die "unknown domain zone: $zone (use primary, secondary or bypass)" ;;
  esac
}

normalize_domain() {
  local domain="${1,,}"
  domain="${domain%.}"
  [[ "$domain" =~ ^(\*\.)?([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,63}$ ]] ||
    die "invalid domain: $1"
  printf '%s\n' "$domain"
}

normalized_domain_count(){ awk '{sub(/#.*/,""); gsub(/^[[:space:]]+|[[:space:]]+$/ ,""); if(length) print tolower($0)}' "$1" | LC_ALL=C sort -u | wc -l; }

# ── Static route helpers ────────────────────────────────────────────────────

resolve_route_zone() {
  local zone="${1:-secondary}"
  case "$zone" in
    primary|--primary) printf '%s\n' "$PRIMARY_ROUTES" ;;
    secondary|--secondary) printf '%s\n' "$SECONDARY_ROUTES" ;;
    *) die "unknown route zone: $zone (use primary or secondary)" ;;
  esac
}

validate_ipv4_address(){
  local address="$1" octet
  local -a octets
  IFS='.' read -r -a octets <<< "$address"
  ((${#octets[@]} == 4)) || return 1
  for octet in "${octets[@]}"; do
    [[ "$octet" =~ ^[0-9]+$ ]] || return 1
    ((octet >= 0 && octet <= 255)) || return 1
  done
}

normalize_cidr() {
  local route="${1,,}" address prefix
  route="${route%$'\r'}"
  route="${route%%#*}"
  route="${route#"${route%%[![:space:]]*}"}"
  route="${route%"${route##*[![:space:]]}"}"
  [[ -n "$route" ]] || die 'empty route'
  [[ "$route" != *[[:space:]]* ]] || die "invalid route: $1"

  if [[ "$route" == */* ]]; then
    address="${route%%/*}"
    prefix="${route#*/}"
    [[ -n "$address" && -n "$prefix" && "$prefix" =~ ^[0-9]+$ ]] || die "invalid route: $1"
  else
    address="$route"
    prefix=''
  fi

  if [[ "$address" == *.* ]]; then
    validate_ipv4_address "$address" || die "invalid IPv4 route: $1"
    if [[ -n "$prefix" ]]; then
      ((prefix <= 32)) || die "invalid IPv4 prefix in route: $1"
      printf '%s/%s\n' "$address" "$prefix"
    else
      printf '%s\n' "$address"
    fi
    return
  fi

  if [[ "$address" == *:* && "$address" =~ ^[0-9a-f:]+$ ]]; then
    if [[ -n "$prefix" ]]; then
      ((prefix <= 128)) || die "invalid IPv6 prefix in route: $1"
      printf '%s/%s\n' "$address" "$prefix"
    else
      printf '%s\n' "$address"
    fi
    return
  fi

  die "invalid IP/CIDR route: $1"
}
