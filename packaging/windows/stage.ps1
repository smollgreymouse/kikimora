# SPDX-License-Identifier: MIT
#
# stage.ps1 — stage the Kikimora Windows portable package.
#
# Usage (from a shell with the binaries already built, or let the script build them):
#   powershell -File stage.ps1 -OutDir .\dist [-UiExe .\kikimora-ui.exe]
#       [-CoreExe .\kikimora-core.exe] [-ToadExe .\kikimora-toad.exe] [-SkipBuild]
#       [-WintunDll .\wintun.dll] [-AllowMissingWintun]
#
# The package layout is the VM acceptance contract:
#   kikimora-<version>-windows-amd64/
#     kikimora-core.exe      service + control plane (owns Wintun adapters)
#     kikimora-toad.exe      per-role transport supervisor
#     wintun.dll             required driver redistributable (see below)
#     kikimora-ui.exe        optional desktop UI (RealCoreClient by default)
#     install.ps1            elevated installer: ProgramData layout + service
#     uninstall.ps1          elevated removal with explicit purge semantics
#     manifest.json          product/version/components
#     checksums.txt          SHA256 of every staged file

param (
    [string]$OutDir = ".\dist",
    [string]$UiExe = "",
    [string]$CoreExe = "",
    [string]$ToadExe = "",
    [string]$WintunDll = "",
    [string]$LeshyDir = "",
    # The Qt kit the UI exe was built against: windeployqt must harvest the
    # payload from the SAME kit, or the MSI ships DLLs the exe cannot load
    # (the 2026-10-30 VM incident: MSVC exe over a mingw payload).
    [string]$QtDir = "",
    [string]$VcRuntimeDir = "",
    [switch]$SkipBuild,
    [switch]$AllowMissingWintun
)

$ErrorActionPreference = "Stop"

$Version = "0.0.0"
$VersionFile = Join-Path $PSScriptRoot "..\..\VERSION"
if (Test-Path $VersionFile) {
    $Version = (Get-Content $VersionFile -Raw).Trim()
}
if ($Version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') {
    throw "invalid Kikimora version: $Version"
}

# Resolve all paths to absolute form up front: the script pushes into the
# toad directory and the packaging directory for builds, and relative input
# paths must not follow it.
if (-not [System.IO.Path]::IsPathRooted($OutDir)) {
    $OutDir = Join-Path (Get-Location) $OutDir
}
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\..")
foreach ($name in @("UiExe", "CoreExe", "ToadExe", "WintunDll", "LeshyDir")) {
    $value = (Get-Variable $name -ValueOnly)
    if ($value -and -not [System.IO.Path]::IsPathRooted($value)) {
        $resolved = Join-Path (Get-Location) $value
        if (Test-Path $resolved) { Set-Variable $name ((Resolve-Path $resolved).Path) }
    }
}
$PackageDir = Join-Path $OutDir "kikimora-$Version-windows-amd64"

Write-Host "Kikimora Windows packaging"
Write-Host "  Version: $Version"
Write-Host "  Output:  $PackageDir"

New-Item -ItemType Directory -Force -Path $PackageDir | Out-Null
# Normalize: later stages cut package-relative names with
# FullName.Substring($PackageDir.Length + 1). FullName is always normalized,
# so a package path carrying a ".\" segment (CI passes ".\dist-test") would
# chop real characters off every staged file name ("kikimora" -> "kimora").
$PackageDir = (Get-Item $PackageDir).FullName

function Build-ToadBinary([string]$Name, [string]$OutPath) {
    Write-Host "  building $Name (windows/amd64)..."
    $ToadDir = Join-Path $RepoRoot "toad"
    $Env:CGO_ENABLED = "0"
    $Env:GOOS = "windows"
    $Env:GOARCH = "amd64"
    Push-Location $ToadDir
    try {
        go build -ldflags "-X main.coreVersion=$Version -X main.toadVersion=$Version" -o $OutPath ./cmd/$Name
        if ($LASTEXITCODE -ne 0) { throw "go build ./cmd/$Name failed" }
    } finally {
        Pop-Location
        Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    }
}

if ($CoreExe -and (Test-Path $CoreExe)) {
    Copy-Item $CoreExe (Join-Path $PackageDir "kikimora-core.exe") -Force
    Write-Host "  staged: kikimora-core.exe (provided)"
} elseif (-not $SkipBuild) {
    Build-ToadBinary "kikimora-core" (Join-Path $PackageDir "kikimora-core.exe")
    Write-Host "  staged: kikimora-core.exe (built)"
} else {
    throw "kikimora-core.exe not provided and -SkipBuild forbids building"
}

if ($ToadExe -and (Test-Path $ToadExe)) {
    Copy-Item $ToadExe (Join-Path $PackageDir "kikimora-toad.exe") -Force
    Write-Host "  staged: kikimora-toad.exe (provided)"
} elseif (-not $SkipBuild) {
    Build-ToadBinary "kikimora-toad" (Join-Path $PackageDir "kikimora-toad.exe")
    Write-Host "  staged: kikimora-toad.exe (built)"
} else {
    throw "kikimora-toad.exe not provided and -SkipBuild forbids building"
}

if ($UiExe -and (Test-Path $UiExe)) {
    Copy-Item $UiExe (Join-Path $PackageDir "kikimora-ui.exe") -Force
    Write-Host "  staged: kikimora-ui.exe (provided)"
}

# Wintun driver redistributable: the Toad platform layer reaches the driver
# through golang.zx2c4.com/wintun, which loads wintun.dll with
# LOAD_LIBRARY_SEARCH_APPLICATION_DIR|SYSTEM32 only - PATH is never searched.
# The file must therefore sit next to kikimora-core.exe (or be installed
# machine-wide into System32), otherwise no role can create its adapter and the
# failure only shows up at runtime. Release packaging refuses to build a package
# without it; -AllowMissingWintun is for layout-only tests.
$WintunSource = ""
$WintunStaged = $false
$WintunFromDropIn = $false
if ($WintunDll) {
    $WintunSource = $WintunDll
} elseif ($Env:KIKIMORA_WINTUN_DLL) {
    $WintunSource = $Env:KIKIMORA_WINTUN_DLL
} else {
    $DropIn = Join-Path $PSScriptRoot "files\wintun\wintun.dll"
    if (Test-Path $DropIn) {
        $WintunSource = $DropIn
        $WintunFromDropIn = $true
    }
}

if ($WintunSource) {
    if (-not (Test-Path $WintunSource)) {
        throw "wintun.dll not found at $WintunSource"
    }
    $WintunHash = (Get-FileHash $WintunSource -Algorithm SHA256).Hash.ToLower()
    # An optional pin file turns the vendor drop-in into a reviewed artifact: when
    # packaging\windows\files\wintun\wintun.sha256 exists its hash must match.
    # Explicitly provided paths (-WintunDll / env) are the operator's choice and
    # bypass the pin.
    $WintunPin = Join-Path $PSScriptRoot "files\wintun\wintun.sha256"
    if ($WintunFromDropIn -and (Test-Path $WintunPin)) {
        $Pinned = ((Get-Content $WintunPin -Raw) -replace "\s", "").ToLower()
        if ($Pinned -and $Pinned -ne $WintunHash) {
            throw "wintun.dll SHA256 mismatch: pin $WintunPin says $Pinned, got $WintunHash for $WintunSource"
        }
    }
    Copy-Item $WintunSource (Join-Path $PackageDir "wintun.dll") -Force
    $WintunStaged = $true
    $WintunVersion = (Get-Item $WintunSource).VersionInfo.FileVersion
    Write-Host "  staged: wintun.dll (version $WintunVersion, sha256 $WintunHash)"
} elseif ($AllowMissingWintun) {
    Write-Host "  WARNING: wintun.dll not staged (-AllowMissingWintun): this package cannot create Wintun adapters"
} else {
    throw @"
wintun.dll is required for the Windows package and was not found.
  Obtain the redistributable from https://www.wintun.net/ and use one of:
    - powershell -File stage.ps1 ... -WintunDll <path to wintun.dll>,
    - set `$Env:KIKIMORA_WINTUN_DLL=<path to wintun.dll>,
    - drop it into packaging\windows\files\wintun\wintun.dll
      (optionally pin it with packaging\windows\files\wintun\wintun.sha256).
  Pass -AllowMissingWintun only for non-product layout tests.
"@
}

Copy-Item (Join-Path $PSScriptRoot "install.ps1") $PackageDir -Force
Copy-Item (Join-Path $PSScriptRoot "uninstall.ps1") $PackageDir -Force
Write-Host "  staged: install.ps1, uninstall.ps1"

# The ownership template is an MSI payload (conffile semantics: install only
# when missing, never overwrite operator edits). Staging it into the package
# directory lets candle resolve Source="$(var.PackageDir)\ownership.conf" and
# keeps the portable zip layout equal to the MSI payload.
$OwnershipConf = Join-Path $PSScriptRoot "files\ownership.conf"
if (-not (Test-Path $OwnershipConf)) { throw "ownership template not found: $OwnershipConf" }
Copy-Item $OwnershipConf $PackageDir -Force
Write-Host "  staged: ownership.conf"

$components = @(
    @{ name = "kikimora-core"; path = "kikimora-core.exe" },
    @{ name = "kikimora-toad"; path = "kikimora-toad.exe" }
)

# Optional Leshy runtime (leshy-win): without it the tunnels connect but
# system traffic does not steer into them on Windows - the MSI documents this
# dependency, the portable zip can carry it. -LeshyDir points at an extracted
# leshy-windows portable build (leshy.exe, win\ service files).
if ($LeshyDir) {
    if (-not (Test-Path $LeshyDir)) { throw "LeshyDir not found: $LeshyDir" }
    $leshyTarget = Join-Path $PackageDir "leshy"
    New-Item -ItemType Directory -Force -Path $leshyTarget | Out-Null
    Copy-Item (Join-Path $LeshyDir "*") $leshyTarget -Recurse -Force
    Write-Host "  staged: leshy/ (traffic steering runtime from $LeshyDir)"
    $components += @{ name = "leshy-win"; path = "leshy/" }
    $LeshyStaged = $true
}

if ($UiExe -and (Test-Path $UiExe)) {
    $components += @{ name = "kikimora-ui"; path = "kikimora-ui.exe" }
}

# The MSI installs the tray UI into Program Files\Kikimora\ui with its complete
# Qt runtime (the core and the UI must not share a directory: the core's
# GnuTLS/curl/ffi DLLs collide with the UI's). ui-payload.wxs is the heat
# harvest of that runtime; PayloadDir must hold exactly the files it lists.
$UiPayloadStaged = $false
if ($UiExe -and (Test-Path $UiExe)) {
    # The payload MUST come from the same Qt kit as the exe. windeployqt
    # against -QtDir guarantees the DLL set matches the binary by
    # construction; a hand-harvested payload once shipped mingw DLLs next to
    # an MSVC exe and the UI died at the first DLL symbol call.
    if (-not $QtDir) { $QtDir = "C:\Qt-6.8.3\6.8.3\msvc2022_64" }
    if (-not (Test-Path (Join-Path $QtDir "bin\windeployqt.exe"))) { throw "QtDir invalid (windeployqt.exe not found): $QtDir" }
    $UiPayloadTarget = Join-Path $PackageDir "ui"
    if (Test-Path $UiPayloadTarget) { Remove-Item $UiPayloadTarget -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $UiPayloadTarget | Out-Null
    Copy-Item $UiExe (Join-Path $UiPayloadTarget "kikimora-ui.exe") -Force
    # cmd /c wrapper: windeployqt writes warnings to stderr, and PowerShell 5.1
    # with ErrorActionPreference=Stop turns any native stderr line into a
    # terminating error (RemoteException) even though the tool succeeded.
    $wdLog = Join-Path $OutDir "windeployqt.log"
    cmd /c "`"$(Join-Path $QtDir 'bin\windeployqt.exe')`" --release --no-translations --no-opengl-sw --no-system-d3d-compiler `"$(Join-Path $UiPayloadTarget 'kikimora-ui.exe')`" > `"$wdLog`" 2>&1"
    if ($LASTEXITCODE -ne 0) { throw "windeployqt failed (see $wdLog)" }
    # Release DLLs only: the Qt bin dir also contains debug builds (*d.dll)
    # that would bloat the payload and mask a debug-built exe.
    Get-ChildItem (Join-Path $QtDir "bin") -Filter "Qt6*.dll" | Where-Object { $_.Name -notmatch 'd\.dll$' } | ForEach-Object {
        Copy-Item $_.FullName $UiPayloadTarget -Force
    }
    Copy-Item (Join-Path $QtDir "qml") $UiPayloadTarget -Recurse -Force
    Copy-Item (Join-Path $QtDir "plugins\styles") (Join-Path $UiPayloadTarget "styles") -Recurse -Force
    foreach ($plugin in @("qoffscreen.dll", "qminimal.dll", "qwindows.dll")) {
        $source = Join-Path $QtDir ("plugins\platforms\" + $plugin)
        if (Test-Path $source) { Copy-Item $source (Join-Path $UiPayloadTarget ("platforms\" + $plugin)) -Force }
    }
    # MSVC runtime next to the exe: a clean Win10 has the UCRT inbox, but
    # vcruntime140/msvcp140 come only with a VC redist install.
    if (-not $VcRuntimeDir) {
        # Search all VS editions, Build Tools, and use vswhere as the fallback
        $searchPaths = @(
            "C:\Program Files\Microsoft Visual Studio\2022",
            "C:\Program Files\Microsoft Visual Studio\BuildTools"
        )
        foreach ($sp in $searchPaths) {
            $redist = Get-ChildItem (Join-Path $sp "VC\Redist\MSVC") -Directory -ErrorAction SilentlyContinue |
                Where-Object { Test-Path (Join-Path $_.FullName "x64\Microsoft.VC143.CRT") } |
                Sort-Object Name -Descending | Select-Object -First 1
            if ($redist) { $VcRuntimeDir = Join-Path $redist.FullName "x64\Microsoft.VC143.CRT"; break }
        }
        if (-not $VcRuntimeDir) {
            $vswhere = "C:\Program Files (x86)\Microsoft Visual Studio\Installer\vswhere.exe"
            if (Test-Path $vswhere) {
                $vsPath = (& $vswhere -latest -property installationPath 2>$null)
                if ($vsPath -and (Test-Path (Join-Path $vsPath "VC\Redist\MSVC"))) {
                    $redist = Get-ChildItem (Join-Path $vsPath "VC\Redist\MSVC") -Directory -ErrorAction SilentlyContinue |
                        Where-Object { Test-Path (Join-Path $_.FullName "x64\Microsoft.VC143.CRT") } |
                        Sort-Object Name -Descending | Select-Object -First 1
                    if ($redist) { $VcRuntimeDir = Join-Path $redist.FullName "x64\Microsoft.VC143.CRT" }
                }
            }
        }
    }
    if ($VcRuntimeDir -and (Test-Path $VcRuntimeDir)) {
        foreach ($dll in @("vcruntime140.dll", "vcruntime140_1.dll", "msvcp140.dll")) {
            if (Test-Path (Join-Path $VcRuntimeDir $dll)) { Copy-Item (Join-Path $VcRuntimeDir $dll) $UiPayloadTarget -Force }
        }
    } else {
        Write-Warning "VC runtime not found - the UI may fail on machines without a VC redist"
    }
    Write-Host "  staged: ui/ (windeployqt from $QtDir)"
    $components += @{ name = "kikimora-ui-payload"; path = "ui/" }
    $UiPayloadStaged = $true
}

# The MSI heat fragment must NOT see kikimora-ui.exe inside ui\ - the UiExe
# component already installs it there, and two components claiming one file
# break ICE30. The exe is harvested separately (heat only sees the Qt runtime).
# For the portable zip the exe goes back into ui\ after the harvest.
if ($WintunStaged) {
    $components += @{ name = "wintun"; path = "wintun.dll" }
}
$manifest = [ordered]@{
    product     = "Kikimora"
    version     = $Version
    platform    = "windows"
    architecture = "amd64"
    components  = $components
}
$manifest | ConvertTo-Json -Depth 3 | Out-File -Encoding utf8 (Join-Path $PackageDir "manifest.json")
Write-Host "  created: manifest.json"

# checksums.txt covers every staged payload file (scripts and the ui payload
# included), keyed by the package-relative path.
$hashLines = Get-ChildItem $PackageDir -Recurse -File |
    Where-Object { $_.Name -ne "checksums.txt" } |
    ForEach-Object { $_.FullName.Substring($PackageDir.Length + 1) } |
    Sort-Object |
    ForEach-Object {
        $hash = (Get-FileHash (Join-Path $PackageDir $_) -Algorithm SHA256).Hash.ToLower()
        "$hash  $_"
    }
$hashLines | Out-File -Encoding ascii (Join-Path $PackageDir "checksums.txt")
Write-Host "  created: checksums.txt"

# The MSI is the primary Windows installation contract: declarative service
# registration, ProgramData layout with ACLs, MajorUpgrade and ARP uninstall.
$WixBin = if ($Env:WIX_BIN) { $Env:WIX_BIN } else { Join-Path $Env:USERPROFILE "Tools\wix311" }
if (-not (Test-Path (Join-Path $WixBin "candle.exe"))) {
    throw "WiX toolchain not found at $WixBin; set WIX_BIN or download wix311-binaries.zip"
}
Write-Host "  building MSI (WiX: $WixBin)..."
Push-Location $PSScriptRoot
try {
    $defines = @()
    if ($UiExe -and (Test-Path $UiExe)) { $defines += "-dUiPresented=1" }
    if ($WintunStaged) { $defines += "-dWintunPresented=1" }
    if ($LeshyStaged) { $defines += "-dLeshyPresented=1" }
    $candleArgs = @("-arch", "x64", "-dVersion=$Version", "-dPackageDir=$PackageDir") + $defines + @("kikimora.wxs")
    & (Join-Path $WixBin "candle.exe") -ext (Join-Path $WixBin "WixUtilExtension.dll") @candleArgs -out (Join-Path $OutDir "kikimora.wixobj")
    if ($LASTEXITCODE -ne 0) { throw "candle failed" }
    $lightObjects = @(Join-Path $OutDir "kikimora.wixobj")
    if ($UiPayloadStaged) {
        # Harvest the staged ui/ directory fresh on every build: a committed
        # fragment went stale the moment the Qt runtime changed (new DLLs were
        # silently not installed). heat emits the ComponentGroup "UiPayload"
        # that kikimora.wxs references under UiPresented; stable component
        # GUIDs (-gg) keep upgrades incremental. The exe is pulled out for the
        # harvest: the UiExe component already owns kikimora-ui.exe, and two
        # components claiming one file break ICE30.
        $uiExePath = Join-Path $PackageDir "ui\kikimora-ui.exe"
        if (Test-Path $uiExePath) { Remove-Item $uiExePath -Force }
        & (Join-Path $WixBin "heat.exe") dir "$PackageDir\ui" `
            -dr UISUBDIR -cg UiPayload -gg -sfrag -srd -scom -sreg -suid `
            -var var.PayloadDir `
            -out (Join-Path $OutDir "ui-payload.wxs")
        if ($LASTEXITCODE -ne 0) { throw "heat ui-payload failed" }
        & (Join-Path $WixBin "candle.exe") -arch x64 -dPayloadDir="$PackageDir\ui" `
            -ext (Join-Path $WixBin "WixUtilExtension.dll") `
            (Join-Path $OutDir "ui-payload.wxs") -out (Join-Path $OutDir "ui-payload.wixobj")
        if ($LASTEXITCODE -ne 0) { throw "candle ui-payload failed" }
        $lightObjects += (Join-Path $OutDir "ui-payload.wixobj")
        Copy-Item (Join-Path $PackageDir "kikimora-ui.exe") $uiExePath -Force
    }
    & (Join-Path $WixBin "light.exe") -ext (Join-Path $WixBin "WixUtilExtension.dll") `
        -ext (Join-Path $WixBin "WixUIExtension.dll") `
        -loc (Join-Path $PSScriptRoot "ru-ru.wxl") `
        -out (Join-Path $OutDir "kikimora-$Version-windows-amd64.msi") @lightObjects
    if ($LASTEXITCODE -ne 0) { throw "light failed" }
} finally {
    Pop-Location
    Remove-Item (Join-Path $OutDir "kikimora.wixobj") -Force -ErrorAction SilentlyContinue
    Remove-Item (Join-Path $OutDir "ui-payload.wixobj") -Force -ErrorAction SilentlyContinue
}
Write-Host "  created: kikimora-$Version-windows-amd64.msi"

$zipPath = Join-Path $OutDir "kikimora-$Version-windows-amd64.zip"
if (Test-Path $zipPath) { Remove-Item $zipPath -Force }
Compress-Archive -Path $PackageDir -DestinationPath $zipPath
Write-Host "  created: $zipPath"
Write-Host "Windows packaging complete."
