#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly ROOT_INSTALLER="${SCRIPT_DIR}/../../install.sh"
readonly BRAND_IMAGE="${SCRIPT_DIR}/../../kikimora.png"

python3 -m json.tool "${SCRIPT_DIR}/extension/manifest.json" >/dev/null
python3 -m json.tool "${SCRIPT_DIR}/native-host/com.kikimora.domain_manager.json" >/dev/null
python3 -c 'import pathlib, sys; p=pathlib.Path(sys.argv[1]); compile(p.read_text(encoding="utf-8"), str(p), "exec")' \
  "${SCRIPT_DIR}/native-host/kikimora_native_host.py"
node --check "${SCRIPT_DIR}/extension/popup.js"
python3 -m unittest discover -s "${SCRIPT_DIR}/tests" -p 'test_*.py'
bash -n "${ROOT_INSTALLER}"
bash -n "${SCRIPT_DIR}/install.sh"
bash -n "${SCRIPT_DIR}/uninstall.sh"

python3 - "${BRAND_IMAGE}" "${SCRIPT_DIR}/extension/manifest.json" <<'PY_ICON'
import json
import pathlib
import sys

image_path = pathlib.Path(sys.argv[1])
manifest_path = pathlib.Path(sys.argv[2])

if not image_path.is_file():
    raise SystemExit(f"missing Kikimora artwork: {image_path}")
if image_path.read_bytes()[:8] != b"\x89PNG\r\n\x1a\n":
    raise SystemExit(f"not a PNG image: {image_path}")

manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
expected_icons = {"16", "32", "48", "128"}
icons = manifest.get("icons", {})
if set(icons) != expected_icons or any(value != "kikimora.png" for value in icons.values()):
    raise SystemExit("manifest icons must use kikimora.png for 16/32/48/128")

action_icons = manifest.get("action", {}).get("default_icon", {})
if action_icons != {"16": "kikimora.png", "32": "kikimora.png"}:
    raise SystemExit("toolbar icon must use kikimora.png")
PY_ICON

grep -Fq 'src="kikimora.png"' "${SCRIPT_DIR}/extension/popup.html"
grep -Fq 'BRAND_IMAGE_SOURCE="${SCRIPT_DIR}/../../kikimora.png"' "${SCRIPT_DIR}/install.sh"

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
