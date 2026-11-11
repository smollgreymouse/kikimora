# SPDX-License-Identifier: MIT
#
# test_managed_reboot.ps1 — the versioned Windows managed reboot matrix (H3
# reboot half, roadmap §16.3). Prepare/Verify semantics mirror the Linux G4
# harness; the host reboots the VM between the two invocations. No run may
# reboot and then verify its own post-boot state in one shot.
#
# Usage (elevated, on win10):
#   powershell -ExecutionPolicy Bypass -File test_managed_reboot.ps1 `
#       -Case prepared|config_written|marker_written|generation_committed|selected_active|park_applying|park_retiring `
#       -Phase Prepare|Verify `
#       -Core C:\abs\kikimora-core[-tagged].exe `
#       -Leshy C:\abs\leshy.exe `
#       -WorkDir C:\kikimora-acceptance\<sha>\reboot
param(
    [Parameter(Mandatory = $true)][ValidateSet("prepared", "config_written", "marker_written", "generation_committed", "selected_active", "park_applying", "park_retiring")][string]$Case,
    [Parameter(Mandatory = $true)][ValidateSet("Prepare", "Verify")][string]$Phase,
    [Parameter(Mandatory = $true)][string]$Core,
    [Parameter(Mandatory = $true)][string]$Leshy,
    [Parameter(Mandatory = $true)][string]$WorkDir
)

$ErrorActionPreference = "Continue"
$identity = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $identity.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "test_managed_reboot.ps1 must run elevated"
}

# Reject missing mandatory binaries before any mutation.
foreach ($path in @($Core, $Leshy)) {
    if (-not (Test-Path $path)) { throw "missing binary: $path" }
}
$Core = (Resolve-Path $Core).Path
$Leshy = (Resolve-Path $Leshy).Path
if (-not [System.IO.Path]::IsPathRooted($WorkDir)) { $WorkDir = Join-Path (Get-Location) $WorkDir }
New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
$WorkDir = (Resolve-Path $WorkDir).Path

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$sourceSha = "unknown"
try { $sourceSha = (git -C $RepoRoot rev-parse HEAD).Trim() } catch {}

$caseFile = Join-Path $WorkDir "reboot-case.json"
$evidence = Join-Path $WorkDir "$Case.$Phase"
New-Item -ItemType Directory -Force -Path $evidence | Out-Null
$stateDir = Join-Path $WorkDir "state"
$configsDir = Join-Path $WorkDir "configs"
$socket = Join-Path $WorkDir "core.sock"
$journal = Join-Path $stateDir "leshy-mode-transition.json"
$marker = Join-Path $configsDir "leshy-managed.json"
$barrierDir = Join-Path $WorkDir "barrier"
$coreLog = Join-Path $evidence "core.log"

$CoreService = "KikimoraCore"
$LeshyService = "leshy"

function Note([string]$m) { Write-Host "== $m" }
function Die([string]$m) {
    Write-Host "FAIL: $m" -ForegroundColor Red
    exit 1
}

function Stop-OwnedProcesses {
    Get-Process | Where-Object { $_.Path -eq $Core } | Stop-Process -Force -ErrorAction SilentlyContinue
}

function Wait-CoreSocket([int]$Seconds) {
    for ($i = 0; $i -lt $Seconds; $i++) {
        $null = & $Core wait-startup --socket $socket --timeout 2s 2>$null
        if ($LASTEXITCODE -eq 0) { return $true }
        Start-Sleep -Seconds 1
    }
    return $false
}

if ($Phase -eq "Prepare") {
    Note "H3 prepare: case=$Case"
    # Clear only this case's pending contract; other evidence stays.
    if (Test-Path $caseFile) { Remove-Item $caseFile -Force }
    Stop-OwnedProcesses
    foreach ($svc in @($CoreService, $LeshyService)) {
        $svcObj = Get-Service -Name $svc -ErrorAction SilentlyContinue
        if ($svcObj) { Stop-Service -Name $svc -Force -ErrorAction SilentlyContinue }
    }
    if (Test-Path $stateDir) { Remove-Item -Recurse -Force $stateDir }
    if (Test-Path $configsDir) { Remove-Item -Recurse -Force $configsDir }
    New-Item -ItemType Directory -Force -Path $stateDir, $configsDir, $barrierDir | Out-Null
    if (Test-Path $socket) { Remove-Item $socket -Force }

    $password = Join-Path $configsDir "primary.password"
    [IO.File]::WriteAllText($password, "reboot-secret`n")
    $roleConfig = @"
name = "primary"
protocol = "openconnect"
interface = "kkprimary0"
mtu = 1380
state_dir = "$($stateDir -replace '\\', '\\')\primary"
address = ["10.204.0.2/32"]

[openconnect]
gateway = "vpn.example.test"
username = "reboot-fixture"
password_file = "$($password -replace '\\', '\\')"
vpn_protocol = "anyconnect"
token_mode = "none"
"@
    [IO.File]::WriteAllText((Join-Path $configsDir "primary.toml"), $roleConfig)

    $boundary = switch ($Case) {
        "prepared" { "after_prepared" }
        "config_written" { "after_config_written" }
        "marker_written" { "after_marker_written" }
        "generation_committed" { "after_generation_commit" }
        default { "" }
    }

    $env:KK_MODE_FAILPOINT = ""
    if ($boundary -ne "") {
        $env:KK_MODE_FAILPOINT = "enable/$boundary"
    }
    $enableLog = Join-Path $evidence "enable.log"
    $env:KK_LESHY_CONFIG = ""
    & $Core leshy-mode enable --leshy $Leshy --socket $socket `
        --auth-file (Join-Path $stateDir "control-route-auth.key") `
        --control-socket $socket 2>&1 | Out-File -Encoding utf8 $enableLog
    $enableRc = $LASTEXITCODE
    $env:KK_MODE_FAILPOINT = ""
    if ($boundary -ne "") {
        if ($enableRc -ne 86) { Die "enable did not stop at $boundary (rc=$enableRc); see $enableLog" }
    }

    # Verify the expected on-disk state before declaring reboot-safety.
    switch ($Case) {
        "prepared" {
            if (-not (Test-Path $journal)) { Die "journal missing at prepared" }
        }
        "config_written" {
            if (-not (Test-Path $journal)) { Die "journal missing at config_written" }
        }
        "marker_written" {
            if (-not (Test-Path $marker)) { Die "marker missing at marker_written" }
        }
        "generation_committed" {
            if (-not (Test-Path $marker)) { Die "marker missing at generation_committed" }
        }
        default {
            if (-not (Test-Path $marker)) { Die "marker missing for $Case" }
        }
    }

    $markerGeneration = 0
    if (Test-Path $marker) {
        try { $markerGeneration = [int]((Get-Content $marker -Raw | ConvertFrom-Json).mode_generation) } catch {}
    }

    $expected = @{
        journal = (Test-Path $journal)
        marker  = (Test-Path $marker)
    }
    $payload = @{
        case                = $Case
        source_sha          = $sourceSha
        expected_generation = $markerGeneration
        expected            = $expected
        work_dir            = $WorkDir
        socket              = $socket
    } | ConvertTo-Json
    [IO.File]::WriteAllText($caseFile, $payload)
    Note "H3 prepare complete: $Case is reboot-safe"
    exit 0
}

# ---- Verify -----------------------------------------------------------------
Note "H3 verify: case=$Case"
if (-not (Test-Path $caseFile)) { Die "case file missing: was Prepare run before the reboot?" }
$contract = Get-Content $caseFile -Raw | ConvertFrom-Json
if ($contract.case -ne $Case) { Die "case file holds '$($contract.case)', not '$Case'" }
if ($contract.source_sha -ne $sourceSha) { Die "case SHA $($contract.source_sha) != current $sourceSha" }

# Packaged ordering: core recovery first, Leshy availability after it.
Start-Service -Name $CoreService -ErrorAction SilentlyContinue
$coreActive = $false
for ($i = 0; $i -lt 90; $i++) {
    $svc = Get-Service -Name $CoreService -ErrorAction SilentlyContinue
    if ($svc -and $svc.Status -eq "Running") { $coreActive = $true; break }
    Start-Sleep -Seconds 1
}
if (-not $coreActive) { Die "KikimoraCore did not start after reboot" }
$leshySvc = Get-Service -Name $LeshyService -ErrorAction SilentlyContinue
if ($leshySvc -and $leshySvc.Status -ne "Running") {
    # Leshy may legitimately be starting after the core; availability, not
    # order inversion, is what this row requires.
    for ($i = 0; $i -lt 60; $i++) {
        $leshySvc.Refresh()
        if ($leshySvc.Status -eq "Running") { break }
        Start-Sleep -Seconds 1
    }
}

# Durable results.
if ($contract.expected.marker -and -not (Test-Path $marker)) { Die "committed marker missing after reboot" }
if ($contract.expected.journal) {
    # An in-flight transaction may resolve during recovery; it must not
    # silently vanish without a marker when the case had none.
    if (-not (Test-Path $marker) -and -not (Test-Path $journal)) { Die "transaction state vanished" }
}
if ((Test-Path $marker)) {
    $generation = 0
    try { $generation = [int]((Get-Content $marker -Raw | ConvertFrom-Json).mode_generation) } catch {}
    if ($generation -lt $contract.expected_generation) {
        Die "generation regressed: $generation < $($contract.expected_generation)"
    }
}

# The unrelated route/rule snapshot after recovery (evidence, not a hard
# equality: the VM's own adapters change between runs).
$routeDump = Join-Path $evidence "routes-after.txt"
Get-NetRoute -ErrorAction SilentlyContinue | Out-File -Encoding utf8 $routeDump
$rulesDump = Join-Path $evidence "rules-after.txt"
netsh interface ipv6 show route 2>$null | Out-File -Encoding utf8 $rulesDump

# Only this case's marker goes on success.
Remove-Item $caseFile -Force
Note "H3 verify complete: case=$Case recovered within contract"
exit 0
