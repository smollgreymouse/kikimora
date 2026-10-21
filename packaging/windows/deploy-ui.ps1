param(
    [Parameter(Mandatory = $true)][string]$UiExe,
    [Parameter(Mandatory = $true)][string]$QtDir,
    [string]$Dest = 'C:\Program Files\Kikimora\ui',
    [string]$MingwRuntimeDir = '',
    [switch]$Shortcuts
)

# Deploy the Kikimora desktop UI into its own directory on Windows.
#
# The UI must not share a directory with the core: C:\Program Files\Kikimora holds the
# core's GnuTLS/curl/ffi DLLs, and loading the UI there fails with 0xC0000139. windeployqt
# alone is not enough either - the QML is embedded in the executable, so it cannot see the
# imports and skips the QtQuick.Controls modules and the Basic style plugin. So: the
# executable, every Qt runtime DLL, the whole qml tree, the styles plugin directory and the
# offscreen/minimal platform plugins (headless runs) all go next to it.

$ErrorActionPreference = 'Continue'

New-Item -ItemType Directory -Force $Dest | Out-Null
Copy-Item $UiExe (Join-Path $Dest 'kikimora-ui.exe') -Force
Write-Host ('copied kikimora-ui.exe -> ' + $Dest)

$windeploy = Join-Path $QtDir 'bin\windeployqt.exe'
& $windeploy --release --no-translations --no-opengl-sw --no-system-d3d-compiler (Join-Path $Dest 'kikimora-ui.exe') | Out-Null

Get-ChildItem (Join-Path $QtDir 'bin') -Filter 'Qt6*.dll' | ForEach-Object {
    Copy-Item $_.FullName (Join-Path $Dest $_.Name) -Force
}

Copy-Item (Join-Path $QtDir 'qml') $Dest -Recurse -Force
Copy-Item (Join-Path $QtDir 'plugins\styles') (Join-Path $Dest 'styles') -Recurse -Force

foreach ($plugin in @('qoffscreen.dll', 'qminimal.dll')) {
    $source = Join-Path $QtDir ('plugins\platforms\' + $plugin)
    if (Test-Path $source) { Copy-Item $source (Join-Path $Dest ('platforms\' + $plugin)) -Force }
}

if ($MingwRuntimeDir) {
    foreach ($dll in @('libstdc++-6.dll', 'libgcc_s_seh-1.dll', 'libwinpthread-1.dll')) {
        $source = Join-Path $MingwRuntimeDir $dll
        if (Test-Path $source) { Copy-Item $source (Join-Path $Dest $dll) -Force }
    }
}

if ($Shortcuts) {
    $shell = New-Object -ComObject WScript.Shell
    $targets = @(
        (Join-Path $env:ProgramData 'Microsoft\Windows\Start Menu\Programs'),
        (Join-Path $env:USERPROFILE 'Desktop')
    )
    foreach ($dir in $targets) {
        if (-not (Test-Path $dir)) { continue }
        $link = $shell.CreateShortcut((Join-Path $dir 'Kikimora.lnk'))
        $link.TargetPath = Join-Path $Dest 'kikimora-ui.exe'
        $link.WorkingDirectory = $Dest
        $link.Description = 'Kikimora VPN client'
        $link.Save()
        Write-Host ('shortcut: ' + (Join-Path $dir 'Kikimora.lnk'))
    }
}

$files = @(Get-ChildItem $Dest -Recurse -File)
$size = [math]::Round((($files | Measure-Object Length -Sum).Sum) / 1MB, 1)
Write-Host ('deployed: ' + $files.Count + ' files, ' + $size + ' MB in ' + $Dest)
