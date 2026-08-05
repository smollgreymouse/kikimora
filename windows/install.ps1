[CmdletBinding()]
param(
  [Parameter(Mandatory)][string]$PrimaryInterface,
  [Parameter(Mandatory)][string]$SecondaryInterface,
  [Parameter(Mandatory)][string[]]$DnsInterface,
  [Parameter(Mandatory)][string]$LeshyConfig,
  [string]$LeshyBinary = (Join-Path $env:ProgramFiles 'Kikimora\leshy.exe'),
  [switch]$Start
)

$ErrorActionPreference = 'Stop'
$SourceDir = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $SourceDir 'lib.ps1')

function Test-SafeName {
  param([Parameter(Mandatory)][string]$Name)
  return $Name -match '^[A-Za-z0-9_.:-]+$'
}

function Quote-Psd1String {
  param([Parameter(Mandatory)][string]$Value)
  return "'" + ($Value -replace "'", "''") + "'"
}

function Write-Psd1Array {
  param([string[]]$Values)
  (($Values | ForEach-Object { Quote-Psd1String $_ }) -join ', ')
}

function Register-KikimoraTask {
  param(
    [Parameter(Mandatory)][string]$Name,
    [Parameter(Mandatory)][string]$ScriptPath,
    [Parameter(Mandatory)][string]$Description
  )
  $powershell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
  $argument = "-NoProfile -ExecutionPolicy Bypass -File `"$ScriptPath`""
  $action = New-ScheduledTaskAction -Execute $powershell -Argument $argument
  $trigger = New-ScheduledTaskTrigger -AtStartup
  $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -RunLevel Highest
  $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)

  if (Get-ScheduledTask -TaskPath $Script:TaskPath -TaskName $Name -ErrorAction SilentlyContinue) {
    Unregister-ScheduledTask -TaskPath $Script:TaskPath -TaskName $Name -Confirm:$false
  }
  Register-ScheduledTask -TaskPath $Script:TaskPath -TaskName $Name -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description $Description | Out-Null
  Disable-ScheduledTask -TaskPath $Script:TaskPath -TaskName $Name | Out-Null
}

Assert-Windows
Assert-Admin
if (-not (Test-SafeName $PrimaryInterface)) { throw 'invalid -PrimaryInterface' }
if (-not (Test-SafeName $SecondaryInterface)) { throw 'invalid -SecondaryInterface' }
if ($PrimaryInterface -eq $SecondaryInterface) { throw 'VPN interfaces must differ' }
if ($DnsInterface.Count -eq 0) { throw 'at least one -DnsInterface is required' }
if (-not (Test-Path -LiteralPath $LeshyBinary)) { throw "Leshy binary not found: $LeshyBinary" }
if (-not (Test-Path -LiteralPath $LeshyConfig)) { throw "Leshy config not found: $LeshyConfig" }

foreach ($interface in @($PrimaryInterface, $SecondaryInterface) + $DnsInterface) {
  if (-not (Get-NetAdapter -Name $interface -ErrorAction SilentlyContinue)) {
    throw "unknown network interface: $interface"
  }
}

foreach ($script in @('lib.ps1', 'reconcile.ps1', 'route-watch.ps1', 'health-watch.ps1', 'leshy-dns.ps1', 'start-leshy.ps1', 'kikimora.ps1')) {
  $tokens = $null
  $errors = $null
  [System.Management.Automation.Language.Parser]::ParseFile((Join-Path $SourceDir $script), [ref]$tokens, [ref]$errors) | Out-Null
  if ($errors.Count -gt 0) {
    throw "PowerShell parse failed for ${script}: $($errors[0].Message)"
  }
}

New-Item -ItemType Directory -Force -Path $Script:InstallDir, $Script:ConfigDir, $Script:RuntimeDir, $Script:StateDir, $Script:LogDir | Out-Null
foreach ($script in @('lib.ps1', 'reconcile.ps1', 'route-watch.ps1', 'health-watch.ps1', 'leshy-dns.ps1', 'start-leshy.ps1', 'kikimora.ps1')) {
  Copy-Item -Force -LiteralPath (Join-Path $SourceDir $script) -Destination (Join-Path $Script:InstallDir $script)
}

$installedConfig = Join-Path $Script:ConfigDir 'config.toml'
$sourceConfigPath = (Resolve-Path -LiteralPath $LeshyConfig).Path
$installedConfigPath = [IO.Path]::GetFullPath($installedConfig)
if (-not (Test-Path -LiteralPath $installedConfig) -or $sourceConfigPath -ne $installedConfigPath) {
  Copy-Item -Force -LiteralPath $LeshyConfig -Destination $installedConfig
}

$vpnPsd1 = @"
@{
  PrimaryInterface = $(Quote-Psd1String $PrimaryInterface)
  PrimaryDeviceFile = $(Quote-Psd1String (Join-Path $Script:RuntimeDir 'primary.dev'))
  SecondaryInterface = $(Quote-Psd1String $SecondaryInterface)
  SecondaryDeviceFile = $(Quote-Psd1String (Join-Path $Script:RuntimeDir 'secondary.dev'))
}
"@
Set-Content -Encoding UTF8 -Path $Script:VpnConfig -Value $vpnPsd1

$windowsPsd1 = @"
@{
  DnsInterfaces = @($(Write-Psd1Array $DnsInterface))
  DnsServer = '127.0.0.1'
  LeshyBinary = $(Quote-Psd1String $LeshyBinary)
  LeshyConfig = $(Quote-Psd1String $installedConfig)
}
"@
Set-Content -Encoding UTF8 -Path $Script:WindowsConfig -Value $windowsPsd1

Register-KikimoraTask -Name 'KikimoraLeshy' -ScriptPath (Join-Path $Script:InstallDir 'start-leshy.ps1') -Description 'Kikimora Leshy process runner'
Register-KikimoraTask -Name 'KikimoraRouteWatch' -ScriptPath (Join-Path $Script:InstallDir 'route-watch.ps1') -Description 'Kikimora VPN interface watcher'
Register-KikimoraTask -Name 'KikimoraHealthWatch' -ScriptPath (Join-Path $Script:InstallDir 'health-watch.ps1') -Description 'Kikimora DNS health watcher'

$shimDir = Split-Path -Parent $Script:InstallDir
$shim = @"
param([Parameter(ValueFromRemainingArguments = `$true)][string[]]`$KikimoraArgs)
& $(Quote-Psd1String (Join-Path $Script:InstallDir 'kikimora.ps1')) @KikimoraArgs
exit `$LASTEXITCODE
"@
Set-Content -Encoding UTF8 -Path (Join-Path $shimDir 'kikimora.ps1') -Value $shim
Set-Content -Encoding UTF8 -Path (Join-Path $shimDir 'kk.ps1') -Value $shim

if ($Start) {
  & (Join-Path $Script:InstallDir 'kikimora.ps1') enable --now
}

Write-Output 'Installed Windows Kikimora orchestration.'
Write-Output "CLI: $shimDir\kikimora.ps1 or $shimDir\kk.ps1"
Write-Output 'Tasks are installed disabled by default. Use: kikimora.ps1 enable --now'
