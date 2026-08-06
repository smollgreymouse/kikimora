#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly EXTENSION_SOURCE="${SCRIPT_DIR}/extension"
readonly HOST_SOURCE="${SCRIPT_DIR}/native-host/kikimora_native_host.py"
readonly MANIFEST_SOURCE="${SCRIPT_DIR}/native-host/com.kikimora.domain_manager.json"
readonly EXTENSION_DEST="/usr/local/share/kikimora/chrome-domain-extension"
readonly HOST_DEST="/usr/local/libexec/kikimora/browser/kikimora-native-host"
readonly HOST_MANIFEST="com.kikimora.domain_manager.json"
readonly EXTENSION_ID="amllchapajpfdibbngeghpjbbofemaif"
readonly -a MANIFEST_DIRS=(
  /etc/opt/chrome/native-messaging-hosts
  /etc/chromium/native-messaging-hosts
  /etc/opt/chrome_for_testing/native-messaging-hosts
)

die() { printf 'Error: %s\n' "$*" >&2; exit 1; }
log() { printf '==> %s\n' "$*"; }

show_help() {
  cat <<'EOF_HELP'
Install only the optional Kikimora Chrome extension and native messaging host.

Usage:
  sudo ./install.sh chrome-extension

The normal command `sudo ./install.sh` installs Kikimora without the browser
extension. Kikimora must already be installed before this command is used.
EOF_HELP
}

while (($#)); do
  case "$1" in
    -h|--help)
      show_help
      exit 0
      ;;
    *)
      die "unknown Chrome extension installer option: $1"
      ;;
  esac
done

[[ ${EUID} -eq 0 ]] || die 'run via sudo'
command -v python3 >/dev/null 2>&1 || die 'python3 is required'
[[ -x /usr/bin/pkexec ]] || die 'pkexec is required (install the policykit-1 package)'
[[ -x /usr/local/sbin/kikimora ]] || die 'install Kikimora before the Chrome integration'

for file in manifest.json popup.html popup.css popup.js; do
  [[ -f "${EXTENSION_SOURCE}/${file}" ]] || die "missing extension file: ${file}"
done
[[ -f ${HOST_SOURCE} ]] || die "missing native host: ${HOST_SOURCE}"
[[ -f ${MANIFEST_SOURCE} ]] || die "missing native host manifest: ${MANIFEST_SOURCE}"

python3 -m json.tool "${EXTENSION_SOURCE}/manifest.json" >/dev/null
python3 -m json.tool "${MANIFEST_SOURCE}" >/dev/null
python3 -c 'import pathlib, sys; p=pathlib.Path(sys.argv[1]); compile(p.read_text(encoding="utf-8"), str(p), "exec")' \
  "${HOST_SOURCE}"

log 'Installing unpacked Chrome extension files'
install -d -o root -g root -m 0755 "${EXTENSION_DEST}"
install -o root -g root -m 0644 \
  "${EXTENSION_SOURCE}/manifest.json" \
  "${EXTENSION_SOURCE}/popup.html" \
  "${EXTENSION_SOURCE}/popup.css" \
  "${EXTENSION_SOURCE}/popup.js" \
  "${EXTENSION_DEST}/"

log 'Installing native messaging host'
install -d -o root -g root -m 0755 "$(dirname -- "${HOST_DEST}")"
install -o root -g root -m 0755 "${HOST_SOURCE}" "${HOST_DEST}"

log 'Registering native host for Chrome and Chromium'
for directory in "${MANIFEST_DIRS[@]}"; do
  install -d -o root -g root -m 0755 "${directory}"
  install -o root -g root -m 0644 "${MANIFEST_SOURCE}" "${directory}/${HOST_MANIFEST}"
done

printf '\nChrome integration installed.\n'
printf 'Extension ID: %s\n' "${EXTENSION_ID}"
printf 'Load unpacked extension from: %s\n' "${EXTENSION_DEST}"
printf '\nOpen chrome://extensions, enable Developer mode, choose Load unpacked, and select that directory.\n'
printf 'Each domain change uses pkexec and asks for administrator authorization.\n'
