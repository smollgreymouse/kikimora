#!/bin/bash
set -eu

readonly SOURCE_DIR="$(cd "$(dirname "$0")" && pwd)"
readonly LIBEXEC_DIR='/usr/local/libexec/kikimora/macos'
readonly CONFIG_DIR='/usr/local/etc/kikimora/leshy'
readonly PLIST_DIR='/Library/LaunchDaemons'
readonly CLI='/usr/local/sbin/kikimora'
readonly ALIAS='/usr/local/bin/kk'
readonly LESHY_BIN='/usr/local/bin/leshy'

# Leshy release the installer provisions automatically when no local binary
# is found. Asset names are stable per tag: leshy-macos-<arch>.tar.gz.
readonly LESHY_RELEASE_TAG='v0.5.1'
readonly LESHY_RELEASE_BASE_URL="https://github.com/smollgreymouse/leshy/releases/download/${LESHY_RELEASE_TAG}"
readonly EXPECTED_VERSION='0.5.1'

primary=''
secondary=''
leshy_config=''
leshy_download_url=''
leshy_download_disabled=0
start_after_install=0
declare -a dns_services=()

die() { printf 'Error: %s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'EOF'
Usage: sudo ./macos/install.sh --primary-interface IFACE --secondary-interface IFACE \
  --dns-service SERVICE [--dns-service SERVICE ...] --leshy-config PATH [--start]
  [--leshy-download-url URL] [--no-leshy-download]

Deploys the macOS launchd, DNS and VPN-interface orchestration. When
/usr/local/bin/leshy is absent, the installer downloads the pinned Leshy
release (smollgreymouse/leshy v0.5.1) for this architecture; point
--leshy-download-url at another archive to override, or pass
--no-leshy-download to require a locally installed Leshy.
EOF
}
valid_name() { [[ $1 =~ ^[[:alnum:]_.:-]+$ ]]; }

while (($#)); do
  case "$1" in
    --primary-interface) primary="${2:?--primary-interface requires a value}"; shift 2 ;;
    --secondary-interface) secondary="${2:?--secondary-interface requires a value}"; shift 2 ;;
    --dns-service) dns_services+=("${2:?--dns-service requires a value}"); shift 2 ;;
    --leshy-config) leshy_config="${2:?--leshy-config requires a path}"; shift 2 ;;
    --leshy-download-url) leshy_download_url="${2:?--leshy-download-url requires a value}"; shift 2 ;;
    --no-leshy-download) leshy_download_disabled=1; shift ;;
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

# --- Leshy binary provisioning ---------------------------------------------
if [[ -z $leshy_download_url ]]; then
  case "$(uname -m)" in
    arm64) leshy_download_url="${LESHY_RELEASE_BASE_URL}/leshy-macos-aarch64.tar.gz" ;;
    x86_64) leshy_download_url="${LESHY_RELEASE_BASE_URL}/leshy-macos-x86_64.tar.gz" ;;
    *) die "unsupported macOS architecture: $(uname -m); install Leshy at ${LESHY_BIN} manually" ;;
  esac
fi

if [[ ! -x $LESHY_BIN ]]; then
  ((leshy_download_disabled)) && die "install Leshy at ${LESHY_BIN} first (download disabled by --no-leshy-download)"
  tmp_dir="$(mktemp -d /tmp/leshy-download.XXXXXX)"
  trap 'rm -rf "$tmp_dir"' EXIT
  tarball="${tmp_dir}/leshy.tar.gz"
  printf 'Downloading Leshy %s: %s\n' "${EXPECTED_VERSION}" "$leshy_download_url"
  curl -fsSL --retry 3 -o "$tarball" "$leshy_download_url" || die "Leshy download failed: $leshy_download_url"
  tar -xzf "$tarball" -C "$tmp_dir" || die 'Leshy archive extraction failed'
  [[ -x "${tmp_dir}/leshy" ]] || die 'Leshy archive does not contain an executable leshy binary'
  mkdir -p /usr/local/bin
  install -m 0755 "${tmp_dir}/leshy" "$LESHY_BIN"
fi

leshy_version="$("$LESHY_BIN" --version 2>/dev/null || true)"
printf 'Leshy binary: %s (%s)\n' "$LESHY_BIN" "${leshy_version:-no version output}"
if [[ "$leshy_version" != "leshy ${EXPECTED_VERSION}" ]]; then
  printf 'Warning: expected leshy %s, got: %s\n' "${EXPECTED_VERSION}" "${leshy_version:-no output}" >&2
fi

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
