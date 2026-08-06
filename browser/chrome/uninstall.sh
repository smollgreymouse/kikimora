#!/usr/bin/env bash
set -Eeuo pipefail

readonly EXTENSION_DEST="/usr/local/share/kikimora/chrome-domain-extension"
readonly HOST_DEST="/usr/local/libexec/kikimora/browser/kikimora-native-host"
readonly HOST_MANIFEST="com.kikimora.domain_manager.json"
readonly -a MANIFEST_DIRS=(
  /etc/opt/chrome/native-messaging-hosts
  /etc/chromium/native-messaging-hosts
  /etc/opt/chrome_for_testing/native-messaging-hosts
)

[[ ${EUID} -eq 0 ]] || { printf 'Error: run via sudo\n' >&2; exit 1; }

rm -rf -- "${EXTENSION_DEST}"
rm -f -- "${HOST_DEST}"
for directory in "${MANIFEST_DIRS[@]}"; do
  rm -f -- "${directory}/${HOST_MANIFEST}"
done

printf 'Kikimora Chrome integration removed. Remove the unpacked extension in chrome://extensions.\n'
