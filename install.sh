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
  MINGW*|MSYS*|CYGWIN*)
    printf 'Error: use the Windows PowerShell installer: .\\install.ps1\n' >&2
    exit 1
    ;;
  *)
    printf 'Error: Kikimora installer supports Linux and macOS via install.sh. Use install.ps1 on Windows.\n' >&2
    exit 1
    ;;
esac
