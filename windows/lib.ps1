$ErrorActionPreference = 'Stop'

$Script:InstallDir = if ($env:KIKIMORA_INSTALL_DIR) { $env:KIKIMORA_INSTALL_DIR } else { Join-Path $env:ProgramFiles 'Kikimora\windows' }
$Script:ConfigDir = if ($env:KIKIMORA_CONFIG_DIR) { $env:KIKIMORA_CONFIG_DIR } else { Join-Path $env:ProgramData 'Kikimora\Leshy' }
$Script:RuntimeDir = if ($env:KIKIMORA_RUNTIME_DIR) { $env:KIKIMORA_RUNTIME_DIR } else { Join-Path $env:ProgramData 'Kikimora\Leshy\run\vpn' }
$Script:StateDir = if ($env:KIKIMORA_STATE_DIR) { $env:KIKIMORA_STATE_DIR } else { Join-Path $env:ProgramData 'Kikimora\Leshy\state' }
$Script:LogDir = if ($env:KIKIMORA_LOG_DIR) { $env:KIKIMORA_LOG_DIR } else { Join-Path $env:ProgramData 'Kikimora\logs' }
$Script:VpnConfig = Join-Path $Script:ConfigDir 'vpn.psd1'
$Script:WindowsConfig = Join-Path $Script:ConfigDir 'windows.psd1'
$Script:TaskPath = '\Kikimora\'
$Script:TaskNames = @('KikimoraLeshy', 'KikimoraRouteWatch', 'KikimoraHealthWatch')

function Assert-Windows {
  if ($env:OS -ne 'Windows_NT') {
    throw 'this backend requires Windows'
  }
}

function Assert-Admin {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = [Security.Principal.WindowsPrincipal]::new($identity)
  if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'run from an elevated PowerShell session'
  }
}

function Write-KikimoraLog {
  param([Parameter(Mandatory)][string]$Message)
  New-Item -ItemType Directory -Force -Path $Script:LogDir | Out-Null
  $stamp = Get-Date -Format o
  Add-Content -Encoding UTF8 -Path (Join-Path $Script:LogDir 'kikimora.log') -Value "$stamp $Message"
}

function Import-KikimoraConfig {
  if (-not (Test-Path -LiteralPath $Script:VpnConfig)) { throw "missing $Script:VpnConfig" }
  if (-not (Test-Path -LiteralPath $Script:WindowsConfig)) { throw "missing $Script:WindowsConfig" }

  $vpn = Import-PowerShellDataFile -LiteralPath $Script:VpnConfig
  $windows = Import-PowerShellDataFile -LiteralPath $Script:WindowsConfig

  $Script:PrimaryInterface = [string]$vpn.PrimaryInterface
  $Script:PrimaryDeviceFile = [string]$vpn.PrimaryDeviceFile
  $Script:SecondaryInterface = [string]$vpn.SecondaryInterface
  $Script:SecondaryDeviceFile = [string]$vpn.SecondaryDeviceFile
  $Script:DnsInterfaces = @($windows.DnsInterfaces)
  $Script:DnsServer = if ($windows.DnsServer) { [string]$windows.DnsServer } else { '127.0.0.1' }
  $Script:LeshyBin = if ($windows.LeshyBinary) { [string]$windows.LeshyBinary } else { Join-Path $env:ProgramFiles 'Kikimora\leshy.exe' }
  $Script:LeshyConfig = if ($windows.LeshyConfig) { [string]$windows.LeshyConfig } else { Join-Path $Script:ConfigDir 'config.toml' }

  if (-not $Script:PrimaryInterface) { throw 'PrimaryInterface is required' }
  if (-not $Script:SecondaryInterface) { throw 'SecondaryInterface is required' }
  if ($Script:DnsInterfaces.Count -eq 0) { throw 'at least one DNS interface is required' }
}

function Test-KikimoraInterfaceReady {
  param([Parameter(Mandatory)][string]$InterfaceAlias)
  $adapter = Get-NetAdapter -Name $InterfaceAlias -ErrorAction SilentlyContinue
  if (-not $adapter -or $adapter.Status -ne 'Up') { return $false }

  $address = Get-NetIPAddress -InterfaceAlias $InterfaceAlias -AddressFamily IPv4 -ErrorAction SilentlyContinue |
    Where-Object { $_.IPAddress -and $_.IPAddress -notlike '169.254.*' } |
    Select-Object -First 1
  return [bool]$address
}

function Set-KikimoraFileAtomically {
  param(
    [Parameter(Mandatory)][string]$Path,
    [Parameter(Mandatory)][string]$Value
  )
  New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Path) | Out-Null
  $tmp = "$Path.tmp.$PID"
  Set-Content -Encoding ASCII -Path $tmp -Value $Value
  Move-Item -Force -LiteralPath $tmp -Destination $Path
}

function Get-KikimoraTaskState {
  param([Parameter(Mandatory)][string]$Name)
  $task = Get-ScheduledTask -TaskPath $Script:TaskPath -TaskName $Name -ErrorAction SilentlyContinue
  if (-not $task) { return 'missing' }
  return [string]$task.State
}
