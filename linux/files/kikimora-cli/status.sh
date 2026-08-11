# Status and interface inspection
#
# This file is sourced by the kikimora entrypoint.

cmd_status(){
  local verbose=0; [[ "${1:-}" == -v || "${1:-}" == --verbose ]] && { verbose=1; shift; }; [[ $# -eq 0 ]] || die 'usage: kk status [-v|--verbose]'; load_interfaces
  local ps ss ds def; ps="$(vpn_role_state primary "$PRIMARY_INTERFACE")"; ss="$(vpn_role_state secondary "$SECONDARY_INTERFACE")"; ds="$(interface_state leshy-dns0 dns)"; def="$(get_default_zone)"
  printf '== Services ==

Leshy                 %s
Route watcher         %s
Health watcher        %s

' "$(service_word leshy.service)" "$(service_word leshy-route-watch.service)" "$(service_word leshy-health-watch.service)"
  printf '== Interfaces ==

Role        Interface    State
Primary     %-12s %s
Secondary   %-12s %s
DNS         %-12s %s

' "$PRIMARY_INTERFACE" "$ps" "$SECONDARY_INTERFACE" "$ss" leshy-dns0 "$ds"
  printf '== DNS zones ==

primary (%s) — %s domains
secondary (%s) — %s domains
bypass — %s domains
default — %s

' "$PRIMARY_INTERFACE" "$(normalized_domain_count "$PRIMARY_DOMAINS")" "$SECONDARY_INTERFACE" "$(normalized_domain_count "$SECONDARY_DOMAINS")" "$(normalized_domain_count "$BYPASS_DOMAINS")" "$def"
  if [[ -e "${ENDPOINT_STATE_DIR}/migration.pending" ]]; then
    printf 'Endpoint underlay migration is pending; disconnect both managed VPNs once.\n\n'
  fi
  printf '== Startup ==

'; local u; for u in "${MANAGED_UNITS[@]}"; do printf '%-30s %s
' "$u" "$(systemctl is-enabled "$u" 2>/dev/null || true)"; done
  if ((verbose)); then printf '
== systemd details ==
'; systemctl status --no-pager --full "${MANAGED_UNITS[@]}" || true; fi
}

cmd_interfaces(){
  [[ $# -eq 0 ]] || die 'usage: kk interfaces'; load_interfaces; printf 'Role        Interface    State              Addresses                         Routes  Domains
'
  local role role_key iface state addrs routes domains
  for role in Primary Secondary; do if [[ "$role" == Primary ]]; then role_key=primary; iface="$PRIMARY_INTERFACE"; domains="$(normalized_domain_count "$PRIMARY_DOMAINS")"; else role_key=secondary; iface="$SECONDARY_INTERFACE"; domains="$(normalized_domain_count "$SECONDARY_DOMAINS")"; fi; state="$(vpn_role_state "$role_key" "$iface")"; addrs="$(ip -o addr show dev "$iface" scope global 2>/dev/null | awk '{print $4}' | paste -sd, -)"; [[ -n "$addrs" ]] || addrs=-; routes=$(( $(ip route show dev "$iface" 2>/dev/null | wc -l) + $(ip -6 route show dev "$iface" 2>/dev/null | wc -l) )); printf '%-11s %-12s %-18s %-33s %-7s %s
' "$role" "$iface" "$state" "$addrs" "$routes" "$domains"; done
  printf '%-11s %-12s %-18s %-33s %-7s %s
' DNS leshy-dns0 "$(interface_state leshy-dns0 dns)" - - -
}

cmd_diag() {
  case "${1:-}" in
    -h|--help)
      printf 'Usage: sudo kk diag [DOMAIN]\nCollect a focused secondary-VPN diagnostic log.\nDefault DOMAIN: gitlab.sca.ad-tech.ru\n'
      return
      ;;
  esac

  [[ $# -le 1 ]] || die 'usage: sudo kk diag [DOMAIN]'
  require_root
  load_interfaces

  local domain="${1:-gitlab.sca.ad-tech.ru}"
  [[ -n "$domain" && "$domain" != *[[:space:]/]* ]] || die "invalid domain: $domain"

  local iface="$SECONDARY_INTERFACE"
  local output
  output="/tmp/kikimora-diag-$(date +%Y%m%d-%H%M%S).log"
  local before_routes before_all after_routes ip dig_out short_out
  before_routes="$(mktemp)"
  before_all="$(mktemp)"
  after_routes="$(mktemp)"

  printf 'Collecting secondary diagnostics for %s via %s (about 2 minutes)...\n' "$domain" "$iface"

  (
    set +e

    printf '============================================================\n'
    printf ' KIKIMORA SECONDARY DIAGNOSTIC\n'
    printf '============================================================\n'
    printf 'Started: %s\n' "$(date -Is)"
    printf 'Domain:  %s\n' "$domain"
    printf 'Host:    %s\n' "$(hostname)"
    printf 'Secondary interface: %s\n' "$iface"

    printf '\n===== KIKIMORA BEFORE DNS PROBE =====\n'
    /usr/local/bin/kk status 2>&1 || kikimora status 2>&1 || true
    printf '\n'
    /usr/local/bin/kk interfaces 2>&1 || kikimora interfaces 2>&1 || true

    printf '\n===== VPN CONFIG =====\n'
    cat /etc/kikimora/leshy/vpn.conf 2>&1 || true

    printf '\n===== SECONDARY DOMAIN ENTRY =====\n'
    grep -nF -- "$domain" /etc/kikimora/leshy/domains/secondary.txt 2>&1 \
      || printf '%s not found literally in secondary.txt\n' "$domain"

    printf '\n===== RUNTIME DEVICE FILES =====\n'
    ls -la /run/kikimora/leshy/vpn/ 2>&1 || true
    for f in /run/kikimora/leshy/vpn/*.dev; do
      [[ -f "$f" ]] || continue
      printf '%s: ' "$f"
      cat "$f" 2>&1 || true
    done

    printf '\n===== NETWORKMANAGER =====\n'
    nmcli -f DEVICE,TYPE,STATE,CONNECTION device status 2>&1 || true
    printf '\n'
    nmcli -f NAME,TYPE,DEVICE connection show --active 2>&1 || true

    printf '\n===== SECONDARY INTERFACE =====\n'
    ip -details link show dev "$iface" 2>&1 || true
    ip -4 addr show dev "$iface" 2>&1 || true
    ip -6 addr show dev "$iface" 2>&1 || true

    printf '\n===== RULES =====\n'
    ip rule show 2>&1 || true

    printf '\n===== ROUTES ON SECONDARY BEFORE DNS =====\n'
    ip -4 route show dev "$iface" 2>&1 | tee "$before_routes"
    ip -4 route show table all >"$before_all" 2>&1

    printf '\n===== BROWSER / OPENCONNECT SOCKETS BEFORE DNS =====\n'
    ss -tpn 2>&1 | grep -Ei 'chrome|chromium|openconnect' || true
    printf '\n--- SYN-SENT sockets ---\n'
    ss -tpn state syn-sent 2>&1 || true

    printf '\n===== OPENCONNECT PROCESSES =====\n'
    pgrep -a -f 'openconnect|NetworkManager' 2>&1 || true

    printf '\n============================================================\n'
    printf ' CONTROLLED LESHY DNS PROBE\n'
    printf '============================================================\n'
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

    printf 'Selected IPv4: %s\n' "${ip:-<none>}"
    printf 'Time after DNS: %s\n' "$(date -Is)"

    printf '\n===== SECONDARY ROUTE DIFF CAUSED BY DNS PROBE =====\n'
    ip -4 route show dev "$iface" >"$after_routes" 2>&1
    diff -u "$before_routes" "$after_routes" 2>&1 || true

    if [[ -n "$ip" ]]; then
      printf '\n===== ROUTE TO SELECTED IP AFTER DNS =====\n'
      ip -4 route show exact "$ip/32" 2>&1 || true
      ip -4 route get "$ip" 2>&1 || true
      printf '\nMatching route anywhere BEFORE controlled DNS:\n'
      grep -E "(^|[[:space:]])${ip//./\\.}(/32)?([[:space:]]|$)" "$before_all" 2>&1 \
        || printf 'NO matching route anywhere before controlled DNS probe\n'

      sleep 1
      printf '\n===== ROUTE ONE SECOND AFTER DNS =====\n'
      ip -4 route show exact "$ip/32" 2>&1 || true
      ip -4 route get "$ip" 2>&1 || true

      printf '\n===== DATA PLANE IMMEDIATELY AFTER DNS =====\n'
      curl -k -sS -o /dev/null \
        --interface "$iface" \
        --resolve "$domain:443:$ip" \
        --connect-timeout 3 --max-time 5 \
        -w 'VPN CURL local=%{local_ip} remote=%{remote_ip} code=%{http_code} connect=%{time_connect}s tls=%{time_appconnect}s total=%{time_total}s\n' \
        "https://$domain/" 2>&1 || true

      curl -k -sS -o /dev/null \
        --resolve "$domain:443:$ip" \
        --connect-timeout 3 --max-time 5 \
        -w 'KERNEL CURL local=%{local_ip} remote=%{remote_ip} code=%{http_code} connect=%{time_connect}s tls=%{time_appconnect}s total=%{time_total}s\n' \
        "https://$domain/" 2>&1 || true

      printf '\n============================================================\n'
      printf ' RECOVERY WATCH\n'
      printf '============================================================\n'
      for i in $(seq 1 8); do
        printf '\n----- SAMPLE %s/8 : %s -----\n' "$i" "$(date -Is)"
        printf 'secondary.dev: '
        cat /run/kikimora/leshy/vpn/secondary.dev 2>/dev/null || printf '<missing>\n'
        ip -4 route show exact "$ip/32" 2>&1 || true
        ip -4 route get "$ip" 2>&1 || true
        ip -br link show dev "$iface" 2>&1 || true
        ip -br -4 addr show dev "$iface" 2>&1 || true
        curl -k -sS -o /dev/null \
          --interface "$iface" \
          --resolve "$domain:443:$ip" \
          --connect-timeout 3 --max-time 4 \
          -w 'VPN CURL local=%{local_ip} remote=%{remote_ip} code=%{http_code} connect=%{time_connect}s tls=%{time_appconnect}s total=%{time_total}s\n' \
          "https://$domain/" 2>&1 || true
        ((i < 8)) && sleep 10
      done
    else
      printf '\nNo IPv4 answer; fixed-IP data-plane tests skipped.\n'
    fi

    printf '\n============================================================\n'
    printf ' FINAL STATE AND JOURNALS\n'
    printf '============================================================\n'
    printf 'Time: %s\n' "$(date -Is)"

    printf '\n===== FINAL SECONDARY ROUTES =====\n'
    ip -4 route show dev "$iface" 2>&1 || true

    printf '\n===== FINAL KIKIMORA =====\n'
    /usr/local/bin/kk status 2>&1 || kikimora status 2>&1 || true
    /usr/local/bin/kk interfaces 2>&1 || kikimora interfaces 2>&1 || true

    printf '\n===== RESOLVER STATE =====\n'
    resolvectl status 2>&1 || true

    printf '\n===== MANAGED + NETWORKMANAGER JOURNAL, LAST 30 MIN =====\n'
    journalctl -b --since '-30 min' \
      -u leshy.service \
      -u leshy-route-watch.service \
      -u leshy-health-watch.service \
      -u NetworkManager.service \
      --no-pager -o short-iso 2>&1 || true

    printf '\n===== FILTERED SYSTEM JOURNAL, LAST 30 MIN =====\n'
    journalctl -b --since '-30 min' --no-pager -o short-iso 2>&1 \
      | grep -Ei 'openconnect|vpn0|leshy|kikimora|route-lifecycle|route-watch|secondary|dead peer|CSTP|DTLS|NetworkManager.*vpn' \
      || true

    printf '\n===== FILTERED KERNEL JOURNAL, LAST 30 MIN =====\n'
    journalctl -b -k --since '-30 min' --no-pager -o short-iso 2>&1 \
      | grep -Ei 'vpn0|tun|route|network|link' \
      || true

    printf '\nFinished: %s\n' "$(date -Is)"
  ) >"$output" 2>&1

  rm -f -- "$before_routes" "$before_all" "$after_routes"
  chmod 0644 "$output" 2>/dev/null || true
  if [[ -n ${SUDO_UID:-} && -n ${SUDO_GID:-} ]]; then
    chown "$SUDO_UID:$SUDO_GID" "$output" 2>/dev/null || true
  fi

  printf 'FILE: %s\n' "$output"
}
