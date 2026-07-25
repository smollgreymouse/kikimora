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
Kikimora 1.0.0 — установка Leshy 0.4.0

Обычный запуск:
  sudo kk install [ПАРАМЕТРЫ]
  sudo kikimora install [ПАРАМЕТРЫ]

Первый запуск из распакованного пакета:
  sudo ./kikimora install [ПАРАМЕТРЫ]

Параметры:
  --primary-interface NAME
      VPN-интерфейс с более высоким приоритетом.
      Пример: amn0, wg0, tun0.

  --secondary-interface NAME
      VPN-интерфейс с более низким приоритетом.
      Пример: vpn0, wg1, tun1.

  --leshy-binary PATH
      Использовать указанный бинарник Leshy, если /usr/local/bin/leshy отсутствует.
      Без параметра Kikimora также проверит files/leshy и ~/.cargo/bin/leshy.

  --non-interactive
      Не задавать интерактивных вопросов.

  --stop-services
      Разрешить Kikimora остановить активные службы Leshy перед установкой.
      В интерактивном режиме без этого флага будет задан вопрос.

  -y, --yes
      Согласиться на требуемую остановку служб без вопроса.

  -h, --help
      Показать справку.

На чистой системе без параметров Kikimora запросит оба интерфейса.
При переустановке значения из /etc/kikimora/leshy/vpn.conf сохраняются.
Переданные параметры явно заменяют сохранённые значения.

После установки доступен короткий алиас kk:
  sudo kk start
  sudo kk dns enable
  kk status
  sudo kk verify
  sudo kk doctor
  sudo kk backup
  sudo kk restore
  sudo kk upgrade PATH

Автодополнение для kikimora и kk устанавливается для Bash, Zsh и Fish.
EOF_HELP
}
parse_arguments() {
    while (($#)); do
        case "$1" in
            --primary-interface)
                (($# >= 2)) || die "для --primary-interface требуется имя"
                primary_interface_arg="$2"
                shift 2
                ;;
            --primary-interface=*)
                primary_interface_arg="${1#*=}"
                shift
                ;;
            --secondary-interface)
                (($# >= 2)) || die "для --secondary-interface требуется имя"
                secondary_interface_arg="$2"
                shift 2
                ;;
            --secondary-interface=*)
                secondary_interface_arg="${1#*=}"
                shift
                ;;
            --leshy-binary)
                (($# >= 2)) || die "для --leshy-binary требуется путь"
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
                (($# == 0)) || die "позиционные аргументы не поддерживаются: $*"
                ;;
            *)
                die "неизвестный параметр: $1 (см. ./install.sh --help)"
                ;;
        esac
    done
}

validate_interface_name() {
    local name="$1"
    local label="$2"

    [[ -n "$name" ]] || die "$label: имя не задано"
    ((${#name} <= 15)) || die "$label: имя '$name' длиннее 15 символов"
    [[ "$name" =~ ^[[:alnum:]_.:-]+$ ]] || \
        die "$label: недопустимое имя '$name'"
    [[ "$name" != "." && "$name" != ".." ]] || \
        die "$label: недопустимое имя '$name'"
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

    IFS= read -r value </dev/tty || die "не удалось прочитать имя интерфейса"
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
            die "на чистой системе передай --primary-interface и --secondary-interface"
        fi
        [[ -e /dev/tty ]] || \
            die "нет интерактивного терминала; передай --primary-interface и --secondary-interface"

        printf '\nНастройка VPN-интерфейсов\n' >/dev/tty
        printf 'Установщик не подключает VPN. Укажи имена, которые видны в `ip -brief link`.\n\n' >/dev/tty

        if [[ -z "$PRIMARY_INTERFACE" ]]; then
            PRIMARY_INTERFACE="$(prompt_interface 'VPN-интерфейс с более высоким приоритетом' '')"
        fi
        if [[ -z "$SECONDARY_INTERFACE" ]]; then
            SECONDARY_INTERFACE="$(prompt_interface 'VPN-интерфейс с более низким приоритетом' '')"
        fi
    fi

    validate_interface_name "$PRIMARY_INTERFACE" "основной интерфейс"
    validate_interface_name "$SECONDARY_INTERFACE" "второй интерфейс"
    [[ "$PRIMARY_INTERFACE" != "$SECONDARY_INTERFACE" ]] || \
        die "основной и второй VPN-интерфейсы должны различаться"
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
            die "службы Leshy активны; повтори с --stop-services (или --yes)"
        fi
        cat >/dev/tty <<'EOF_STOP'

Leshy запущен.

Для безопасной установки будут остановлены:
  • leshy.service
  • leshy-route-watch.service
  • leshy-health-watch.service

Продолжить? [Y/n]:
EOF_STOP
        IFS= read -r answer </dev/tty || true
        case "$answer" in
            ''|y|Y|yes|YES|д|Д|да|ДА) ;;
            *) die "установка отменена: службы Leshy не остановлены" ;;
        esac
    fi

    log "Остановка служб Leshy"
    if [[ -x "$LESHY_DNS" ]]; then
        "$LESHY_DNS" suspend || true
    fi
    systemctl stop leshy-health-watch.service leshy-route-watch.service leshy.service || true
    for unit in leshy.service leshy-route-watch.service leshy-health-watch.service; do
        systemctl is-active --quiet "$unit" && die "не удалось остановить $unit"
    done
    printf 'Службы Leshy остановлены. После установки они не запускаются автоматически.
'
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
            printf 'Удалён legacy runtime-файл: %s
' "$path"
        fi
    done
}

write_vpn_config() {
    local destination="$1"

    cat >"$destination" <<EOF_VPN
# Создано Kikimora ${KIKIMORA_VERSION} для Leshy ${EXPECTED_VERSION}.
# Имена можно изменить командой kikimora install с параметрами интерфейсов.

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
    printf 'Ошибка: %s\n' "$*" >&2
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
    printf 'Резервная копия: %s\n' "$backup"
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

    [[ -f "$source" ]] || die "не найден файл установки: $source"

    backup_file "$destination"
    install -o root -g root -m "$mode" -- "$source" "$destination"
}

install_initial_domain_file() {
    local source="$1"
    local destination="$2"

    if [[ -e "$destination" ]]; then
        printf 'Сохранён пользовательский список: %s\n' "$destination"
        return 0
    fi

    install -o root -g root -m 0644 -- "$source" "$destination"
    printf 'Установлен начальный список: %s\n' "$destination"
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

    printf '\nОшибка во время фиксации. Восстанавливаю прежнее состояние...\n' >&2

    set +e
    for ((i=${#targets[@]}-1; i>=0; i--)); do
        restore_target "$i" "${targets[$i]}"
    done
    systemctl daemon-reload >/dev/null 2>&1 || true
    set -e

    printf 'Откат завершён.\n' >&2
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
    die "запусти инсталлятор через sudo"
fi

resolve_interfaces
confirm_stop_services

printf 'Выбраны VPN-интерфейсы: primary=%s, secondary=%s\n' "$PRIMARY_INTERFACE" "$SECONDARY_INTERFACE"

log "Проверка файлов пакета"

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
    [[ -f "$required" ]] || die "отсутствует $required"
done

log "Проверка бинарника Leshy"

leshy_source="$LESHY_BIN"
install_binary=0
leshy_source_kind="existing"

if [[ ! -x "$leshy_source" ]]; then
    if [[ -n "$leshy_binary_arg" ]]; then
        [[ -x "$leshy_binary_arg" ]] || die "указанный --leshy-binary не является исполняемым файлом: $leshy_binary_arg"
        leshy_source="$(readlink -f -- "$leshy_binary_arg")"
        leshy_source_kind="argument"
    elif [[ -x "${FILES_DIR}/leshy" ]]; then
        leshy_source="${FILES_DIR}/leshy"
        leshy_source_kind="package"
    else
        leshy_source="$(find_cargo_binary || true)"
        [[ -n "$leshy_source" ]] || \
            die "Leshy не найден. Передай --leshy-binary PATH, положи бинарник в files/leshy или установи его в ~/.cargo/bin/leshy"
        leshy_source_kind="cargo"
    fi
    install_binary=1
fi

installed_version="$("$leshy_source" --version 2>/dev/null || true)"
printf 'Версия: %s\n' "$installed_version"

if [[ "$installed_version" != "leshy ${EXPECTED_VERSION}" ]]; then
    die "ожидался leshy ${EXPECTED_VERSION}, получено: ${installed_version:-нет вывода}"
fi

log "Предварительная проверка shell-файлов"

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
printf 'Shell-файлы пакета: OK\n'

log "Подготовка транзакции"

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
        printf 'Мигрирован пользовательский список: %s -> %s\n' "${DOMAINS_DIR}/${legacy_name}" "${DOMAINS_DIR}/${domain_list}"
    elif [[ -r "${LEGACY_CONFIG_DIR}/domains/${legacy_name}" ]]; then
        install -m 0644 "${LEGACY_CONFIG_DIR}/domains/${legacy_name}" "${work_dir}/domains/${domain_list}"
        printf 'Мигрирован пользовательский список: %s\n' "${LEGACY_CONFIG_DIR}/domains/${legacy_name}"
    else
        install -m 0644 "${FILES_DIR}/domains/${domain_list}" "${work_dir}/domains/${domain_list}"
    fi
done

log "Пробная генерация конфигурации"

bash "${FILES_DIR}/build-config" \
    "${work_dir}/domains" \
    "${work_dir}/config.toml" \
    "${work_dir}/routing.conf"

log "Пробная проверка конфигурации Leshy"

bash "${FILES_DIR}/check-config" \
    "$leshy_source" \
    "${work_dir}/config.toml"

log "Статическая проверка unit-файлов пакета"

grep -Fqx 'ExecStart=/usr/local/bin/leshy /etc/kikimora/leshy/config.toml' "${FILES_DIR}/leshy.service"
grep -Fqx 'ExecStart=/usr/local/libexec/kikimora/leshy/route-watch' "${FILES_DIR}/leshy-route-watch.service"
grep -Fqx 'ExecStart=/usr/local/libexec/kikimora/leshy/health-watch' "${FILES_DIR}/leshy-health-watch.service"
printf 'Структура unit-файлов пакета: OK\n'

log "Подготовка отката"

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

log "Фиксация проверенных файлов"

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
    printf 'Kikimora установила Leshy: %s\n' "$LESHY_BIN"
    printf 'Происхождение записано: %s\n' "$INSTALL_STATE_FILE"
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
    printf 'Используется ранее установленный Leshy: %s\n' "$LESHY_BIN"
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

log "Перечитывание unit-файлов systemd"

systemctl daemon-reload

log "Проверка установленных unit-файлов systemd"

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
    die "проверка установленных unit-файлов systemd завершилась ошибкой"
fi

printf 'Unit-файлы Kikimora: OK\n'
if grep -Fq 'Configuration file /etc/systemd/system/AmneziaVPN.service is marked executable.' "$verify_output"; then
    printf 'Примечание: AmneziaVPN.service имеет executable-биты; Kikimora этот файл не изменяет.\n'
fi

log "Проверка установленной конфигурации"

"$CHECK_CONFIG" "$LESHY_BIN" "$LESHY_CONFIG"

log "Очистка legacy runtime-файлов VPN"

cleanup_legacy_runtime_files

log "Первичная синхронизация файлов состояния VPN"

"$RECONCILE"

log "Проверка установленного lifecycle drop-in"

systemctl cat leshy.service | grep -F 'leshy-route-watch.service leshy-health-watch.service' >/dev/null
systemctl cat leshy.service | grep -F 'ExecStartPre=/usr/local/libexec/kikimora/leshy/reconcile' >/dev/null
systemctl cat leshy.service | grep -F 'route-lifecycle snapshot' >/dev/null
systemctl cat leshy.service | grep -F 'route-lifecycle cleanup' >/dev/null
systemctl cat leshy.service | grep -F 'leshy-dns resume' >/dev/null
systemctl cat leshy.service | grep -F 'leshy-dns suspend' >/dev/null
systemctl cat leshy-route-watch.service | grep -F 'route-watch' >/dev/null
systemctl cat leshy-health-watch.service | grep -F 'health-watch' >/dev/null

commit_finished=1

log "Контроль состояния Leshy"

if systemctl is-active --quiet leshy.service; then
    printf 'ВНИМАНИЕ: leshy.service уже был активен до или во время установки.\n'
else
    printf 'Сервис: inactive\n'
fi

if systemctl is-enabled --quiet leshy.service 2>/dev/null; then
    printf 'ВНИМАНИЕ: leshy.service уже был включён до установки.\n'
else
    printf 'Автозапуск: disabled\n'
fi

printf '\nСодержимое %s:\n' "$RUNTIME_DIR"
ls -la "$RUNTIME_DIR"

printf '\nKikimora %s завершила установку Leshy %s.\n' "$KIKIMORA_VERSION" "$EXPECTED_VERSION"
printf 'Пакет и конфигурация полностью проверены до записи в систему.\n'
printf 'При ошибке фиксации прежние управляемые файлы восстанавливаются.\n'
printf 'Lifecycle-очистка, VPN watcher, DNS lifecycle-интеграция и DNS health watchdog установлены транзакционно.\n'
printf 'VPN-интерфейс высокого приоритета: %s\n' "$PRIMARY_INTERFACE"
printf 'VPN-интерфейс низкого приоритета: %s\n' "$SECONDARY_INTERFACE"
printf 'Короткий алиас установлен: kk -> kikimora\n'
printf 'Автодополнение установлено для Bash, Zsh и Fish.\n'
printf 'DNS остаётся неизменным до явной команды: sudo kk dns enable\n'
printf 'После ручного enable остановка или три последовательных сбоя DNS Leshy восстановят исходный DNS; следующий запуск Leshy вернёт DNS через Leshy.\n'
printf 'Инсталлятор не запускал и не включал Leshy.\n'
printf 'Системный DNS не изменялся.\n'
printf 'VPN-подключения не изменялись.\n'
printf 'AmneziaVPN.service не изменялся.\n'
