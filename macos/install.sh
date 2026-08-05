#!/bin/bash
set -Eeu -o pipefail

readonly EXPECTED_VERSION='0.4.0'
readonly KIKIMORA_VERSION='1.0.0'
readonly SOURCE_DIR="$(cd "$(dirname "$0")" && pwd)"
readonly LIBEXEC_DIR='/usr/local/libexec/kikimora/macos'
readonly CONFIG_DIR='/usr/local/etc/kikimora/leshy'
readonly DOMAINS_DIR="${CONFIG_DIR}/domains"
readonly ROUTES_DIR="${CONFIG_DIR}/routes"
readonly STATE_DIR='/var/db/kikimora/leshy'
readonly RUNTIME_DIR='/var/run/kikimora/leshy/vpn'
readonly PLIST_DIR='/Library/LaunchDaemons'
readonly CLI='/usr/local/sbin/kikimora'
readonly ALIAS='/usr/local/bin/kk'
readonly LESHY_BIN='/usr/local/bin/leshy'
readonly LABELS='com.kikimora.leshy com.kikimora.route-watch com.kikimora.health-watch'
readonly LEGACY_LABELS='io.github.smollgreymouse.kikimora.leshy io.github.smollgreymouse.kikimora.route-watch io.github.smollgreymouse.kikimora.health-watch'

primary_arg=''
secondary_arg=''
primary_service_arg=''
secondary_service_arg=''
leshy_binary_arg=''
legacy_config_arg=''
non_interactive=0
stop_allowed=0
start_after_install=0
dns_service_count=0
declare -a dns_services
work=''
commit_started=0
commit_finished=0
rollback_ready=0

die() { printf 'Error: %s\n' "$*" >&2; exit 1; }
section() { printf '\n==> %s\n' "$*"; }
cleanup() {
  status=$?
  if ((status != 0 && commit_started == 1 && commit_finished == 0 && rollback_ready == 1)); then rollback_commit; fi
  [[ -z $work || ! -d $work ]] || rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
Kikimora 1.0.0 — Leshy 0.4.0 installer for macOS

Usage:
  sudo ./install.sh [OPTIONS]

Options:
  --primary-interface NAME       Primary VPN interface (for example utun4)
  --secondary-interface NAME     Secondary VPN interface (for example utun5)
  --primary-vpn-service NAME     Resolve the current primary utun from this VPN service
  --secondary-vpn-service NAME   Resolve the current secondary utun from this VPN service
  --dns-service SERVICE          macOS network service managed by Kikimora; repeatable
  --leshy-binary PATH            Existing Leshy 0.4.0 binary to install
  --leshy-config PATH            Import an existing config.toml (legacy macOS option)
  --non-interactive              Do not prompt for missing values
  --stop-services, -y, --yes     Allow stopping an active Kikimora installation
  --start                        Start services after a successful install
  -h, --help                     Show this help

On reinstall, saved interfaces, DNS services, domain lists, route lists and
routing.conf are preserved. The installer does not connect either VPN.
EOF
}

while (($#)); do
  case "$1" in
    --primary-interface) (($# >= 2)) || die 'missing primary interface'; primary_arg="$2"; shift 2 ;;
    --primary-interface=*) primary_arg="${1#*=}"; shift ;;
    --secondary-interface) (($# >= 2)) || die 'missing secondary interface'; secondary_arg="$2"; shift 2 ;;
    --secondary-interface=*) secondary_arg="${1#*=}"; shift ;;
    --primary-vpn-service) (($# >= 2)) || die 'missing primary VPN service'; primary_service_arg="$2"; shift 2 ;;
    --primary-vpn-service=*) primary_service_arg="${1#*=}"; shift ;;
    --secondary-vpn-service) (($# >= 2)) || die 'missing secondary VPN service'; secondary_service_arg="$2"; shift 2 ;;
    --secondary-vpn-service=*) secondary_service_arg="${1#*=}"; shift ;;
    --dns-service) (($# >= 2)) || die 'missing DNS service'; dns_services[$dns_service_count]="$2"; dns_service_count=$((dns_service_count + 1)); shift 2 ;;
    --dns-service=*) dns_services[$dns_service_count]="${1#*=}"; dns_service_count=$((dns_service_count + 1)); shift ;;
    --leshy-binary) (($# >= 2)) || die 'missing Leshy binary'; leshy_binary_arg="$2"; shift 2 ;;
    --leshy-binary=*) leshy_binary_arg="${1#*=}"; shift ;;
    --leshy-config) (($# >= 2)) || die 'missing Leshy config'; legacy_config_arg="$2"; shift 2 ;;
    --leshy-config=*) legacy_config_arg="${1#*=}"; shift ;;
    --non-interactive) non_interactive=1; shift ;;
    --stop-services|-y|--yes) stop_allowed=1; shift ;;
    --start) start_after_install=1; shift ;;
    -h|--help) usage; exit 0 ;;
    --) shift; (($# == 0)) || die "unexpected arguments: $*" ;;
    *) die "unknown option: $1" ;;
  esac
done

[[ $(uname -s) == Darwin ]] || die 'macOS is required'
[[ $(id -u) -eq 0 ]] || die 'run via sudo'
had_system_plists=0
for label in $LABELS; do [[ -e $PLIST_DIR/$label.plist ]] && had_system_plists=1; done

read_saved_value() {
  local file="$1" name="$2"
  [[ -r $file ]] || return 0
  /bin/bash -c 'source "$1"; name="$2"; printf "%s\n" "${!name:-}"' _ "$file" "$name"
}

valid_interface() {
  [[ -n $1 && ${#1} -le 15 && $1 =~ ^[[:alnum:]_.:-]+$ && $1 != . && $1 != .. ]]
}

valid_vpn_service() {
  [[ -n $1 ]] || return 1
  /usr/sbin/scutil --nc list 2>/dev/null | grep -Fq -- "\"$1\""
}

prompt_value() {
  local prompt="$1" default_value="${2:-}" value=''
  if [[ -n $default_value ]]; then printf '%s [%s]: ' "$prompt" "$default_value" > /dev/tty; else printf '%s: ' "$prompt" > /dev/tty; fi
  IFS= read -r value < /dev/tty || die 'failed to read interactive value'
  printf '%s\n' "${value:-$default_value}"
}

saved_primary="$(read_saved_value "$CONFIG_DIR/vpn.conf" PRIMARY_INTERFACE || true)"
saved_secondary="$(read_saved_value "$CONFIG_DIR/vpn.conf" SECONDARY_INTERFACE || true)"
saved_primary_service="$(read_saved_value "$CONFIG_DIR/vpn.conf" PRIMARY_VPN_SERVICE || true)"
saved_secondary_service="$(read_saved_value "$CONFIG_DIR/vpn.conf" SECONDARY_VPN_SERVICE || true)"
primary="${primary_arg:-$saved_primary}"
secondary="${secondary_arg:-$saved_secondary}"
primary_service="${primary_service_arg:-$saved_primary_service}"
secondary_service="${secondary_service_arg:-$saved_secondary_service}"
if { [[ -z $primary ]] && [[ -z $primary_service ]]; } || { [[ -z $secondary ]] && [[ -z $secondary_service ]]; }; then
  ((non_interactive == 0)) && [[ -e /dev/tty ]] || die 'pass both VPN interface options on a clean non-interactive install'
  [[ -n $primary || -n $primary_service ]] || primary="$(prompt_value 'Primary VPN interface' 'utun4')"
  [[ -n $secondary || -n $secondary_service ]] || secondary="$(prompt_value 'Secondary VPN interface' 'utun5')"
fi
[[ -z $primary ]] || valid_interface "$primary" || die "invalid primary interface: $primary"
[[ -z $secondary ]] || valid_interface "$secondary" || die "invalid secondary interface: $secondary"
[[ -z $primary || -z $secondary || $primary != "$secondary" ]] || die 'VPN interfaces must differ'
[[ -z $primary_service || -z $secondary_service || $primary_service != "$secondary_service" ]] || die 'VPN services must differ'
[[ -z $primary_service ]] || valid_vpn_service "$primary_service" || die "unknown primary VPN service: $primary_service"
[[ -z $secondary_service ]] || valid_vpn_service "$secondary_service" || die "unknown secondary VPN service: $secondary_service"

if ((dns_service_count == 0)) && [[ -r $CONFIG_DIR/macos.conf ]]; then
  while IFS= read -r service; do
    [[ -n $service ]] || continue
    dns_services[$dns_service_count]="$service"
    dns_service_count=$((dns_service_count + 1))
  done < <(/bin/bash -c 'source "$1"; printf "%s\n" "${DNS_SERVICES[@]}"' _ "$CONFIG_DIR/macos.conf")
fi
if ((dns_service_count == 0)); then
  ((non_interactive == 0)) && [[ -e /dev/tty ]] || die 'pass at least one --dns-service'
  dns_services[0]="$(prompt_value 'Network service whose DNS Kikimora should manage' 'Wi-Fi')"
  dns_service_count=1
fi
for service in "${dns_services[@]}"; do
  networksetup -getdnsservers "$service" >/dev/null || die "unknown network service: $service"
done

active=0
for label in $LABELS $LEGACY_LABELS; do /bin/launchctl print "system/$label" >/dev/null 2>&1 && active=1 || true; done
if ((active)); then
  if ((stop_allowed == 0)); then
    ((non_interactive == 0)) && [[ -e /dev/tty ]] || die 'services are active; retry with --stop-services'
    answer="$(prompt_value 'Kikimora is running. Stop it before installation? [Y/n]' 'Y')"
    case "$answer" in y|Y|yes|YES) ;; *) die 'installation cancelled' ;; esac
  fi
  [[ -x $LIBEXEC_DIR/leshy-dns ]] && "$LIBEXEC_DIR/leshy-dns" suspend || true
  for label in com.kikimora.health-watch com.kikimora.route-watch com.kikimora.leshy \
    io.github.smollgreymouse.kikimora.health-watch io.github.smollgreymouse.kikimora.route-watch io.github.smollgreymouse.kikimora.leshy; do
    /bin/launchctl bootout "system/$label" >/dev/null 2>&1 || true
  done
fi

required='lib.sh reconcile route-lifecycle route-watch health-watch leshy-dns leshy-service build-config check-config kikimora install.sh com.kikimora.leshy.plist com.kikimora.route-watch.plist com.kikimora.health-watch.plist'
for file in $required; do [[ -f $SOURCE_DIR/$file ]] || die "package file missing: macos/$file"; done
for file in "$SOURCE_DIR"/*.sh "$SOURCE_DIR"/reconcile "$SOURCE_DIR"/route-lifecycle "$SOURCE_DIR"/route-watch "$SOURCE_DIR"/health-watch "$SOURCE_DIR"/leshy-dns "$SOURCE_DIR"/leshy-service "$SOURCE_DIR"/build-config "$SOURCE_DIR"/check-config "$SOURCE_DIR"/kikimora; do /bin/bash -n "$file"; done
plutil -lint "$SOURCE_DIR"/*.plist >/dev/null

leshy_source="$LESHY_BIN"
if [[ ! -x $leshy_source ]]; then
  if [[ -n $leshy_binary_arg ]]; then leshy_source="$leshy_binary_arg"
  elif [[ -x $SOURCE_DIR/leshy ]]; then leshy_source="$SOURCE_DIR/leshy"
  elif [[ -n ${SUDO_USER:-} && -x /Users/${SUDO_USER}/.cargo/bin/leshy ]]; then leshy_source="/Users/${SUDO_USER}/.cargo/bin/leshy"
  else die 'Leshy not found; pass --leshy-binary PATH'; fi
fi
[[ -x $leshy_source ]] || die "not executable: $leshy_source"
version="$($leshy_source --version 2>/dev/null || true)"
[[ $version == "leshy $EXPECTED_VERSION" ]] || die "expected leshy $EXPECTED_VERSION, got: ${version:-no output}"

work="$(mktemp -d /tmp/kikimora-install.XXXXXX)"
install -d -m 0755 "$work/domains" "$work/routes"
for name in primary secondary bypass; do
  if [[ -r $DOMAINS_DIR/$name.txt ]]; then cp "$DOMAINS_DIR/$name.txt" "$work/domains/$name.txt"; else cp "$SOURCE_DIR/domains/$name.txt" "$work/domains/$name.txt"; fi
done
for name in primary secondary; do
  if [[ -r $ROUTES_DIR/$name.txt ]]; then cp "$ROUTES_DIR/$name.txt" "$work/routes/$name.txt"; else cp "$SOURCE_DIR/routes/$name.txt" "$work/routes/$name.txt"; fi
done
if [[ -r $CONFIG_DIR/routing.conf ]]; then cp "$CONFIG_DIR/routing.conf" "$work/routing.conf"; else cp "$SOURCE_DIR/routing.conf.example" "$work/routing.conf"; fi

extract_zone_array() {
  local config="$1" zone="$2" field="$3" output="$4"
  awk -v wanted="$zone" -v field="$field" '
    function trim(s) { gsub(/^[[:space:]]+|[[:space:]]+$/, "", s); return s }
    /^\[\[zones\]\]/ { in_zone=0; in_array=0; next }
    /^[[:space:]]*name[[:space:]]*=/ {
      line=$0; sub(/^[^=]*=/, "", line); line=trim(line); gsub(/^"|"$/, "", line)
      in_zone=(line == wanted); in_array=0; next
    }
    in_zone && $0 ~ "^[[:space:]]*" field "[[:space:]]*=" { in_array=1; next }
    in_zone && in_array && /^[[:space:]]*]/ { exit }
    in_zone && in_array {
      line=$0; sub(/#.*/, "", line); gsub(/[",]/, "", line); line=trim(line)
      if (line != "") print tolower(line)
    }
  ' "$config" | LC_ALL=C sort -u > "$output"
}

if [[ -r $CONFIG_DIR/config.toml && ! -d $DOMAINS_DIR ]]; then
  section 'Migrating the earlier macOS configuration to domain and route lists'
  extract_zone_array "$CONFIG_DIR/config.toml" primary domains "$work/domains/primary.txt"
  extract_zone_array "$CONFIG_DIR/config.toml" secondary domains "$work/domains/secondary.txt"
  extract_zone_array "$CONFIG_DIR/config.toml" primary static_routes "$work/routes/primary.txt"
  extract_zone_array "$CONFIG_DIR/config.toml" secondary static_routes "$work/routes/secondary.txt"
  if grep -Fq 'name = "default-primary"' "$CONFIG_DIR/config.toml"; then migrated_default=primary
  elif grep -Fq 'name = "default-secondary"' "$CONFIG_DIR/config.toml"; then migrated_default=secondary
  elif grep -Fq 'name = "default-deny"' "$CONFIG_DIR/config.toml"; then migrated_default=none
  else migrated_default=direct; fi
  if [[ $migrated_default != direct ]]; then
    extract_zone_array "$CONFIG_DIR/config.toml" "default-${migrated_default/none/deny}" domains "$work/default.exclusions"
    cat "$work/domains/primary.txt" "$work/domains/secondary.txt" | LC_ALL=C sort -u > "$work/explicit.domains"
    comm -23 "$work/default.exclusions" "$work/explicit.domains" > "$work/domains/bypass.txt"
  fi
  migrated_upstreams="$(awk '
    /^[[:space:]]*default_upstream[[:space:]]*=/ { active=1 }
    active { line=$0; while (match(line, /"[^"]+"/)) { value=substr(line,RSTART+1,RLENGTH-2); printf "%s%s", sep, value; sep=" "; line=substr(line,RSTART+RLENGTH) } }
    active && /]/ { exit }
  ' "$CONFIG_DIR/config.toml")"
  [[ -n $migrated_upstreams ]] || migrated_upstreams='1.1.1.1:53 8.8.8.8:53'
  cat > "$work/routing.conf" <<EOF_MIGRATION
DEFAULT_ZONE=$migrated_default
DEFAULT_UPSTREAMS="$migrated_upstreams"
UPSTREAM_ZONE=direct
EOF_MIGRATION
fi

cat > "$work/vpn.conf" <<EOF_VPN
# Created by Kikimora ${KIKIMORA_VERSION} for Leshy ${EXPECTED_VERSION} on macOS.
PRIMARY_INTERFACE="$primary"
PRIMARY_VPN_SERVICE="$primary_service"
PRIMARY_DEVICE_FILE="/var/run/kikimora/leshy/vpn/primary.dev"

SECONDARY_INTERFACE="$secondary"
SECONDARY_VPN_SERVICE="$secondary_service"
SECONDARY_DEVICE_FILE="/var/run/kikimora/leshy/vpn/secondary.dev"

VPN_LINK_READY_SUCCESSES=3
EOF_VPN
printf 'DNS_SERVICES=(' > "$work/macos.conf"
for service in "${dns_services[@]}"; do printf ' %q' "$service" >> "$work/macos.conf"; done
printf ' )\n' >> "$work/macos.conf"

section 'Generating and validating configuration'
if [[ -n $legacy_config_arg ]]; then
  [[ -r $legacy_config_arg ]] || die "cannot read --leshy-config: $legacy_config_arg"
  awk '
    /^[[:space:]]*listen_address[[:space:]]*=/ && !done { print "listen_address = \"127.0.0.1:53\""; done=1; next }
    { print }
    END { if (!done) exit 42 }
  ' "$legacy_config_arg" > "$work/config.toml" || die 'imported config has no listen_address'
else
  /bin/bash "$SOURCE_DIR/build-config" "$work/domains" "$work/config.toml" "$work/routing.conf" "$work/routes"
fi
/bin/bash "$SOURCE_DIR/check-config" "$leshy_source" "$work/config.toml"

backup_file() {
  local target="$1" stamp backup
  [[ -e $target || -L $target ]] || return 0
  stamp="$(date '+%Y%m%d-%H%M%S')"; backup="${target}.backup-${stamp}"
  [[ ! -e $backup ]] || backup="${backup}-$$"
  cp -pR "$target" "$backup"
  printf 'Backup: %s\n' "$backup"
}
install_managed() { backup_file "$2"; install -o root -g wheel -m "$3" "$1" "$2"; }

snapshot_target() {
  local index="$1" target="$2" dir="$work/rollback/$index"
  mkdir -p "$dir"
  if [[ -e $target || -L $target ]]; then
    printf 'present\n' > "$dir/state"
    cp -pR "$target" "$dir/content"
  else
    printf 'absent\n' > "$dir/state"
  fi
}

restore_target() {
  local index="$1" target="$2" dir="$work/rollback/$index" state
  [[ -r $dir/state ]] || return 0
  IFS= read -r state < "$dir/state"
  rm -rf "$target"
  if [[ $state == present ]]; then
    mkdir -p "$(dirname "$target")"
    cp -pR "$dir/content" "$target"
  fi
}

rollback_commit() {
  local index
  printf '\nInstall failed; restoring previous macOS managed files...\n' >&2
  set +e
  for ((index=${#TRANSACTION_TARGETS[@]}-1; index>=0; index--)); do restore_target "$index" "${TRANSACTION_TARGETS[$index]}"; done
  set -e
  printf 'Rollback complete. Services remain stopped.\n' >&2
}

readonly -a TRANSACTION_TARGETS=(
  "$LESHY_BIN" "$CLI" "$ALIAS" /usr/local/sbin/leshy-dns "$STATE_DIR/installation.env"
  "$CONFIG_DIR/vpn.conf" "$CONFIG_DIR/macos.conf" "$CONFIG_DIR/routing.conf" "$CONFIG_DIR/config.toml"
  "$DOMAINS_DIR/primary.txt" "$DOMAINS_DIR/secondary.txt" "$DOMAINS_DIR/bypass.txt"
  "$ROUTES_DIR/primary.txt" "$ROUTES_DIR/secondary.txt"
  "$PLIST_DIR/com.kikimora.leshy.plist" "$PLIST_DIR/com.kikimora.route-watch.plist" "$PLIST_DIR/com.kikimora.health-watch.plist"
  "$LIBEXEC_DIR/lib.sh" "$LIBEXEC_DIR/reconcile" "$LIBEXEC_DIR/route-lifecycle" "$LIBEXEC_DIR/route-watch"
  "$LIBEXEC_DIR/health-watch" "$LIBEXEC_DIR/leshy-dns" "$LIBEXEC_DIR/leshy-service" "$LIBEXEC_DIR/build-config"
  "$LIBEXEC_DIR/check-config" "$LIBEXEC_DIR/install.sh" "$LIBEXEC_DIR/kikimora" "$LIBEXEC_DIR/routing.conf.example"
  "$LIBEXEC_DIR/com.kikimora.leshy.plist" "$LIBEXEC_DIR/com.kikimora.route-watch.plist" "$LIBEXEC_DIR/com.kikimora.health-watch.plist"
  "$LIBEXEC_DIR/domains/primary.txt" "$LIBEXEC_DIR/domains/secondary.txt" "$LIBEXEC_DIR/domains/bypass.txt"
  "$LIBEXEC_DIR/routes/primary.txt" "$LIBEXEC_DIR/routes/secondary.txt"
)

section 'Preparing rollback snapshot'
for index in "${!TRANSACTION_TARGETS[@]}"; do snapshot_target "$index" "${TRANSACTION_TARGETS[$index]}"; done
rollback_ready=1

section 'Installing validated macOS package'
commit_started=1
install -d -o root -g wheel -m 0755 /usr/local/bin /usr/local/sbin "$LIBEXEC_DIR" "$LIBEXEC_DIR/domains" "$LIBEXEC_DIR/routes" "$CONFIG_DIR" "$DOMAINS_DIR" "$ROUTES_DIR" "$STATE_DIR" "$RUNTIME_DIR" /var/log/kikimora "$PLIST_DIR"
if [[ $leshy_source != "$LESHY_BIN" ]]; then install_managed "$leshy_source" "$LESHY_BIN" 0755; fi
for script in lib.sh reconcile route-lifecycle route-watch health-watch leshy-dns leshy-service build-config check-config install.sh; do install_managed "$SOURCE_DIR/$script" "$LIBEXEC_DIR/$script" 0755; done
install_managed "$SOURCE_DIR/kikimora" "$LIBEXEC_DIR/kikimora" 0755
install_managed "$SOURCE_DIR/kikimora" "$CLI" 0755
ln -sfn "$CLI" "$ALIAS"
install_managed "$SOURCE_DIR/leshy-dns" /usr/local/sbin/leshy-dns 0755
for plist in "$SOURCE_DIR"/*.plist; do
  install_managed "$plist" "$LIBEXEC_DIR/$(basename "$plist")" 0644
  if ((had_system_plists)); then install_managed "$plist" "$PLIST_DIR/$(basename "$plist")" 0644; fi
done
if ((had_system_plists == 0)); then
  for label in $LABELS; do /bin/launchctl enable "system/$label" >/dev/null 2>&1 || true; done
fi
install_managed "$SOURCE_DIR/routing.conf.example" "$LIBEXEC_DIR/routing.conf.example" 0644
for name in primary secondary bypass; do install_managed "$SOURCE_DIR/domains/$name.txt" "$LIBEXEC_DIR/domains/$name.txt" 0644; done
for name in primary secondary; do install_managed "$SOURCE_DIR/routes/$name.txt" "$LIBEXEC_DIR/routes/$name.txt" 0644; done
for name in primary secondary bypass; do install_managed "$work/domains/$name.txt" "$DOMAINS_DIR/$name.txt" 0644; done
for name in primary secondary; do install_managed "$work/routes/$name.txt" "$ROUTES_DIR/$name.txt" 0644; done
install_managed "$work/vpn.conf" "$CONFIG_DIR/vpn.conf" 0644
install_managed "$work/macos.conf" "$CONFIG_DIR/macos.conf" 0644
install_managed "$work/routing.conf" "$CONFIG_DIR/routing.conf" 0644
install_managed "$work/config.toml" "$CONFIG_DIR/config.toml" 0644

cat > "$STATE_DIR/installation.env" <<EOF_STATE
KIKIMORA_VERSION=$KIKIMORA_VERSION
LESHY_VERSION=$EXPECTED_VERSION
INSTALLED_PATH=$LESHY_BIN
SHA256=$(shasum -a 256 "$LESHY_BIN" | awk '{print $1}')
OBSERVED_AT=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
EOF_STATE
chmod 0644 "$STATE_DIR/installation.env"

KIKIMORA_CONFIG_DIR="$CONFIG_DIR" KIKIMORA_STATE_DIR="$STATE_DIR" KIKIMORA_RUNTIME_DIR="$RUNTIME_DIR" "$LIBEXEC_DIR/reconcile"
plutil -lint "$LIBEXEC_DIR"/com.kikimora.*.plist >/dev/null
if ((had_system_plists)); then plutil -lint "$PLIST_DIR"/com.kikimora.*.plist >/dev/null; fi
"$LIBEXEC_DIR/check-config" "$LESHY_BIN" "$CONFIG_DIR/config.toml"
commit_finished=1

if ((start_after_install)); then "$CLI" start; fi
printf '\nKikimora %s for macOS installed.\n' "$KIKIMORA_VERSION"
printf 'Primary: %s; secondary: %s.\n' "${primary_service:-$primary}" "${secondary_service:-$secondary}"
printf 'Leshy listens on 127.0.0.1:53 so macOS network services can use it directly.\n'
if ((start_after_install == 0)); then
  printf 'Services were not started or enabled. Use sudo kk start or sudo kk enable --now.\n'
fi
printf 'System DNS changes only after sudo kk start or sudo kk dns enable.\n'
printf 'VPN connections were not changed.\n'
