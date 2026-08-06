#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR

python3 -m json.tool "${SCRIPT_DIR}/extension/manifest.json" >/dev/null
python3 -m json.tool "${SCRIPT_DIR}/native-host/com.kikimora.domain_manager.json" >/dev/null
python3 -c 'import pathlib, sys; p=pathlib.Path(sys.argv[1]); compile(p.read_text(encoding="utf-8"), str(p), "exec")' \
  "${SCRIPT_DIR}/native-host/kikimora_native_host.py"
node --check "${SCRIPT_DIR}/extension/popup.js"
python3 -m unittest discover -s "${SCRIPT_DIR}/tests" -p 'test_*.py'
bash -n "${SCRIPT_DIR}/install.sh"
bash -n "${SCRIPT_DIR}/uninstall.sh"
printf 'Chrome integration tests: OK\n'
