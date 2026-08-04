# Maintenance commands — install, verify, doctor, logs, debuglog, backup,
# restore, upgrade, uninstall, completion
#
# This file is sourced by the kikimora entrypoint.
# Uses SELF_DIR, BACKUP_DIR, MANAGED_UNITS from common.sh.

# ── Install ─────────────────────────────────────────────────────────────────

cmd_install() {
  require_root
  local installer
  installer="$(find_installer || true)"
  [[ -n "$installer" ]] || die "installer not found; run ./kikimora install from an extracted release or use kk upgrade PATH"
  exec "$installer" "$@"
}

# ── Verify ──────────────────────────────────────────────────────────────────

verify_one() {
  local path="$1" mode="${2:-}"
  if [[ ! -e "$path" ]]; then
    printf 'FAIL  %s missing\n' "$path"
    return 1
  fi
  if [[ -n "$mode" && "$(stat -c '%a' "$path")" != "$mode" ]]; then
    printf 'FAIL  %s mode %s, expected %s\n' "$path" "$(stat -c '%a' "$path")" "$mode"
    return 1
  fi
  printf 'OK    %s\n' "$path"
}

cmd_verify() {
  require_root
  local failed=0
  local files=(
    /usr/local/bin/leshy
    /var/lib/kikimora/leshy/installation.env
    /usr/local/sbin/leshy-dns
    /usr/local/sbin/kikimora
    /usr/local/bin/kk
    /usr/share/bash-completion/completions/kikimora
    /usr/local/share/zsh/site-functions/_kikimora
    /usr/share/fish/vendor_completions.d/kikimora.fish
    /usr/local/libexec/kikimora/leshy/reconcile
    /usr/local/libexec/kikimora/leshy/check-config
    /usr/local/libexec/kikimora/leshy/build-config
    /usr/local/libexec/kikimora/leshy/route-lifecycle
    /usr/local/libexec/kikimora/leshy/route-watch
    /usr/local/libexec/kikimora/leshy/health-watch
    /usr/local/libexec/kikimora/cli/common.sh
    /usr/local/libexec/kikimora/cli/help.sh
    /usr/local/libexec/kikimora/cli/dns.sh
    /usr/local/libexec/kikimora/cli/service.sh
    /usr/local/libexec/kikimora/cli/status.sh
    /usr/local/libexec/kikimora/cli/domains.sh
    /usr/local/libexec/kikimora/cli/config.sh
    /usr/local/libexec/kikimora/cli/maintenance.sh
    /etc/kikimora/leshy/vpn.conf
    /etc/kikimora/leshy/domains/primary.txt
    /etc/kikimora/leshy/domains/secondary.txt
    /etc/kikimora/leshy/domains/bypass.txt
    /etc/kikimora/leshy/routing.conf
    /etc/kikimora/leshy/config.toml
    /etc/systemd/system/leshy.service
    /etc/systemd/system/leshy-route-watch.service
    /etc/systemd/system/leshy-health-watch.service
  )
  local f
  for f in "${files[@]}"; do verify_one "$f" || failed=1; done

  if [[ -x /usr/local/libexec/kikimora/leshy/check-config && -x /usr/local/bin/leshy ]]; then
    if /usr/local/libexec/kikimora/leshy/check-config /usr/local/bin/leshy /etc/kikimora/leshy/config.toml; then
      printf 'OK    Leshy configuration\n'
    else
      printf 'FAIL  Leshy configuration\n'; failed=1
    fi
  fi

  systemd-analyze verify /etc/systemd/system/leshy.service \
    /etc/systemd/system/leshy-route-watch.service \
    /etc/systemd/system/leshy-health-watch.service || failed=1

  ((failed == 0)) || die "verification completed with errors"
  printf '\nVerification completed successfully.\n'
}

# ── Doctor ──────────────────────────────────────────────────────────────────

cmd_doctor() {
  require_root
  printf 'Kikimora %s\n\n' "$VERSION"
  if [[ -r /var/lib/kikimora/leshy/installation.env ]]; then
    printf '== Leshy origin ==\n'
    cat /var/lib/kikimora/leshy/installation.env
    printf '\n'
  fi
  printf '== Versions ==\n'
  /usr/local/bin/leshy --version 2>/dev/null || true
  printf '\n== Services ==\n'
  systemctl --no-pager --full status leshy.service leshy-route-watch.service leshy-health-watch.service 2>&1 || true
  printf '\n== Interfaces ==\n'
  ip -brief link || true
  printf '\n== Routes ==\n'
  ip route show table main || true
  printf '\n== Kikimora route lists ==\n'
  kk routes list 2>/dev/null || true
  printf '\n== Runtime ==\n'
  ls -la /run/kikimora/leshy/vpn 2>/dev/null || true
  printf '\n== DNS ==\n'
  resolvectl status 2>/dev/null || cat /etc/resolv.conf
  printf '\n== Recent journal logs ==\n'
  journalctl -u leshy.service -u leshy-route-watch.service -u leshy-health-watch.service -n 80 --no-pager 2>/dev/null || true
  printf '\n== Verification ==\n'
  cmd_verify
}

# ── Logs ────────────────────────────────────────────────────────────────────

cmd_logs() {
  local lines=100 follow=1 all=0
  while (($#)); do case "$1" in --lines|-n) lines="${2:?}"; shift 2;; --no-follow) follow=0; shift;; --all) all=1; shift;; -f|--follow) follow=1; shift;; -h|--help) printf 'Usage: kk logs [-n N|--lines N] [--no-follow] [--all]\n'; return;; *) die "unknown logs option: $1";; esac; done
  local -a args=(-n "$lines"); ((follow)) && args+=(-f) || args+=(--no-pager); if ((all)); then args+=(-u leshy.service -u leshy-route-watch.service -u leshy-health-watch.service); else args+=(-u leshy.service); fi; [[ -t 1 ]] && args+=(--output=short) || args+=(--no-hostname); exec journalctl "${args[@]}"
}

# ── Debug log bundle ────────────────────────────────────────────────────────

debuglog_section() {
  printf '\n== %s ==\n' "$*"
}

debuglog_run() {
  local title="$1"
  shift
  debuglog_section "$title"
  printf '$'
  printf ' %q' "$@"
  printf '\n'
  "$@" 2>&1 || true
}

cmd_debuglog() {
  require_root
  local output='' since='' lines='' arg
  while (($#)); do
    arg="$1"
    case "$arg" in
      -o|--output)
        output="${2:?$arg requires a path}"
        shift 2
        ;;
      --since)
        since="${2:?--since requires a journalctl time expression}"
        shift 2
        ;;
      -n|--lines)
        lines="${2:?$arg requires a number}"
        [[ "$lines" =~ ^[0-9]+$ ]] || die "invalid line count: $lines"
        shift 2
        ;;
      -h|--help)
        cat <<'HELP'
Usage: sudo kikimora debuglog [OPTIONS]

Collect a Linux Kikimora debug bundle into one log file.

Options:
  -o, --output PATH       Write to PATH instead of ./kikimora-debug-TIMESTAMP.log
  --since TIME           Use journalctl --since TIME instead of current boot
  -n, --lines N          Limit journal output to the last N matching records
  -h, --help             Show this help

Examples:
  sudo kk debuglog
  sudo kk debuglog --since "30 minutes ago"
  sudo kk debuglog -n 500 -o kikimora-debug.log
HELP
        return
        ;;
      *) die "unknown debuglog option: $arg" ;;
    esac
  done

  [[ -n "$output" ]] || output="./kikimora-debug-$(date +%Y%m%d-%H%M%S).log"
  install -d -m 0755 "$(dirname -- "$output")"
  : > "$output" || die "cannot write debug log: $output"
  chmod 0600 "$output" 2>/dev/null || true

  local -a journal_args=(
    -u leshy.service
    -u leshy-route-watch.service
    -u leshy-health-watch.service
    --no-pager
    -o short-iso
  )
  if [[ -n "$since" ]]; then
    journal_args=(--since "$since" "${journal_args[@]}")
  else
    journal_args=(-b "${journal_args[@]}")
  fi
  [[ -n "$lines" ]] && journal_args=(-n "$lines" "${journal_args[@]}")

  {
    debuglog_section 'Meta'
    printf 'Generated: %s\n' "$(date -Is)"
    printf 'User: %s\n' "${SUDO_USER:-$(id -un)}"
    printf 'Effective UID: %s\n' "$EUID"
    printf 'Working directory: %s\n' "$PWD"
    printf 'Output file: %s\n' "$output"

    debuglog_section 'System'
    uname -a || true
    [[ -r /etc/os-release ]] && cat /etc/os-release || true
    command -v hostnamectl >/dev/null 2>&1 && hostnamectl 2>&1 || true
    command -v timedatectl >/dev/null 2>&1 && timedatectl 2>&1 || true

    debuglog_section 'Versions'
    printf 'Kikimora: %s\n' "$VERSION"
    /usr/local/bin/leshy --version 2>&1 || true

    debuglog_section 'Installation state'
    if [[ -r /var/lib/kikimora/leshy/installation.env ]]; then
      cat /var/lib/kikimora/leshy/installation.env
    else
      printf 'missing: /var/lib/kikimora/leshy/installation.env\n'
    fi

    debuglog_run 'Kikimora status' /usr/local/bin/kk status --verbose
    debuglog_run 'Managed service status' systemctl status leshy.service leshy-route-watch.service leshy-health-watch.service --no-pager -l
    debuglog_run 'Systemd jobs' systemctl list-jobs
    debuglog_run 'Managed unit definitions' systemctl cat leshy.service leshy-route-watch.service leshy-health-watch.service

    debuglog_section 'Journal'
    printf '$ journalctl'
    printf ' %q' "${journal_args[@]}"
    printf '\n'
    journalctl "${journal_args[@]}" 2>&1 || true

    debuglog_run 'Interfaces' ip -br link
    debuglog_run 'IPv4 addresses' ip -4 addr
    debuglog_run 'IPv6 addresses' ip -6 addr
    debuglog_run 'IPv4 main routes' ip route show table main
    debuglog_run 'IPv6 main routes' ip -6 route show table main
    debuglog_run 'Routing rules' ip rule show

    debuglog_section 'Kikimora configuration files'
    for path in \
      /etc/kikimora/leshy/vpn.conf \
      /etc/kikimora/leshy/routing.conf \
      /etc/kikimora/leshy/config.toml; do
      printf '\n--- %s ---\n' "$path"
      if [[ -r "$path" ]]; then
        sed -n '1,220p' "$path"
      else
        printf 'missing or unreadable\n'
      fi
    done

    debuglog_section 'Kikimora list files'
    find /etc/kikimora/leshy -maxdepth 3 -type f \
      \( -path '*/domains/*' -o -path '*/routes/*' \) \
      -print -exec sed -n '1,220p' {} \; 2>&1 || true

    debuglog_run 'Runtime directory' ls -la /run/kikimora/leshy/vpn
    debuglog_run 'Resolver status' resolvectl status
    debuglog_run 'Resolver DNS view' resolvectl dns
    debuglog_run 'Resolver domain view' resolvectl domain
    debuglog_run 'Leshy DNS status' /usr/local/sbin/leshy-dns status
    debuglog_run 'Leshy DNS check' /usr/local/sbin/leshy-dns check

    debuglog_section 'Leshy direct probe'
    if command -v dig >/dev/null 2>&1; then
      dig @127.0.0.1 -p 53053 . NS +time=1 +tries=1 +short 2>&1 || true
    else
      printf 'dig not found\n'
    fi

    debuglog_section 'Verification'
    cmd_verify || true
  } > "$output" 2>&1

  printf 'Debug log written: %s\n' "$output"
}

# ── Backup / Restore ────────────────────────────────────────────────────────

cmd_backup() {
  require_root
  local stamp archive
  stamp="$(date +%Y%m%d-%H%M%S)"
  mkdir -p "$BACKUP_DIR"
  archive="$BACKUP_DIR/leshy-${stamp}.tar.gz"
  tar -czf "$archive" --ignore-failed-read \
    /etc/kikimora/leshy \
    /etc/systemd/system/leshy.service \
    /etc/systemd/system/leshy.service.d \
    /etc/systemd/system/leshy-route-watch.service \
    /etc/systemd/system/leshy-health-watch.service
  chmod 0600 "$archive"
  printf 'Backup created: %s\n' "$archive"
}

cmd_restore() {
  require_root
  local archive="${1:-}"
  if [[ "$archive" == "--help" ]]; then
    printf 'Usage: sudo kikimora restore [BACKUP.tar.gz]\nWithout a path, restores the latest backup from %s.\n' "$BACKUP_DIR"
    return
  fi
  if [[ -z "$archive" ]]; then
    archive="$(find "$BACKUP_DIR" -maxdepth 1 -type f -name 'leshy-*.tar.gz' -printf '%T@ %p\n' 2>/dev/null | sort -nr | head -1 | cut -d' ' -f2-)"
  fi
  [[ -n "$archive" && -f "$archive" ]] || die "backup archive not found"
  systemctl stop leshy.service 2>/dev/null || true
  tar -xzf "$archive" -C /
  systemctl daemon-reload
  printf 'Restored from: %s\n' "$archive"
  printf 'Leshy not started automatically.\n'
}

# ── Upgrade ─────────────────────────────────────────────────────────────────

cmd_upgrade() {
  require_root
  local source="${1:-}"
  [[ "$source" != "--help" && -n "$source" ]] || {
    printf 'Usage: sudo kikimora upgrade PATH\nPATH — Kikimora release directory or ZIP archive.\n'
    return
  }
  local tmp="" root="$source"
  if [[ -f "$source" && "$source" == *.zip ]]; then
    tmp="$(mktemp -d /tmp/kikimora-upgrade.XXXXXX)"
    trap '[[ -n "${tmp:-}" ]] && rm -rf "$tmp"' EXIT
    unzip -q "$source" -d "$tmp"
    root="$(find "$tmp" -mindepth 1 -maxdepth 1 -type d | head -1)"
  fi
  if [[ -x "$root/linux/kikimora" && -x "$root/install.sh" ]]; then
    exec "$root/install.sh" "${@:2}"
  fi
  [[ -x "$root/kikimora" && -x "$root/install.sh" ]] || die "PATH does not contain a Kikimora package"
  exec "$root/kikimora" install "${@:2}"
}

# ── Uninstall ───────────────────────────────────────────────────────────────

cmd_uninstall() {
  require_root
  local purge=0 assume_yes=0 arg answer=''
  for arg in "$@"; do
    case "$arg" in
      --purge) purge=1 ;;
      -y|--yes) assume_yes=1 ;;
      -h|--help)
        cat <<'HELP'
Usage: sudo kikimora uninstall [--purge] [-y|--yes]

Removes only components created by Kikimora:
  • kikimora command, kk alias and completions;
  • Kikimora-created systemd units and lifecycle/watchdog scripts;
  • DNS integration and temporary runtime state.

The /usr/local/bin/leshy binary is not removed: Leshy may have existed before Kikimora.
Without --purge, configuration and backups are kept.
With --purge, additionally removes:
  /etc/kikimora/leshy
  /var/lib/kikimora/leshy
  /var/backups/kikimora/leshy
HELP
        return ;;
      *) die "unknown uninstall option: $arg" ;;
    esac
  done

  if ((assume_yes == 0)); then
    [[ -e /dev/tty ]] || die 'no interactive terminal; retry with --yes'
    if ((purge == 1)); then
      printf 'Remove Kikimora and completely delete all Leshy configuration created by it? [y/N]: ' >/dev/tty
    else
      printf 'Remove Kikimora, keeping its configuration and backups? [y/N]: ' >/dev/tty
    fi
    IFS= read -r answer </dev/tty || true
    case "$answer" in y|Y|yes|YES|д|Д|да|ДА) ;; *) printf 'Uninstall cancelled.\n'; return ;; esac
  fi

  printf 'Stopping managed services and restoring DNS...\n'
  if [[ -x /usr/local/sbin/leshy-dns ]]; then
    /usr/local/sbin/leshy-dns disable || true
  fi
  systemctl disable --now leshy.service leshy-route-watch.service leshy-health-watch.service 2>/dev/null || true
  /usr/local/libexec/kikimora/leshy/route-lifecycle cleanup 2>/dev/null || true

  rm -f -- \
    /etc/systemd/system/leshy.service \
    /etc/systemd/system/leshy-route-watch.service \
    /etc/systemd/system/leshy-health-watch.service \
    /etc/systemd/system/leshy.service.d/route-cleanup.conf
  rmdir /etc/systemd/system/leshy.service.d 2>/dev/null || true
  systemctl daemon-reload
  systemctl reset-failed leshy.service leshy-route-watch.service leshy-health-watch.service 2>/dev/null || true

  rm -rf -- /usr/local/libexec/kikimora
  rm -f -- /usr/local/sbin/leshy-dns
  rm -f -- /usr/share/bash-completion/completions/kikimora
  rm -f -- /usr/local/share/zsh/site-functions/_kikimora
  rm -f -- /usr/share/fish/vendor_completions.d/kikimora.fish
  rm -rf -- /run/kikimora/leshy

  if ((purge == 1)); then
    rm -rf -- /etc/kikimora/leshy /var/lib/kikimora/leshy /var/backups/kikimora/leshy
    rmdir /etc/kikimora /var/lib/kikimora /var/backups/kikimora 2>/dev/null || true
    printf 'Kikimora configuration, state and backups for Leshy deleted.\n'
  else
    printf 'Configuration kept in /etc/kikimora/leshy.\n'
    printf 'Backups kept in /var/backups/kikimora/leshy.\n'
  fi

  rm -f -- /usr/local/bin/kk /usr/local/sbin/kikimora
  printf 'Kikimora removed. Leshy binary unchanged.\n'
}

# ── Shell completion helpers ────────────────────────────────────────────────

cmd_completion() {
  local shell="${1:-}"
  case "$shell" in
    bash) cat /usr/share/bash-completion/completions/kikimora ;;
    zsh) cat /usr/local/share/zsh/site-functions/_kikimora ;;
    fish) cat /usr/share/fish/vendor_completions.d/kikimora.fish ;;
    -h|--help|'')
      cat <<'HELP'
Usage: kikimora completion SHELL

Print the installed completion script.
Supported shells: bash, zsh, fish.
This command is usually not needed: completions are installed automatically.
HELP
      ;;
    *) die "unsupported shell: $shell" ;;
  esac
}
