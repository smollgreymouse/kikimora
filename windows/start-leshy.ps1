$ErrorActionPreference = 'Stop'
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $ScriptDir 'lib.ps1')

Assert-Windows
Import-KikimoraConfig
New-Item -ItemType Directory -Force -Path $Script:LogDir | Out-Null
$logPath = Join-Path $Script:LogDir 'leshy.log'

while ($true) {
  try {
    if (-not (Test-Path -LiteralPath $Script:LeshyBin)) { throw "Leshy binary not found: $Script:LeshyBin" }
    if (-not (Test-Path -LiteralPath $Script:LeshyConfig)) { throw "Leshy config not found: $Script:LeshyConfig" }
    Write-KikimoraLog "starting Leshy: $Script:LeshyBin $Script:LeshyConfig"
    & $Script:LeshyBin $Script:LeshyConfig *>> $logPath
    Write-KikimoraLog "Leshy exited with code $LASTEXITCODE; restarting in 3 seconds"
  } catch {
    Write-KikimoraLog "Leshy runner failed: $($_.Exception.Message)"
  }
  Start-Sleep -Seconds 3
}
