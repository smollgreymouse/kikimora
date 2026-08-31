# Status and interface inspection
#
# This file is sourced by the kikimora entrypoint.

cmd_status(){
  local verbose=0; [[ "${1:-}" == -v || "${1:-}" == --verbose ]] && { verbose=1; shift; }; [[ $# -eq 0 ]] || die 'usage: kk status [-v|--verbose]'; load_interfaces
  local ps ss ds def; ps="$(vpn_role_state primary "$PRIMARY_INTERFACE")"; ss="$(vpn_role_state secondary "$SECONDARY_INTERFACE")"; ds="$(interface_state leshy-dns0 dns)"; def="$(get_default_zone)"
  printf '== Services ==\n\nLeshy                 %s\nRoute watcher         %s\nHealth watcher        %s\n\n' "$(service_word leshy.service)" "$(service_word leshy-route-watch.service)" "$(service_word leshy-health-watch.service)"
  printf '== Interfaces ==\n\nRole        Interface    State\nPrimary     %-12s %s\nSecondary   %-12s %s\nDNS         %-12s %s\n\n' "$PRIMARY_INTERFACE" "$ps" "$SECONDARY_INTERFACE" "$ss" leshy-dns0 "$ds"
  printf '== DNS zones ==\n\nprimary (%s) — %s domains\nsecondary (%s) — %s domains\nbypass — %s domains\ndefault — %s\n\n' "$PRIMARY_INTERFACE" "$(normalized_domain_count "$PRIMARY_DOMAINS")" "$SECONDARY_INTERFACE" "$(normalized_domain_count "$SECONDARY_DOMAINS")" "$(normalized_domain_count "$BYPASS_DOMAINS")" "$def"
  if [[ -e "${ENDPOINT_STATE_DIR}/migration.pending" ]]; then
    printf 'Endpoint underlay migration is pending; disconnect both managed VPNs once.\n\n'
  fi
  printf '== Startup ==\n\n'; local u; for u in "${MANAGED_UNITS[@]}"; do printf '%-30s %s\n' "$u" "$(systemctl is-enabled "$u" 2>/dev/null || true)"; done
  if ((verbose)); then printf '\n== systemd details ==\n'; systemctl status --no-pager --full "${MANAGED_UNITS[@]}" || true; fi
}

cmd_interfaces(){
  [[ $# -eq 0 ]] || die 'usage: kk interfaces'; load_interfaces; printf 'Role        Interface    State              Addresses                         Routes  Domains\n'
  local role role_key iface state addrs routes domains
  for role in Primary Secondary; do if [[ "$role" == Primary ]]; then role_key=primary; iface="$PRIMARY_INTERFACE"; domains="$(normalized_domain_count "$PRIMARY_DOMAINS")"; else role_key=secondary; iface="$SECONDARY_INTERFACE"; domains="$(normalized_domain_count "$SECONDARY_DOMAINS")"; fi; state="$(vpn_role_state "$role_key" "$iface")"; addrs="$(ip -o addr show dev "$iface" scope global 2>/dev/null | awk '{print $4}' | paste -sd, -)"; [[ -n "$addrs" ]] || addrs=-; routes=$(( $(ip route show dev "$iface" 2>/dev/null | wc -l) + $(ip -6 route show dev "$iface" 2>/dev/null | wc -l) )); printf '%-11s %-12s %-18s %-33s %-7s %s\n' "$role" "$iface" "$state" "$addrs" "$routes" "$domains"; done
  printf '%-11s %-12s %-18s %-33s %-7s %s\n' DNS leshy-dns0 "$(interface_state leshy-dns0 dns)" - - -
}

diag_pick_domain() {
  local file="$1" fallback="$2" line
  if [[ -r "$file" ]]; then
    while IFS= read -r line || [[ -n "$line" ]]; do
      line="${line%%#*}"
      line="${line#"${line%%[![:space:]]*}"}"
      line="${line%"${line##*[![:space:]]}"}"
      [[ -n "$line" ]] || continue
      [[ "$line" != \*.* ]] || continue
      [[ "$line" != *[[:space:]/]* ]] || continue
      printf '%s\n' "$line"
      return 0
    done < "$file"
  fi
  printf '%s\n' "$fallback"
}

diag_run_kk() {
  local kk_bin="${KIKIMORA_DIAG_KK_BIN:-/usr/local/bin/kk}"
  if [[ -x "$kk_bin" ]]; then
    "$kk_bin" "$@"
  elif command -v kikimora >/dev/null 2>&1; then
    kikimora "$@"
  else
    printf 'kikimora CLI not found\n'
    return 127
  fi
}

cmd_diag() {
  case "${1:-}" in
    -h|--help)
      cat <<'HELP'
Usage: sudo kk diag [SECONDARY_DOMAIN]
Collect a self-contained full network diagnostic log for both managed VPN roles.

Without arguments, the primary probe domain is selected from primary.txt and the
secondary probe keeps the historical default gitlab.sca.ad-tech.ru. Passing a
DOMAIN overrides only the secondary probe target.
HELP
      return
      ;;
  esac

  [[ $# -le 1 ]] || die 'usage: sudo kk diag [SECONDARY_DOMAIN]'
  require_root
  load_interfaces

  local config_root="${KIKIMORA_DIAG_CONFIG_DIR:-/etc/kikimora/leshy}"
  local runtime_root="${KIKIMORA_DIAG_RUNTIME_DIR:-/run/kikimora/leshy/vpn}"
  local primary_domain secondary_domain
  primary_domain="$(diag_pick_domain "$config_root/domains/primary.txt" api.openai.com)"
  secondary_domain="${1:-gitlab.sca.ad-tech.ru}"
  [[ -n "$secondary_domain" && "$secondary_domain" != *[[:space:]/]* ]] || die "invalid domain: $secondary_domain"

  local output="${KIKIMORA_DIAG_OUTPUT:-/tmp/kikimora-diag-$(date +%Y%m%d-%H%M%S).log}"
  local samples="${KIKIMORA_DIAG_SAMPLES:-8}"
  local sample_interval="${KIKIMORA_DIAG_INTERVAL:-10}"
  [[ "$samples" =~ ^[1-9][0-9]*$ ]] || die "invalid diagnostic sample count: $samples"
  [[ "$sample_interval" =~ ^[0-9]+$ ]] || die "invalid diagnostic sample interval: $sample_interval"

  local tmp_dir
  tmp_dir="$(mktemp -d)"
  local before_all="$tmp_dir/routes-all-before"
  local -a roles=(primary secondary)
  local -A iface_by_role=(
    [primary]="$PRIMARY_INTERFACE"
    [secondary]="$SECONDARY_INTERFACE"
  )
  local -A domain_by_role=(
    [primary]="$primary_domain"
    [secondary]="$secondary_domain"
  )
  local -A before_routes=(
    [primary]="$tmp_dir/primary-routes-before"
    [secondary]="$tmp_dir/secondary-routes-before"
  )
  local -A after_routes=(
    [primary]="$tmp_dir/primary-routes-after"
    [secondary]="$tmp_dir/secondary-routes-after"
  )
  local -A ip_by_role=([primary]="" [secondary]="")

  install -d -m 0755 "$(dirname -- "$output")"
  : > "$output" || die "cannot write diagnostic log: $output"
  chmod 0600 "$output" 2>/dev/null || true

  printf 'Collecting full diagnostics: primary=%s via %s, secondary=%s via %s (about 2 minutes)...\n' \
    "$primary_domain" "$PRIMARY_INTERFACE" "$secondary_domain" "$SECONDARY_INTERFACE"

  (
    set +e

    printf '============================================================\n'
    printf ' KIKIMORA FULL NETWORK DIAGNOSTIC\n'
    printf '============================================================\n'
    printf 'Started: %s\n' "$(date -Is)"
    printf 'Host:    %s\n' "$(hostname)"
    printf 'Primary:   %s -> %s\n' "$primary_domain" "$PRIMARY_INTERFACE"
    printf 'Secondary: %s -> %s\n' "$secondary_domain" "$SECONDARY_INTERFACE"

    printf '\n===== SYSTEM =====\n'
    uname -a 2>&1 || true
    if [[ -r /etc/os-release ]]; then
      cat /etc/os-release
    fi
    hostnamectl 2>&1 || true
    timedatectl 2>&1 || true

    printf '\n===== KIKIMORA BEFORE DNS PROBES =====\n'
    diag_run_kk status 2>&1 || true
    printf '\n'
    diag_run_kk interfaces 2>&1 || true

    printf '\n===== VPN CONFIG =====\n'
    cat "$config_root/vpn.conf" 2>&1 || true

    printf '\n===== DOMAIN PROBE SELECTION =====\n'
    printf 'Primary domain: %s\n' "$primary_domain"
    grep -nF -- "$primary_domain" "$config_root/domains/primary.txt" 2>&1 \
      || printf '%s not found literally in primary.txt\n' "$primary_domain"
    printf 'Secondary domain: %s\n' "$secondary_domain"
    grep -nF -- "$secondary_domain" "$config_root/domains/secondary.txt" 2>&1 \
      || printf '%s not found literally in secondary.txt\n' "$secondary_domain"

    printf '\n===== RUNTIME DEVICE FILES =====\n'
    ls -la "$runtime_root" 2>&1 || true
    local f
    for f in "$runtime_root"/*.dev; do
      [[ -f "$f" ]] || continue
      printf '%s: ' "$f"
      cat "$f" 2>&1 || true
    done

    printf '\n===== NETWORKMANAGER =====\n'
    nmcli -f DEVICE,TYPE,STATE,CONNECTION device status 2>&1 || true
    printf '\n'
    nmcli -f NAME,TYPE,DEVICE connection show --active 2>&1 || true

    printf '\n===== ALL INTERFACES AND ADDRESSES =====\n'
    ip -details link show 2>&1 || true
    ip -4 addr show 2>&1 || true
    ip -6 addr show 2>&1 || true

    printf '\n===== MANAGED VPN INTERFACES =====\n'
    local role iface domain
    for role in "${roles[@]}"; do
      iface="${iface_by_role[$role]}"
      domain="${domain_by_role[$role]}"
      printf '\n--- %s: %s (%s) ---\n' "${role^^}" "$iface" "$domain"
      ip -details link show dev "$iface" 2>&1 || true
      ip -4 addr show dev "$iface" 2>&1 || true
      ip -6 addr show dev "$iface" 2>&1 || true
    done

    printf '\n===== ROUTING RULES =====\n'
    ip rule show 2>&1 || true
    ip -6 rule show 2>&1 || true

    printf '\n===== ROUTES BEFORE DNS PROBES =====\n'
    ip -4 route show table all >"$before_all" 2>&1
    for role in "${roles[@]}"; do
      iface="${iface_by_role[$role]}"
      printf '\n--- %s routes on %s ---\n' "${role^^}" "$iface"
      ip -4 route show dev "$iface" 2>&1 | tee "${before_routes[$role]}"
      ip -6 route show dev "$iface" 2>&1 || true
    done

    printf '\n===== NEIGHBORS =====\n'
    ip neigh show 2>&1 || true

    printf '\n===== SOCKETS BEFORE DNS =====\n'
    ss -tunap 2>&1 || true
    printf '\n--- SYN-SENT sockets ---\n'
    ss -tpn state syn-sent 2>&1 || true

    printf '\n===== OPENCONNECT / NETWORKMANAGER PROCESSES =====\n'
    pgrep -a -f 'openconnect|NetworkManager' 2>&1 || true

    printf '\n============================================================\n'
    printf ' CONTROLLED LESHY DNS + DATA-PLANE PROBES\n'
    printf '============================================================\n'

    local dig_out short_out ip
    for role in "${roles[@]}"; do
      iface="${iface_by_role[$role]}"
      domain="${domain_by_role[$role]}"
      printf '\n===== %s DNS PROBE: %s via %s =====\n' "${role^^}" "$domain" "$iface"
      printf 'Time before DNS: %s\n' "$(date -Is)"

      ip=''
      if command -v dig >/dev/null 2>&1; then
        dig_out="$(dig @127.0.0.1 -p 53053 "$domain" A +time=3 +tries=1 +noall +comments +answer 2>&1)"
        printf '%s\n' "$dig_out"
        ip="$(printf '%s\n' "$dig_out" | awk '$4 == "A" { print $5; exit }')"
        if [[ -z "$ip" ]]; then
          short_out="$(dig @127.0.0.1 -p 53053 "$domain" A +short +time=3 +tries=1 2>&1)"
          printf '%s\n' "$short_out"
          ip="$(printf '%s\n' "$short_out" | awk '/^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/ { print; exit }')"
        fi
      else
        printf 'dig not found\n'
      fi
      ip_by_role[$role]="$ip"

      printf 'Selected IPv4: %s\n' "${ip:-<none>}"
      printf 'Time after DNS: %s\n' "$(date -Is)"

      printf '\n===== %s ROUTE DIFF CAUSED BY DNS PROBE =====\n' "${role^^}"
      ip -4 route show dev "$iface" >"${after_routes[$role]}" 2>&1
      diff -u "${before_routes[$role]}" "${after_routes[$role]}" 2>&1 || true

      if [[ -n "$ip" ]]; then
        printf '\n===== %s ROUTE TO SELECTED IP =====\n' "${role^^}"
        ip -4 route show exact "$ip/32" 2>&1 || true
        ip -4 route get "$ip" 2>&1 || true
        printf '\nMatching route anywhere BEFORE controlled DNS:\n'
        grep -E "(^|[[:space:]])${ip//./\\.}(/32)?([[:space:]]|$)" "$before_all" 2>&1 \
          || printf 'NO matching route anywhere before controlled DNS probe\n'

        printf '\n===== %s DATA PLANE IMMEDIATELY AFTER DNS =====\n' "${role^^}"
        curl -k -sS -o /dev/null \
          --interface "$iface" \
          --resolve "$domain:443:$ip" \
          --connect-timeout 3 --max-time 5 \
          -w "${role^^} VPN CURL local=%{local_ip} remote=%{remote_ip} code=%{http_code} connect=%{time_connect}s tls=%{time_appconnect}s total=%{time_total}s\\n" \
          "https://$domain/" 2>&1 || true

        curl -k -sS -o /dev/null \
          --resolve "$domain:443:$ip" \
          --connect-timeout 3 --max-time 5 \
          -w "${role^^} KERNEL CURL local=%{local_ip} remote=%{remote_ip} code=%{http_code} connect=%{time_connect}s tls=%{time_appconnect}s total=%{time_total}s\\n" \
          "https://$domain/" 2>&1 || true
      else
        printf 'No IPv4 answer; fixed-IP data-plane tests skipped for %s.\n' "$role"
      fi
    done

    printf '\n============================================================\n'
    printf ' DUAL-ROLE RECOVERY WATCH\n'
    printf '============================================================\n'
    local i role_ip
    for ((i=1; i<=samples; i++)); do
      printf '\n----- SAMPLE %s/%s : %s -----\n' "$i" "$samples" "$(date -Is)"
      for role in "${roles[@]}"; do
        iface="${iface_by_role[$role]}"
        domain="${domain_by_role[$role]}"
        role_ip="${ip_by_role[$role]}"
        printf '\n--- %s (%s / %s) ---\n' "${role^^}" "$iface" "$domain"
        printf '%s.dev: ' "$role"
        cat "$runtime_root/$role.dev" 2>/dev/null || printf '<missing>\n'
        ip -br link show dev "$iface" 2>&1 || true
        ip -br -4 addr show dev "$iface" 2>&1 || true
        if [[ -n "$role_ip" ]]; then
          ip -4 route show exact "$role_ip/32" 2>&1 || true
          ip -4 route get "$role_ip" 2>&1 || true
          curl -k -sS -o /dev/null \
            --interface "$iface" \
            --resolve "$domain:443:$role_ip" \
            --connect-timeout 3 --max-time 4 \
            -w "${role^^} VPN CURL local=%{local_ip} remote=%{remote_ip} code=%{http_code} connect=%{time_connect}s tls=%{time_appconnect}s total=%{time_total}s\\n" \
            "https://$domain/" 2>&1 || true
        else
          printf 'No IPv4 selected for this role.\n'
        fi
      done
      if ((i < samples && sample_interval > 0)); then
        sleep "$sample_interval"
      fi
    done

    printf '\n============================================================\n'
    printf ' FINAL STATE AND JOURNALS\n'
    printf '============================================================\n'
    printf 'Time: %s\n' "$(date -Is)"

    printf '\n===== FINAL MANAGED ROUTES =====\n'
    for role in "${roles[@]}"; do
      iface="${iface_by_role[$role]}"
      printf '\n--- %s routes on %s ---\n' "${role^^}" "$iface"
      ip -4 route show dev "$iface" 2>&1 || true
      ip -6 route show dev "$iface" 2>&1 || true
    done

    printf '\n===== FINAL KIKIMORA =====\n'
    diag_run_kk status 2>&1 || true
    diag_run_kk interfaces 2>&1 || true

    printf '\n===== RESOLVER STATE =====\n'
    resolvectl status 2>&1 || true
    resolvectl dns 2>&1 || true
    resolvectl domain 2>&1 || true

    printf '\n===== MANAGED + NETWORKMANAGER JOURNAL, LAST 30 MIN =====\n'
    journalctl -b --since '-30 min' \
      -u leshy.service \
      -u leshy-route-watch.service \
      -u leshy-health-watch.service \
      -u NetworkManager.service \
      --no-pager -o short-iso 2>&1
    printf 'journalctl exit status: %s\n' "$?"

    printf '\n===== FULL SYSTEM JOURNAL, LAST 30 MIN =====\n'
    journalctl -b --since '-30 min' --no-pager -o short-iso 2>&1
    printf 'journalctl exit status: %s\n' "$?"

    printf '\n===== FULL KERNEL JOURNAL, LAST 30 MIN =====\n'
    journalctl -b -k --since '-30 min' --no-pager -o short-iso 2>&1
    printf 'journalctl exit status: %s\n' "$?"

    printf '\nFinished: %s\n' "$(date -Is)"
  ) >"$output" 2>&1

  rm -rf -- "$tmp_dir"
  chmod 0600 "$output" 2>/dev/null || true
  if [[ -n ${SUDO_UID:-} && -n ${SUDO_GID:-} ]]; then
    chown "$SUDO_UID:$SUDO_GID" "$output" 2>/dev/null || true
  fi

  printf 'FILE: %s\n' "$output"
}
