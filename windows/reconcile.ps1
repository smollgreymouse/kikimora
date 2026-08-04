$ErrorActionPreference = 'Stop'
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $ScriptDir 'lib.ps1')

Assert-Windows
Import-KikimoraConfig
New-Item -ItemType Directory -Force -Path $Script:RuntimeDir | Out-Null

function Sync-KikimoraInterface {
  param(
    [Parameter(Mandatory)][string]$Role,
    [Parameter(Mandatory)][string]$InterfaceAlias,
    [Parameter(Mandatory)][string]$DeviceFile
  )

  if (Test-KikimoraInterfaceReady -InterfaceAlias $InterfaceAlias) {
    Set-KikimoraFileAtomically -Path $DeviceFile -Value $InterfaceAlias
    Write-Output "$Role $InterfaceAlias ready"
  } else {
    Remove-Item -LiteralPath $DeviceFile -Force -ErrorAction SilentlyContinue
    Write-Output "$Role $InterfaceAlias unavailable"
  }
}

Sync-KikimoraInterface -Role 'primary' -InterfaceAlias $Script:PrimaryInterface -DeviceFile $Script:PrimaryDeviceFile
Sync-KikimoraInterface -Role 'secondary' -InterfaceAlias $Script:SecondaryInterface -DeviceFile $Script:SecondaryDeviceFile
