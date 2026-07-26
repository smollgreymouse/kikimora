# Status and interface inspection
#
# This file is sourced by the kikimora entrypoint.

cmd_status(){
  local verbose=0; [[ "${1:-}" == -v || "${1:-}" == --verbose ]] && { verbose=1; shift; }; [[ $# -eq 0 ]] || die 'usage: kk status [-v|--verbose]'; load_interfaces
  local ps ss ds def; ps="$(interface_state "$PRIMARY_INTERFACE" vpn)"; ss="$(interface_state "$SECONDARY_INTERFACE" vpn)"; ds="$(interface_state leshy-dns0 dns)"; def="$(get_default_zone)"
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
  printf '== Startup ==

'; local u; for u in "${MANAGED_UNITS[@]}"; do printf '%-30s %s
' "$u" "$(systemctl is-enabled "$u" 2>/dev/null || true)"; done
  if ((verbose)); then printf '
== systemd details ==
'; systemctl status --no-pager --full "${MANAGED_UNITS[@]}" || true; fi
}

cmd_interfaces(){
  [[ $# -eq 0 ]] || die 'usage: kk interfaces'; load_interfaces; printf 'Role        Interface    State    Addresses                         Routes  Domains
'
  local role iface state addrs routes domains
  for role in Primary Secondary; do if [[ "$role" == Primary ]]; then iface="$PRIMARY_INTERFACE"; domains="$(normalized_domain_count "$PRIMARY_DOMAINS")"; else iface="$SECONDARY_INTERFACE"; domains="$(normalized_domain_count "$SECONDARY_DOMAINS")"; fi; state="$(interface_state "$iface" vpn)"; addrs="$(ip -o addr show dev "$iface" scope global 2>/dev/null | awk '{print $4}' | paste -sd, -)"; [[ -n "$addrs" ]] || addrs=-; routes=$(( $(ip route show dev "$iface" 2>/dev/null | wc -l) + $(ip -6 route show dev "$iface" 2>/dev/null | wc -l) )); printf '%-11s %-12s %-8s %-33s %-7s %s
' "$role" "$iface" "$state" "$addrs" "$routes" "$domains"; done
  printf '%-11s %-12s %-8s %-33s %-7s %s
' DNS leshy-dns0 "$(interface_state leshy-dns0 dns)" - - -
}