#!/bin/bash
set -eu

readonly SOURCE_DIR="$(cd "$(dirname "$0")" && pwd)"
readonly LIBEXEC_DIR='/usr/local/libexec/kikimora/macos'
readonly CONFIG_DIR='/usr/local/etc/kikimora/leshy'
readonly PLIST_DIR='/Library/LaunchDaemons'
readonly CLI='/usr/local/sbin/kikimora'
readonly ALIAS='/usr/local/bin/kk'
primary=''
secondary=''
leshy_config=''
start_after_install=0
declare -a dns_services=()

die() { printf 'Error: %s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'EOF'
Usage: sudo ./macos/install.sh --primary-interface IFACE --secondary-interface IFACE \
  --dns-service SERVICE [--dns-service SERVICE ...] --leshy-config PATH [--start]

The installer does not install Leshy. It deploys the macOS launchd, DNS and
VPN-interface orchestration around an existing Leshy binary.
EOF
}
valid_name() { [[ $1 =~ ^[[:alnum:]_.:-]+$ ]]; }

while (($#)); do
  case "$1" in
    --primary-interface) primary="${2:?--primary-interface requires a value}"; shift 2 ;;
    --secondary-interface) secondary="${2:?--secondary-interface requires a value}"; shift 2 ;;
    --dns-service) dns_services+=("${2:?--dns-service requires a value}"); shift 2 ;;
    --leshy-config) leshy_config="${2:?--leshy-config requires a path}"; shift 2 ;;
    --start) start_after_install=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

[[ $(uname -s) == Darwin ]] || die 'macOS is required'
[[ $(id -u) -eq 0 ]] || die 'run via sudo'
valid_name "$primary" || die 'invalid --primary-interface'
valid_name "$secondary" || die 'invalid --secondary-interface'
[[ $primary != "$secondary" ]] || die 'VPN interfaces must differ'
((${#dns_services[@]} > 0)) || die 'at least one --dns-service is required'
[[ -x /usr/local/bin/leshy ]] || die 'install Leshy at /usr/local/bin/leshy first'
[[ -r $leshy_config ]] || die 'readable --leshy-config is required'
for service in "${dns_services[@]}"; do
  networksetup -getdnsservers "$service" >/dev/null || die "unknown network service: $service"
done

plutil -lint "$SOURCE_DIR"/*.plist >/dev/null
for script in lib.sh reconcile route-watch leshy-dns health-watch kikimora install.sh; do
  bash -n "$SOURCE_DIR/$script"
done

install -d -m 0755 "$LIBEXEC_DIR" "$CONFIG_DIR" /var/log/kikimora /var/db/kikimora/leshy
for script in lib.sh reconcile route-watch leshy-dns health-watch kikimora-macos; do
  install -m 0755 "$SOURCE_DIR/$script" "$LIBEXEC_DIR/$script"
done
install -m 0755 "$SOURCE_DIR/kikimora" "$CLI"
ln -sfn "$CLI" "$ALIAS"
for plist in "$SOURCE_DIR"/*.plist; do install -m 0644 "$plist" "$PLIST_DIR/$(basename "$plist")"; done

cat > "$CONFIG_DIR/vpn.conf" <<EOF
PRIMARY_INTERFACE="$primary"
PRIMARY_DEVICE_FILE="/var/run/kikimora/leshy/vpn/primary.dev"
SECONDARY_INTERFACE="$secondary"
SECONDARY_DEVICE_FILE="/var/run/kikimora/leshy/vpn/secondary.dev"
EOF
{
  printf 'DNS_SERVICES=('
  for service in "${dns_services[@]}"; do printf ' %q' "$service"; done
  printf ' )\n'
} > "$CONFIG_DIR/macos.conf"
if [[ ! -e "$CONFIG_DIR/config.toml" || ! "$leshy_config" -ef "$CONFIG_DIR/config.toml" ]]; then
  install -m 0644 "$leshy_config" "$CONFIG_DIR/config.toml"
fi

if ((start_after_install)); then "$CLI" start; fi
printf 'Installed. Use: sudo kikimora start (or sudo kk start)\n'
