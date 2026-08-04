param(
  [ValidateSet('enable', 'disable', 'suspend', 'resume', 'check', 'status')]
  [string]$Command = 'status'
)

$ErrorActionPreference = 'Stop'
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $ScriptDir 'lib.ps1')

Assert-Windows
Assert-Admin
Import-KikimoraConfig
New-Item -ItemType Directory -Force -Path $Script:StateDir | Out-Null

$Manifest = Join-Path $Script:StateDir 'windows-dns-interfaces.json'

function Get-DnsSnapshotPath {
  param([Parameter(Mandatory)][string]$InterfaceAlias)
  $bytes = [Text.Encoding]::UTF8.GetBytes($InterfaceAlias)
  $key = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
  Join-Path $Script:StateDir "windows-dns-$key.json"
}

function Get-CurrentDnsServers {
  param([Parameter(Mandatory)][string]$InterfaceAlias)
  $entry = Get-DnsClientServerAddress -InterfaceAlias $InterfaceAlias -AddressFamily IPv4 -ErrorAction Stop
  return @($entry.ServerAddresses)
}

function Save-DnsSnapshot {
  param([Parameter(Mandatory)][string]$InterfaceAlias)
  $path = Get-DnsSnapshotPath -InterfaceAlias $InterfaceAlias
  if (Test-Path -LiteralPath $path) { return }
  $snapshot = [ordered]@{
    InterfaceAlias = $InterfaceAlias
    ServerAddresses = @(Get-CurrentDnsServers -InterfaceAlias $InterfaceAlias)
  }
  $snapshot | ConvertTo-Json -Depth 4 | Set-Content -Encoding UTF8 -Path $path
}

function Restore-DnsSnapshot {
  param([Parameter(Mandatory)][string]$InterfaceAlias)
  $path = Get-DnsSnapshotPath -InterfaceAlias $InterfaceAlias
  if (-not (Test-Path -LiteralPath $path)) { return }
  $snapshot = Get-Content -Raw -Path $path | ConvertFrom-Json
  $servers = @($snapshot.ServerAddresses)
  if ($servers.Count -eq 0) {
    Set-DnsClientServerAddress -InterfaceAlias $InterfaceAlias -ResetServerAddresses
  } else {
    Set-DnsClientServerAddress -InterfaceAlias $InterfaceAlias -ServerAddresses $servers
  }
}

function Write-DnsManifest {
  $Script:DnsInterfaces | ConvertTo-Json -Depth 3 | Set-Content -Encoding UTF8 -Path $Manifest
}

function Read-DnsManifest {
  if (-not (Test-Path -LiteralPath $Manifest)) { return @() }
  return @(Get-Content -Raw -Path $Manifest | ConvertFrom-Json)
}

function Enable-KikimoraDns {
  foreach ($interface in $Script:DnsInterfaces) { Save-DnsSnapshot -InterfaceAlias $interface }
  Write-DnsManifest
  Resume-KikimoraDns
}

function Resume-KikimoraDns {
  if (-not (Test-Path -LiteralPath $Manifest)) { return $false }
  foreach ($interface in $Script:DnsInterfaces) {
    Set-DnsClientServerAddress -InterfaceAlias $interface -ServerAddresses @($Script:DnsServer)
  }
  Test-KikimoraDns
}

function Suspend-KikimoraDns {
  $interfaces = Read-DnsManifest
  foreach ($interface in $interfaces) {
    if ($interface) { Restore-DnsSnapshot -InterfaceAlias ([string]$interface) }
  }
}

function Disable-KikimoraDns {
  Suspend-KikimoraDns
  Remove-Item -LiteralPath $Manifest -Force -ErrorAction SilentlyContinue
  Remove-Item -Path (Join-Path $Script:StateDir 'windows-dns-*.json') -Force -ErrorAction SilentlyContinue
}

function Test-KikimoraDns {
  foreach ($interface in $Script:DnsInterfaces) {
    $servers = @(Get-CurrentDnsServers -InterfaceAlias $interface)
    if ($servers.Count -ne 1 -or $servers[0] -ne $Script:DnsServer) { return $false }
  }
  return $true
}

function Show-KikimoraDns {
  foreach ($interface in $Script:DnsInterfaces) {
    $servers = @(Get-CurrentDnsServers -InterfaceAlias $interface)
    $display = if ($servers.Count) { $servers -join ', ' } else { '<dhcp/default>' }
    Write-Output "$interface: $display"
  }
}

switch ($Command) {
  'enable' { Enable-KikimoraDns; if (-not (Test-KikimoraDns)) { exit 1 } }
  'disable' { Disable-KikimoraDns }
  'suspend' { Suspend-KikimoraDns }
  'resume' { if (-not (Resume-KikimoraDns)) { exit 1 } }
  'check' { if (Test-KikimoraDns) { exit 0 } else { exit 1 } }
  'status' { Show-KikimoraDns }
}
