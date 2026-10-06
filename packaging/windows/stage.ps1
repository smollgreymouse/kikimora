# SPDX-License-Identifier: MIT
#
# stage.ps1 — stage the Kikimora Windows portable package.
#
# Usage (from a shell with the binaries already built, or let the script build them):
#   powershell -File stage.ps1 -OutDir .\dist [-UiExe .\kikimora-ui.exe]
#       [-CoreExe .\kikimora-core.exe] [-ToadExe .\kikimora-toad.exe] [-SkipBuild]
#
# The package layout is the VM acceptance contract:
#   kikimora-<version>-windows-amd64/
#     kikimora-core.exe      service + control plane (owns Wintun adapters)
#     kikimora-toad.exe      per-role transport supervisor
#     kikimora-ui.exe        optional desktop UI (FakeCore on Windows today)
#     install.ps1            elevated installer: ProgramData layout + service
#     uninstall.ps1          elevated removal with explicit purge semantics
#     manifest.json          product/version/components
#     checksums.txt          SHA256 of every staged file

param (
    [string]$OutDir = ".\dist",
    [string]$UiExe = "",
    [string]$CoreExe = "",
    [string]$ToadExe = "",
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"

$Version = "0.0.0"
$VersionFile = Join-Path $PSScriptRoot "..\..\VERSION"
if (Test-Path $VersionFile) {
    $Version = (Get-Content $VersionFile -Raw).Trim()
}
if ($Version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') {
    throw "invalid Kikimora version: $Version"
}

# Resolve all paths to absolute form up front: the script pushes into the
# toad directory for builds, and relative -OutDir values must not follow it.
if (-not [System.IO.Path]::IsPathRooted($OutDir)) {
    $OutDir = Join-Path (Get-Location) $OutDir
}
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$PackageDir = Join-Path $OutDir "kikimora-$Version-windows-amd64"

Write-Host "Kikimora Windows packaging"
Write-Host "  Version: $Version"
Write-Host "  Output:  $PackageDir"

New-Item -ItemType Directory -Force -Path $PackageDir | Out-Null

function Build-ToadBinary([string]$Name, [string]$OutPath) {
    Write-Host "  building $Name (windows/amd64)..."
    $ToadDir = Join-Path $RepoRoot "toad"
    $Env:CGO_ENABLED = "0"
    $Env:GOOS = "windows"
    $Env:GOARCH = "amd64"
    Push-Location $ToadDir
    try {
        go build -ldflags "-X main.coreVersion=$Version -X main.toadVersion=$Version" -o $OutPath ./cmd/$Name
        if ($LASTEXITCODE -ne 0) { throw "go build ./cmd/$Name failed" }
    } finally {
        Pop-Location
        Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    }
}

if ($CoreExe -and (Test-Path $CoreExe)) {
    Copy-Item $CoreExe (Join-Path $PackageDir "kikimora-core.exe") -Force
    Write-Host "  staged: kikimora-core.exe (provided)"
} elseif (-not $SkipBuild) {
    Build-ToadBinary "kikimora-core" (Join-Path $PackageDir "kikimora-core.exe")
    Write-Host "  staged: kikimora-core.exe (built)"
} else {
    throw "kikimora-core.exe not provided and -SkipBuild forbids building"
}

if ($ToadExe -and (Test-Path $ToadExe)) {
    Copy-Item $ToadExe (Join-Path $PackageDir "kikimora-toad.exe") -Force
    Write-Host "  staged: kikimora-toad.exe (provided)"
} elseif (-not $SkipBuild) {
    Build-ToadBinary "kikimora-toad" (Join-Path $PackageDir "kikimora-toad.exe")
    Write-Host "  staged: kikimora-toad.exe (built)"
} else {
    throw "kikimora-toad.exe not provided and -SkipBuild forbids building"
}

if ($UiExe -and (Test-Path $UiExe)) {
    Copy-Item $UiExe (Join-Path $PackageDir "kikimora-ui.exe") -Force
    Write-Host "  staged: kikimora-ui.exe (provided)"
}

Copy-Item (Join-Path $PSScriptRoot "install.ps1") $PackageDir -Force
Copy-Item (Join-Path $PSScriptRoot "uninstall.ps1") $PackageDir -Force
Write-Host "  staged: install.ps1, uninstall.ps1"

$components = @(
    @{ name = "kikimora-core"; path = "kikimora-core.exe" },
    @{ name = "kikimora-toad"; path = "kikimora-toad.exe" }
)
if ($UiExe -and (Test-Path $UiExe)) {
    $components += @{ name = "kikimora-ui"; path = "kikimora-ui.exe" }
}
$manifest = [ordered]@{
    product     = "Kikimora"
    version     = $Version
    platform    = "windows"
    architecture = "amd64"
    components  = $components
}
$manifest | ConvertTo-Json -Depth 3 | Out-File -Encoding utf8 (Join-Path $PackageDir "manifest.json")
Write-Host "  created: manifest.json"

# checksums.txt covers every staged payload file (scripts included).
$hashLines = Get-ChildItem $PackageDir -File |
    Where-Object { $_.Name -ne "checksums.txt" } |
    Sort-Object Name |
    ForEach-Object {
        $hash = (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower()
        "$hash  $($_.Name)"
    }
$hashLines | Out-File -Encoding ascii (Join-Path $PackageDir "checksums.txt")
Write-Host "  created: checksums.txt"

$zipPath = Join-Path $OutDir "kikimora-$Version-windows-amd64.zip"
if (Test-Path $zipPath) { Remove-Item $zipPath -Force }
Compress-Archive -Path $PackageDir -DestinationPath $zipPath
Write-Host "  created: $zipPath"
Write-Host "Windows packaging complete."
