$ErrorActionPreference = 'Stop'
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $ScriptDir 'lib.ps1')

Assert-Windows
$intervalText = if ($env:KIKIMORA_ROUTE_WATCH_INTERVAL) { $env:KIKIMORA_ROUTE_WATCH_INTERVAL } else { '2' }
[int]$interval = 0
if (-not [int]::TryParse($intervalText, [ref]$interval) -or $interval -lt 1) {
  throw 'invalid KIKIMORA_ROUTE_WATCH_INTERVAL'
}

while ($true) {
  try {
    & (Join-Path $ScriptDir 'reconcile.ps1') | ForEach-Object { Write-KikimoraLog $_ }
  } catch {
    Write-KikimoraLog "route reconciliation failed: $($_.Exception.Message)"
  }
  Start-Sleep -Seconds $interval
}
