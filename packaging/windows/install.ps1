# SPDX-License-Identifier: MIT
#
# install.ps1 — elevated Kikimora Windows installer (portable package).
#
# Installs the staged binaries into the install root, creates the crash-safe
# ProgramData layout with restrictive ACLs and registers the KikimoraCore
# service (automatic start, staged restart recovery). Fresh installs never
# write VPN role desired state: the service starts with all roles disabled.
#
# Run from an elevated prompt, inside the extracted package directory:
#   powershell -ExecutionPolicy Bypass -File install.ps1 [-Purge]

param (
    [switch]$Purge
)

$ErrorActionPreference = "Stop"
$identity = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $identity.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "install.ps1 must run from an elevated prompt"
}

$InstallRoot = Join-Path $Env:ProgramFiles "Kikimora"
$DataRoot = Join-Path $Env:ProgramData "Kikimora"

Write-Host "Kikimora Windows installer"
Write-Host "  install root: $InstallRoot"
Write-Host "  data root:    $DataRoot"

if (-not (Test-Path (Join-Path $PSScriptRoot "kikimora-core.exe"))) {
    throw "run install.ps1 from the extracted package directory (kikimora-core.exe not found next to it)"
}

New-Item -ItemType Directory -Force -Path $InstallRoot | Out-Null
Copy-Item (Join-Path $PSScriptRoot "kikimora-core.exe") $InstallRoot -Force
Copy-Item (Join-Path $PSScriptRoot "kikimora-toad.exe") $InstallRoot -Force
# wintun.dll must sit next to kikimora-toad.exe: the driver loads it with
# LOAD_LIBRARY_SEARCH_APPLICATION_DIR only, so without it no Wintun adapter
# can ever be created and every AWG2 role fails.
if (Test-Path (Join-Path $PSScriptRoot "wintun.dll")) {
    Copy-Item (Join-Path $PSScriptRoot "wintun.dll") $InstallRoot -Force
} else {
    Write-Warning "wintun.dll is not in the package: AWG2 roles will not start"
}
# The UI keeps its own directory with the complete Qt runtime (ui/): it must
# not share a directory with the core, whose GnuTLS/curl/ffi DLLs collide
# with the UI's.
$uiSource = Join-Path $PSScriptRoot "ui"
if (Test-Path (Join-Path $PSScriptRoot "kikimora-ui.exe")) {
    $uiDest = Join-Path $InstallRoot "ui"
    New-Item -ItemType Directory -Force -Path $uiDest | Out-Null
    Copy-Item (Join-Path $PSScriptRoot "kikimora-ui.exe") $uiDest -Force
    if (Test-Path $uiSource) {
        Copy-Item (Join-Path $uiSource "*") $uiDest -Recurse -Force
    } else {
        Write-Warning "kikimora-ui.exe staged without the ui/ payload: the UI will not start"
    }
}
Write-Host "  binaries installed"

foreach ($dir in @(
    $DataRoot,
    (Join-Path $DataRoot "state"),
    (Join-Path $DataRoot "toads"),
    (Join-Path $DataRoot "logs"),
    (Join-Path $DataRoot "leshy"),
    (Join-Path $DataRoot "leshy\vpn")
)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}
# ProgramData inherits the machine defaults (BUILTIN\Users keep Read+Execute
# and can create files in subdirectories). Role TOMLs hold VPN private keys
# and endpoint_policy commands execute as SYSTEM: state, toads and logs must
# be SYSTEM/Administrators only.
foreach ($hardened in @(
    (Join-Path $DataRoot "state"),
    (Join-Path $DataRoot "toads"),
    (Join-Path $DataRoot "logs"),
    (Join-Path $DataRoot "leshy")
)) {
    & icacls $hardened /inheritance:r /grant "SYSTEM:(OI)(CI)F" /grant "Administrators:(OI)(CI)F" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "icacls failed to harden $hardened" }
}
Write-Host "  ProgramData layout ready (state/toads/logs/leshy ACLs hardened)"

# The core service registers itself with automatic start and staged restart
# recovery, and starts immediately; roles stay disabled on fresh installs.
$coreExe = Join-Path $InstallRoot "kikimora-core.exe"
& $coreExe service install
if ($LASTEXITCODE -ne 0) { throw "kikimora-core service install failed" }
Write-Host "  KikimoraCore service installed and started"
Write-Host "Kikimora installation complete."

if ($Purge) {
    Write-Host "  (-Purge requested) stale state under $DataRoot is NOT removed automatically;"
    Write-Host "  run uninstall.ps1 -Purge for explicit purge semantics."
}
