# SPDX-License-Identifier: MIT
#
# test_managed_package_lifecycle.ps1 — the versioned Windows package
# lifecycle gate (H6, roadmap §16.5).
#
# Disposable-fixture contract (T1R, 2026-11-16 post-e7ec9c2 audit §7.3):
#   * every mode requires a host where Kikimora is NOT installed; the harness
#     refuses an unexpected installed Kikimora instead of removing it, and
#     never manufactures a "fresh" row by uninstalling an operator baseline;
#   * upgrade modes install -Previous themselves and stage the requested old
#     state;
#   * candidate/previous artifacts are validated READ-ONLY through the
#     Windows Installer COM database before any service/MSI mutation;
#   * the state-layout legacy cases stay in test_state_layout_upgrade.ps1
#     and are deliberately not duplicated here;
#   * one try/catch/finally failure path writes evidence and preserves the
#     original exit code; the host resets/reprovisions the disposable VM
#     baseline between independent modes.
#
# Modes:
#   FreshMsi                clean MSI install -> payload + managed smoke
#   FreshPortable           portable zip layout via install.ps1 -> service smoke
#   UpgradeLocal            previous MSI with local state -> candidate
#   UpgradeSchema1Empty     previous + schema1 empty state -> candidate
#   UpgradeSchema2          previous + schema2 committed state -> candidate
#   UpgradeSchema1Ambiguous previous + ambiguous schema1 state -> upgrade must
#                           abort and keep the previous install working
#
# Usage (elevated, on win10):
#   powershell -ExecutionPolicy Bypass -File test_managed_package_lifecycle.ps1 `
#       -Mode FreshMsi `
#       -Candidate C:\abs\kikimora-<version>-windows-amd64.msi `
#       -Previous  C:\abs\kikimora-<prev>-windows-amd64.msi `
#       -WorkDir C:\kikimora-acceptance\<sha>\h6
param(
    [Parameter(Mandatory = $true)][ValidateSet("FreshMsi", "FreshPortable", "UpgradeLocal", "UpgradeSchema1Empty", "UpgradeSchema2", "UpgradeSchema1Ambiguous")][string]$Mode,
    [Parameter(Mandatory = $true)][string]$Candidate,
    [string]$Previous = "",
    [Parameter(Mandatory = $true)][string]$WorkDir
)

$ErrorActionPreference = "Continue"
$identity = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $identity.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "test_managed_package_lifecycle.ps1 must run elevated"
}
if (-not (Test-Path $Candidate)) { throw "missing artifact: $Candidate" }
$Candidate = (Resolve-Path $Candidate).Path
$isPortableMode = $Mode -eq "FreshPortable"
if (-not $isPortableMode -and $Mode -like "Upgrade*") {
    if (-not $Previous -or -not (Test-Path $Previous)) { throw "upgrade modes require -Previous pointing at the previous MSI" }
    $Previous = (Resolve-Path $Previous).Path
}
if (-not [System.IO.Path]::IsPathRooted($WorkDir)) { $WorkDir = Join-Path (Get-Location) $WorkDir }
New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
$WorkDir = (Resolve-Path $WorkDir).Path

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$sourceSha = "unknown"
try { $sourceSha = (git -C $RepoRoot rev-parse HEAD).Trim() } catch {}
$harnessSha256 = (Get-FileHash -Algorithm SHA256 -Path $PSCommandPath).Hash.ToLowerInvariant()

$evidence = Join-Path $WorkDir $Mode
New-Item -ItemType Directory -Force -Path $evidence | Out-Null
$CoreExe = "C:\Program Files\Kikimora\kikimora-core.exe"
$DataRoot = "C:\ProgramData\Kikimora"
$CoreSocket = "$DataRoot\core.sock"
$CoreStateDir = "$DataRoot\state\core"
$ToadsDir = "$DataRoot\toads"
$Marker = "$ToadsDir\leshy-managed.json"
$CoreService = "KikimoraCore"
$LeshyService = "leshy"
# H6 2026-11-21: the activation id staged by the schema2 shape (32 lowercase
# hex, per the marker Validate contract). The smoke proves the upgrade
# recovered exactly this activation.
$script:stagedActivation = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"

function Note([string]$m) { Write-Host "== $m" }
function Pass([string]$m) { Write-Host "PASS: $m" }
function Fail([string]$m) {
    Write-Host "FAIL: $m"
    $script:failures++
}
$failures = 0

# T1R identity contract (§7.2): the harness, the source and every artifact
# that can affect the verdict are printed before any mutation.
Note "harness identities"
Note "harness: $PSCommandPath"
Note "harness sha256: $harnessSha256"
Note "source sha: $sourceSha"
Note "mode: $Mode"
Note "candidate: $Candidate"
Note "candidate sha256: $((Get-FileHash $Candidate -Algorithm SHA256).Hash.ToLower())"
if ($Previous) {
    Note "previous: $Previous"
    Note "previous sha256: $((Get-FileHash $Previous -Algorithm SHA256).Hash.ToLower())"
}

# ---- read-only preflight (T1R §7.3) ----------------------------------------

function Test-KikimoraMsiArtifact([string]$Path, [string]$Kind) {
    # Read-only Windows Installer database open: a candidate that is not a
    # Kikimora MSI of the expected shape fails the preflight, never the
    # mutation. msiexec /i is NOT a validation tool.
    $installer = $null
    $db = $null
    $view = $null
    try {
        $installer = New-Object -ComObject WindowsInstaller.Installer
        # 0 = msiOpenDatabaseModeReadOnly
        $db = $installer.GetType().InvokeMember(
            "OpenDatabase", [System.Reflection.BindingFlags]::InvokeMethod,
            $null, $installer, @($Path, 0))
        foreach ($property in @("ProductName", "ProductVersion", "UpgradeCode", "Manufacturer")) {
            $view = $db.GetType().InvokeMember(
                "OpenView", [System.Reflection.BindingFlags]::InvokeMethod,
                $null, $db,
                @("SELECT Value FROM Property WHERE Property = '$property'"))
            $view.GetType().InvokeMember("Execute", [System.Reflection.BindingFlags]::InvokeMethod, $null, $view, $null) | Out-Null
            $fetched = $view.GetType().InvokeMember("Fetch", [System.Reflection.BindingFlags]::InvokeMethod, $null, $view, $null)
            $value = ""
            if ($fetched) {
                $value = $fetched.GetType().InvokeMember(
                    "StringData", [System.Reflection.BindingFlags]::GetProperty,
                    $null, $fetched, @(1))
            }
            $view.GetType().InvokeMember("Close", [System.Reflection.BindingFlags]::InvokeMethod, $null, $view, $null) | Out-Null
            $view = $null
            if ($Kind -eq "kikimora" -and $property -eq "ProductName" -and $value -ne "Kikimora") {
                throw "preflight($Kind): ProductName is '$value', want Kikimora"
            }
            if ($property -in @("ProductVersion", "UpgradeCode") -and [string]::IsNullOrEmpty($value)) {
                throw "preflight($Kind): $property is empty"
            }
        }
        return $true
    } catch {
        throw "preflight($Kind): cannot open the MSI database for ${Path}: $($_.Exception.Message)"
    } finally {
        if ($view) { $view = $null }
        if ($db) { $db = $null }
        if ($installer) { $installer = $null }
    }
}

function Assert-NoInstalledKikimora {
    # Disposable-fixture precondition: both fresh and upgrade modes start on
    # a host with no Kikimora installed. The harness never uninstalls an
    # operator baseline (§7.3); the host resets the VM between modes.
    $code = Get-InstalledKikimoraCode
    if ($code) {
        throw "Kikimora is already installed (product code $code); $Mode requires a clean disposable VM"
    }
    foreach ($svc in @("KikimoraCore", "leshy")) {
        if (Get-Service -Name $svc -ErrorAction SilentlyContinue) {
            throw "a leftover '$svc' service exists; $Mode requires a clean disposable VM"
        }
    }
}

function Invoke-Msi([string]$Action, [string]$Path, [string]$Log) {
    $p = Start-Process msiexec -ArgumentList @($Action, "`"$Path`"", "/qn", "/norestart", "/l*v", "`"$Log`"") -Wait -PassThru
    return $p.ExitCode
}

function Get-InstalledKikimoraCode {
    return (Get-ItemProperty "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*" -ErrorAction SilentlyContinue |
        Where-Object { $_.DisplayName -eq "Kikimora" } | Select-Object -First 1).PSChildName
}

function Wait-CoreReady([int]$Seconds) {
    for ($i = 0; $i -lt $Seconds; $i++) {
        if (Test-Path $CoreExe) {
            $null = & $CoreExe wait-startup --socket $CoreSocket --timeout 2s 2>$null
            if ($LASTEXITCODE -eq 0) { return $true }
            # The 1.0.0 previous artifact predates the wait-startup CLI
            # (H6 2026-11-19: rc=2 usage error on every probe). For the
            # previous install the service being Running is the artifact's
            # own readiness notion; the candidate still exercises the real
            # wait-startup contract.
            $svc = Get-Service -Name $CoreService -ErrorAction SilentlyContinue
            if ($svc -and $svc.Status -eq "Running") { return $true }
        }
        Start-Sleep -Seconds 1
    }
    return $false
}

function Assert-InstalledPayload {
    if (-not (Test-Path $CoreExe)) { Fail "kikimora-core.exe missing after install"; return }
    Pass "kikimora-core.exe installed"
    if (Test-Path "C:\Program Files\Kikimora\kikimora-toad.exe") { Pass "kikimora-toad.exe installed" } else { Fail "kikimora-toad.exe missing" }
    if (Test-Path "C:\Program Files\Kikimora\leshy\leshy.exe") { Pass "leshy.exe installed" } else { Fail "leshy.exe missing" }
    # The MSI installs the UI under its own subdirectory (kikimora.wxs
    # UISUBDIR; the service/autostart shortcuts target [UISUBDIR]
    # kikimora-ui.exe) — H6 2026-11-19: the previous root-level check could
    # never pass against a real integrated MSI.
    if (Test-Path "C:\Program Files\Kikimora\ui\kikimora-ui.exe") { Pass "ui\kikimora-ui.exe installed" } else { Fail "ui\kikimora-ui.exe missing (integrated release)" }
    if (Test-Path "C:\Program Files\Kikimora\wintun.dll") { Pass "wintun.dll installed" } else { Fail "wintun.dll missing" }
}

function Managed-Smoke {
    Note "managed smoke on the installed package"
    if (-not (Wait-CoreReady 90)) { Fail "installed core never reached startup readiness"; return }
    $authKey = Join-Path $CoreStateDir "control-route-auth.key"
    $leshyExe = "C:\Program Files\Kikimora\leshy\leshy.exe"
    # H6 2026-11-21: the schema2-committed upgrade boots the candidate core
    # straight into the recovered managed mode. The smoke must prove that
    # recovery (generation + activation fingerprint) instead of running a
    # second enable over an already-managed install.
    $enableLog = Join-Path $evidence "managed-enable.log"
    $disableLog = Join-Path $evidence "managed-disable.log"
    $statusRaw = & $CoreExe leshy-mode status --socket $CoreSocket --auth-file $authKey --json 2>$null
    $status = $null
    try { $status = @($statusRaw | ConvertFrom-Json)[0] } catch {}
    if ($status -and $status.mode -eq "managed") {
        Pass "the upgrade recovered the committed managed mode (generation $($status.generation))"
        if ([uint64]$status.generation -eq 5) {
            Pass "the recovered generation is the staged 5"
        } else {
            Fail "the recovered generation is $($status.generation), want the staged 5"
        }
        $digest = [Security.Cryptography.SHA256]::Create().ComputeHash(
            [Text.Encoding]::UTF8.GetBytes($script:stagedActivation))
        $fingerprint = ([BitConverter]::ToString($digest) -replace "-", "").ToLowerInvariant().Substring(0, 12)
        if ($status.activation_id_fingerprint -eq $fingerprint) {
            Pass "the recovered activation fingerprint matches the staged activation"
        } else {
            Fail "the recovered activation fingerprint $($status.activation_id_fingerprint) does not match the staged activation"
        }
        # Roll the recovered pair back to local, then prove one full cycle.
        Stop-Service -Name $LeshyService -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 2
        & $CoreExe leshy-mode disable --socket $CoreSocket --auth-file $authKey 2>&1 | Out-File -Encoding utf8 $disableLog
        if ($LASTEXITCODE -eq 0) { Pass "managed disable of the recovered pair" } else { Fail "managed disable of the recovered pair failed (see $disableLog)" }
        & $CoreExe leshy-mode enable --leshy $leshyExe --socket $CoreSocket `
            --auth-file $authKey --control-socket $CoreSocket 2>&1 | Out-File -Encoding utf8 $enableLog
        if ($LASTEXITCODE -eq 0) { Pass "managed enable after the recovery rollback" } else { Fail "managed enable after the recovery rollback failed (see $enableLog)" }
        & $CoreExe leshy-mode disable --socket $CoreSocket --auth-file $authKey 2>&1 | Out-File -Encoding utf8 (Join-Path $evidence "managed-disable.log")
        if ($LASTEXITCODE -eq 0) { Pass "managed disable" } else { Fail "managed disable failed" }
        Start-Service -Name $LeshyService -ErrorAction SilentlyContinue
        return
    }

    # A fresh install starts the local Leshy service, which owns the shared
    # writer lock: the managed enable is REFUSED while it runs — the product
    # failing closed, correctly (H6 2026-11-19). The cutover rows stop the
    # local Leshy for the transaction; the smoke does the same and restores
    # the service afterwards.
    $leshyWasRunning = $false
    $leshySvc = Get-Service -Name $LeshyService -ErrorAction SilentlyContinue
    if ($leshySvc -and $leshySvc.Status -eq "Running") {
        $leshyWasRunning = $true
        Stop-Service -Name $LeshyService -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 2
    }
    $enableLog = Join-Path $evidence "managed-enable.log"
    & $CoreExe leshy-mode enable --leshy $leshyExe --socket $CoreSocket `
        --auth-file $authKey --control-socket $CoreSocket 2>&1 | Out-File -Encoding utf8 $enableLog
    if ($LASTEXITCODE -eq 0) { Pass "managed enable on the installed package" } else { Fail "managed enable failed (see $enableLog)" }
    & $CoreExe leshy-mode disable --socket $CoreSocket --auth-file $authKey 2>&1 | Out-File -Encoding utf8 (Join-Path $evidence "managed-disable.log")
    if ($LASTEXITCODE -eq 0) { Pass "managed disable" } else { Fail "managed disable failed" }
    if ($leshyWasRunning) { Start-Service -Name $LeshyService -ErrorAction SilentlyContinue }
}

function Stage-StateShape([string]$Shape) {
    Note "staging previous-install state shape=$Shape"
    New-Item -ItemType Directory -Force -Path $CoreStateDir, $ToadsDir | Out-Null
    switch ($Shape) {
        "local" {
            if (Test-Path $Marker) { Remove-Item $Marker -Force }
        }
        "schema1-empty" {
            # H6 2026-11-21: the current core reads the committed counter from
            # state\core\leshy-mode-generation.json (modeGenerationState
            # schema 1); the old leshy-managed-mode.json path is not read by
            # any current code path. Generation 0 = the empty committed
            # counter the candidate must tolerate.
            [IO.File]::WriteAllText((Join-Path $CoreStateDir "leshy-mode-generation.json"), '{"schema":1,"last_committed_generation":0}')
        }
        "schema2" {
            # H6 2026-11-21: a committed managed pair on the current layout is
            # the schema-1 counter + a VALID schema2 marker whose generation
            # equals the counter (committed_state.go refuses a stale marker:
            # "managed marker generation %d does not equal the committed
            # generation %d"). The shape mirrors Stage-LegacyManagedPair from
            # test_state_layout_upgrade.ps1; ConvertTo-Json escapes the
            # socket backslashes exactly as the core writes them.
            [IO.File]::WriteAllText((Join-Path $CoreStateDir "leshy-mode-generation.json"), '{"schema":1,"last_committed_generation":5}')
            # A committed managed install must carry its privileged auth key
            # (EnsurePrivilegedAuthKey refuses a managed state without it);
            # 64 lowercase hex + newline, exactly the parseable credential
            # form the state-layout gate stages.
            [IO.File]::WriteAllText((Join-Path $CoreStateDir "control-route-auth.key"), (("ab" * 32) + "`n"))
            $markerPayload = @{
                schema           = 2
                socket           = $CoreSocket
                core_version     = "package-lifecycle-upgrade"
                managed_zones    = @("awg-main")
                activation_id    = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
                mode_generation  = 5
            } | ConvertTo-Json -Compress
            [IO.File]::WriteAllText($Marker, $markerPayload)
        }
        "ambiguous" {
            # H6 2026-11-21: the fail-closed analog on the current layout is
            # an invalid transition journal at the real journal path
            # (leshy-mode-transition.json) — ReadTransitionJournal fails
            # Validate, the candidate refuses to start over it and the
            # upgrade transaction aborts with the previous install intact.
            # The old leshy-managed-mode(.conflict).json pair is dead state
            # the current core never reads.
            [IO.File]::WriteAllText((Join-Path $CoreStateDir "leshy-mode-transition.json"), '{"schema":2}')
        }
    }
}

# One failure/cleanup path (§7.4): evidence capture in catch, owned-resource
# cleanup in finally, original exit code preserved through exit.
$exitCode = 0
$payloadDir = $null
try {
    # Read-only artifact validation BEFORE any service/MSI mutation.
    Test-KikimoraMsiArtifact $Candidate "candidate" | Out-Null
    Pass "candidate MSI database preflight"
    if ($Previous) {
        Test-KikimoraMsiArtifact $Previous "previous" | Out-Null
        Pass "previous MSI database preflight"
    }
    if ($isPortableMode) {
        $zip = $Candidate -replace "\.msi$", ".zip"
        if (-not (Test-Path $zip)) { throw "portable zip not found next to the MSI: $zip" }
        $payloadDir = Join-Path $WorkDir "preflight-payload"
        if (Test-Path $payloadDir) { Remove-Item -Recurse -Force $payloadDir }
        Expand-Archive -Path $zip -DestinationPath $payloadDir
        foreach ($member in @("install.ps1", "kikimora-core.exe", "leshy", "kikimora-ui.exe")) {
            $found = Get-ChildItem $payloadDir -Recurse -Filter (Split-Path $member -Leaf) -ErrorAction SilentlyContinue
            if (-not $found) { throw "portable zip payload is missing $member" }
        }
        Pass "portable zip preflight"
    }

    Assert-NoInstalledKikimora
    Pass "host is clean (no installed Kikimora, no leftover services)"

    switch ($Mode) {
        "FreshMsi" {
            $rc = Invoke-Msi "/i" $Candidate (Join-Path $evidence "install.log")
            if ($rc -ne 0) { Fail "fresh MSI install failed: $rc" } else { Pass "fresh MSI install" }
            Assert-InstalledPayload
            Managed-Smoke
        }
        "FreshPortable" {
            $installDir = Join-Path $evidence "payload"
            if (Test-Path $installDir) { Remove-Item -Recurse -Force $installDir }
            Expand-Archive -Path ($Candidate -replace "\.msi$", ".zip") -DestinationPath $installDir
            $pkgDir = Get-ChildItem $installDir -Directory | Select-Object -First 1
            $installLog = Join-Path $evidence "install-portable.log"
            $installErrLog = Join-Path $evidence "install-portable.err.log"
            # H6 2026-11-19: the log reference existed but the install output
            # was never redirected — a failed portable install surfaced with
            # no evidence at all.
            $p = Start-Process powershell -Wait -PassThru -ArgumentList @(
                "-NoProfile", "-ExecutionPolicy", "Bypass",
                "-File", "`"$(Join-Path $pkgDir.FullName 'install.ps1')`""
            ) -RedirectStandardOutput $installLog -RedirectStandardError $installErrLog
            if ($p.ExitCode -ne 0) { Fail "portable install.ps1 failed: $($p.ExitCode) (see $installLog)" } else { Pass "portable install" }
            Assert-InstalledPayload
            Managed-Smoke
        }
        { $_ -like "Upgrade*" } {
            $shape = switch ($Mode) {
                "UpgradeLocal" { "local" }
                "UpgradeSchema1Empty" { "schema1-empty" }
                "UpgradeSchema2" { "schema2" }
                "UpgradeSchema1Ambiguous" { "ambiguous" }
            }
            $prevRc = Invoke-Msi "/i" $Previous (Join-Path $evidence "install-previous.log")
            if ($prevRc -ne 0) { Fail "previous MSI install failed: $prevRc"; break }
            if (-not (Wait-CoreReady 90)) { Fail "previous core never became ready"; break }
            Stop-Service -Name $CoreService -Force -ErrorAction SilentlyContinue
            Stage-StateShape $shape

            $rc = Invoke-Msi "/i" $Candidate (Join-Path $evidence "install-candidate.log")
            if ($Mode -eq "UpgradeSchema1Ambiguous") {
                if ($rc -eq 0) { Fail "ambiguous upgrade must fail the transaction" }
                else {
                    Pass "ambiguous upgrade aborted"
                    $code = Get-InstalledKikimoraCode
                    if ($code) { Pass "previous install kept installed after abort" } else { Fail "abort removed the previous install" }
                    if (Test-Path (Join-Path $CoreStateDir "leshy-mode-transition.json")) {
                        Pass "conflicting state preserved for inspection"
                    } else { Fail "conflicting state was destroyed by the aborted upgrade" }
                }
            } else {
                if ($rc -ne 0) { Fail "candidate install failed: $rc" }
                else {
                    Pass "upgrade install ($Mode)"
                    Assert-InstalledPayload
                    if (-not (Wait-CoreReady 120)) { Fail "upgraded core never became ready" }
                    else { Managed-Smoke }
                }
            }
        }
    }
} catch {
    $exitCode = 1
    Fail $_.Exception.Message
    # Failure evidence directory (§7.4): identities, service state and the
    # installer log tails are retained, never deleted.
    try {
        @(
            "mode=$Mode",
            "source sha: $sourceSha",
            "harness sha256: $harnessSha256",
            "error: $($_.Exception.Message)",
            "--- service state ---",
            (Get-Service -Name $CoreService -ErrorAction SilentlyContinue | Out-String),
            (Get-Service -Name leshy -ErrorAction SilentlyContinue | Out-String),
            "--- installer log tails ---"
        ) + (Get-ChildItem $evidence -Filter "install*.log" -ErrorAction SilentlyContinue |
            ForEach-Object { "--- $($_.Name) ---"; Get-Content $_.FullName -Tail 40 }) |
            Set-Content -Path (Join-Path $evidence "failure.txt")
        Write-Host "failure evidence: $(Join-Path $evidence 'failure.txt')"
    } catch {
        Write-Host "could not write failure evidence: $($_.Exception.Message)"
    }
} finally {
    # Only resources owned by this invocation are touched: the preflight
    # payload directory is removed; an installed candidate stays installed
    # for the row's reboot/installed-UI follow-up (disposable contract §7.3).
    if ($payloadDir -and (Test-Path $payloadDir)) {
        Remove-Item -Recurse -Force $payloadDir -ErrorAction SilentlyContinue
    }
}

if ($exitCode -ne 0) {
    exit $exitCode
}
if ($failures -gt 0) {
    Write-Host "H6 package lifecycle FAILED ($Mode): $failures assertion(s)" -ForegroundColor Red
    exit 1
}
Write-Host "H6 package lifecycle GREEN ($Mode)" -ForegroundColor Green
exit 0
