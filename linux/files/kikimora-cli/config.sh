# Configuration and VPN interface profile management
#
# This file is sourced by the kikimora entrypoint.

vpn_config_path() {
  printf '%s\n' "${KIKIMORA_VPN_CONFIG:-/etc/kikimora/leshy/vpn.conf}"
}

profiles_config_path() {
  printf '%s\n' "${KIKIMORA_PROFILE_CONFIG:-/etc/kikimora/leshy/profiles.conf}"
}

validate_profile_name() {
  local name="$1"
  [[ -n "$name" ]] || die 'profile name is empty'
  ((${#name} <= 64)) || die "profile name is too long: $name"
  [[ "$name" =~ ^[[:alnum:]_.-]+$ ]] || die "invalid profile name: $name"
  [[ "$name" != "." && "$name" != ".." ]] || die "invalid profile name: $name"
}

validate_profile_interface() {
  local iface="$1"
  [[ -n "$iface" ]] || die 'VPN interface name is empty'
  ((${#iface} <= 15)) || die "VPN interface name is longer than 15 characters: $iface"
  [[ "$iface" =~ ^[[:alnum:]_.:-]+$ ]] || die "invalid VPN interface name: $iface"
  [[ "$iface" != "." && "$iface" != ".." ]] || die "invalid VPN interface name: $iface"
}

validate_profile_pair() {
  local primary="$1" secondary="$2"
  validate_profile_interface "$primary"
  validate_profile_interface "$secondary"
  [[ "$primary" != "$secondary" ]] || die "primary and secondary VPN interfaces must differ: $primary"
}

validate_endpoint_provider() {
  local provider="$1"
  [[ -n "$provider" ]] || die 'endpoint provider name is empty'
  ((${#provider} <= 64)) || die "endpoint provider name is too long: $provider"
  [[ "$provider" =~ ^[[:alnum:]][[:alnum:]_.-]*$ ]] || die "invalid endpoint provider name: $provider"
}

validate_endpoint_provider_args() {
  local args="$1"
  ((${#args} <= 1024)) || die 'endpoint provider args are longer than 1024 characters'
  [[ "$args" != *$'\n'* && "$args" != *$'\r'* ]] || die 'endpoint provider args must be a single line'
}

read_vpn_setting() {
  local modern_name="$1" legacy_name="$2" default_value="$3" config
  config="$(vpn_config_path)"
  [[ -r "$config" ]] || die "VPN config not found: $config"

  bash -c '
    set -Eeuo pipefail
    source "$1"
    modern_name="$2"
    legacy_name="$3"
    default_value="$4"
    value="${!modern_name-}"
    if [[ -z "$value" && -n "$legacy_name" ]]; then
      value="${!legacy_name-}"
    fi
    [[ -n "$value" ]] || value="$default_value"
    printf "%s\n" "$value"
  ' _ "$config" "$modern_name" "$legacy_name" "$default_value"
}

read_current_primary_interface() {
  read_vpn_setting PRIMARY_INTERFACE AMN_IFACE ''
}

read_current_secondary_interface() {
  read_vpn_setting SECONDARY_INTERFACE VPN_IFACE ''
}

read_current_primary_provider() {
  read_vpn_setting PRIMARY_ENDPOINT_PROVIDER '' static
}

read_current_secondary_provider() {
  read_vpn_setting SECONDARY_ENDPOINT_PROVIDER '' static
}

read_current_primary_provider_args() {
  read_vpn_setting PRIMARY_ENDPOINT_PROVIDER_ARGS '' ''
}

read_current_secondary_provider_args() {
  read_vpn_setting SECONDARY_ENDPOINT_PROVIDER_ARGS '' ''
}

load_vpn_profiles() {
  local config current_primary current_secondary current_primary_provider current_secondary_provider
  local current_primary_args current_secondary_args name primary secondary primary_provider secondary_provider
  local primary_args secondary_args state
  local -A seen_states=()

  config="$(profiles_config_path)"
  current_primary="$(read_current_primary_interface)"
  current_secondary="$(read_current_secondary_interface)"
  current_primary_provider="$(read_current_primary_provider)"
  current_secondary_provider="$(read_current_secondary_provider)"
  current_primary_args="$(read_current_primary_provider_args)"
  current_secondary_args="$(read_current_secondary_provider_args)"
  validate_profile_pair "$current_primary" "$current_secondary"
  validate_endpoint_provider "$current_primary_provider"
  validate_endpoint_provider "$current_secondary_provider"
  validate_endpoint_provider_args "$current_primary_args"
  validate_endpoint_provider_args "$current_secondary_args"

  declare -gA VPN_PROFILE_PRIMARY=()
  declare -gA VPN_PROFILE_SECONDARY=()
  declare -gA VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER=()
  declare -gA VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER=()
  declare -gA VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS=()
  declare -gA VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS=()

  # Compatibility with the first draft of PR #13. A legacy entry described only
  # PRIMARY_INTERFACE; its secondary side was intentionally shared.
  declare -gA PRIMARY_PROFILES=()
  # 0=current, 1=primary-only draft, 2=pair format without provider metadata.
  declare -g PROFILE_CONFIG_LEGACY=0

  if [[ -r "$config" ]]; then
    # shellcheck source=/etc/kikimora/leshy/profiles.conf
    source "$config"
  else
    VPN_PROFILE_PRIMARY[default]="$current_primary"
    VPN_PROFILE_SECONDARY[default]="$current_secondary"
    VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER[default]="$current_primary_provider"
    VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER[default]="$current_secondary_provider"
    VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS[default]="$current_primary_args"
    VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS[default]="$current_secondary_args"
  fi

  if ((${#VPN_PROFILE_PRIMARY[@]} == 0 && ${#VPN_PROFILE_SECONDARY[@]} == 0 && ${#PRIMARY_PROFILES[@]} > 0)); then
    PROFILE_CONFIG_LEGACY=1
    for name in "${!PRIMARY_PROFILES[@]}"; do
      VPN_PROFILE_PRIMARY["$name"]="${PRIMARY_PROFILES[$name]}"
      VPN_PROFILE_SECONDARY["$name"]="$current_secondary"
    done
  fi

  ((${#VPN_PROFILE_PRIMARY[@]} > 0)) || die 'no VPN profiles configured'
  ((${#VPN_PROFILE_PRIMARY[@]} == ${#VPN_PROFILE_SECONDARY[@]})) || die 'profile store has mismatched primary/secondary mappings'

  # Pair-only profiles predate endpoint providers. Preserve the provider state of
  # the currently selected pair and default every other historical profile to
  # static, which is exactly the old behavior.
  for name in "${!VPN_PROFILE_PRIMARY[@]}"; do
    [[ -n "${VPN_PROFILE_SECONDARY[$name]+x}" ]] || die "profile $name has no secondary interface"
    primary="${VPN_PROFILE_PRIMARY[$name]}"
    secondary="${VPN_PROFILE_SECONDARY[$name]}"

    if [[ -z "${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER[$name]+x}" ]]; then
      if [[ "$primary" == "$current_primary" && "$secondary" == "$current_secondary" ]]; then
        VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER["$name"]="$current_primary_provider"
        VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS["$name"]="$current_primary_args"
      else
        VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER["$name"]='static'
        VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS["$name"]=''
      fi
      [[ $PROFILE_CONFIG_LEGACY -eq 1 ]] || PROFILE_CONFIG_LEGACY=2
    fi
    if [[ -z "${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER[$name]+x}" ]]; then
      if [[ "$primary" == "$current_primary" && "$secondary" == "$current_secondary" ]]; then
        VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER["$name"]="$current_secondary_provider"
        VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS["$name"]="$current_secondary_args"
      else
        VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER["$name"]='static'
        VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS["$name"]=''
      fi
      [[ $PROFILE_CONFIG_LEGACY -eq 1 ]] || PROFILE_CONFIG_LEGACY=2
    fi
    [[ -n "${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS[$name]+x}" ]] || VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS["$name"]=''
    [[ -n "${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS[$name]+x}" ]] || VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS["$name"]=''

    primary_provider="${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER[$name]}"
    secondary_provider="${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER[$name]}"
    primary_args="${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS[$name]}"
    secondary_args="${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS[$name]}"

    validate_profile_name "$name"
    validate_profile_pair "$primary" "$secondary"
    validate_endpoint_provider "$primary_provider"
    validate_endpoint_provider "$secondary_provider"
    validate_endpoint_provider_args "$primary_args"
    validate_endpoint_provider_args "$secondary_args"

    state="${primary}"$'\t'"${secondary}"$'\t'"${primary_provider}"$'\t'"${primary_args}"$'\t'"${secondary_provider}"$'\t'"${secondary_args}"
    [[ -z "${seen_states[$state]:-}" ]] || \
      die "profiles $name and ${seen_states[$state]} describe the same VPN interfaces and endpoint providers"
    seen_states[$state]="$name"
  done

  for name in "${!VPN_PROFILE_SECONDARY[@]}"; do
    [[ -n "${VPN_PROFILE_PRIMARY[$name]+x}" ]] || die "profile $name has no primary interface"
  done
  for name in "${!VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER[@]}"; do
    [[ -n "${VPN_PROFILE_PRIMARY[$name]+x}" ]] || die "endpoint-provider metadata exists for unknown profile: $name"
  done
  for name in "${!VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER[@]}"; do
    [[ -n "${VPN_PROFILE_PRIMARY[$name]+x}" ]] || die "endpoint-provider metadata exists for unknown profile: $name"
  done
}

write_vpn_profiles() {
  local config tmp name
  config="$(profiles_config_path)"
  install -d -m 0755 "$(dirname -- "$config")"
  tmp="$(mktemp "${config}.tmp.XXXXXX")"

  {
    printf '# Managed by Kikimora. Each profile selects both VPN role interfaces and endpoint providers.\n'
    printf '# Domain lists, route lists and static endpoint files remain shared.\n\n'
    for array_name in \
      VPN_PROFILE_PRIMARY \
      VPN_PROFILE_SECONDARY \
      VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER \
      VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER \
      VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS \
      VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS; do
      printf 'declare -Ag %s=(\n' "$array_name"
      while IFS= read -r name; do
        case "$array_name" in
          VPN_PROFILE_PRIMARY) value="${VPN_PROFILE_PRIMARY[$name]}" ;;
          VPN_PROFILE_SECONDARY) value="${VPN_PROFILE_SECONDARY[$name]}" ;;
          VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER) value="${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER[$name]}" ;;
          VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER) value="${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER[$name]}" ;;
          VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS) value="${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS[$name]}" ;;
          VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS) value="${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS[$name]}" ;;
        esac
        printf '    [%q]=%q\n' "$name" "$value"
      done < <(printf '%s\n' "${!VPN_PROFILE_PRIMARY[@]}" | LC_ALL=C sort)
      printf ')\n\n'
    done
  } >"$tmp"

  chmod 0644 "$tmp"
  mv -f -- "$tmp" "$config"
  PROFILE_CONFIG_LEGACY=0
}

replace_current_profile_state() {
  local primary="$1" secondary="$2" primary_provider="$3" primary_args="$4" secondary_provider="$5" secondary_args="$6"
  local config tmp mode owner group line primary_seen=0 secondary_seen=0
  local primary_provider_seen=0 primary_args_seen=0 secondary_provider_seen=0 secondary_args_seen=0

  config="$(vpn_config_path)"
  [[ -r "$config" ]] || die "VPN config not found: $config"
  validate_profile_pair "$primary" "$secondary"
  validate_endpoint_provider "$primary_provider"
  validate_endpoint_provider "$secondary_provider"
  validate_endpoint_provider_args "$primary_args"
  validate_endpoint_provider_args "$secondary_args"

  tmp="$(mktemp "${config}.tmp.XXXXXX")"
  while IFS= read -r line || [[ -n "$line" ]]; do
    case "$line" in
      PRIMARY_INTERFACE=*) printf 'PRIMARY_INTERFACE=%q\n' "$primary" >>"$tmp"; primary_seen=1 ;;
      AMN_IFACE=*) printf 'AMN_IFACE=%q\n' "$primary" >>"$tmp"; primary_seen=1 ;;
      SECONDARY_INTERFACE=*) printf 'SECONDARY_INTERFACE=%q\n' "$secondary" >>"$tmp"; secondary_seen=1 ;;
      VPN_IFACE=*) printf 'VPN_IFACE=%q\n' "$secondary" >>"$tmp"; secondary_seen=1 ;;
      PRIMARY_ENDPOINT_PROVIDER=*) printf 'PRIMARY_ENDPOINT_PROVIDER=%q\n' "$primary_provider" >>"$tmp"; primary_provider_seen=1 ;;
      PRIMARY_ENDPOINT_PROVIDER_ARGS=*) printf 'PRIMARY_ENDPOINT_PROVIDER_ARGS=%q\n' "$primary_args" >>"$tmp"; primary_args_seen=1 ;;
      SECONDARY_ENDPOINT_PROVIDER=*) printf 'SECONDARY_ENDPOINT_PROVIDER=%q\n' "$secondary_provider" >>"$tmp"; secondary_provider_seen=1 ;;
      SECONDARY_ENDPOINT_PROVIDER_ARGS=*) printf 'SECONDARY_ENDPOINT_PROVIDER_ARGS=%q\n' "$secondary_args" >>"$tmp"; secondary_args_seen=1 ;;
      *) printf '%s\n' "$line" >>"$tmp" ;;
    esac
  done <"$config"

  if ((primary_seen == 0 || secondary_seen == 0)); then
    rm -f -- "$tmp"
    die "PRIMARY_INTERFACE/SECONDARY_INTERFACE are not both defined in $config"
  fi

  ((primary_provider_seen == 1)) || printf 'PRIMARY_ENDPOINT_PROVIDER=%q\n' "$primary_provider" >>"$tmp"
  ((primary_args_seen == 1)) || printf 'PRIMARY_ENDPOINT_PROVIDER_ARGS=%q\n' "$primary_args" >>"$tmp"
  ((secondary_provider_seen == 1)) || printf 'SECONDARY_ENDPOINT_PROVIDER=%q\n' "$secondary_provider" >>"$tmp"
  ((secondary_args_seen == 1)) || printf 'SECONDARY_ENDPOINT_PROVIDER_ARGS=%q\n' "$secondary_args" >>"$tmp"

  mode="$(stat -c '%a' "$config")"
  owner="$(stat -c '%u' "$config")"
  group="$(stat -c '%g' "$config")"
  chmod "$mode" "$tmp"
  chown "$owner:$group" "$tmp"
  mv -f -- "$tmp" "$config"
}

profile_for_state() {
  local wanted_primary="$1" wanted_secondary="$2" wanted_primary_provider="$3" wanted_primary_args="$4"
  local wanted_secondary_provider="$5" wanted_secondary_args="$6" name
  for name in "${!VPN_PROFILE_PRIMARY[@]}"; do
    if [[ "${VPN_PROFILE_PRIMARY[$name]}" == "$wanted_primary" && \
          "${VPN_PROFILE_SECONDARY[$name]}" == "$wanted_secondary" && \
          "${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER[$name]}" == "$wanted_primary_provider" && \
          "${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS[$name]}" == "$wanted_primary_args" && \
          "${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER[$name]}" == "$wanted_secondary_provider" && \
          "${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS[$name]}" == "$wanted_secondary_args" ]]; then
      printf '%s\n' "$name"
      return 0
    fi
  done
  return 1
}

show_profiles() {
  local current_primary current_secondary current_primary_provider current_secondary_provider
  local current_primary_args current_secondary_args active name marker config
  current_primary="$(read_current_primary_interface)"
  current_secondary="$(read_current_secondary_interface)"
  current_primary_provider="$(read_current_primary_provider)"
  current_secondary_provider="$(read_current_secondary_provider)"
  current_primary_args="$(read_current_primary_provider_args)"
  current_secondary_args="$(read_current_secondary_provider_args)"
  load_vpn_profiles
  active="$(profile_for_state "$current_primary" "$current_secondary" "$current_primary_provider" "$current_primary_args" "$current_secondary_provider" "$current_secondary_args" || true)"
  config="$(profiles_config_path)"

  if [[ -n "$active" ]]; then
    printf 'Active profile: %s (%s[%s] / %s[%s])\n\n' "$active" "$current_primary" "$current_primary_provider" "$current_secondary" "$current_secondary_provider"
  else
    printf 'Active profile: unmanaged (%s[%s] / %s[%s])\n\n' "$current_primary" "$current_primary_provider" "$current_secondary" "$current_secondary_provider"
  fi

  printf '  %-20s %-15s %-14s %-15s %-14s\n' 'Profile' 'Primary' 'P.Provider' 'Secondary' 'S.Provider'
  while IFS= read -r name; do
    marker=' '
    [[ "$name" == "$active" ]] && marker='*'
    printf '%s %-20s %-15s %-14s %-15s %-14s\n' \
      "$marker" "$name" "${VPN_PROFILE_PRIMARY[$name]}" "${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER[$name]}" \
      "${VPN_PROFILE_SECONDARY[$name]}" "${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER[$name]}"
    if [[ -n "${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS[$name]}" || -n "${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS[$name]}" ]]; then
      printf '    provider args: primary=%q secondary=%q\n' \
        "${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS[$name]}" "${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS[$name]}"
    fi
  done < <(printf '%s\n' "${!VPN_PROFILE_PRIMARY[@]}" | LC_ALL=C sort)

  if [[ ! -r "$config" ]]; then
    printf '\nProfile storage is not initialized yet; the first profiles add command will preserve the current complete VPN state as profile default.\n'
  elif ((PROFILE_CONFIG_LEGACY == 1)); then
    printf '\nLegacy primary-only profile storage detected; the next modifying profile command will migrate it.\n'
  elif ((PROFILE_CONFIG_LEGACY == 2)); then
    printf '\nLegacy pair-only profile storage detected; the next modifying profile command will add endpoint-provider metadata.\n'
  fi
  printf '\nDomain and static-route lists are shared. Endpoint provider selection belongs to each profile role; static providers read the shared role endpoint files.\n'
}

cmd_profiles() {
  local subcommand="${1:-list}" name primary secondary current_primary current_secondary active existing
  local current_primary_provider current_secondary_provider current_primary_args current_secondary_args
  local primary_provider secondary_provider primary_args secondary_args
  local override_primary_provider='' override_secondary_provider='' override_primary_args='' override_secondary_args=''
  local primary_args_set=0 secondary_args_set=0
  [[ $# -eq 0 ]] || shift

  case "$subcommand" in
    list|status)
      [[ $# -eq 0 ]] || die 'usage: kikimora profiles list'
      show_profiles
      ;;
    add)
      [[ $# -ge 2 ]] || die 'usage: sudo kikimora profiles add NAME PRIMARY_INTERFACE [SECONDARY_INTERFACE] [provider options]'
      require_root
      name="$1"
      primary="$2"
      shift 2
      secondary='-'
      if (($# > 0)) && [[ "$1" != --* ]]; then
        secondary="$1"
        shift
      fi
      validate_profile_name "$name"

      current_primary="$(read_current_primary_interface)"
      current_secondary="$(read_current_secondary_interface)"
      current_primary_provider="$(read_current_primary_provider)"
      current_secondary_provider="$(read_current_secondary_provider)"
      current_primary_args="$(read_current_primary_provider_args)"
      current_secondary_args="$(read_current_secondary_provider_args)"

      if [[ "$primary" == '-' ]]; then
        primary="$current_primary"
        primary_provider="$current_primary_provider"
        primary_args="$current_primary_args"
      else
        primary_provider='static'
        primary_args=''
      fi
      if [[ "$secondary" == '-' ]]; then
        secondary="$current_secondary"
        secondary_provider="$current_secondary_provider"
        secondary_args="$current_secondary_args"
      else
        secondary_provider='static'
        secondary_args=''
      fi

      while (($#)); do
        case "$1" in
          --primary-provider)
            (($# >= 2)) || die '--primary-provider requires a provider name'
            override_primary_provider="$2"
            shift 2
            ;;
          --primary-provider=*) override_primary_provider="${1#*=}"; shift ;;
          --secondary-provider)
            (($# >= 2)) || die '--secondary-provider requires a provider name'
            override_secondary_provider="$2"
            shift 2
            ;;
          --secondary-provider=*) override_secondary_provider="${1#*=}"; shift ;;
          --primary-provider-args)
            (($# >= 2)) || die '--primary-provider-args requires one argument string'
            override_primary_args="$2"
            primary_args_set=1
            shift 2
            ;;
          --primary-provider-args=*) override_primary_args="${1#*=}"; primary_args_set=1; shift ;;
          --secondary-provider-args)
            (($# >= 2)) || die '--secondary-provider-args requires one argument string'
            override_secondary_args="$2"
            secondary_args_set=1
            shift 2
            ;;
          --secondary-provider-args=*) override_secondary_args="${1#*=}"; secondary_args_set=1; shift ;;
          *) die "unknown profiles add option: $1" ;;
        esac
      done

      if [[ -n "$override_primary_provider" ]]; then
        primary_provider="$override_primary_provider"
        ((primary_args_set == 1)) || primary_args=''
      fi
      if [[ -n "$override_secondary_provider" ]]; then
        secondary_provider="$override_secondary_provider"
        ((secondary_args_set == 1)) || secondary_args=''
      fi
      ((primary_args_set == 0)) || primary_args="$override_primary_args"
      ((secondary_args_set == 0)) || secondary_args="$override_secondary_args"

      validate_profile_pair "$primary" "$secondary"
      validate_endpoint_provider "$primary_provider"
      validate_endpoint_provider "$secondary_provider"
      validate_endpoint_provider_args "$primary_args"
      validate_endpoint_provider_args "$secondary_args"
      load_vpn_profiles
      [[ -z "${VPN_PROFILE_PRIMARY[$name]+x}" ]] || die "profile already exists: $name"
      existing="$(profile_for_state "$primary" "$secondary" "$primary_provider" "$primary_args" "$secondary_provider" "$secondary_args" || true)"
      [[ -z "$existing" ]] || die "the same VPN interfaces and endpoint providers already belong to profile: $existing"

      VPN_PROFILE_PRIMARY["$name"]="$primary"
      VPN_PROFILE_SECONDARY["$name"]="$secondary"
      VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER["$name"]="$primary_provider"
      VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER["$name"]="$secondary_provider"
      VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS["$name"]="$primary_args"
      VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS["$name"]="$secondary_args"
      write_vpn_profiles
      printf 'Profile added: %s -> primary=%s[%s] secondary=%s[%s]\n' "$name" "$primary" "$primary_provider" "$secondary" "$secondary_provider"
      ;;
    use)
      [[ $# -eq 1 ]] || die 'usage: sudo kikimora profiles use NAME'
      require_root
      name="$1"
      validate_profile_name "$name"
      load_vpn_profiles
      [[ -n "${VPN_PROFILE_PRIMARY[$name]+x}" ]] || die "profile not found: $name"
      primary="${VPN_PROFILE_PRIMARY[$name]}"
      secondary="${VPN_PROFILE_SECONDARY[$name]}"
      primary_provider="${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER[$name]}"
      secondary_provider="${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER[$name]}"
      primary_args="${VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS[$name]}"
      secondary_args="${VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS[$name]}"

      current_primary="$(read_current_primary_interface)"
      current_secondary="$(read_current_secondary_interface)"
      current_primary_provider="$(read_current_primary_provider)"
      current_secondary_provider="$(read_current_secondary_provider)"
      current_primary_args="$(read_current_primary_provider_args)"
      current_secondary_args="$(read_current_secondary_provider_args)"
      if [[ "$current_primary" == "$primary" && "$current_secondary" == "$secondary" && \
            "$current_primary_provider" == "$primary_provider" && "$current_primary_args" == "$primary_args" && \
            "$current_secondary_provider" == "$secondary_provider" && "$current_secondary_args" == "$secondary_args" ]]; then
        printf 'Profile already active: %s (%s[%s] / %s[%s])\n' "$name" "$primary" "$primary_provider" "$secondary" "$secondary_provider"
        return 0
      fi

      replace_current_profile_state "$primary" "$secondary" "$primary_provider" "$primary_args" "$secondary_provider" "$secondary_args"
      [[ $PROFILE_CONFIG_LEGACY -eq 0 ]] || write_vpn_profiles
      printf 'Active VPN profile changed to %s (primary=%s[%s] secondary=%s[%s]).\n' "$name" "$primary" "$primary_provider" "$secondary" "$secondary_provider"
      printf 'Route watcher will reconcile changed interfaces and endpoint providers on its next cycle.\n'
      printf 'Existing Leshy-owned destinations on a withdrawn VPN are handled by fail-closed route parking.\n'
      ;;
    remove)
      [[ $# -eq 1 ]] || die 'usage: sudo kikimora profiles remove NAME'
      require_root
      name="$1"
      validate_profile_name "$name"
      load_vpn_profiles
      [[ -n "${VPN_PROFILE_PRIMARY[$name]+x}" ]] || die "profile not found: $name"
      current_primary="$(read_current_primary_interface)"
      current_secondary="$(read_current_secondary_interface)"
      current_primary_provider="$(read_current_primary_provider)"
      current_secondary_provider="$(read_current_secondary_provider)"
      current_primary_args="$(read_current_primary_provider_args)"
      current_secondary_args="$(read_current_secondary_provider_args)"
      active="$(profile_for_state "$current_primary" "$current_secondary" "$current_primary_provider" "$current_primary_args" "$current_secondary_provider" "$current_secondary_args" || true)"
      [[ "$active" != "$name" ]] || die 'cannot remove the active profile; switch profiles first'
      ((${#VPN_PROFILE_PRIMARY[@]} > 1)) || die 'cannot remove the last profile'
      unset 'VPN_PROFILE_PRIMARY[$name]'
      unset 'VPN_PROFILE_SECONDARY[$name]'
      unset 'VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER[$name]'
      unset 'VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER[$name]'
      unset 'VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS[$name]'
      unset 'VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS[$name]'
      write_vpn_profiles
      printf 'Profile removed: %s\n' "$name"
      ;;
    help|-h|--help)
      cat <<'HELP'
Usage: kikimora profiles COMMAND

Commands:
  profiles
      List VPN profiles including role-local endpoint providers.

  profiles add NAME PRIMARY [SECONDARY] [OPTIONS]
      Add a profile. Omitted SECONDARY keeps the current secondary role.
      Use '-' for either interface to keep that current role, including its
      current endpoint provider and provider args.

      --primary-provider NAME
      --secondary-provider NAME
      --primary-provider-args ARG
      --secondary-provider-args ARG

  profiles use NAME
      Atomically switch both interfaces and both endpoint-provider selections.

  profiles remove NAME
      Remove an inactive profile.

A newly specified interface defaults to endpoint provider `static`. A role kept
with '-' (or an omitted secondary) keeps its current provider unless explicitly
overridden. Built-in providers are static, happ and command.

Examples:
  sudo kikimora profiles add office amn1
  sudo kikimora profiles add backup - vpn1
  sudo kikimora profiles add happ-test amn1 tun0 --secondary-provider happ
  sudo kikimora profiles add custom amn1 tun0 --secondary-provider command \
      --secondary-provider-args /usr/local/libexec/my-endpoint-provider

Profile switching also participates in PR #16 endpoint-underlay safety and
fail-closed route parking. See docs/endpoint-providers.md for the provider API.
HELP
      ;;
    *) die "unknown profiles command: $subcommand" ;;
  esac
}

cmd_config() {
  local subcommand="${1:-show}"
  shift || true
  case "$subcommand" in
    show)
      [[ $# -eq 0 ]] || die "unexpected arguments for kikimora config show"
      printf '%s\n' '== /etc/kikimora/leshy/profiles.conf =='
      if [[ -r "$(profiles_config_path)" ]]; then
        cat "$(profiles_config_path)"
      else
        printf '(not initialized; current vpn.conf state acts as profile default)\n'
      fi
      printf '\n%s\n' '== /etc/kikimora/leshy/vpn.conf =='
      cat "$(vpn_config_path)"
      printf '\n%s\n' '== Static VPN endpoint files =='
      local endpoint_file
      for endpoint_file in \
        /etc/kikimora/leshy/endpoints/primary.txt \
        /etc/kikimora/leshy/endpoints/secondary.txt; do
        printf '%s\n' "--- ${endpoint_file} ---"
        if [[ -r "$endpoint_file" ]]; then
          cat "$endpoint_file"
        else
          printf '(missing; reinstall/upgrade or restart route-watch to recreate the empty template)\n'
        fi
      done
      printf '\n%s\n' '== /etc/kikimora/leshy/config.toml =='
      cat /etc/kikimora/leshy/config.toml
      ;;
    edit)
      [[ $# -eq 0 ]] || die "unexpected arguments for kikimora config edit"
      require_root
      local editor="${SUDO_EDITOR:-${EDITOR:-vi}}"
      "$editor" "$(vpn_config_path)"
      /usr/local/libexec/kikimora/leshy/build-config
      /usr/local/libexec/kikimora/leshy/check-config /usr/local/bin/leshy /etc/kikimora/leshy/config.toml
      printf 'Configuration updated and validated. To apply: sudo kikimora restart\n'
      printf 'If you edited interface or endpoint-provider assignments directly, kikimora profiles may report the state as unmanaged.\n'
      ;;
    validate)
      [[ $# -eq 0 ]] || die "unexpected arguments for kikimora config validate"
      /usr/local/libexec/kikimora/leshy/check-config /usr/local/bin/leshy /etc/kikimora/leshy/config.toml
      ;;
    help|-h|--help)
      cat <<'HELP'
Usage: kikimora config COMMAND

Commands:
  show       Show profiles.conf, vpn.conf, static VPN endpoint files and generated config.toml
  edit       Edit vpn.conf, rebuild and validate config.toml
  validate   Validate current config.toml

VPN interfaces and endpoint providers are managed together with `kikimora profiles`.
The built-in static provider reads exact endpoint hostnames/IPs from:
  /etc/kikimora/leshy/endpoints/primary.txt
  /etc/kikimora/leshy/endpoints/secondary.txt

See docs/endpoint-providers.md for dynamic and custom providers.
HELP
      ;;
    *) die "unknown config command: $subcommand" ;;
  esac
}
