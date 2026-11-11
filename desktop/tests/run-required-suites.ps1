# Required desktop UI suites (U6, docs/roadmap.md §13) - Windows runner.
#
# Same contract as run-required-suites.sh: run every required suite directly,
# fail on nonzero exit, mandatory SKIP, or unexpected QML warnings; missing
# Layer-C binaries are a harness failure, never a skip.
#
# Usage:
#   powershell -File desktop\tests\run-required-suites.ps1 -BuildDir <dir> [-Suites a,b,c] [-SkipGoFixtures]

param(
    [Parameter(Mandatory = $true)][string]$BuildDir,
    [string[]]$Suites = @(),
    [switch]$SkipGoFixtures
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path $BuildDir)) {
    Write-Error "usage: run-required-suites.ps1 -BuildDir <build-dir>"
    exit 2
}
$BuildDir = (Resolve-Path $BuildDir).Path
# Multi-config generators (the default Visual Studio generator on Windows)
# place the suite binaries under a per-config subdirectory; single-config
# builds keep them at the BuildDir root. Logs and Go fixtures always live at
# the BuildDir root.
$BinDir = $BuildDir
if (-not (Test-Path (Join-Path $BuildDir 'real-core-client-test.exe')) -and
    (Test-Path (Join-Path $BuildDir 'Release\real-core-client-test.exe'))) {
    $BinDir = Join-Path $BuildDir 'Release'
}
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$ArtifactDir = Join-Path $BuildDir 'required-suite-logs'
New-Item -ItemType Directory -Force -Path $ArtifactDir | Out-Null

$env:QT_QPA_PLATFORM = 'offscreen'
$env:QT_QUICK_BACKEND = 'software'

if (-not $SkipGoFixtures) {
    Write-Host '== building Go fixtures (core + fake toad)'
    $fixtures = Join-Path $BuildDir 'fixtures'
    New-Item -ItemType Directory -Force -Path $fixtures | Out-Null
    $coreBin = Join-Path $fixtures 'kikimora-core.exe'
    $toadBin = Join-Path $fixtures 'kikimora-fake-toad.exe'
    Push-Location (Join-Path $RepoRoot 'toad')
    try {
        go build -o $coreBin ./cmd/kikimora-core
        if ($LASTEXITCODE -ne 0) { throw "go build kikimora-core failed" }
        go build -o $toadBin ./internal/control/testhelper
        if ($LASTEXITCODE -ne 0) { throw "go build fake-toad failed" }
    }
    finally {
        Pop-Location
    }
    $env:KIKIMORA_CORE_BINARY = $coreBin
    $env:KIKIMORA_FAKE_TOAD_BINARY = $toadBin
}

$WarningPatterns = @(
    'ReferenceError', 'TypeError', 'Binding loop',
    'QQmlApplicationEngine failed',
    'QQuickStyle::setStyle\(\) must be called',
    'Unable to assign', 'Cannot assign', 'required property'
)

$failures = 0
$mandatorySkips = 0
$summary = @()

function Run-Required([string]$name) {
    $binary = Join-Path $BinDir "$name.exe"
    $log = Join-Path $ArtifactDir "$name.log"
    if (-not (Test-Path $binary)) {
        Write-Host "HARNESS FAILURE: required suite binary is missing: $binary" -ForegroundColor Red
        return 2
    }
    Write-Host "== $name"
    & $binary 2>&1 | Tee-Object -FilePath $log
    $rc = $LASTEXITCODE
    if ($rc -ne 0) {
        Write-Host "$name: exit $rc" -ForegroundColor Red
        return $rc
    }
    $skipHit = Select-String -Path $log -Pattern '^\s*SKIP\s*:' -ErrorAction SilentlyContinue
    if ($skipHit) {
        Write-Host "$name: mandatory QtTest skip" -ForegroundColor Red
        $script:mandatorySkips++
        return 1
    }
    foreach ($pattern in $WarningPatterns) {
        $hit = Select-String -Path $log -Pattern $pattern -ErrorAction SilentlyContinue
        if ($hit) {
            Write-Host "$name: unexpected QML warning ($pattern)" -ForegroundColor Red
            return 1
        }
    }
    return 0
}

$layerA = @('real-core-client-test', 'role-list-model-test', 'system-theme-test')
$layerB = @('ui-behavior-test', 'tray-menu-test')
$layerC = @('real-core-e2e-test', 'real-core-ui-test', 'real-core-matrix-test', 'tray-live-test')

$all = $layerA + $layerB + $layerC
if ($Suites.Count -gt 0) { $all = $Suites }

foreach ($suite in $all) {
    $rc = Run-Required $suite
    if ($rc -ne 0) { $failures++ }
    $totals = '<no log>'
    $log = Join-Path $ArtifactDir "$suite.log"
    if (Test-Path $log) {
        $line = (Select-String -Path $log -Pattern '^Totals:' | Select-Object -Last 1)
        if ($line) { $totals = $line.Line }
    }
    $summary += ('{0,-26} {1}' -f $suite, $totals)
}

Write-Host ''
Write-Host '== required-suite summary'
$summary | ForEach-Object { Write-Host $_ }

if ($failures -gt 0 -or $mandatorySkips -gt 0) {
    Write-Host "REQUIRED SUITES FAILED: failures=$failures mandatorySkips=$mandatorySkips" -ForegroundColor Red
    exit 1
}
Write-Host 'required suites: all green, zero mandatory skips' -ForegroundColor Green
exit 0
