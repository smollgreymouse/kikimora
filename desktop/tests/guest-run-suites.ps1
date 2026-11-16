# SPDX-License-Identifier: MIT
#
# guest-run-suites.ps1 — installed-Windows desktop acceptance runner (U9i/UIR3).
#
# It runs the desktop suites against the installed core and fails closed:
# readiness failure, suite failure, a mandatory QSKIP, missing QtTest totals or
# a nonzero exit all make the runner exit 1. A readiness timeout is a failure,
# never a "continue".
#
# The pure suites (system-theme, role-list-model, real-core-client) run here
# too; the QML/visual lanes (ui-accessibility, ui-visual-state) stay delegated
# to the exact-head CI artifact and are recorded in the summary as delegated.
#
# Usage (elevated, on the disposable win10 guest):
#   powershell -ExecutionPolicy Bypass -File guest-run-suites.ps1 `
#       [-Root C:\uitests] [-TimeoutSeconds 240]

param(
    [string]$Root = 'C:\uitests',
    [int]$TimeoutSeconds = 240
)

$ErrorActionPreference = 'Continue'

$coreExe = 'C:\Program Files\Kikimora\kikimora-core.exe'
$socket = 'C:\ProgramData\Kikimora\core.sock'
$env:KIKIMORA_UI_SOCKET = $socket
$env:QT_QPA_PLATFORM = 'offscreen'
$env:QT_QUICK_BACKEND = 'software'
$env:QTEST_FUNCTION_TIMEOUT = '600000'
# The executables need the Qt runtime next to them; without this they die with
# 0xC0000135 before a single case runs.
$qtBin = Get-ChildItem 'C:\Qt' -Directory -ErrorAction SilentlyContinue |
    Where-Object { Test-Path (Join-Path $_.FullName 'mingw_64\bin') } |
    Sort-Object Name -Descending | Select-Object -First 1
if ($qtBin) {
    $env:Path = "$Root;" + (Join-Path $qtBin.FullName 'mingw_64\bin') + ';' + $env:Path
} else {
    $env:Path = "$Root;" + $env:Path
}

$failures = 0
$skips = 0
$results = @()

function Fail([string]$Message) {
    Write-Host "FAIL: $Message" -ForegroundColor Red
    $script:failures++
}

function Wait-Ready([int]$Seconds) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        $json = & $coreExe status --socket $socket --json 2>$null | Out-String
        if ($json -match '"startup_ready":true' -and $json -match '"core_state":"Ready"') {
            return $true
        }
        Start-Sleep -Seconds 5
    }
    return $false
}

function Run-Suite([string]$Name, [string]$Exe, [bool]$Live) {
    Write-Host ''
    Write-Host "=== $Name ==="
    $path = Join-Path $Root $Exe
    if (-not (Test-Path $path)) {
        Fail "${Name}: binary missing at $path"
        $script:results += [pscustomobject]@{ Suite = $Name; Totals = '<missing>'; Exit = 'n/a' }
        return
    }
    if ($Live) {
        & $coreExe start *> $null
        if (-not (Wait-Ready $TimeoutSeconds)) {
            Fail "${Name}: the installed composition never became ready"
            $script:results += [pscustomobject]@{ Suite = $Name; Totals = '<not ready>'; Exit = 'n/a' }
            return
        }
    }
    $output = & $path 2>&1 | Out-String
    $rc = $LASTEXITCODE
    $lines = $output -split "`r?`n"
    ($lines | Where-Object { $_ -match '^(Totals|FAIL!|SKIP)' }) | ForEach-Object { Write-Host $_ }

    $totals = ($lines | Where-Object { $_ -match '^Totals: \d+ passed, \d+ failed, \d+ skipped' } | Select-Object -Last 1)
    if (-not $totals) {
        Fail "${Name}: no QtTest totals; refusing to count it as evidence"
    } elseif ($totals -match '^Totals: (\d+) passed, (\d+) failed, (\d+) skipped') {
        $executed = [int]$Matches[1] + [int]$Matches[2]
        if ($executed -le 0) { Fail "${Name}: executed no tests" }
        if ([int]$Matches[2] -gt 0) { Fail "${Name}: $($Matches[2]) failed case(s)" }
        if ([int]$Matches[3] -gt 0) {
            $script:skips += [int]$Matches[3]
            Fail "${Name}: $($Matches[3]) mandatory skip(s)"
        }
    }
    if ($rc -ne 0) { Fail "${Name}: exit $rc" }

    $script:results += [pscustomobject]@{ Suite = $Name; Totals = $totals; Exit = $rc }
}

if (-not (Test-Path $coreExe)) { Fail "core executable missing: $coreExe" }
if (-not (Wait-Ready $TimeoutSeconds)) { Fail "the installed core is not ready" }

Run-Suite 'ui-behavior (fake)' 'ui-behavior-test.exe' $false
Run-Suite 'system-theme' 'system-theme-test.exe' $false
Run-Suite 'role-list-model' 'role-list-model-test.exe' $false
Run-Suite 'real-core-client' 'real-core-client-test.exe' $false
Run-Suite 'tray-menu (fake)' 'tray-menu-test.exe' $false
Run-Suite 'real-core-ui (live)' 'real-core-ui-test.exe' $true
Run-Suite 'real-core-matrix (live)' 'real-core-matrix-test.exe' $true
Run-Suite 'tray-live (live)' 'tray-live-test.exe' $true

Write-Host ''
Write-Host '== installed-suite summary'
$results | ForEach-Object {
    '{0,-32} {1} (exit {2})' -f $_.Suite, $_.Totals, $_.Exit
}
Write-Host 'delegated to the exact-head CI artifact: ui-accessibility, ui-visual-state'
Write-Host "failures=$failures skips=$skips"
if ($failures -gt 0 -or $skips -gt 0) {
    Write-Host 'INSTALLED WINDOWS SUITES FAILED' -ForegroundColor Red
    exit 1
}
Write-Host 'installed desktop suites: all green, zero mandatory skips'
exit 0
