$ErrorActionPreference = 'Stop'
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $ScriptDir 'lib.ps1')

Assert-Windows
Import-KikimoraConfig
$intervalText = if ($env:KIKIMORA_HEALTH_WATCH_INTERVAL) { $env:KIKIMORA_HEALTH_WATCH_INTERVAL } else { '5' }
[int]$interval = 0
if (-not [int]::TryParse($intervalText, [ref]$interval) -or $interval -lt 1) {
  throw 'invalid KIKIMORA_HEALTH_WATCH_INTERVAL'
}

while ($true) {
  try {
    Resolve-DnsName -Name '.' -Type NS -Server $Script:DnsServer -DnsOnly -ErrorAction Stop | Out-Null
    & (Join-Path $ScriptDir 'leshy-dns.ps1') check
    if ($LASTEXITCODE -ne 0) {
      & (Join-Path $ScriptDir 'leshy-dns.ps1') resume
      if ($LASTEXITCODE -ne 0) { Write-KikimoraLog 'DNS integration repair failed' }
    }
  } catch {
    try {
      & (Join-Path $ScriptDir 'leshy-dns.ps1') suspend
    } catch {
      Write-KikimoraLog "DNS integration suspend failed: $($_.Exception.Message)"
    }
  }
  Start-Sleep -Seconds $interval
}
