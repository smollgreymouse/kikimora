# SPDX-License-Identifier: MIT
#
# test_release_payload.ps1 — integrated artifact contract for the Windows
# release lane (roadmap P1.3). Inspects already-built MSI/zip/manifest/
# checksums WITHOUT rebuilding anything.
#
#   powershell -File test_release_payload.ps1 -OutDir <built dist> `
#       [-RequireUi] [-RequireLeshy] [-RequireWintun]
#
# Every required payload piece is a hard assertion: there is deliberately no
# "UI payload regression skipped" path. A missing piece fails the contract.
# The MSI database checks need WiX's Microsoft.Deployment.WindowsInstaller.dll
# (WIX_BIN or %USERPROFILE%\Tools\wix311), same as test_package.ps1.

param (
    [Parameter(Mandatory = $true)][string]$OutDir,
    [switch]$RequireUi,
    [switch]$RequireLeshy,
    [switch]$RequireWintun
)

$ErrorActionPreference = "Stop"
if (-not [System.IO.Path]::IsPathRooted($OutDir)) {
    $OutDir = Join-Path (Get-Location) $OutDir
}
$version = (Get-Content (Join-Path $PSScriptRoot "..\..\VERSION") -Raw).Trim()

$failures = 0
function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) {
        Write-Host "FAIL: $Message"
        $script:failures++
    } else {
        Write-Host "ok: $Message"
    }
}

$zipPath = Join-Path $OutDir "kikimora-$version-windows-amd64.zip"
$msiPath = Join-Path $OutDir "kikimora-$version-windows-amd64.msi"

Write-Host "==> integrated release payload contract for $OutDir"
Assert-True (Test-Path $zipPath) "the release zip exists"
Assert-True (Test-Path $msiPath) "the release MSI exists"
if (-not (Test-Path $zipPath) -or -not (Test-Path $msiPath)) {
    Write-Host "FAIL: the release artifacts must be built before this contract runs" -ForegroundColor Red
    exit 1
}

# ---- Portable zip payload -------------------------------------------------
$work = Join-Path ([IO.Path]::GetTempPath()) ("kikimora-payload-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $work | Out-Null
try {
    Expand-Archive -Path $zipPath -DestinationPath $work
    $packageDir = Join-Path $work "kikimora-$version-windows-amd64"
    Assert-True (Test-Path $packageDir) "the zip contains the package directory"

    $mandatoryFiles = @("kikimora-core.exe", "kikimora-toad.exe", "install.ps1", "uninstall.ps1", "manifest.json", "checksums.txt", "ownership.conf")
    foreach ($name in $mandatoryFiles) {
        Assert-True (Test-Path (Join-Path $packageDir $name)) "zip payload stages $name"
    }

    if ($RequireWintun) {
        Assert-True (Test-Path (Join-Path $packageDir "wintun.dll")) "zip payload stages wintun.dll"
    }

    if ($RequireLeshy) {
        Assert-True (Test-Path (Join-Path $packageDir "leshy\leshy.exe")) "zip payload stages the Leshy runtime"
    }

    if ($RequireUi) {
        Assert-True (Test-Path (Join-Path $packageDir "kikimora-ui.exe")) "zip payload stages kikimora-ui.exe"
        $ui = Join-Path $packageDir "ui"
        Assert-True (Test-Path $ui) "zip payload stages the Qt runtime directory ui\"

        $qtFiles = @("Qt6Core.dll", "Qt6Gui.dll", "Qt6Network.dll", "Qt6Qml.dll", "Qt6Quick.dll", "Qt6QuickControls2.dll", "Qt6Widgets.dll")
        foreach ($name in $qtFiles) {
            Assert-True (Test-Path (Join-Path $ui $name)) "Qt runtime ships $name"
        }
        foreach ($plugin in @("platforms\qwindows.dll", "platforms\qoffscreen.dll")) {
            Assert-True (Test-Path (Join-Path $ui $plugin)) "Qt platform plugin ships $plugin"
        }
        $styles = Join-Path $ui "styles"
        $styleDlls = @(Get-ChildItem $styles -Filter *.dll -ErrorAction SilentlyContinue)
        Assert-True ($styleDlls.Count -ge 1) "Qt styles ship at least one style plugin"
        $qmlDir = Join-Path $ui "qml"
        Assert-True (Test-Path $qmlDir) "Qt QML plugin tree ships (qml\)"

        foreach ($runtime in @("vcruntime140.dll", "vcruntime140_1.dll", "msvcp140.dll")) {
            Assert-True (Test-Path (Join-Path $ui $runtime)) "MSVC runtime ships $runtime"
        }
    }

    # ---- manifest + checksums ---------------------------------------------
    $manifestPath = Join-Path $packageDir "manifest.json"
    $manifest = Get-Content $manifestPath -Raw | ConvertFrom-Json
    Assert-True ($manifest.version -eq $version) "manifest carries the release version"

    $manifestPaths = @($manifest.components | ForEach-Object { $_.path })
    foreach ($mandatory in @("kikimora-core.exe", "kikimora-toad.exe")) {
        Assert-True ($manifestPaths -contains $mandatory) "manifest lists $mandatory"
    }
    if ($RequireWintun) {
        Assert-True ($manifestPaths -contains "wintun.dll") "manifest lists the wintun component"
    }
    if ($RequireLeshy) {
        Assert-True ($manifestPaths -contains "leshy/") "manifest lists the Leshy runtime component"
    }
    if ($RequireUi) {
        Assert-True ($manifestPaths -contains "kikimora-ui.exe") "manifest lists the UI executable"
        Assert-True ($manifestPaths -contains "ui/") "manifest lists the Qt payload component"
    }

    # checksums.txt must cover every staged payload file exactly, and the
    # hashes must match the bytes in the zip.
    $checksumsPath = Join-Path $packageDir "checksums.txt"
    Assert-True (Test-Path $checksumsPath) "zip payload stages checksums.txt"
    $expected = Get-ChildItem $packageDir -Recurse -File |
        Where-Object { $_.FullName -ne $checksumsPath } |
        ForEach-Object { $_.FullName.Substring($packageDir.Length + 1) } |
        Sort-Object
    $recorded = Get-Content $checksumsPath | ForEach-Object { ($_ -split "  ", 2)[1] } | Sort-Object
    $diff = Compare-Object $expected $recorded
    Assert-True ($null -eq $diff) "checksums.txt covers exactly the staged payload files"

    $mismatch = $false
    foreach ($line in (Get-Content $checksumsPath)) {
        $parts = $line -split "  ", 2
        if ($parts.Count -lt 2) { continue }
        $actual = (Get-FileHash (Join-Path $packageDir $parts[1]) -Algorithm SHA256).Hash.ToLower()
        if ($actual -ne $parts[0]) {
            Write-Host "FAIL: checksum mismatch for $($parts[1])"
            $script:failures++
            $mismatch = $true
        }
    }
    if (-not $mismatch) { Write-Host "ok: every payload checksum matches" }

    # ---- MSI database rows -------------------------------------------------
    $wixBin = if ($Env:WIX_BIN) { $Env:WIX_BIN } else { Join-Path $Env:USERPROFILE "Tools\wix311" }
    $installerDll = Join-Path $wixBin "Microsoft.Deployment.WindowsInstaller.dll"
    Assert-True (Test-Path $installerDll) "WiX installer assembly is available ($installerDll)"
    if (Test-Path $installerDll) {
        Add-Type -Path $installerDll
        $database = New-Object Microsoft.Deployment.WindowsInstaller.Database($msiPath)

        function Test-MsiRow([Microsoft.Deployment.WindowsInstaller.Database]$Db, [string]$Table, [string]$Column, [string]$Value) {
            # File table names are stored as "short|long" pairs, so iterate the
            # whole column instead of exact WHERE matches.
            $view = $Db.OpenView(('SELECT `{0}` FROM `{1}`' -f $Column, $Table))
            $view.Execute()
            while ($record = $view.Fetch()) {
                $found = $record.GetString(1)
                if ($found -eq $Value -or $found -like "*|$Value") {
                    $view.Close()
                    return $true
                }
            }
            $view.Close()
            return $false
        }

        foreach ($file in @("kikimora-core.exe", "kikimora-toad.exe", "ownership.conf")) {
            Assert-True (Test-MsiRow $database "File" "FileName" $file) "MSI installs $file"
        }
        Assert-True (Test-MsiRow $database "ServiceInstall" "Name" "KikimoraCore") "MSI registers the KikimoraCore service"
        Assert-True (Test-MsiRow $database "ServiceControl" "Name" "KikimoraCore") "MSI removes the KikimoraCore service on uninstall"

        if ($RequireWintun) {
            Assert-True (Test-MsiRow $database "File" "FileName" "wintun.dll") "MSI installs wintun.dll"
        }
        if ($RequireLeshy) {
            Assert-True (Test-MsiRow $database "File" "FileName" "leshy.exe") "MSI installs the Leshy runtime"
            Assert-True (Test-MsiRow $database "ServiceInstall" "Name" "leshy") "MSI registers the leshy service"
            # Leshy -> KikimoraCore SCM dependency (08D1 11.2): WiX3 cannot put
            # it in ServiceInstall, the InstallExecuteSequence carries the
            # sc.exe config step instead.
            Assert-True (Test-MsiRow $database "CustomAction" "Action" "LeshyDependOnCore") "MSI carries the Leshy->KikimoraCore dependency action"
            Assert-True (Test-MsiRow $database "InstallExecuteSequence" "Action" "LeshyDependOnCore") "the Leshy dependency action is scheduled"
        }
        if ($RequireUi) {
            Assert-True (Test-MsiRow $database "File" "FileName" "kikimora-ui.exe") "MSI installs kikimora-ui.exe"
            # UI shortcuts and the session autostart row.
            Assert-True (Test-MsiRow $database "Shortcut" "Shortcut" "KikimoraMenuShortcut") "MSI installs the Start Menu shortcut"
            Assert-True (Test-MsiRow $database "Shortcut" "Shortcut" "KikimoraStartupShortcut") "MSI installs the startup shortcut (autostart)"
            Assert-True (Test-MsiRow $database "Registry" "Name" "StartupShortcut") "MSI carries the autostart registry row"
            # The mandatory Qt payload components reach the MSI through the
            # harvested UiPayload group: a representative File row must exist.
            Assert-True (Test-MsiRow $database "File" "FileName" "Qt6Quick.dll") "MSI installs the Qt Quick runtime"
            Assert-True (Test-MsiRow $database "File" "FileName" "qwindows.dll") "MSI installs the qwindows platform plugin"
            Assert-True (Test-MsiRow $database "File" "FileName" "msvcp140.dll") "MSI installs the MSVC runtime payload"
        }

        # Mandatory release features/components: the Complete feature must
        # reference the payload component groups. Single-quoted: the backtick
        # is the MSI identifier quote and would escape the closing double
        # quote inside a double-quoted PowerShell string.
        $featureView = $database.OpenView('SELECT `Feature` FROM `Feature`')
        $featureView.Execute()
        $features = @()
        while ($record = $featureView.Fetch()) { $features += $record.GetString(1) }
        $featureView.Close()
        Assert-True ($features -contains "Complete") "MSI exposes the Complete feature"

        $database.Close()
    }
}
finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}

if ($failures -gt 0) {
    Write-Host "RELEASE PAYLOAD CONTRACT FAILED: $failures assertion(s)" -ForegroundColor Red
    exit 1
}
Write-Host "==> Windows integrated release payload contract GREEN" -ForegroundColor Green
exit 0
