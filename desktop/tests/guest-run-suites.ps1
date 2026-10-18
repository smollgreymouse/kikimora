# Run the desktop UI suites against the installed core in the Windows lab guest.
#
# Live suites need a ready composition, and each of them moves roles around, so the
# readiness gate runs before every live suite instead of once at the start.

$ErrorActionPreference = 'Continue'
$coreExe = 'C:\Program Files\Kikimora\kikimora-core.exe'
$root = 'C:\uitests'
$env:KIKIMORA_UI_SOCKET = 'C:\ProgramData\Kikimora\core.sock'
# The test executables need the Qt runtime: without this line they die with
# 0xC0000135 (DLL not found) before a single case runs.
$env:Path = "C:\uitests;C:\Qt\6.10.2\mingw_64\bin;" + $env:Path
$env:QT_QPA_PLATFORM = 'offscreen'
$env:QT_QUICK_BACKEND = 'software'
$env:QTEST_FUNCTION_TIMEOUT = '600000'

function Wait-Ready([int]$timeoutSeconds) {
    for ($waited = 0; $waited -lt $timeoutSeconds; $waited += 5) {
        $json = & $coreExe status --json 2>$null | Out-String
        if ($json -match '"core_state":"Ready"' -and $json -match '"aggregate_state":"Ready"') { return $true }
        Start-Sleep -Seconds 5
    }
    return $false
}

$suites = @(
    @{ name = 'ui-behavior (fake)'; exe = 'ui-behavior-test.exe'; live = $false },
    @{ name = 'tray-menu (fake)'; exe = 'tray-menu-test.exe'; live = $false },
    @{ name = 'real-core-ui (live, observe-ready)'; exe = 'real-core-ui-test.exe'; live = $true; mode = 'observe-ready' },
    @{ name = 'real-core-matrix (live)'; exe = 'real-core-matrix-test.exe'; live = $true },
    @{ name = 'tray-live (live)'; exe = 'tray-live-test.exe'; live = $true },
    @{ name = 'role-list-model'; exe = 'role-list-model-test.exe'; live = $false }
)

foreach ($suite in $suites) {
    ''
    '=== ' + $suite.name + ' ==='
    if ($suite.live) {
        & $coreExe start *> $null
        if (-not (Wait-Ready 240)) { '  composition never became ready'; continue }
    }
    if ($suite.mode) { $env:KIKIMORA_UI_TEST_MODE = $suite.mode }
    $output = & (Join-Path $root $suite.exe) 2>&1 | Out-String
    ($output -split "`r?`n" | Select-String -Pattern '^(Totals|FAIL!|SKIP)' | ForEach-Object { $_.Line }) -join "`n"
    '  exit=' + $LASTEXITCODE
    Remove-Item Env:KIKIMORA_UI_TEST_MODE -ErrorAction SilentlyContinue
}
