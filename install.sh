#!/usr/bin/env bash
set -Eeuo pipefail

readonly ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

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
