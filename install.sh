#!/usr/bin/env bash
set -Eeuo pipefail

readonly EXPECTED_VERSION="0.4.0"
readonly KIKIMORA_VERSION="1.0.0"

readonly SOURCE_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly FILES_DIR="${SOURCE_DIR}/files"

readonly LESHY_BIN="/usr/local/bin/leshy"
readonly LESHY_CONFIG_DIR="/etc/kikimora/leshy"
readonly LEGACY_CONFIG_DIR="/etc/leshy"
readonly LEGACY_RUNTIME_DIR="/run/vpn"
readonly LESHY_CONFIG="${LESHY_CONFIG_DIR}/config.toml"
readonly DOMAINS_DIR="${LESHY_CONFIG_DIR}/domains"
readonly VPN_CONFIG="${LESHY_CONFIG_DIR}/vpn.conf"
readonly ROUTING_CONFIG="${LESHY_CONFIG_DIR}/routing.conf"
readonly LIBEXEC_DIR="/usr/local/libexec/kikimora/leshy"
readonly RECONCILE="${LIBEXEC_DIR}/reconcile"
readonly CHECK_CONFIG="${LIBEXEC_DIR}/check-config"
readonly BUILD_CONFIG="${LIBEXEC_DIR}/build-config"
readonly ROUTE_LIFECYCLE="${LIBEXEC_DIR}/route-lifecycle"
readonly ROUTE_WATCH="${LIBEXEC_DIR}/route-watch"
readonly HEALTH_WATCH="${LIBEXEC_DIR}/health-watch"
readonly LESHY_DNS="/usr/local/sbin/leshy-dns"
readonly RUNTIME_DIR="/run/kikimora/leshy/vpn"
readonly UNIT_FILE="/etc/systemd/system/leshy.service"
readonly DROPIN_DIR="/etc/systemd/system/leshy.service.d"
readonly ROUTE_CLEANUP_DROPIN="${DROPIN_DIR}/route-cleanup.conf"
readonly ROUTE_WATCH_UNIT="/etc/systemd/system/leshy-route-watch.service"
readonly HEALTH_WATCH_UNIT="/etc/systemd/system/leshy-health-watch.service"
readonly KIKIMORA_BIN="/usr/local/sbin/kikimora"
readonly KIKIMORA_ALIAS="/usr/local/bin/kk"
readonly BASH_COMPLETION="/usr/share/bash-completion/completions/kikimora"
readonly ZSH_COMPLETION="/usr/local/share/zsh/site-functions/_kikimora"
readonly FISH_COMPLETION="/usr/share/fish/vendor_completions.d/kikimora.fish"
readonly INSTALL_STATE_DIR="/var/lib/kikimora/leshy"
readonly INSTALL_STATE_FILE="${INSTALL_STATE_DIR}/installation.env"

work_dir=""
commit_started=0
commit_finished=0
rollback_ready=0

primary_interface_arg=""
secondary_interface_arg=""
non_interactive=0
stop_services_allowed=0
services_were_active=0
leshy_binary_arg=""
leshy_source_kind="existing"

show_help() {
    cat <<'EOF_HELP'
Kikimora 1.0.0 — Leshy 0.4.0 installer

Normal invocation:
  sudo kk install [OPTIONS]
  sudo kikimora install [OPTIONS]

First run from an extracted package:
  sudo ./kikimora install [OPTIONS]

Options:
  --primary-interface NAME
      VPN interface with higher priority (primary zone).
      Example: tun0, amn0, wg0.

  --secondary-interface NAME
      VPN interface with lower priority (secondary zone).
      Example: tun1, vpn0, wg1.

  --leshy-binary PATH
      Use the specified Leshy binary if /usr/local/bin/leshy is absent.
      Without this option, Kikimora also checks files/leshy and ~/.cargo/bin/leshy.

  --non-interactive
      Do not ask interactive questions.

  --stop-services
      Allow Kikimora to stop active Leshy services before installation.
      In interactive mode without this flag, a question will be asked.

  -y, --yes
      Agree to the required service stop without asking.

  -h, --help
      Show this help.

On a clean system without parameters, Kikimora will prompt for both interfaces.
On reinstall, values from /etc/kikimora/leshy/vpn.conf are preserved.
Explicitly passed flags override the saved values.

After installation a short alias kk is available:
  sudo kk start
  sudo kk dns enable
  kk status
  sudo kk verify
  sudo kk doctor
  sudo kk backup
  sudo kk restore
  sudo kk upgrade PATH

Example:
  sudo ./install.sh --primary-interface tun0 --secondary-interface tun1

Shell completion for kikimora and kk is installed for Bash, Zsh and Fish.
EOF_HELP
}
parse_arguments() {
    while (($#)); do
        case "$1" in
            --primary-interface)
                (($# >= 2)) || die "--primary-interface requires a name"
                primary_interface_arg="$2"
                shift 2
                ;;
            --primary-interface=*)
                primary_interface_arg="${1#*=}"
                shift
                ;;
            --secondary-interface)
                (($# >= 2)) || die "--secondary-interface requires a name"
                secondary_interface_arg="$2"
                shift 2
                ;;
            --secondary-interface=*)
                secondary_interface_arg="${1#*=}"
                shift
                ;;
            --leshy-binary)
                (($# >= 2)) || die "--leshy-binary requires a path"
                leshy_binary_arg="$2"
                shift 2
                ;;
            --leshy-binary=*)
                leshy_binary_arg="${1#*=}"
                shift
                ;;
            --non-interactive)
                non_interactive=1
                shift
                ;;
            --stop-services|-y|--yes)
                stop_services_allowed=1
                shift
                ;;
            -h|--help)
                show_help
                exit 0
                ;;
            --)
                shift
                (($# == 0)) || die "positional arguments are not supported: $*"
                ;;
            *)
                die "unknown option: $1 (see ./install.sh --help)"
                ;;
        esac
    done
}

validate_interface_name() {
    local name="$1"
    local label="$2"

    [[ -n "$name" ]] || die "$label: name is empty"
    ((${#name} <= 15)) || die "$label: name '$name' is longer than 15 characters"
    [[ "$name" =~ ^[[:alnum:]_.:-]+$ ]] || \
        die "$label: invalid name '$name'"
    [[ "$name" != "." && "$name" != ".." ]] || \
        die "$label: invalid name '$name'"
}

read_existing_interface() {
    local modern_name="$1"
    local legacy_name="$2"

    local source_config="$VPN_CONFIG"
    if [[ ! -r "$source_config" && -r "${LEGACY_CONFIG_DIR}/vpn.conf" ]]; then
        source_config="${LEGACY_CONFIG_DIR}/vpn.conf"
    fi
    [[ -r "$source_config" ]] || return 0

    bash -c '
        set -Eeuo pipefail
        source "$1"
        modern_name="$2"
        legacy_name="$3"
        value="${!modern_name:-}"
        if [[ -z "$value" ]]; then
            value="${!legacy_name:-}"
        fi
        printf "%s\n" "$value"
    ' _ "$source_config" "$modern_name" "$legacy_name"
}

prompt_interface() {
    local prompt="$1"
    local default_value="${2:-}"
    local value

    if [[ -n "$default_value" ]]; then
        printf '%s [%s]: ' "$prompt" "$default_value" >/dev/tty
    else
        printf '%s: ' "$prompt" >/dev/tty
    fi

    IFS= read -r value </dev/tty || die "failed to read interface name"
    printf '%s\n' "${value:-$default_value}"
}

resolve_interfaces() {
    local existing_primary=""
    local existing_secondary=""

    existing_primary="$(read_existing_interface PRIMARY_INTERFACE AMN_IFACE || true)"
    existing_secondary="$(read_existing_interface SECONDARY_INTERFACE VPN_IFACE || true)"

    PRIMARY_INTERFACE="${primary_interface_arg:-$existing_primary}"
    SECONDARY_INTERFACE="${secondary_interface_arg:-$existing_secondary}"

    if [[ -z "$PRIMARY_INTERFACE" || -z "$SECONDARY_INTERFACE" ]]; then
        if ((non_interactive == 1)); then
            die "on a clean system pass --primary-interface and --secondary-interface"
        fi
        [[ -e /dev/tty ]] || \
            die "no interactive terminal; pass --primary-interface and --secondary-interface"

        printf '\nVPN Interface Setup\n' >/dev/tty
        printf 'The installer does not connect VPN. Specify interface names visible in `ip -brief link`.\n\n' >/dev/tty

        if [[ -z "$PRIMARY_INTERFACE" ]]; then
            PRIMARY_INTERFACE="$(prompt_interface 'VPN interface with higher priority' '')"
        fi
        if [[ -z "$SECONDARY_INTERFACE" ]]; then
            SECONDARY_INTERFACE="$(prompt_interface 'VPN interface with lower priority' '')"
        fi
    fi

    validate_interface_name "$PRIMARY_INTERFACE" "primary interface"
    validate_interface_name "$SECONDARY_INTERFACE" "secondary interface"
    [[ "$PRIMARY_INTERFACE" != "$SECONDARY_INTERFACE" ]] || \
        die "primary and secondary VPN interfaces must differ"
}

confirm_stop_services() {
    local active=0 answer=''
    local unit
    for unit in leshy.service leshy-route-watch.service leshy-health-watch.service; do
        if systemctl is-active --quiet "$unit"; then
            active=1
            break
        fi
    done
    ((active == 1)) || return 0
    services_were_active=1

    if ((stop_services_allowed == 0)); then
        if ((non_interactive == 1)) || [[ ! -e /dev/tty ]]; then
            die "Leshy services are active; retry with --stop-services (or --yes)"
        fi
        cat >/dev/tty <<'EOF_STOP'

Leshy is running.

To install safely, the following services will be stopped:
  • leshy.service
  • leshy-route-watch.service
  • leshy-health-watch.service

Continue? [Y/n]:
EOF_STOP
        IFS= read -r answer </dev/tty || true
        case "$answer" in
            ''|y|Y|yes|YES|д|Д|да|ДА) ;;
            *) die "installation cancelled: Leshy services were not stopped" ;;
        esac
    fi

    log "Stopping Leshy services"
    if [[ -x "$LESHY_DNS" ]]; then
        "$LESHY_DNS" suspend || true
    fi
    systemctl stop leshy-health-watch.service leshy-route-watch.service leshy.service || true
    for unit in leshy.service leshy-route-watch.service leshy-health-watch.service; do
        systemctl is-active --quiet "$unit" && die "failed to stop $unit"
    done
    printf 'Leshy services stopped. They will not be started automatically after installation.\n'
}

cleanup_legacy_runtime_files() {
    local path
    for path in \
        "${RUNTIME_DIR}/${PRIMARY_INTERFACE}.dev" \
        "${RUNTIME_DIR}/${SECONDARY_INTERFACE}.dev" \
        "${LEGACY_RUNTIME_DIR}/${PRIMARY_INTERFACE}.dev" \
        "${LEGACY_RUNTIME_DIR}/${SECONDARY_INTERFACE}.dev"; do
        case "$path" in
            "${RUNTIME_DIR}/primary.dev"|"${RUNTIME_DIR}/secondary.dev") continue ;;
        esac
        if [[ -e "$path" || -L "$path" ]]; then
            rm -f -- "$path"
            printf 'Removed legacy runtime file: %s\n' "$path"
        fi
    done
}

write_vpn_config() {
    local destination="$1"

    cat >"$destination" <<EOF_VPN
# Created by Kikimora ${KIKIMORA_VERSION} for Leshy ${EXPECTED_VERSION}.
# Interface names can be changed by running kikimora install with the interface flags.

PRIMARY_INTERFACE="${PRIMARY_INTERFACE}"
PRIMARY_DEVICE_FILE="/run/kikimora/leshy/vpn/primary.dev"

SECONDARY_INTERFACE="${SECONDARY_INTERFACE}"
SECONDARY_DEVICE_FILE="/run/kikimora/leshy/vpn/secondary.dev"
EOF_VPN
    chmod 0644 "$destination"
}

log() {
    printf '\n==> %s\n' "$*"
}

die() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

backup_file() {
    local path="$1"
    local timestamp
    local backup

    [[ -e "$path" || -L "$path" ]] || return 0

    timestamp="$(date +%Y%m%d-%H%M%S)"
    backup="${path}.backup-${timestamp}"

    if [[ -e "$backup" || -L "$backup" ]]; then
        backup="${backup}-$$"
    fi

    cp -a -- "$path" "$backup"
    printf 'Backup: %s\n' "$backup"
}

find_cargo_binary() {
    local invoking_user
    local invoking_home
    local candidate

    invoking_user="${SUDO_USER:-$(id -un)}"
    invoking_home="$(getent passwd "$invoking_user" | cut -d: -f6)"

    [[ -n "$invoking_home" ]] || return 1

    candidate="${invoking_home}/.cargo/bin/leshy"

    if [[ -x "$candidate" ]]; then
        printf '%s\n' "$candidate"
        return 0
    fi

    return 1
}

install_managed_file() {
    local source="$1"
    local destination="$2"
    local mode="$3"

    [[ -f "$source" ]] || die "installation file not found: $source"

    backup_file "$destination"
    install -o root -g root -m "$mode" -- "$source" "$destination"
}

install_initial_domain_file() {
    local source="$1"
    local destination="$2"

    if [[ -e "$destination" ]]; then
        printf 'Preserved user-provided list: %s\n' "$destination"
        return 0
    fi

    install -o root -g root -m 0644 -- "$source" "$destination"
    printf 'Installed initial list: %s\n' "$destination"
}

snapshot_target() {
    local index="$1"
    local target="$2"
    local snapshot_dir="${work_dir}/rollback/${index}"

    mkdir -p -- "$snapshot_dir"

    if [[ -e "$target" || -L "$target" ]]; then
        printf 'present\n' > "${snapshot_dir}/state"
        cp -a -- "$target" "${snapshot_dir}/content"
    else
        printf 'absent\n' > "${snapshot_dir}/state"
    fi
}

restore_target() {
    local index="$1"
    local target="$2"
    local snapshot_dir="${work_dir}/rollback/${index}"
    local state

    [[ -f "${snapshot_dir}/state" ]] || return 0
    state="$(cat "${snapshot_dir}/state")"

    rm -rf -- "$target"

    if [[ "$state" == "present" ]]; then
        mkdir -p -- "$(dirname -- "$target")"
        cp -a -- "${snapshot_dir}/content" "$target"
    fi
}

rollback_commit() {
    local -a targets=(
        "$LESHY_BIN"
        "$INSTALL_STATE_FILE"
        "${DOMAINS_DIR}/primary.txt"
        "${DOMAINS_DIR}/secondary.txt"
        "${DOMAINS_DIR}/amnezia.txt"
        "${DOMAINS_DIR}/vpn2.txt"
        "${DOMAINS_DIR}/bypass.txt"
        "$VPN_CONFIG"
        "$RECONCILE"
        "$CHECK_CONFIG"
        "$BUILD_CONFIG"
        "$ROUTE_LIFECYCLE"
        "$ROUTE_WATCH"
        "$HEALTH_WATCH"
        "$LESHY_DNS"
        "$UNIT_FILE"
        "$ROUTE_WATCH_UNIT"
        "$HEALTH_WATCH_UNIT"
        "$ROUTE_CLEANUP_DROPIN"
        "$LESHY_CONFIG"
        "$KIKIMORA_BIN"
        "$KIKIMORA_ALIAS"
        "$BASH_COMPLETION"
        "$ZSH_COMPLETION"
        "$FISH_COMPLETION"
    )
    local i

    printf '\nError during commit. Restoring previous state...\n' >&2

    set +e
    for ((i=${#targets[@]}-1; i>=0; i--)); do
        restore_target "$i" "${targets[$i]}"
    done
    systemctl daemon-reload >/dev/null 2>&1 || true
    set -e

    printf 'Rollback complete.\n' >&2
}

cleanup() {
    local status=$?

    if ((status != 0 && commit_started == 1 && commit_finished == 0 && rollback_ready == 1)); then
        rollback_commit
    fi

    if [[ -n "$work_dir" && -d "$work_dir" ]]; then
        rm -rf -- "$work_dir"
    fi

    exit "$status"
}

trap cleanup EXIT

parse_arguments "$@"

if [[ "$EUID" -ne 0 ]]; then
    die "run the installer via sudo"
fi

resolve_interfaces
confirm_stop_services

printf 'Selected VPN interfaces: primary=%s, secondary=%s\n' "$PRIMARY_INTERFACE" "$SECONDARY_INTERFACE"

log "Checking package files"

readonly -a REQUIRED_FILES=(
    "${FILES_DIR}/vpn.conf"
    "${FILES_DIR}/reconcile"
    "${FILES_DIR}/check-config"
    "${FILES_DIR}/build-config"
    "${FILES_DIR}/route-lifecycle"
    "${FILES_DIR}/route-watch"
    "${FILES_DIR}/health-watch"
    "${FILES_DIR}/leshy-dns"
    "${FILES_DIR}/route-cleanup.conf"
    "${FILES_DIR}/leshy-route-watch.service"
    "${FILES_DIR}/leshy-health-watch.service"
    "${FILES_DIR}/leshy.service"
    "${FILES_DIR}/domains/primary.txt"
    "${FILES_DIR}/domains/secondary.txt"
    "${FILES_DIR}/domains/bypass.txt"
    "${SOURCE_DIR}/kikimora"
    "${SOURCE_DIR}/completions/kikimora.bash"
    "${SOURCE_DIR}/completions/_kikimora"
    "${SOURCE_DIR}/completions/kikimora.fish"
)

for required in "${REQUIRED_FILES[@]}"; do
    [[ -f "$required" ]] || die "missing $required"
done

log "Checking Leshy binary"

leshy_source="$LESHY_BIN"
install_binary=0
leshy_source_kind="existing"

if [[ ! -x "$leshy_source" ]]; then
    if [[ -n "$leshy_binary_arg" ]]; then
        [[ -x "$leshy_binary_arg" ]] || die "specified --leshy-binary is not an executable file: $leshy_binary_arg"
        leshy_source="$(readlink -f -- "$leshy_binary_arg")"
        leshy_source_kind="argument"
    elif [[ -x "${FILES_DIR}/leshy" ]]; then
        leshy_source="${FILES_DIR}/leshy"
        leshy_source_kind="package"
    else
        leshy_source="$(find_cargo_binary || true)"
        [[ -n "$leshy_source" ]] || \
            die "Leshy not found. Pass --leshy-binary PATH, place the binary in files/leshy, or install it in ~/.cargo/bin/leshy"
        leshy_source_kind="cargo"
    fi
    install_binary=1
fi

installed_version="$("$leshy_source" --version 2>/dev/null || true)"
printf 'Version: %s\n' "$installed_version"

if [[ "$installed_version" != "leshy ${EXPECTED_VERSION}" ]]; then
    die "expected leshy ${EXPECTED_VERSION}, got: ${installed_version:-no output}"
fi

log "Pre-flight shell file validation"

bash -n "${FILES_DIR}/reconcile"
bash -n "${FILES_DIR}/check-config"
bash -n "${FILES_DIR}/build-config"
bash -n "${FILES_DIR}/route-lifecycle"
bash -n "${FILES_DIR}/route-watch"
bash -n "${FILES_DIR}/health-watch"
bash -n "${FILES_DIR}/leshy-dns"
bash -n "${FILES_DIR}/vpn.conf"
bash -n "${SOURCE_DIR}/kikimora"
grep -Fqx 'Wants=leshy-route-watch.service leshy-health-watch.service' "${FILES_DIR}/route-cleanup.conf"
grep -Fqx 'ExecStartPre=/usr/local/libexec/kikimora/leshy/reconcile' "${FILES_DIR}/route-cleanup.conf"
grep -Fqx 'ExecStartPre=/usr/local/libexec/kikimora/leshy/route-lifecycle snapshot' "${FILES_DIR}/route-cleanup.conf"
grep -Fqx 'ExecStartPost=/usr/local/sbin/leshy-dns resume' "${FILES_DIR}/route-cleanup.conf"
grep -Fqx 'ExecStopPost=-/usr/local/sbin/leshy-dns suspend' "${FILES_DIR}/route-cleanup.conf"
grep -Fqx 'ExecStopPost=-/usr/local/libexec/kikimora/leshy/route-lifecycle cleanup' "${FILES_DIR}/route-cleanup.conf"
grep -Fqx 'ExecStart=/usr/local/libexec/kikimora/leshy/route-watch' "${FILES_DIR}/leshy-route-watch.service"
grep -Fqx 'ExecStart=/usr/local/libexec/kikimora/leshy/health-watch' "${FILES_DIR}/leshy-health-watch.service"
printf 'Package shell files: OK\n'

log "Preparing transaction"

work_dir="$(mktemp -d /tmp/leshy-install.XXXXXX)"
install -d -m 0755 "${work_dir}/domains"
write_vpn_config "${work_dir}/vpn.conf"
if [[ -r "$ROUTING_CONFIG" ]]; then install -m 0644 "$ROUTING_CONFIG" "${work_dir}/routing.conf"; else printf 'DEFAULT_ZONE=direct\n' >"${work_dir}/routing.conf"; chmod 0644 "${work_dir}/routing.conf"; fi

for domain_list in primary.txt secondary.txt bypass.txt; do
    legacy_name="$domain_list"
    [[ "$domain_list" == primary.txt ]] && legacy_name="amnezia.txt"
    [[ "$domain_list" == secondary.txt ]] && legacy_name="vpn2.txt"
    if [[ -r "${DOMAINS_DIR}/${domain_list}" ]]; then
        install -m 0644 "${DOMAINS_DIR}/${domain_list}" "${work_dir}/domains/${domain_list}"
    elif [[ -r "${DOMAINS_DIR}/${legacy_name}" ]]; then
        install -m 0644 "${DOMAINS_DIR}/${legacy_name}" "${work_dir}/domains/${domain_list}"
        printf 'Migrated user-provided list: %s -> %s\n' "${DOMAINS_DIR}/${legacy_name}" "${DOMAINS_DIR}/${domain_list}"
    elif [[ -r "${LEGACY_CONFIG_DIR}/domains/${legacy_name}" ]]; then
        install -m 0644 "${LEGACY_CONFIG_DIR}/domains/${legacy_name}" "${work_dir}/domains/${domain_list}"
        printf 'Migrated user-provided list: %s\n' "${LEGACY_CONFIG_DIR}/domains/${legacy_name}"
    else
        install -m 0644 "${FILES_DIR}/domains/${domain_list}" "${work_dir}/domains/${domain_list}"
    fi
done

log "Dry-run configuration generation"

bash "${FILES_DIR}/build-config" \
    "${work_dir}/domains" \
    "${work_dir}/config.toml" \
    "${work_dir}/routing.conf"

log "Dry-run Leshy configuration validation"

bash "${FILES_DIR}/check-config" \
    "$leshy_source" \
    "${work_dir}/config.toml"

log "Static unit file validation"

grep -Fqx 'ExecStart=/usr/local/bin/leshy /etc/kikimora/leshy/config.toml' "${FILES_DIR}/leshy.service"
grep -Fqx 'ExecStart=/usr/local/libexec/kikimora/leshy/route-watch' "${FILES_DIR}/leshy-route-watch.service"
grep -Fqx 'ExecStart=/usr/local/libexec/kikimora/leshy/health-watch' "${FILES_DIR}/leshy-health-watch.service"
printf 'Package unit file structure: OK\n'

log "Preparing rollback"

readonly -a TRANSACTION_TARGETS=(
    "$LESHY_BIN"
    "$INSTALL_STATE_FILE"
    "${DOMAINS_DIR}/primary.txt"
    "${DOMAINS_DIR}/secondary.txt"
    "${DOMAINS_DIR}/amnezia.txt"
    "${DOMAINS_DIR}/vpn2.txt"
    "${DOMAINS_DIR}/bypass.txt"
    "$VPN_CONFIG"
    "$ROUTING_CONFIG"
    "$RECONCILE"
    "$CHECK_CONFIG"
    "$BUILD_CONFIG"
    "$ROUTE_LIFECYCLE"
    "$ROUTE_WATCH"
    "$HEALTH_WATCH"
    "$LESHY_DNS"
    "$UNIT_FILE"
    "$ROUTE_WATCH_UNIT"
    "$HEALTH_WATCH_UNIT"
    "$ROUTE_CLEANUP_DROPIN"
    "$LESHY_CONFIG"
    "$KIKIMORA_BIN"
    "$KIKIMORA_ALIAS"
    "$BASH_COMPLETION"
    "$ZSH_COMPLETION"
    "$FISH_COMPLETION"
)

for i in "${!TRANSACTION_TARGETS[@]}"; do
    snapshot_target "$i" "${TRANSACTION_TARGETS[$i]}"
done
rollback_ready=1

log "Committing validated files"

commit_started=1

install -d -o root -g root -m 0755 "$LESHY_CONFIG_DIR"
install -d -o root -g root -m 0755 "$DOMAINS_DIR"
install -d -o root -g root -m 0755 "$LIBEXEC_DIR"
install -d -o root -g root -m 0755 "$RUNTIME_DIR"
install -d -o root -g root -m 0755 "$DROPIN_DIR"
install -d -o root -g root -m 0755 "$(dirname "$BASH_COMPLETION")"
install -d -o root -g root -m 0755 "$(dirname "$ZSH_COMPLETION")"
install -d -o root -g root -m 0755 "$(dirname "$FISH_COMPLETION")"
install_managed_file "${SOURCE_DIR}/kikimora" "$KIKIMORA_BIN" 0755
ln -sfn /usr/local/sbin/kikimora "$KIKIMORA_ALIAS"
install_managed_file "${SOURCE_DIR}/completions/kikimora.bash" "$BASH_COMPLETION" 0644
install_managed_file "${SOURCE_DIR}/completions/_kikimora" "$ZSH_COMPLETION" 0644
install_managed_file "${SOURCE_DIR}/completions/kikimora.fish" "$FISH_COMPLETION" 0644

install -d -o root -g root -m 0755 "$INSTALL_STATE_DIR"
if ((install_binary == 1)); then
    install_managed_file "$leshy_source" "$LESHY_BIN" 0755
    cat >"$INSTALL_STATE_FILE" <<EOF_STATE
MANAGED_BY_KIKIMORA=yes
KIKIMORA_VERSION=${KIKIMORA_VERSION}
LESHY_VERSION=${EXPECTED_VERSION}
INSTALLED_PATH=${LESHY_BIN}
SOURCE_KIND=${leshy_source_kind}
SOURCE_PATH=${leshy_source}
SHA256=$(sha256sum "$LESHY_BIN" | awk '{print $1}')
INSTALLED_AT=$(date --iso-8601=seconds)
EOF_STATE
    chmod 0644 "$INSTALL_STATE_FILE"
    printf 'Kikimora installed Leshy: %s\n' "$LESHY_BIN"
    printf 'Origin recorded: %s\n' "$INSTALL_STATE_FILE"
else
    cat >"$INSTALL_STATE_FILE" <<EOF_STATE
MANAGED_BY_KIKIMORA=no
KIKIMORA_VERSION=${KIKIMORA_VERSION}
LESHY_VERSION=${EXPECTED_VERSION}
INSTALLED_PATH=${LESHY_BIN}
SOURCE_KIND=preexisting
SOURCE_PATH=${LESHY_BIN}
SHA256=$(sha256sum "$LESHY_BIN" | awk '{print $1}')
OBSERVED_AT=$(date --iso-8601=seconds)
EOF_STATE
    chmod 0644 "$INSTALL_STATE_FILE"
    printf 'Using previously installed Leshy: %s\n' "$LESHY_BIN"
fi

install_managed_file "${work_dir}/domains/primary.txt" "${DOMAINS_DIR}/primary.txt" 0644
install_managed_file "${work_dir}/domains/secondary.txt" "${DOMAINS_DIR}/secondary.txt" 0644
install_managed_file "${work_dir}/domains/bypass.txt" "${DOMAINS_DIR}/bypass.txt" 0644
rm -f -- "${DOMAINS_DIR}/amnezia.txt" "${DOMAINS_DIR}/vpn2.txt"
install_managed_file "${work_dir}/vpn.conf" "$VPN_CONFIG" 0644
install_managed_file "${work_dir}/routing.conf" "$ROUTING_CONFIG" 0644
install_managed_file "${FILES_DIR}/reconcile" "$RECONCILE" 0755
install_managed_file "${FILES_DIR}/check-config" "$CHECK_CONFIG" 0755
install_managed_file "${FILES_DIR}/build-config" "$BUILD_CONFIG" 0755
install_managed_file "${FILES_DIR}/route-lifecycle" "$ROUTE_LIFECYCLE" 0755
install_managed_file "${FILES_DIR}/route-watch" "$ROUTE_WATCH" 0755
install_managed_file "${FILES_DIR}/health-watch" "$HEALTH_WATCH" 0755
install_managed_file "${FILES_DIR}/leshy-dns" "$LESHY_DNS" 0755
install_managed_file "${FILES_DIR}/leshy.service" "$UNIT_FILE" 0644
install_managed_file "${FILES_DIR}/leshy-route-watch.service" "$ROUTE_WATCH_UNIT" 0644
install_managed_file "${FILES_DIR}/leshy-health-watch.service" "$HEALTH_WATCH_UNIT" 0644
install_managed_file "${FILES_DIR}/route-cleanup.conf" "$ROUTE_CLEANUP_DROPIN" 0644
install_managed_file "${work_dir}/config.toml" "$LESHY_CONFIG" 0644

log "Reloading systemd unit files"

systemctl daemon-reload

log "Verifying installed systemd unit files"

verify_output="${work_dir}/systemd-verify.log"
verify_filtered="${work_dir}/systemd-verify.filtered.log"
verify_status=0
systemd-analyze verify "$UNIT_FILE" "$ROUTE_WATCH_UNIT" "$HEALTH_WATCH_UNIT" \
    >"$verify_output" 2>&1 || verify_status=$?

grep -vF 'Configuration file /etc/systemd/system/AmneziaVPN.service is marked executable.' "$verify_output" \
    | grep -vF 'Please remove executable permission bits. Proceeding anyway.' \
    >"$verify_filtered" || true

if [[ -s "$verify_filtered" || "$verify_status" -ne 0 ]]; then
    cat "$verify_filtered" >&2
    die "verification of installed systemd unit files failed"
fi

printf 'Kikimora unit files: OK\n'
if grep -Fq 'Configuration file /etc/systemd/system/AmneziaVPN.service is marked executable.' "$verify_output"; then
    printf 'Note: AmneziaVPN.service has executable bits; Kikimora does not modify this file.\n'
fi

log "Validating installed configuration"

"$CHECK_CONFIG" "$LESHY_BIN" "$LESHY_CONFIG"

log "Cleaning up legacy VPN runtime files"

cleanup_legacy_runtime_files

log "Initial VPN state file reconciliation"

"$RECONCILE"

log "Checking installed lifecycle drop-in"

systemctl cat leshy.service | grep -F 'leshy-route-watch.service leshy-health-watch.service' >/dev/null
systemctl cat leshy.service | grep -F 'ExecStartPre=/usr/local/libexec/kikimora/leshy/reconcile' >/dev/null
systemctl cat leshy.service | grep -F 'route-lifecycle snapshot' >/dev/null
systemctl cat leshy.service | grep -F 'route-lifecycle cleanup' >/dev/null
systemctl cat leshy.service | grep -F 'leshy-dns resume' >/dev/null
systemctl cat leshy.service | grep -F 'leshy-dns suspend' >/dev/null
systemctl cat leshy-route-watch.service | grep -F 'route-watch' >/dev/null
systemctl cat leshy-health-watch.service | grep -F 'health-watch' >/dev/null

commit_finished=1

log "Checking Leshy status"

if systemctl is-active --quiet leshy.service; then
    printf 'WARNING: leshy.service was already active before or during installation.\n'
else
    printf 'Service: inactive\n'
fi

if systemctl is-enabled --quiet leshy.service 2>/dev/null; then
    printf 'WARNING: leshy.service was already enabled before installation.\n'
else
    printf 'Autostart: disabled\n'
fi

printf '\nContents of %s:\n' "$RUNTIME_DIR"
ls -la "$RUNTIME_DIR"

printf '\nKikimora %s has completed the installation of Leshy %s.\n' "$KIKIMORA_VERSION" "$EXPECTED_VERSION"
printf 'Package and configuration fully validated before writing to system.\n'
printf 'On commit error, previous managed files are restored.\n'
printf 'Lifecycle cleanup, VPN watcher, DNS lifecycle integration, and DNS health watchdog installed transactionally.\n'
printf 'High priority VPN interface: %s\n' "$PRIMARY_INTERFACE"
printf 'Low priority VPN interface: %s\n' "$SECONDARY_INTERFACE"
printf 'Short alias installed: kk -> kikimora\n'
printf 'Shell completions installed for Bash, Zsh and Fish.\n'
printf 'DNS remains unchanged until the explicit command: sudo kk dns enable\n'
printf 'After manual enable, stopping or three consecutive DNS failures will restore the original DNS; the next Leshy start will restore DNS via Leshy.\n'
printf 'The installer did not start or enable Leshy.\n'
printf 'System DNS was not changed.\n'
printf 'VPN connections were not changed.\n'
printf 'AmneziaVPN.service was not changed.\n'
