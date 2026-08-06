#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly ROOT_INSTALLER="${SCRIPT_DIR}/../../install.sh"

python3 -m json.tool "${SCRIPT_DIR}/extension/manifest.json" >/dev/null
python3 -m json.tool "${SCRIPT_DIR}/native-host/com.kikimora.domain_manager.json" >/dev/null
python3 -c 'import pathlib, sys; p=pathlib.Path(sys.argv[1]); compile(p.read_text(encoding="utf-8"), str(p), "exec")' \
  "${SCRIPT_DIR}/native-host/kikimora_native_host.py"
node --check "${SCRIPT_DIR}/extension/popup.js"
python3 -m unittest discover -s "${SCRIPT_DIR}/tests" -p 'test_*.py'
bash -n "${ROOT_INSTALLER}"
bash -n "${SCRIPT_DIR}/install.sh"
bash -n "${SCRIPT_DIR}/uninstall.sh"

installer_help="$(bash "${ROOT_INSTALLER}" --help)"
grep -Fq 'sudo ./install.sh chrome-extension' <<<"${installer_help}"
grep -Fq 'Optional browser integration is not installed' <<<"${installer_help}"

component_help="$(bash "${ROOT_INSTALLER}" chrome-extension --help)"
grep -Fq 'sudo ./install.sh chrome-extension' <<<"${component_help}"
grep -Fq 'installs Kikimora without the browser' <<<"${component_help}"

if grep -Fq 'browser/chrome/install.sh' "${SCRIPT_DIR}/../../linux/install.sh"; then
  printf 'Default Linux installer must not install the Chrome integration.\n' >&2
  exit 1
fi

printf 'Chrome integration tests: OK\n'
