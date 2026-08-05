param(
  [Parameter(Position = 0)]
  [string]$Command = 'help',

  [Parameter(ValueFromRemainingArguments = $true)]
  [string[]]$RemainingArgs
)

$ErrorActionPreference = 'Stop'
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $ScriptDir 'lib.ps1')

function Show-Usage {
  @'
usage: kikimora.ps1 COMMAND [OPTIONS]

Commands:
  start                  Start scheduled tasks and ensure DNS integration
  stop                   Suspend DNS integration and stop scheduled tasks
  restart                Stop, start and ensure DNS integration
  enable [--now]         Enable startup tasks, optionally start now
  disable [--now]        Disable startup tasks, optionally stop now
  status                 Show task and DNS status
  dns COMMAND            Run DNS command: status, check, enable, disable, suspend, resume
'@
}

function Start-KikimoraTasks {
  Assert-Admin
  foreach ($name in $Script:TaskNames) {
    Enable-ScheduledTask -TaskPath $Script:TaskPath -TaskName $name | Out-Null
  }
  Start-ScheduledTask -TaskPath $Script:TaskPath -TaskName 'KikimoraLeshy'
  Start-Sleep -Seconds 1
  Start-ScheduledTask -TaskPath $Script:TaskPath -TaskName 'KikimoraRouteWatch'
  Start-ScheduledTask -TaskPath $Script:TaskPath -TaskName 'KikimoraHealthWatch'
  & (Join-Path $ScriptDir 'leshy-dns.ps1') resume
  if ($LASTEXITCODE -ne 0) { & (Join-Path $ScriptDir 'leshy-dns.ps1') enable }
}

function Stop-KikimoraTasks {
  Assert-Admin
  & (Join-Path $ScriptDir 'leshy-dns.ps1') suspend
  foreach ($name in @('KikimoraHealthWatch', 'KikimoraRouteWatch', 'KikimoraLeshy')) {
    Stop-ScheduledTask -TaskPath $Script:TaskPath -TaskName $name -ErrorAction SilentlyContinue
  }
}

function Enable-KikimoraTasks {
  Assert-Admin
  foreach ($name in $Script:TaskNames) {
    Enable-ScheduledTask -TaskPath $Script:TaskPath -TaskName $name | Out-Null
  }
}

function Disable-KikimoraTasks {
  Assert-Admin
  foreach ($name in $Script:TaskNames) {
    Disable-ScheduledTask -TaskPath $Script:TaskPath -TaskName $name | Out-Null
  }
}

function Show-KikimoraStatus {
  foreach ($name in $Script:TaskNames) {
    Write-Output "${name}: $(Get-KikimoraTaskState -Name $name)"
  }
  & (Join-Path $ScriptDir 'leshy-dns.ps1') status
}

Assert-Windows
switch ($Command) {
  'start' { Start-KikimoraTasks }
  'stop' { Stop-KikimoraTasks }
  'restart' { Stop-KikimoraTasks; Start-KikimoraTasks }
  'enable' { Enable-KikimoraTasks; if ($RemainingArgs -contains '--now') { Start-KikimoraTasks } }
  'disable' { if ($RemainingArgs -contains '--now') { Stop-KikimoraTasks }; Disable-KikimoraTasks; & (Join-Path $ScriptDir 'leshy-dns.ps1') disable }
  'status' { Show-KikimoraStatus }
  'dns' { & (Join-Path $ScriptDir 'leshy-dns.ps1') @RemainingArgs; exit $LASTEXITCODE }
  'help' { Show-Usage }
  default { Show-Usage; exit 64 }
}
