# SPDX-License-Identifier: MIT
#
# install.ps1 — elevated Kikimora Windows installer (portable package).
#
# Installs the staged binaries into the install root, creates the crash-safe
# ProgramData layout with restrictive ACLs, registers the KikimoraCore service
# (automatic start, staged restart recovery) and, when the package carries the
# Leshy runtime, performs the one-writer schema2 cutover. Fresh installs never
# write VPN role desired state: the service starts with all roles disabled.
#
# Run from an elevated prompt, inside the extracted package directory:
#   powershell -ExecutionPolicy Bypass -File install.ps1 [-Purge]
#
# The order below is the 08D1 §11.2 contract:
#   copy payload -> create/harden ProgramData -> install core -> wait ready ->
#   install Leshy (SCM dependency on KikimoraCore) -> start Leshy local ->
#   preflight -> graceful stop -> writer-lock proof -> schema2 Enable ->
#   start managed Leshy -> verify generation/fingerprint.

param (
    [switch]$Purge
)

$ErrorActionPreference = "Stop"
$identity = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $identity.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "install.ps1 must run from an elevated prompt"
}

$InstallRoot = Join-Path $Env:ProgramFiles "Kikimora"
$DataRoot = Join-Path $Env:ProgramData "Kikimora"
$CoreSocket = Join-Path $DataRoot "core.sock"
$AuthKey = Join-Path $DataRoot "state\core\control-route-auth.key"
$LeshyConfig = "C:\ProgramData\leshy\config.toml"
$Marker = Join-Path $DataRoot "toads\leshy-managed.json"
$WriterLock = Join-Path $DataRoot "state\leshy-writer.lock"
$coreExe = Join-Path $InstallRoot "kikimora-core.exe"
$leshyBin = Join-Path $InstallRoot "leshy\leshy.exe"

Write-Host "Kikimora Windows installer"
Write-Host "  install root: $InstallRoot"
Write-Host "  data root:    $DataRoot"

if (-not (Test-Path (Join-Path $PSScriptRoot "kikimora-core.exe"))) {
    throw "run install.ps1 from the extracted package directory (kikimora-core.exe not found next to it)"
}

# ─────────────────────────────────────────────────────────────────────────────
# Cutover helpers. Every one of them is fail-closed: an unprovable state is an
# installer failure, never a warning that lets two writers overlap.

function Get-LeshyModeStatus {
    $json = & $coreExe leshy-mode status --socket $CoreSocket --auth-file $AuthKey --json 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "kikimora-core leshy-mode status failed: $($json | Out-String)"
    }
    # A local install omits transition/activation fields (omitempty); callers
    # must treat a missing property as "settled", not as a mismatch.
    return (($json -join "`n") | ConvertFrom-Json)
}

function Test-TransitionSettled([object]$Status) {
    if (-not $Status) { return $false }
    if ($Status.transition -and $Status.transition -ne "none") { return $false }
    return $true
}

function Test-WriterLockFree {
    if (-not ("KikimoraPackaging.WriterLockProbe" -as [type])) {
        Add-Type -TypeDefinition @"
using System;
using System.IO;
using System.Runtime.InteropServices;

namespace KikimoraPackaging {
    public static class WriterLockProbe {
        [StructLayout(LayoutKind.Sequential)]
        private struct OVERLAPPED {
            public IntPtr Internal;
            public IntPtr InternalHigh;
            public uint Offset;
            public uint OffsetHigh;
            public IntPtr hEvent;
        }

        [DllImport("kernel32.dll", SetLastError = true)]
        private static extern bool LockFileEx(IntPtr hFile, uint flags, uint reserved,
            uint low, uint high, ref OVERLAPPED overlapped);

        [DllImport("kernel32.dll", SetLastError = true)]
        private static extern bool UnlockFileEx(IntPtr hFile, uint reserved,
            uint low, uint high, ref OVERLAPPED overlapped);

        // Returns 0 when the byte-range fence is free, 33 (ERROR_LOCK_VIOLATION)
        // when a live local/managed Leshy owns it, and any other Win32 error.
        public static int TryExclusive(string path) {
            FileStream stream;
            try {
                stream = new FileStream(path, FileMode.OpenOrCreate, FileAccess.ReadWrite,
                    FileShare.ReadWrite | FileShare.Delete);
            } catch (Exception) {
                return Marshal.GetLastWin32Error();
            }
            try {
                OVERLAPPED overlapped = new OVERLAPPED();
                // LOCKFILE_FAIL_IMMEDIATELY | LOCKFILE_EXCLUSIVE_LOCK
                if (LockFileEx(stream.SafeFileHandle.DangerousGetHandle(), 3, 0, 1, 0, ref overlapped)) {
                    UnlockFileEx(stream.SafeFileHandle.DangerousGetHandle(), 0, 1, 0, ref overlapped);
                    return 0;
                }
                return Marshal.GetLastWin32Error();
            } finally {
                stream.Dispose();
            }
        }
    }
}
"@
    }
    $rc = [KikimoraPackaging.WriterLockProbe]::TryExclusive($WriterLock)
    switch ($rc) {
        0 { return $true }
        33 { return $false }
        default { throw "the Leshy writer-lock probe failed with Win32 error $rc on $WriterLock" }
    }
}

function Wait-CoreReady {
    & $coreExe wait-startup --socket $CoreSocket --timeout 60s
    if ($LASTEXITCODE -ne 0) {
        throw "KikimoraCore did not report startup readiness"
    }
}

function Stop-LeshyGracefully {
    $svc = Get-Service -Name "leshy" -ErrorAction SilentlyContinue
    if (-not $svc -or $svc.Status -eq "Stopped") { return }
    Write-Host "  stopping leshy gracefully"
    Stop-Service -Name "leshy" -Force
    (Get-Service -Name "leshy").WaitForStatus("Stopped", [TimeSpan]::FromSeconds(60))
}

function Start-LeshyService {
    $svc = Get-Service -Name "leshy" -ErrorAction SilentlyContinue
    if (-not $svc) { throw "the leshy service is not installed" }
    if ($svc.Status -ne "Running") {
        Start-Service -Name "leshy"
        (Get-Service -Name "leshy").WaitForStatus("Running", [TimeSpan]::FromSeconds(60))
    }
}

function Assert-LeshyDependency {
    $svc = Get-Service -Name "leshy" -ErrorAction SilentlyContinue
    if (-not $svc) { throw "the leshy service is not installed" }
    if (-not ($svc.ServicesDependedOn.Name -contains "KikimoraCore")) {
        throw "the leshy service does not depend on KikimoraCore; the SCM may start it against a dead core"
    }
}

function Wait-RolesDrained([int]$TimeoutSeconds = 60) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $json = & $coreExe status --socket $CoreSocket --json 2>&1
        if ($LASTEXITCODE -eq 0) {
            try {
                $snapshot = (($json -join "`n") | ConvertFrom-Json)
                $drained = $true
                foreach ($role in @($snapshot.roles)) {
                    if ($role.state -notin @("Stopped", "Disconnected", "Failed", "")) { $drained = $false }
                    if ($role.parking -and ($role.parking.active -or $role.parking.count -gt 0)) { $drained = $false }
                }
                if ($drained) { return $true }
            } catch {
                # A malformed snapshot is not an emptiness proof.
            }
        }
        Start-Sleep -Milliseconds 500
    }
    return $false
}

function Assert-ManagedIdentity([object]$Status) {
    if ($Status.mode -ne "managed") {
        throw "the managed pair did not converge (mode=$($Status.mode))"
    }
    if (-not (Test-TransitionSettled $Status)) {
        throw "the managed pair still has a transition in flight ($($Status.transition))"
    }
    if (-not $Status.activation_id_fingerprint) {
        throw "managed mode is missing its activation fingerprint"
    }
    if ([uint64]$Status.generation -lt 1) {
        throw "managed mode reports generation $($Status.generation); the committed generation must be positive"
    }
    $configText = Get-Content -Path $LeshyConfig -Raw
    $activation = [regex]::Match($configText, '(?m)^kikimora_activation_id\s*=\s*"([0-9a-f]{32})"').Groups[1].Value
    $generation = [regex]::Match($configText, '(?m)^kikimora_mode_generation\s*=\s*(\d+)').Groups[1].Value
    $socket = [regex]::Match($configText, '(?m)^kikimora_socket\s*=\s*"([^"]+)"').Groups[1].Value
    if (-not $activation) { throw "the generated Leshy config has no managed activation id" }
    $digest = [Security.Cryptography.SHA256]::Create().ComputeHash([Text.Encoding]::UTF8.GetBytes($activation))
    $fingerprint = ([BitConverter]::ToString($digest) -replace "-", "").ToLowerInvariant().Substring(0, 12)
    if ($fingerprint -ne $Status.activation_id_fingerprint) {
        throw "the config activation ($fingerprint) does not match the committed fingerprint ($($Status.activation_id_fingerprint))"
    }
    if ([uint64]$generation -ne [uint64]$Status.generation) {
        throw "the config generation ($generation) does not match the committed generation ($($Status.generation))"
    }
    if ($socket -ne $CoreSocket) {
        throw "the generated Leshy config points at '$socket', not the core socket '$CoreSocket'"
    }
}

# 08D1 §11.4: a failed cutover never leaves an unproven owner behind. Stop any
# Leshy instance, inspect the committed mode, drain and disable when the Enable
# transaction already committed, then restore local mode. If any proof fails
# the installer stays fail-closed and refuses to start local Leshy.
function Invoke-CutoverRollback([string]$Reason) {
    Write-Warning "managed cutover failed: $Reason"
    try { Stop-LeshyGracefully } catch { Write-Warning "rollback: stopping leshy: $_" }

    $status = $null
    try { $status = Get-LeshyModeStatus } catch { Write-Warning "rollback: mode status: $_" }

    if ($status -and $status.mode -eq "managed") {
        Write-Host "  rollback: the Enable committed managed state; draining selected ownership"
        & $coreExe disconnect-all --socket $CoreSocket | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw "cutover failed ($Reason) and DisconnectAll failed; the install stays fail-closed. Inspect the core log."
        }
        if (-not (Wait-RolesDrained 60)) {
            throw "cutover failed ($Reason) and selected ownership could not be proven empty; the install stays fail-closed (marker $Marker)."
        }
        & $coreExe leshy-mode disable --socket $CoreSocket --auth-file $AuthKey
        if ($LASTEXITCODE -ne 0) {
            throw "cutover failed ($Reason) and the managed rollback could not disable managed mode; the install stays fail-closed (marker $Marker)."
        }
    }

    if (Test-Path $Marker) {
        throw "cutover failed ($Reason) and the managed marker is still present; refusing to start local Leshy (fail-closed)."
    }
    $final = Get-LeshyModeStatus
    if ($final.mode -ne "local") {
        throw "cutover failed ($Reason) and the rollback did not reach local mode (mode=$($final.mode)); refusing to start local Leshy."
    }
    Start-LeshyService
    throw "the managed cutover failed ($Reason); the rollback restored local Leshy (mode=local, generation=$($final.generation))."
}

# ─────────────────────────────────────────────────────────────────────────────
# Upgrade preflight: release the old services before their binaries are
# replaced. A managed pair is stopped Leshy-first, then the core; the resulting
# managed state is recovered after the new payload starts (08D1 §11.5).

$priorStatus = $null
if (Get-Service -Name "leshy" -ErrorAction SilentlyContinue) {
    Stop-LeshyGracefully
}
if (Get-Service -Name "KikimoraCore" -ErrorAction SilentlyContinue) {
    if (Test-Path $AuthKey) {
        try { $priorStatus = Get-LeshyModeStatus } catch { Write-Warning "could not read the pre-upgrade mode: $_" }
    }
    & $coreExe service stop
    if ($LASTEXITCODE -ne 0) { throw "kikimora-core service stop failed" }
    (Get-Service -Name "KikimoraCore").WaitForStatus("Stopped", [TimeSpan]::FromSeconds(90))
}

# Step 1: copy payload.
New-Item -ItemType Directory -Force -Path $InstallRoot | Out-Null
Copy-Item (Join-Path $PSScriptRoot "kikimora-core.exe") $InstallRoot -Force
Copy-Item (Join-Path $PSScriptRoot "kikimora-toad.exe") $InstallRoot -Force
# wintun.dll must sit next to kikimora-toad.exe: the driver loads it with
# LOAD_LIBRARY_SEARCH_APPLICATION_DIR only, so without it no Wintun adapter
# can ever be created and every AWG2 role fails.
if (Test-Path (Join-Path $PSScriptRoot "wintun.dll")) {
    Copy-Item (Join-Path $PSScriptRoot "wintun.dll") $InstallRoot -Force
} else {
    Write-Warning "wintun.dll is not in the package: AWG2 roles will not start"
}
# The UI keeps its own directory with the complete Qt runtime (ui/): it must
# not share a directory with the core, whose GnuTLS/curl/ffi DLLs collide
# with the UI's.
$uiSource = Join-Path $PSScriptRoot "ui"
if (Test-Path (Join-Path $PSScriptRoot "kikimora-ui.exe")) {
    $uiDest = Join-Path $InstallRoot "ui"
    New-Item -ItemType Directory -Force -Path $uiDest | Out-Null
    Copy-Item (Join-Path $PSScriptRoot "kikimora-ui.exe") $uiDest -Force
    if (Test-Path $uiSource) {
        Copy-Item (Join-Path $uiSource "*") $uiDest -Recurse -Force
    } else {
        Write-Warning "kikimora-ui.exe staged without the ui/ payload: the UI will not start"
    }
}
# Optional Leshy steering runtime, carried when the package is staged with
# -LeshyDir. The service is registered below through the Leshy installer's own
# dependency contract, never a bare `sc create`.
$leshySource = Join-Path $PSScriptRoot "leshy"
$leshyPresent = Test-Path (Join-Path $leshySource "leshy.exe")
if ($leshyPresent) {
    $leshyDest = Join-Path $InstallRoot "leshy"
    New-Item -ItemType Directory -Force -Path $leshyDest | Out-Null
    Copy-Item (Join-Path $leshySource "*") $leshyDest -Recurse -Force
}
Write-Host "  binaries installed"

# Step 2: create and harden ProgramData.
foreach ($dir in @(
    $DataRoot,
    (Join-Path $DataRoot "state"),
    (Join-Path $DataRoot "state\core"),
    (Join-Path $DataRoot "toads"),
    (Join-Path $DataRoot "logs"),
    (Join-Path $DataRoot "leshy"),
    (Join-Path $DataRoot "leshy\vpn")
)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}
# ProgramData inherits the machine defaults (BUILTIN\Users keep Read+Execute
# and can create files in subdirectories). Role TOMLs hold VPN private keys
# and endpoint_policy commands execute as SYSTEM: state, toads and logs must
# be SYSTEM/Administrators only. state\core is hardened separately because an
# already-created child keeps the inherited ACEs of the parent.
foreach ($hardened in @(
    (Join-Path $DataRoot "state"),
    (Join-Path $DataRoot "state\core"),
    (Join-Path $DataRoot "toads"),
    (Join-Path $DataRoot "logs"),
    (Join-Path $DataRoot "leshy")
)) {
    # Raw SIDs: on localized Windows (ru-RU) the account NAMES do not
    # resolve - "Administrators" -> "No mapping between account names and
    # security IDs". S-1-5-18 = SYSTEM, S-1-5-32-544 = Administrators.
    & icacls $hardened /inheritance:r /grant "*S-1-5-18:(OI)(CI)F" /grant "*S-1-5-32-544:(OI)(CI)F" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "icacls failed to harden $hardened" }
}
Write-Host "  ProgramData layout ready (state/state-core/toads/logs/leshy ACLs hardened)"

# Step 3: install KikimoraCore first. The service registers itself with
# automatic start and staged restart recovery, and starts immediately; roles
# stay disabled on fresh installs.
if (-not (Get-Service -Name "KikimoraCore" -ErrorAction SilentlyContinue)) {
    & $coreExe service install
    if ($LASTEXITCODE -ne 0) { throw "kikimora-core service install failed" }
} else {
    & $coreExe service start
    if ($LASTEXITCODE -ne 0) { throw "kikimora-core service start failed" }
}
Write-Host "  KikimoraCore service installed and started"

# Step 4: wait for real application readiness (socket bound, startup restored).
Wait-CoreReady

if ($leshyPresent) {
    # Step 5: install Leshy second and assert the SCM dependency natively.
    if (-not (Get-Service -Name "leshy" -ErrorAction SilentlyContinue)) {
        & $leshyBin service install --config $LeshyConfig --name leshy --depend-on KikimoraCore
        if ($LASTEXITCODE -ne 0) { throw "the Leshy service installer failed (traffic steering will not run)" }
    } elseif (-not ((Get-Service -Name "leshy").ServicesDependedOn.Name -contains "KikimoraCore")) {
        # Repair an older install that predates the dependency contract. This
        # only edits the existing service; it never creates one ad hoc.
        Write-Host "  repairing the missing leshy -> KikimoraCore dependency"
        & sc.exe config leshy depend= KikimoraCore | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "sc.exe config leshy depend= KikimoraCore failed" }
    }
    Assert-LeshyDependency
    Write-Host "  leshy service depends on KikimoraCore"

    if ($priorStatus -and $priorStatus.mode -eq "managed") {
        # 08D1 §11.5 existing schema2 managed candidate: the core recovered the
        # same authority from disk; only start Leshy and prove the identity did
        # not change. Never run a fresh Enable over a committed marker.
        Start-LeshyService
        $recovered = Get-LeshyModeStatus
        Assert-ManagedIdentity $recovered
        if ([uint64]$recovered.generation -ne [uint64]$priorStatus.generation -or
            $recovered.activation_id_fingerprint -ne $priorStatus.activation_id_fingerprint) {
            throw "the upgrade invented a new activation (was generation=$($priorStatus.generation)/$($priorStatus.activation_id_fingerprint), now generation=$($recovered.generation)/$($recovered.activation_id_fingerprint))"
        }
        Write-Host "  managed pair recovered at generation $($recovered.generation)"
    } else {
        # Step 6: start Leshy local.
        Start-LeshyService

        # Step 7: preflight the exact installed Leshy.
        $caps = & $leshyBin capabilities --json 2>&1
        if ($LASTEXITCODE -ne 0) { throw "the installed Leshy capabilities command failed: $($caps | Out-String)" }
        $capsText = ($caps | Out-String)
        foreach ($required in @("af_unix_control", "managed_selected_routes_v2", "privileged_control_auth_v1")) {
            if ($capsText -notmatch [regex]::Escape($required)) {
                Invoke-CutoverRollback "the installed Leshy does not advertise $required"
            }
        }

        $status = Get-LeshyModeStatus
        if ($status.mode -ne "local") {
            Invoke-CutoverRollback "the installation was expected in local mode before the cutover, found '$($status.mode)'"
        }

        # Step 8: stop Leshy gracefully.
        Stop-LeshyGracefully

        # Step 9: prove the writer lock is free before handing it to the
        # transaction (a stale local Leshy or a crash leftover must be visible).
        $deadline = (Get-Date).AddSeconds(30)
        while (-not (Test-WriterLockFree)) {
            if ((Get-Date) -ge $deadline) {
                Invoke-CutoverRollback "the Leshy writer lock stayed busy after the graceful stop"
            }
            Start-Sleep -Milliseconds 200
        }
        Write-Host "  Leshy writer lock is free"

        # Step 10: enable schema2 managed mode through the core transaction.
        $enableOut = & $coreExe leshy-mode enable --leshy $leshyBin --socket $CoreSocket --auth-file $AuthKey --control-socket $CoreSocket 2>&1
        if ($LASTEXITCODE -ne 0) {
            Invoke-CutoverRollback "the schema2 enable transaction failed: $($enableOut | Out-String)"
        }

        # Step 11: start Leshy on the generated managed configuration.
        try {
            Start-LeshyService
        } catch {
            Invoke-CutoverRollback "managed Leshy did not start: $_"
        }

        # Step 12: verify generation/fingerprint against the generated config.
        try {
            $final = Get-LeshyModeStatus
            Assert-ManagedIdentity $final
        } catch {
            Invoke-CutoverRollback "the managed pair did not converge: $_"
        }
        Write-Host "  leshy steering runtime installed; managed pair active at generation $($final.generation)"
    }
}
Write-Host "Kikimora installation complete."

if ($Purge) {
    Write-Host "  (-Purge requested) stale state under $DataRoot is NOT removed automatically;"
    Write-Host "  run uninstall.ps1 -Purge for explicit purge semantics."
}
