# SPDX-License-Identifier: MIT
#
# test_managed_package_lifecycle.ps1 — the versioned Windows package
# lifecycle gate (H6, roadmap §16.5). Each mode is an independent run that
# records and restores its own baseline; the state-layout legacy cases stay
# in test_state_layout_upgrade.ps1 and are deliberately not duplicated here.
#
# Modes:
#   FreshMsi                clean MSI install -> payload + managed smoke
#   FreshPortable           portable zip layout via install.ps1 -> service smoke
#   UpgradeLocal            previous MSI with local state -> candidate
#   UpgradeSchema1Empty     previous + schema1 empty state -> candidate
#   UpgradeSchema2          previous + schema2 committed state -> candidate
#   UpgradeSchema1Ambiguous previous + ambiguous schema1 state -> upgrade must
#                           abort and keep the previous install working
#
# Usage (elevated, on win10):
#   powershell -ExecutionPolicy Bypass -File test_managed_package_lifecycle.ps1 `
#       -Mode FreshMsi `
#       -Candidate C:\abs\kikimora-<version>-windows-amd64.msi `
#       -Previous  C:\abs\kikimora-<prev>-windows-amd64.msi `
#       -WorkDir C:\kikimora-acceptance\<sha>\h6
param(
    [Parameter(Mandatory = $true)][ValidateSet("FreshMsi", "FreshPortable", "UpgradeLocal", "UpgradeSchema1Empty", "UpgradeSchema2", "UpgradeSchema1Ambiguous")][string]$Mode,
    [Parameter(Mandatory = $true)][string]$Candidate,
    [string]$Previous = "",
    [Parameter(Mandatory = $true)][string]$WorkDir
)

$ErrorActionPreference = "Continue"
$identity = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $identity.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "test_managed_package_lifecycle.ps1 must run elevated"
}
if (-not (Test-Path $Candidate)) { throw "missing artifact: $Candidate" }
$Candidate = (Resolve-Path $Candidate).Path
$isPortableMode = $Mode -eq "FreshPortable"
if (-not $isPortableMode -and $Mode -like "Upgrade*") {
    if (-not $Previous -or -not (Test-Path $Previous)) { throw "upgrade modes require -Previous pointing at the previous MSI" }
    $Previous = (Resolve-Path $Previous).Path
}
if (-not [System.IO.Path]::IsPathRooted($WorkDir)) { $WorkDir = Join-Path (Get-Location) $WorkDir }
New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
$WorkDir = (Resolve-Path $WorkDir).Path

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$sourceSha = "unknown"
try { $sourceSha = (git -C $RepoRoot rev-parse HEAD).Trim() } catch {}

$evidence = Join-Path $WorkDir $Mode
New-Item -ItemType Directory -Force -Path $evidence | Out-Null
$CoreExe = "C:\Program Files\Kikimora\kikimora-core.exe"
$DataRoot = "C:\ProgramData\Kikimora"
$CoreSocket = "$DataRoot\core.sock"
$CoreStateDir = "$DataRoot\state\core"
$ToadsDir = "$DataRoot\toads"
$Marker = "$ToadsDir\leshy-managed.json"
$CoreService = "KikimoraCore"
$failures = 0

function Note([string]$m) { Write-Host "== $m" }
function Pass([string]$m) { Write-Host "PASS: $m" }
function Fail([string]$m) {
    Write-Host "FAIL: $m"
    $script:failures++
}

function Invoke-Msi([string]$Action, [string]$Path, [string]$Log) {
    $p = Start-Process msiexec -ArgumentList @($Action, "`"$Path`"", "/qn", "/norestart", "/l*v", "`"$Log`"") -Wait -PassThru
    return $p.ExitCode
}

function Get-InstalledKikimoraCode {
    return (Get-ItemProperty "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*" -ErrorAction SilentlyContinue |
        Where-Object { $_.DisplayName -eq "Kikimora" } | Select-Object -First 1).PSChildName
}

function Remove-InstalledKikimora {
    Note "removing any installed Kikimora for a clean baseline"
    $code = Get-InstalledKikimoraCode
    if ($code) {
        $null = Invoke-Msi "/x" $code (Join-Path $evidence "uninstall-prev.log")
    }
    # Portable services from earlier runs must not leak into this one.
    foreach ($svc in @("KikimoraCore", "leshy")) {
        $s = Get-Service -Name $svc -ErrorAction SilentlyContinue
        if ($s) {
            Stop-Service -Name $svc -Force -ErrorAction SilentlyContinue
            $sc = Start-Process sc.exe -ArgumentList @("delete", $svc) -Wait -PassThru -NoNewWindow
        }
    }
}

function Wait-CoreReady([int]$Seconds) {
    for ($i = 0; $i -lt $Seconds; $i++) {
        if (Test-Path $CoreExe) {
            $null = & $CoreExe wait-startup --socket $CoreSocket --timeout 2s 2>$null
            if ($LASTEXITCODE -eq 0) { return $true }
        }
        Start-Sleep -Seconds 1
    }
    return $false
}

function Assert-InstalledPayload {
    if (-not (Test-Path $CoreExe)) { Fail "kikimora-core.exe missing after install"; return }
    Pass "kikimora-core.exe installed"
    if (Test-Path "C:\Program Files\Kikimora\kikimora-toad.exe") { Pass "kikimora-toad.exe installed" } else { Fail "kikimora-toad.exe missing" }
    if (Test-Path "C:\Program Files\Kikimora\leshy\leshy.exe") { Pass "leshy.exe installed" } else { Fail "leshy.exe missing" }
    if (Test-Path "C:\Program Files\Kikimora\kikimora-ui.exe") { Pass "kikimora-ui.exe installed" } else { Fail "kikimora-ui.exe missing (integrated release)" }
    if (Test-Path "C:\Program Files\Kikimora\wintun.dll") { Pass "wintun.dll installed" } else { Fail "wintun.dll missing" }
}

function Managed-Smoke {
    Note "managed smoke on the installed package"
    if (-not (Wait-CoreReady 90)) { Fail "installed core never reached startup readiness"; return }
    $authKey = Join-Path $CoreStateDir "control-route-auth.key"
    $leshyExe = "C:\Program Files\Kikimora\leshy\leshy.exe"
    $enableLog = Join-Path $evidence "managed-enable.log"
    & $CoreExe leshy-mode enable --leshy $leshyExe --socket $CoreSocket `
        --auth-file $authKey --control-socket $CoreSocket 2>&1 | Out-File -Encoding utf8 $enableLog
    if ($LASTEXITCODE -eq 0) { Pass "managed enable on the installed package" } else { Fail "managed enable failed (see $enableLog)" }
    & $CoreExe leshy-mode disable --socket $CoreSocket --auth-file $authKey 2>&1 | Out-File -Encoding utf8 (Join-Path $evidence "managed-disable.log")
    if ($LASTEXITCODE -eq 0) { Pass "managed disable" } else { Fail "managed disable failed" }
}

function Stage-StateShape([string]$Shape) {
    Note "staging previous-install state shape=$Shape"
    New-Item -ItemType Directory -Force -Path $CoreStateDir, $ToadsDir | Out-Null
    switch ($Shape) {
        "local" {
            if (Test-Path $Marker) { Remove-Item $Marker -Force }
        }
        "schema1-empty" {
            [IO.File]::WriteAllText((Join-Path $CoreStateDir "leshy-managed-mode.json"), '{"schema":1,"last_committed_generation":0}')
        }
        "schema2" {
            [IO.File]::WriteAllText((Join-Path $CoreStateDir "leshy-managed-mode.json"), '{"schema":1,"last_committed_generation":5}')
            $markerPayload = @{
                schema           = 2
                mode             = "managed"
                mode_generation  = 5
                activation_id    = "reboot-lifecycle-fixture"
            } | ConvertTo-Json
            [IO.File]::WriteAllText($Marker, $markerPayload)
        }
        "ambiguous" {
            [IO.File]::WriteAllText((Join-Path $CoreStateDir "leshy-managed-mode.json"), '{"schema":1,"last_committed_generation":7}')
            [IO.File]::WriteAllText((Join-Path $CoreStateDir "leshy-managed-mode.conflict.json"), '{"schema":1,"last_committed_generation":7}')
        }
    }
}

switch ($Mode) {
    "FreshMsi" {
        Remove-InstalledKikimora
        $rc = Invoke-Msi "/i" $Candidate (Join-Path $evidence "install.log")
        if ($rc -ne 0) { Fail "fresh MSI install failed: $rc" } else { Pass "fresh MSI install" }
        Assert-InstalledPayload
        Managed-Smoke
    }
    "FreshPortable" {
        Remove-InstalledKikimora
        $zip = $Candidate -replace "\.msi$", ".zip"
        if (-not (Test-Path $zip)) { Fail "portable zip not found next to the MSI: $zip"; break }
        $payload = Join-Path $evidence "payload"
        Expand-Archive -Path $zip -DestinationPath $payload
        $pkgDir = Get-ChildItem $payload -Directory | Select-Object -First 1
        $installLog = Join-Path $evidence "install-portable.log"
        $p = Start-Process powershell -Wait -PassThru -ArgumentList @(
            "-NoProfile", "-ExecutionPolicy", "Bypass",
            "-File", "`"$(Join-Path $pkgDir.FullName 'install.ps1')`""
        )
        if ($p.ExitCode -ne 0) { Fail "portable install.ps1 failed: $($p.ExitCode) (see $installLog)" } else { Pass "portable install" }
        Assert-InstalledPayload
        Managed-Smoke
    }
    { $_ -like "Upgrade*" } {
        $shape = switch ($Mode) {
            "UpgradeLocal" { "local" }
            "UpgradeSchema1Empty" { "schema1-empty" }
            "UpgradeSchema2" { "schema2" }
            "UpgradeSchema1Ambiguous" { "ambiguous" }
        }
        Remove-InstalledKikimora
        $prevRc = Invoke-Msi "/i" $Previous (Join-Path $evidence "install-previous.log")
        if ($prevRc -ne 0) { Fail "previous MSI install failed: $prevRc"; break }
        if (-not (Wait-CoreReady 90)) { Fail "previous core never became ready"; break }
        Stop-Service -Name $CoreService -Force -ErrorAction SilentlyContinue
        Stage-StateShape $shape

        $rc = Invoke-Msi "/i" $Candidate (Join-Path $evidence "install-candidate.log")
        if ($Mode -eq "UpgradeSchema1Ambiguous") {
            if ($rc -eq 0) { Fail "ambiguous upgrade must fail the transaction" }
            else {
                Pass "ambiguous upgrade aborted"
                $code = Get-InstalledKikimoraCode
                if ($code) { Pass "previous install kept installed after abort" } else { Fail "abort removed the previous install" }
                if (Test-Path (Join-Path $CoreStateDir "leshy-managed-mode.conflict.json")) {
                    Pass "conflicting state preserved for inspection"
                } else { Fail "conflicting state was destroyed by the aborted upgrade" }
            }
        } else {
            if ($rc -ne 0) { Fail "candidate install failed: $rc" }
            else {
                Pass "upgrade install ($Mode)"
                Assert-InstalledPayload
                if (-not (Wait-CoreReady 120)) { Fail "upgraded core never became ready" }
                else { Managed-Smoke }
            }
        }
    }
}

if ($failures -gt 0) {
    Write-Host "H6 package lifecycle FAILED ($Mode): $failures assertion(s)" -ForegroundColor Red
    exit 1
}
Write-Host "H6 package lifecycle GREEN ($Mode)" -ForegroundColor Green
exit 0
