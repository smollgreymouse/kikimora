# SPDX-License-Identifier: MIT
#
# stage.ps1 — Windows packaging scaffold (experimental).
#
# This script is a placeholder for future Windows release packaging.
# It is not part of the supported release pipeline.

Write-Host "Windows packaging scaffold - not yet implemented"
Write-Host "Cross-build kikimora-core.exe and kikimora-toad.exe via:"
Write-Host "  cd toad && go build -o kikimora-core.exe ./cmd/kikimora-core"
Write-Host "  cd toad && go build -o kikimora-toad.exe ./cmd/kikimora-toad"
exit 0