# SPDX-License-Identifier: MIT
#
# uninstall.ps1 — elevated Kikimora Windows removal.
#
# Stops and deletes the KikimoraCore service and removes the installed
# binaries. Desired state, logs and the Leshy publication directory under
# ProgramData are preserved unless -Purge is requested: ordinary
# uninstall/reinstall must survive the user's VPN role choices, while an
# explicit purge must remove everything including the Wintun adapters of
# retired roles (the adapters are keyed by the deterministic role GUID and
# are removed with the driver-level uninstall owned by the package).

param (
    [switch]$Purge
)

$ErrorActionPreference = "Stop"
$identity = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $identity.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "uninstall.ps1 must run from an elevated prompt"
}

$InstallRoot = Join-Path $Env:ProgramFiles "Kikimora"
$DataRoot = Join-Path $Env:ProgramData "Kikimora"
$coreExe = Join-Path $InstallRoot "kikimora-core.exe"

Write-Host "Kikimora Windows uninstaller"

if (Test-Path $coreExe) {
    # Stop before Delete: the SCM "uninstall" verb only deletes the
    # registration, and a still-running service holds the exe images, so the
    # file removal below used to throw and leave both binaries and the
    # service behind.
    Stop-Service -Name "KikimoraCore" -Force -ErrorAction SilentlyContinue
    $deadline = (Get-Date).AddSeconds(30)
    while ((Get-Date) -lt $deadline) {
        $svc = Get-Service -Name "KikimoraCore" -ErrorAction SilentlyContinue
        if (-not $svc -or $svc.Status -eq [ServiceProcess.ServiceControllerStatus]::Stopped) { break }
        Start-Sleep -Milliseconds 250
    }
    & $coreExe service uninstall
    if ($LASTEXITCODE -ne 0) { Write-Warning "kikimora-core service uninstall returned $LASTEXITCODE (continuing)" }
} else {
    Write-Host "  core binary not present; cleaning service registration anyway"
    $service = Get-Service -Name "KikimoraCore" -ErrorAction SilentlyContinue
    if ($service) {
        Stop-Service -Name "KikimoraCore" -Force -ErrorAction SilentlyContinue
        & sc.exe delete "KikimoraCore" | Out-Null
    }
}

# The UI dies with the service stop only if it shares the session; make sure
# no tray process holds the ui payload while it is removed.
Stop-Process -Name kikimora-ui -Force -ErrorAction SilentlyContinue
# The in-app autostart toggle writes HKCU Run values that point at the exe
# being removed. Sweep every local profile's hive: a stale value starts
# nothing but keeps re-appearing in autoruns tools.
Get-ChildItem "HKU:\" -ErrorAction SilentlyContinue | ForEach-Object {
    $runKey = "Registry::" + $_.Name + "\Software\Microsoft\Windows\CurrentVersion\Run"
    if (Get-ItemProperty -Path $runKey -Name "Kikimora" -ErrorAction SilentlyContinue) {
        Remove-ItemProperty -Path $runKey -Name "Kikimora" -Force -ErrorAction SilentlyContinue
        Write-Host "  removed autostart Run entry from $($_.PSChildName)"
    }
}
Start-Sleep -Milliseconds 500
foreach ($file in @("kikimora-core.exe", "kikimora-toad.exe", "wintun.dll")) {
    $path = Join-Path $InstallRoot $file
    if (Test-Path $path) {
        Remove-Item $path -Force
        Write-Host "  removed $file"
    }
}
$uiDir = Join-Path $InstallRoot "ui"
if (Test-Path $uiDir) {
    Remove-Item $uiDir -Recurse -Force
    Write-Host "  removed ui\"
}
if ((Get-ChildItem $InstallRoot -ErrorAction SilentlyContinue | Measure-Object).Count -eq 0) {
    Remove-Item $InstallRoot -Force
    Write-Host "  removed install root"
}

if ($Purge) {
    if (Test-Path $DataRoot) {
        Remove-Item $DataRoot -Recurse -Force
        Write-Host "  purged $DataRoot (explicit purge)"
    }
    Write-Host "  NOTE: Wintun adapters of retired roles persist until the driver"
    Write-Host "  package is removed (wintun Uninstall); the installer owns that step."
} else {
    Write-Host "  preserved $DataRoot (desired state, logs, Leshy publications)"
}
Write-Host "Kikimora uninstall complete."
