#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly ROOT_DIR

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
