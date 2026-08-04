[CmdletBinding()]
param(
  [Parameter(ValueFromRemainingArguments = $true)]
  [string[]]$InstallerArgs
)

$ErrorActionPreference = 'Stop'
$RootDir = Split-Path -Parent $MyInvocation.MyCommand.Path

if ($env:OS -ne 'Windows_NT') {
  throw 'install.ps1 is the Windows installer entrypoint. Use ./install.sh on Linux or macOS.'
}

& (Join-Path $RootDir 'windows\install.ps1') @InstallerArgs
exit $LASTEXITCODE
