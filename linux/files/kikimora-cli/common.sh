# Common constants and utilities for Kikimora CLI
#
# This file is sourced by the kikimora entrypoint.
# Expects SELF_DIR to be set by the entrypoint.

# ── Path constants ──────────────────────────────────────────────────────────

readonly BACKUP_DIR="/var/backups/kikimora/leshy"
readonly DOMAINS_DIR="/etc/kikimora/leshy/domains"
readonly ROUTES_DIR="/etc/kikimora/leshy/routes"
readonly ENDPOINTS_DIR="/etc/kikimora/leshy/endpoints"
readonly ENDPOINT_STATE_DIR="${KIKIMORA_ENDPOINT_STATE_DIR:-/run/kikimora/leshy/endpoint-underlay}"
readonly VPN_RUNTIME_DIR="${KIKIMORA_VPN_RUNTIME_DIR:-/run/kikimora/leshy/vpn}"
readonly VPN_READINESS_DIR="${VPN_RUNTIME_DIR}/readiness"
readonly VPN_FLAP_WINDOW_SECONDS="${KIKIMORA_VPN_FLAP_WINDOW_SECONDS:-120}"
readonly VPN_FLAP_THRESHOLD="${KIKIMORA_VPN_FLAP_THRESHOLD:-2}"
readonly SYS_NET_ROOT="${KIKIMORA_SYS_NET_ROOT:-/sys/class/net}"
readonly PRIMARY_DOMAINS="${DOMAINS_DIR}/primary.txt"
readonly SECONDARY_DOMAINS="${DOMAINS_DIR}/secondary.txt"
readonly BYPASS_DOMAINS="${DOMAINS_DIR}/bypass.txt"
readonly PRIMARY_ROUTES="${ROUTES_DIR}/primary.txt"
readonly SECONDARY_ROUTES="${ROUTES_DIR}/secondary.txt"
readonly ROUTING_CONFIG="/etc/kikimora/leshy/routing.conf"
readonly MANAGED_UNITS=(leshy.service leshy-route-watch.service leshy-health-watch.service)
readonly ENDPOINT_ROUTE_TABLE="${KIKIMORA_ENDPOINT_ROUTE_TABLE:-51890}"
readonly PRIMARY_ENDPOINT_RULE_PRIORITY="${KIKIMORA_PRIMARY_ENDPOINT_RULE_PRIORITY:-50}"
readonly SECONDARY_ENDPOINT_RULE_PRIORITY="${KIKIMORA_SECONDARY_ENDPOINT_RULE_PRIORITY:-51}"

# ── Helpers ──────────────────────────────────────────────────────────────────

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
  [[ -d "${SYS_NET_ROOT}/$iface" ]] || { printf 'missing'; return; }
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

vpn_role_recent_recreate_count() {
  local role="$1"
  local events="${VPN_READINESS_DIR}/${role}.recreates"
  local now window ts count=0
  now="$(date +%s)"
  window="$VPN_FLAP_WINDOW_SECONDS"
  [[ "$window" =~ ^[1-9][0-9]*$ ]] || window=120
  [[ -r "$events" ]] || { printf '0\n'; return; }
  while IFS= read -r ts || [[ -n "$ts" ]]; do
    [[ "$ts" =~ ^[0-9]+$ ]] || continue
    ((ts <= now && now - ts <= window)) && count=$((count + 1))
  done < "$events"
  printf '%s\n' "$count"
}

vpn_role_is_flapping() {
  local threshold count
  threshold="$VPN_FLAP_THRESHOLD"
  [[ "$threshold" =~ ^[1-9][0-9]*$ ]] || threshold=2
  count="$(vpn_role_recent_recreate_count "$1")"
  ((count >= threshold))
}

vpn_role_state() {
  local role="$1" iface="$2" basic published='' device_file
  device_file="${VPN_RUNTIME_DIR}/${role}.dev"

  if [[ -e "${ENDPOINT_STATE_DIR}/${role}.pending" ]]; then
    printf 'underlay-pending'
    return
  fi

  basic="$(interface_state "$iface" vpn)"

  # Repeated same-name interface recreation is more informative than the
  # instantaneous link state. It also catches a delete/create cycle that happens
  # entirely between two status calls.
  if vpn_role_is_flapping "$role"; then
    printf 'flapping'
    return
  fi

  if [[ "$basic" != ready ]]; then
    printf '%s' "$basic"
    return
  fi

  # Leshy routes only through a role after reconcile publishes role.dev. A bare
  # UP interface with an IPv4 address is therefore only a readiness candidate,
  # not an operational VPN role.
  if [[ ! -r "$device_file" ]]; then
    printf 'validating'
    return
  fi

  IFS= read -r published < "$device_file" || true
  if [[ "$published" != "$iface" ]]; then
    printf 'degraded'
    return
  fi

  printf 'ready'
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

# ── Machine-readable JSON API ───────────────────────────────────────────────
#
# The JSON API describes Kikimora concepts only. It never knows the names,
# cache formats, processes or diagnostics of individual endpoint providers.

json_quote() {
  local value="${1-}"
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  value=${value//$'\b'/\\b}
  value=${value//$'\f'/\\f}
  value=${value//$'\n'/\\n}
  value=${value//$'\r'/\\r}
  value=${value//$'\t'/\\t}
  printf '"%s"' "$value"
}

json_bool() {
  if "$@"; then printf 'true'; else printf 'false'; fi
}

json_service_state() {
  local unit="$1"
  if systemctl is-active --quiet "$unit" 2>/dev/null; then
    printf 'running'
  elif systemctl is-failed --quiet "$unit" 2>/dev/null; then
    printf 'failed'
  else
    printf 'stopped'
  fi
}

json_active_profile_name() {
  local current_primary current_secondary current_primary_provider current_secondary_provider
  local current_primary_args current_secondary_args
  current_primary="$(read_current_primary_interface)"
  current_secondary="$(read_current_secondary_interface)"
  current_primary_provider="$(read_current_primary_provider)"
  current_secondary_provider="$(read_current_secondary_provider)"
  current_primary_args="$(read_current_primary_provider_args)"
  current_secondary_args="$(read_current_secondary_provider_args)"
  load_vpn_profiles
  profile_for_state "$current_primary" "$current_secondary" "$current_primary_provider" "$current_primary_args" "$current_secondary_provider" "$current_secondary_args" || true
}

json_status() {
  [[ $# -eq 0 ]] || die 'usage: kk status --json'
  load_interfaces
  local primary_state secondary_state dns_state active default_zone service_state
  primary_state="$(vpn_role_state primary "$PRIMARY_INTERFACE")"
  secondary_state="$(vpn_role_state secondary "$SECONDARY_INTERFACE")"
  dns_state="$(interface_state leshy-dns0 dns)"
  active="$(json_active_profile_name)"
  default_zone="$(get_default_zone)"
  service_state="$(json_service_state leshy.service)"

  printf '{"schema_version":1,"service":'
  json_quote "$service_state"
  printf ',"services":{"leshy":'; json_quote "$(json_service_state leshy.service)"
  printf ',"route_watch":'; json_quote "$(json_service_state leshy-route-watch.service)"
  printf ',"health_watch":'; json_quote "$(json_service_state leshy-health-watch.service)"
  printf '},"profiles":{"active":'
  if [[ -n "$active" ]]; then json_quote "$active"; else printf 'null'; fi
  printf '},"interfaces":{"primary":{"name":'; json_quote "$PRIMARY_INTERFACE"
  printf ',"state":'; json_quote "$primary_state"
  printf '},"secondary":{"name":'; json_quote "$SECONDARY_INTERFACE"
  printf ',"state":'; json_quote "$secondary_state"
  printf '},"dns":{"name":"leshy-dns0","state":'; json_quote "$dns_state"
  printf '}},"dns":{"provider":'
  if [[ "$dns_state" == active ]]; then json_quote leshy; else json_quote system; fi
  printf ',"default_zone":'; json_quote "$default_zone"
  printf '},"startup":{"enabled":'
  json_bool systemctl is-enabled --quiet leshy.service
  printf '},"endpoint_underlay_migration_pending":'
  if [[ -e "${ENDPOINT_STATE_DIR}/migration.pending" ]]; then printf 'true'; else printf 'false'; fi
  printf '}\n'
}

json_profiles() {
  [[ $# -eq 0 ]] || die 'usage: kk profiles --json'
  local current_primary current_secondary current_primary_provider current_secondary_provider
  local current_primary_args current_secondary_args active name first=1
  current_primary="$(read_current_primary_interface)"
  current_secondary="$(read_current_secondary_interface)"
  current_primary_provider="$(read_current_primary_provider)"
  current_secondary_provider="$(read_current_secondary_provider)"
  current_primary_args="$(read_current_primary_provider_args)"
  current_secondary_args="$(read_current_secondary_provider_args)"
  load_vpn_profiles
  active="$(profile_for_state "$current_primary" "$current_secondary" "$current_primary_provider" "$current_primary_args" "$current_secondary_provider" "$current_secondary_args" || true)"

  printf '{"schema_version":1,"active":'
  if [[ -n "$active" ]]; then json_quote "$active"; else printf 'null'; fi
  printf ',"profiles":['
  while IFS= read -r name; do
    ((first)) || printf ','
    first=0
    printf '{"name":'; json_quote "$name"
    printf ',"active":'; if [[ "$name" == "$active" ]]; then printf 'true'; else printf 'false'; fi
    printf ',"primary":{"interface":'; json_quote "${VPN_PROFILE_PRIMARY[$name]}"
    printf ',"endpoint_provider":'; json_quote "${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER[$name]}"
    printf ',"provider_args":'; json_quote "${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS[$name]}"
    printf '},"secondary":{"interface":'; json_quote "${VPN_PROFILE_SECONDARY[$name]}"
    printf ',"endpoint_provider":'; json_quote "${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER[$name]}"
    printf ',"provider_args":'; json_quote "${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS[$name]}"
    printf '}}'
  done < <(printf '%s\n' "${!VPN_PROFILE_PRIMARY[@]}" | LC_ALL=C sort)
  printf ']}\n'
}

json_string_file_array() {
  local file="$1" value first=1
  printf '['
  if [[ -r "$file" ]]; then
    while IFS= read -r value; do
      ((first)) || printf ','
      first=0
      json_quote "$value"
    done < <(awk '{sub(/#.*/,""); gsub(/^[[:space:]]+|[[:space:]]+$/ ,""); if(length) print tolower($0)}' "$file" | LC_ALL=C sort -u)
  fi
  printf ']'
}

endpoint_role_priority() {
  case "$1" in
    primary) printf '%s\n' "$PRIMARY_ENDPOINT_RULE_PRIORITY" ;;
    secondary) printf '%s\n' "$SECONDARY_ENDPOINT_RULE_PRIORITY" ;;
    *) return 64 ;;
  esac
}

endpoint_installed_addresses() {
  local role="$1" priority family
  priority="$(endpoint_role_priority "$role")" || return $?
  for family in 4 6; do
    ip "-${family}" rule show 2>/dev/null | awk -v priority="${priority}:" -v table="$ENDPOINT_ROUTE_TABLE" '
      $1 == priority {
        destination=""
        lookup=""
        for (i = 1; i <= NF; i++) {
          if ($i == "to") destination=$(i + 1)
          if ($i == "lookup") lookup=$(i + 1)
        }
        if (lookup == table && destination != "") {
          sub(/\/(32|128)$/, "", destination)
          print destination
        }
      }
    '
  done | LC_ALL=C sort -u
}

json_command_array() {
  local first=1 value
  printf '['
  while IFS= read -r value; do
    [[ -n "$value" ]] || continue
    ((first)) || printf ','
    first=0
    json_quote "$value"
  done
  printf ']'
}

json_endpoint_role() {
  local role="$1" iface="$2" provider="$3" provider_args="$4"
  printf '{"interface":'; json_quote "$iface"
  printf ',"provider":'; json_quote "$provider"
  printf ',"provider_args":'; json_quote "$provider_args"
  printf ',"state":'; json_quote "$(vpn_role_state "$role" "$iface")"
  printf ',"pending":'; if [[ -e "${ENDPOINT_STATE_DIR}/${role}.pending" ]]; then printf 'true'; else printf 'false'; fi
  printf ',"configured":'; json_string_file_array "${ENDPOINTS_DIR}/${role}.txt"
  printf ',"installed":'; endpoint_installed_addresses "$role" | json_command_array
  printf ',"actions":{"rediscover":true,"invalidate":false}'
  printf '}'
}

json_endpoints() {
  [[ $# -eq 0 ]] || die 'usage: kk endpoints --json'
  load_interfaces
  local primary_provider secondary_provider primary_args secondary_args
  primary_provider="$(read_current_primary_provider)"
  secondary_provider="$(read_current_secondary_provider)"
  primary_args="$(read_current_primary_provider_args)"
  secondary_args="$(read_current_secondary_provider_args)"
  printf '{"schema_version":1,"roles":{"primary":'
  json_endpoint_role primary "$PRIMARY_INTERFACE" "$primary_provider" "$primary_args"
  printf ',"secondary":'
  json_endpoint_role secondary "$SECONDARY_INTERFACE" "$secondary_provider" "$secondary_args"
  printf '}}\n'
}

json_dns() {
  [[ $# -eq 0 ]] || die 'usage: kk dns --json'
  local dns_state
  dns_state="$(interface_state leshy-dns0 dns)"
  printf '{"schema_version":1,"provider":'
  if [[ "$dns_state" == active ]]; then json_quote leshy; else json_quote system; fi
  printf ',"interface":{"name":"leshy-dns0","state":'; json_quote "$dns_state"
  printf '},"service":'; json_quote "$(json_service_state leshy.service)"
  printf ',"listen":"127.0.0.1:53053"}\n'
}

json_logs() {
  local lines=100 all=0
  while (($#)); do
    case "$1" in
      --lines|-n)
        (($# >= 2)) || die "$1 requires a line count"
        lines="$2"
        shift 2
        ;;
      --all) all=1; shift ;;
      *) die "unknown JSON logs option: $1" ;;
    esac
  done
  [[ "$lines" =~ ^[1-9][0-9]*$ ]] || die "invalid line count: $lines"
  ((lines <= 5000)) || die 'JSON log line count must not exceed 5000'

  local -a journal_args=(-n "$lines" --no-pager -o json)
  local -a units=(leshy.service)
  if ((all)); then units=(leshy.service leshy-route-watch.service leshy-health-watch.service); fi
  local unit
  for unit in "${units[@]}"; do journal_args+=(-u "$unit"); done

  printf '{"schema_version":1,"units":['
  local first=1
  for unit in "${units[@]}"; do ((first)) || printf ','; first=0; json_quote "$unit"; done
  printf '],"limit":%s,"entries":[' "$lines"
  first=1
  local record
  while IFS= read -r record; do
    [[ -n "$record" ]] || continue
    ((first)) || printf ','
    first=0
    printf '%s' "$record"
  done < <(journalctl "${journal_args[@]}" 2>/dev/null || true)
  printf ']}\n'
}

cmd_endpoints() {
  local subcommand="${1:-status}" role="${2:-all}"
  case "$subcommand" in
    status)
      [[ $# -le 1 ]] || die 'usage: kk endpoints [status]'
      printf 'Use "kk endpoints --json" for machine-readable endpoint state.\n'
      ;;
    rediscover)
      [[ $# -le 2 ]] || die 'usage: sudo kk endpoints rediscover [primary|secondary|all]'
      case "$role" in primary|secondary|all) ;; *) die "invalid endpoint role: $role" ;; esac
      require_root
      systemctl try-restart leshy-route-watch.service >/dev/null ||
        die 'failed to request endpoint rediscovery from leshy-route-watch.service'
      printf 'Endpoint rediscovery requested: %s\n' "$role"
      ;;
    invalidate)
      die 'generic endpoint cache invalidation is not supported by the active provider API'
      ;;
    *)
      die "unknown endpoints command: $subcommand"
      ;;
  esac
}

json_dispatch() {
  local command="$1"
  shift || true
  case "$command" in
    status) json_status "$@" ;;
    profiles) json_profiles "$@" ;;
    endpoints) json_endpoints "$@" ;;
    dns) json_dns "$@" ;;
    logs) json_logs "$@" ;;
    *) die "JSON mode is not supported for command: $command" ;;
  esac
}
