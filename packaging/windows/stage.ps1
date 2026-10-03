# SPDX-License-Identifier: MIT
#
# stage.ps1 — Windows packaging scaffold (experimental).
#
# Usage:
#   powershell -File stage.ps1 -OutDir .\dist [-UiExe .\kikimora-ui.exe]
#       [-CoreExe .\kikimora-core.exe] [-ToadExe .\kikimora-toad.exe]

param (
    [string]$OutDir = ".\dist",
    [string]$UiExe = "",
    [string]$CoreExe = "",
    [string]$ToadExe = ""
)

$Version = "0.0.0"
$VersionFile = "..\..\VERSION"
if (Test-Path $VersionFile) {
    $Version = (Get-Content $VersionFile -Raw).Trim()
}

Write-Host "Windows packaging scaffold (experimental)"
Write-Host "  Version: $Version"
Write-Host "  Output:  $OutDir"

New-Item -ItemType Directory -Force -Path "$OutDir\kikimora-$Version-windows-amd64" | Out-Null

# Stage UI binary if provided
if ($UiExe -and (Test-Path $UiExe)) {
    Copy-Item $UiExe "$OutDir\kikimora-$Version-windows-amd64\kikimora-ui.exe"
    Write-Host "  staged: kikimora-ui.exe"
}

# Stage or build core
if ($CoreExe -and (Test-Path $CoreExe)) {
    Copy-Item $CoreExe "$OutDir\kikimora-$Version-windows-amd64\kikimora-core.exe"
    Write-Host "  staged: kikimora-core.exe (provided)"
}
else {
    Write-Host "  SKIP: kikimora-core.exe - not provided; cross-build with:"
    Write-Host "    cd toad && go build -ldflags '-X main.coreVersion=$Version' -o kikimora-core.exe ./cmd/kikimora-core"
}

# Stage or build toad
if ($ToadExe -and (Test-Path $ToadExe)) {
    Copy-Item $ToadExe "$OutDir\kikimora-$Version-windows-amd64\kikimora-toad.exe"
    Write-Host "  staged: kikimora-toad.exe (provided)"
}
else {
    Write-Host "  SKIP: kikimora-toad.exe - not provided; cross-build with:"
    Write-Host "    cd toad && go build -ldflags '-X main.toadVersion=$Version' -o kikimora-toad.exe ./cmd/kikimora-toad"
}

# Write manifest
$manifest = @"
{
    "product": "Kikimora",
    "version": "$Version",
    "platform": "windows",
    "architecture": "amd64",
    "status": "experimental",
    "components": [
        $(if ($UiExe -and (Test-Path $UiExe)) { '{"name":"kikimora-ui","path":"kikimora-ui.exe"},' })
        {"name":"kikimora-core","path":"kikimora-core.exe"},
        {"name":"kikimora-toad","path":"kikimora-toad.exe"}
    ]
}
"@
$manifest | Out-File -Encoding utf8 "$OutDir\kikimora-$Version-windows-amd64\manifest.json"
Write-Host "  created: manifest.json"

Write-Host ""
Write-Host "Windows packaging scaffold complete: $OutDir\kikimora-$Version-windows-amd64"
Write-Host "Experimental - no service/driver installation, no supported installer."
exit 0