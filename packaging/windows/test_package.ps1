# SPDX-License-Identifier: MIT
#
# test_package.ps1 — static contract test for the Windows portable package.
# Runs unprivileged and never installs anything: it stages the package, then
# verifies the layout, checksums and script contracts the VM acceptance relies
# on. Exit code 0 = contract holds.
#
#   powershell -File test_package.ps1 [-OutDir .\dist-test]

param (
    [string]$OutDir = ".\dist-test"
)

$ErrorActionPreference = "Stop"
if (-not [System.IO.Path]::IsPathRooted($OutDir)) {
    $OutDir = Join-Path (Get-Location) $OutDir
}
$failures = 0
function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) {
        Write-Host "FAIL: $Message"
        $script:failures++
    } else {
        Write-Host "ok: $Message"
    }
}

# Build the payload binaries first so staging is a single deterministic pass.
$binDir = Join-Path $OutDir "bin"
New-Item -ItemType Directory -Force -Path $binDir | Out-Null
$toadDir = Join-Path $PSScriptRoot "..\..\toad"
$Env:CGO_ENABLED = "0"; $Env:GOOS = "windows"; $Env:GOARCH = "amd64"
Push-Location $toadDir
try {
    go build -o (Join-Path $binDir "kikimora-core.exe") ./cmd/kikimora-core
    if ($LASTEXITCODE -ne 0) { throw "go build kikimora-core failed" }
    go build -o (Join-Path $binDir "kikimora-toad.exe") ./cmd/kikimora-toad
    if ($LASTEXITCODE -ne 0) { throw "go build kikimora-toad failed" }
} finally {
    Pop-Location
    Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
}

$packageScript = Join-Path $PSScriptRoot "stage.ps1"
$coreExe = Join-Path $binDir "kikimora-core.exe"
$toadExe = Join-Path $binDir "kikimora-toad.exe"
$version = (Get-Content (Join-Path $PSScriptRoot "..\..\VERSION") -Raw).Trim()

# --- Wintun redistributable ------------------------------------------------
# The Toad platform layer reaches the driver through golang.zx2c4.com/wintun,
# which loads wintun.dll from the binary directory or System32 only (never
# PATH). A package without it cannot create a single adapter, so the release
# path must refuse to build one, and a provided DLL must reach the payload,
# the manifest, checksums.txt and the MSI File table.
$wintunDropIn = Join-Path $PSScriptRoot "files\wintun\wintun.dll"
$wintunProvided = ""
if ($Env:KIKIMORA_WINTUN_DLL -and (Test-Path $Env:KIKIMORA_WINTUN_DLL)) {
    $wintunProvided = $Env:KIKIMORA_WINTUN_DLL
} elseif (Test-Path $wintunDropIn) {
    $wintunProvided = $wintunDropIn
}

if ($wintunProvided) {
    Write-Host "note: wintun.dll resolved from $wintunProvided; the missing-DLL refusal check is not exercised"
} else {
    # Run the refusal in a child process: an unhandled throw inside this session
    # would abort the contract test instead of being asserted on.
    $missingOut = Join-Path $OutDir "wintun-missing"
    $missingLog = Join-Path $OutDir "wintun-missing.log"
    $missingErr = Join-Path $OutDir "wintun-missing.err.log"
    $refusal = Start-Process -FilePath "powershell" -NoNewWindow -Wait -PassThru `
        -RedirectStandardOutput $missingLog -RedirectStandardError $missingErr `
        -ArgumentList @("-NoProfile", "-File", $packageScript, "-OutDir", $missingOut, "-CoreExe", $coreExe, "-ToadExe", $toadExe)
    Assert-True ($refusal.ExitCode -ne 0) "stage.ps1 refuses to build a package without wintun.dll"
    $refusalText = (Get-Content $missingLog -Raw -ErrorAction SilentlyContinue) + (Get-Content $missingErr -Raw -ErrorAction SilentlyContinue)
    Assert-True ($refusalText -match "wintun\.dll is required") "the refusal names the missing Wintun redistributable"
}

# A synthetic DLL exercises the payload plumbing on every host, independent of
# whether a real redistributable is available.
$syntheticDir = Join-Path $OutDir "wintun-synthetic"
New-Item -ItemType Directory -Force -Path $syntheticDir | Out-Null
$syntheticDll = Join-Path $syntheticDir "wintun.dll"
[IO.File]::WriteAllBytes($syntheticDll, [byte[]](1..255))
$wintunOut = Join-Path $OutDir "wintun-payload"

& $packageScript -OutDir $OutDir -CoreExe $coreExe -ToadExe $toadExe -AllowMissingWintun | Out-Null
if ($LASTEXITCODE -ne 0) { throw "stage.ps1 failed" }
& $packageScript -OutDir $wintunOut -CoreExe $coreExe -ToadExe $toadExe -WintunDll $syntheticDll -SkipBuild | Out-Null
if ($LASTEXITCODE -ne 0) { throw "stage.ps1 failed for the explicitly provided wintun.dll" }

$packageDir = Join-Path $OutDir "kikimora-$version-windows-amd64"
$wintunPackageDir = Join-Path $wintunOut "kikimora-$version-windows-amd64"

Assert-True (Test-Path (Join-Path $packageDir "kikimora-core.exe")) "package stages kikimora-core.exe"
Assert-True (Test-Path (Join-Path $packageDir "kikimora-toad.exe")) "package stages kikimora-toad.exe"
Assert-True (Test-Path (Join-Path $packageDir "install.ps1")) "package stages install.ps1"
Assert-True (Test-Path (Join-Path $packageDir "uninstall.ps1")) "package stages uninstall.ps1"
Assert-True (Test-Path (Join-Path $packageDir "manifest.json")) "package stages manifest.json"
Assert-True (Test-Path (Join-Path $packageDir "checksums.txt")) "package stages checksums.txt"
Assert-True (Test-Path (Join-Path $packageDir "ownership.conf")) "package stages ownership.conf"
if ($wintunProvided) {
    Assert-True (Test-Path (Join-Path $packageDir "wintun.dll")) "package stages wintun.dll when a redistributable is resolvable"
} else {
    Assert-True (-not (Test-Path (Join-Path $packageDir "wintun.dll"))) "package omits wintun.dll when none is available (-AllowMissingWintun)"
}
Assert-True (Test-Path (Join-Path $wintunPackageDir "wintun.dll")) "explicit -WintunDll stages wintun.dll into the payload"
Assert-True (Test-Path (Join-Path $OutDir "kikimora-$version-windows-amd64.zip")) "package builds the zip archive"

$manifest = Get-Content (Join-Path $packageDir "manifest.json") -Raw | ConvertFrom-Json
Assert-True ($manifest.version -eq $version) "manifest carries the package version"
Assert-True ($manifest.components.Count -ge 2) "manifest lists at least core and toad"
$wintunManifest = Get-Content (Join-Path $wintunPackageDir "manifest.json") -Raw | ConvertFrom-Json
Assert-True (@($wintunManifest.components | Where-Object { $_.path -eq "wintun.dll" }).Count -eq 1) "manifest lists the wintun component"
Assert-True ((Get-Content (Join-Path $wintunPackageDir "checksums.txt") -Raw) -match "wintun\.dll") "checksums.txt covers wintun.dll"

# checksums.txt must cover every staged payload file exactly.
$expected = Get-ChildItem $packageDir -File |
    Where-Object { $_.Name -ne "checksums.txt" } |
    Sort-Object Name | ForEach-Object { $_.Name }
$recorded = Get-Content (Join-Path $packageDir "checksums.txt") |
    ForEach-Object { ($_ -split "  ", 2)[1] } | Sort-Object
$diff = Compare-Object $expected $recorded
Assert-True ($null -eq $diff) "checksums.txt covers exactly the staged files"

foreach ($line in (Get-Content (Join-Path $packageDir "checksums.txt"))) {
    $parts = $line -split "  ", 2
    $actual = (Get-FileHash (Join-Path $packageDir $parts[1]) -Algorithm SHA256).Hash.ToLower()
    Assert-True ($actual -eq $parts[0]) "checksum matches for $($parts[1])"
}

# The MSI is the primary installation contract: product identity, payload and
# the declarative service registration must all be present in the database.
$wixBin = if ($Env:WIX_BIN) { $Env:WIX_BIN } else { Join-Path $Env:USERPROFILE "Tools\wix311" }
Add-Type -Path (Join-Path $wixBin "Microsoft.Deployment.WindowsInstaller.dll")

$msiPath = Join-Path $OutDir "kikimora-$version-windows-amd64.msi"
Assert-True (Test-Path $msiPath) "package builds the MSI"
if (Test-Path $msiPath) {
    $database = New-Object Microsoft.Deployment.WindowsInstaller.Database($msiPath)

    function Get-MsiProperty([Microsoft.Deployment.WindowsInstaller.Database]$Db, [string]$Name) {
        $view = $Db.OpenView(('SELECT `Value` FROM `Property` WHERE `Property` = ''{0}''' -f $Name))
        $view.Execute()
        $record = $view.Fetch()
        if ($record) { return $record.GetString(1) }
        return $null
    }

    Assert-True ((Get-MsiProperty $database "ProductName") -eq "Kikimora") "MSI ProductName is Kikimora"
    Assert-True ((Get-MsiProperty $database "ProductVersion") -eq $version) "MSI ProductVersion matches the package version"
    Assert-True ((Get-MsiProperty $database "UpgradeCode") -ne $null) "MSI carries the stable UpgradeCode for MajorUpgrade"

    function Test-MsiRow([Microsoft.Deployment.WindowsInstaller.Database]$Db, [string]$Table, [string]$Column, [string]$Value) {
        # Iterate the whole column: File table names are stored as
        # "short|long" pairs, so exact WHERE matches miss the payload rows.
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

    Assert-True (Test-MsiRow $database "File" "FileName" "kikimora-core.exe") "MSI installs kikimora-core.exe"
    Assert-True (Test-MsiRow $database "File" "FileName" "kikimora-toad.exe") "MSI installs kikimora-toad.exe"
    Assert-True (Test-MsiRow $database "File" "FileName" "ownership.conf") "MSI installs ownership.conf"
    if ($wintunProvided) {
        Assert-True (Test-MsiRow $database "File" "FileName" "wintun.dll") "MSI installs wintun.dll (redistributable was available)"
    } else {
        Assert-True (-not (Test-MsiRow $database "File" "FileName" "wintun.dll")) "MSI omits the wintun.dll row when the payload has none"
    }
    Assert-True (Test-MsiRow $database "ServiceInstall" "Name" "KikimoraCore") "MSI registers the KikimoraCore service"
    Assert-True (Test-MsiRow $database "ServiceControl" "Name" "KikimoraCore") "MSI removes the KikimoraCore service on uninstall"
    Assert-True (Test-MsiRow $database "Directory" "Directory" "DATAROOTSTATE") "MSI creates the ProgramData state directory"

    # ownership.conf must be installed with conffile semantics: Permanent (kept
    # on uninstall) and NeverOverwrite (operator edits survive upgrade).
    $compView = $database.OpenView("SELECT `Attributes` FROM `Component` WHERE `Component` = 'OwnershipConf'")
    $compView.Execute()
    $compRecord = $compView.Fetch()
    if ($compRecord) {
        $attrs = $compRecord.GetInteger(1)
        Assert-True (($attrs -band 16) -ne 0) "OwnershipConf component is Permanent"
        Assert-True (($attrs -band 128) -ne 0) "OwnershipConf component is NeverOverwrite"
    } else {
        Assert-True $false "OwnershipConf component exists in MSI database"
    }
    $compView.Close()
    $database.Close()
}

# The synthetic package proves the conditional MSI payload on every host.
$wintunMsiPath = Join-Path $wintunOut "kikimora-$version-windows-amd64.msi"
Assert-True (Test-Path $wintunMsiPath) "package builds the MSI for an explicitly provided wintun.dll"
if (Test-Path $wintunMsiPath) {
    $wintunDb = New-Object Microsoft.Deployment.WindowsInstaller.Database($wintunMsiPath)
    Assert-True (Test-MsiRow $wintunDb "File" "FileName" "wintun.dll") "MSI installs wintun.dll"
    Assert-True (Test-MsiRow $wintunDb "Component" "Component" "WintunDll") "MSI carries the WintunDll component"
    Assert-True (Test-MsiRow $wintunDb "FeatureComponents" "Component_" "WintunDll") "the WintunDll component is part of the installed feature"
    $wintunDb.Close()
}

# Installer scripts must require elevation and must not mutate VPN desired
# state on fresh installs.
$install = Get-Content (Join-Path $packageDir "install.ps1") -Raw
Assert-True ($install -match "must run from an elevated prompt") "install.ps1 requires elevation"
Assert-True ($install -match "service install") "install.ps1 registers the core service"
$uninstall = Get-Content (Join-Path $packageDir "uninstall.ps1") -Raw
Assert-True ($uninstall -match "service uninstall") "uninstall.ps1 removes the core service"
Assert-True ($uninstall -match 'preserved \$DataRoot') "uninstall keeps desired state without -Purge"

Write-Host ""
if ($script:failures -gt 0) {
    Write-Host "package contract FAILED with $script:failures assertion(s)"
    exit 1
}
Write-Host "package contract holds ($version)"
