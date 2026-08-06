#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly ROOT_DIR

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  cat <<'EOF_COMPONENTS'
Kikimora installer commands:
  sudo ./install.sh [OPTIONS]
      Install or update Kikimora. Optional browser integration is not installed.

  sudo ./install.sh chrome-extension
      Install only the optional Chrome extension and native messaging host.

Platform installer options:
EOF_COMPONENTS
fi

if [[ "${1:-}" == "chrome-extension" ]]; then
  shift
  if [[ "$(uname -s)" != "Linux" ]]; then
    printf 'Error: the Kikimora Chrome integration is supported on Linux only.\n' >&2
    exit 1
  fi
  exec "${ROOT_DIR}/browser/chrome/install.sh" "$@"
fi

case "$(uname -s)" in
  Linux)
    exec "${ROOT_DIR}/linux/install.sh" "$@"
    ;;
  Darwin)
    exec "${ROOT_DIR}/macos/install.sh" "$@"
    ;;
  *)
    printf 'Error: Kikimora installer supports Linux and macOS only.\n' >&2
    exit 1
    ;;
esac
