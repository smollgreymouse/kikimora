# SPDX-License-Identifier: MIT
#
# test_package.ps1 — static contract test for the Windows portable package.
# Runs unprivileged and never installs anything: it stages the package, then
# verifies the layout, checksums and script contracts the VM acceptance relies
# on. Exit code 0 = contract holds.
#
#   powershell -File test_package.ps1 [-OutDir .\dist-test]

param (
    [string]$OutDir = ".\dist-test"
)

$ErrorActionPreference = "Stop"
if (-not [System.IO.Path]::IsPathRooted($OutDir)) {
    $OutDir = Join-Path (Get-Location) $OutDir
}
$failures = 0
function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) {
        Write-Host "FAIL: $Message"
        $script:failures++
    } else {
        Write-Host "ok: $Message"
    }
}

# Build the payload binaries first so staging is a single deterministic pass.
$binDir = Join-Path $OutDir "bin"
New-Item -ItemType Directory -Force -Path $binDir | Out-Null
$toadDir = Join-Path $PSScriptRoot "..\..\toad"
$Env:CGO_ENABLED = "0"; $Env:GOOS = "windows"; $Env:GOARCH = "amd64"
Push-Location $toadDir
try {
    go build -o (Join-Path $binDir "kikimora-core.exe") ./cmd/kikimora-core
    if ($LASTEXITCODE -ne 0) { throw "go build kikimora-core failed" }
    go build -o (Join-Path $binDir "kikimora-toad.exe") ./cmd/kikimora-toad
    if ($LASTEXITCODE -ne 0) { throw "go build kikimora-toad failed" }
} finally {
    Pop-Location
    Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
}

$packageScript = Join-Path $PSScriptRoot "stage.ps1"
& $packageScript -OutDir $OutDir -CoreExe (Join-Path $binDir "kikimora-core.exe") -ToadExe (Join-Path $binDir "kikimora-toad.exe") | Out-Null
if ($LASTEXITCODE -ne 0) { throw "stage.ps1 failed" }

$version = (Get-Content (Join-Path $PSScriptRoot "..\..\VERSION") -Raw).Trim()
$packageDir = Join-Path $OutDir "kikimora-$version-windows-amd64"

Assert-True (Test-Path (Join-Path $packageDir "kikimora-core.exe")) "package stages kikimora-core.exe"
Assert-True (Test-Path (Join-Path $packageDir "kikimora-toad.exe")) "package stages kikimora-toad.exe"
Assert-True (Test-Path (Join-Path $packageDir "install.ps1")) "package stages install.ps1"
Assert-True (Test-Path (Join-Path $packageDir "uninstall.ps1")) "package stages uninstall.ps1"
Assert-True (Test-Path (Join-Path $packageDir "manifest.json")) "package stages manifest.json"
Assert-True (Test-Path (Join-Path $packageDir "checksums.txt")) "package stages checksums.txt"
Assert-True (Test-Path (Join-Path $OutDir "kikimora-$version-windows-amd64.zip")) "package builds the zip archive"

$manifest = Get-Content (Join-Path $packageDir "manifest.json") -Raw | ConvertFrom-Json
Assert-True ($manifest.version -eq $version) "manifest carries the package version"
Assert-True ($manifest.components.Count -ge 2) "manifest lists at least core and toad"

# checksums.txt must cover every staged payload file exactly.
$expected = Get-ChildItem $packageDir -File |
    Where-Object { $_.Name -ne "checksums.txt" } |
    Sort-Object Name | ForEach-Object { $_.Name }
$recorded = Get-Content (Join-Path $packageDir "checksums.txt") |
    ForEach-Object { ($_ -split "  ", 2)[1] } | Sort-Object
$diff = Compare-Object $expected $recorded
Assert-True ($null -eq $diff) "checksums.txt covers exactly the staged files"

foreach ($line in (Get-Content (Join-Path $packageDir "checksums.txt"))) {
    $parts = $line -split "  ", 2
    $actual = (Get-FileHash (Join-Path $packageDir $parts[1]) -Algorithm SHA256).Hash.ToLower()
    Assert-True ($actual -eq $parts[0]) "checksum matches for $($parts[1])"
}

# Installer scripts must require elevation and must not mutate VPN desired
# state on fresh installs.
$install = Get-Content (Join-Path $packageDir "install.ps1") -Raw
Assert-True ($install -match "must run from an elevated prompt") "install.ps1 requires elevation"
Assert-True ($install -match "service install") "install.ps1 registers the core service"
$uninstall = Get-Content (Join-Path $packageDir "uninstall.ps1") -Raw
Assert-True ($uninstall -match "service uninstall") "uninstall.ps1 removes the core service"
Assert-True ($uninstall -match 'preserved \$DataRoot') "uninstall keeps desired state without -Purge"

Write-Host ""
if ($script:failures -gt 0) {
    Write-Host "package contract FAILED with $script:failures assertion(s)"
    exit 1
}
Write-Host "package contract holds ($version)"
