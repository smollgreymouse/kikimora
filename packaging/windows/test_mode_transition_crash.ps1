# SPDX-License-Identifier: MIT
#
# test_mode_transition_crash.ps1 — the versioned native D10 harness for
# win10 (08D1 post-Phase-D audit D10R-2): the real failpoint-tagged core
# through all nine enable and nine disable crash boundaries, then the eight
# 10.5 negative cases as native process proofs, over the real ProgramData
# filesystem and the real LockFileEx writer fence.
#
# The enable/disable transactions go through the real
# `kikimora-core leshy-mode enable` CLI, which runs the real Leshy
# capabilities preflight — the capability array is never injected.
#
# Usage (elevated):
#   powershell -ExecutionPolicy Bypass -File test_mode_transition_crash.ps1 `
#       -Core C:\path\kikimora-core.exe -Leshy C:\path\leshy.exe `
#       -RouteProbe C:\path\routeprobe.exe -WorkDir C:\path\work
# The system leshy service is stopped while the harness runs (the shared
# writer lock) and restored on exit.
param(
    [Parameter(Mandatory = $true)][string]$Core,
    [Parameter(Mandatory = $true)][string]$Leshy,
    [Parameter(Mandatory = $true)][string]$RouteProbe,
    [Parameter(Mandatory = $true)][string]$WorkDir
)

$ErrorActionPreference = "Continue"
foreach ($bin in @($Core, $Leshy, $RouteProbe)) {
    if (-not (Test-Path $bin)) { throw "missing binary: $bin" }
}

$Core = (Resolve-Path $Core).Path
$Leshy = (Resolve-Path $Leshy).Path
$RouteProbe = (Resolve-Path $RouteProbe).Path
New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
$WorkDir = (Resolve-Path $WorkDir).Path

$Socket = "$WorkDir\core.sock"
$State = "$WorkDir\state"
$Configs = "$WorkDir\configs"
$LeshyHome = "$WorkDir\leshy"
$AuthKey = "$State\control-route-auth.key"
$WriterLock = "C:\ProgramData\Kikimora\state\leshy-writer.lock"
$Journal = "$State\leshy-mode-transition.json"
$Marker = "$Configs\leshy-managed.json"
$CoreLog = "$WorkDir\core.log"
$CoreErrLog = "$WorkDir\core.err.log"
$script:CoreProc = $null

$Boundaries = @(
    "before_journal", "after_prepared", "after_config_write", "after_config_written",
    "after_marker_operation", "after_marker_written", "after_apply",
    "after_generation_commit", "before_journal_delete"
)

$script:Pass = 0
$script:Fail = 0
$leshyService = Get-Service | Where-Object { $_.Name -like "*leshy*" -and $_.Name -notlike "*dns*" } | Select-Object -First 1

function Note([string]$m) { Write-Output "== $m" }
function Pass([string]$m) { Write-Output "PASS: $m"; $script:Pass++ }
function Fail([string]$m) { Write-Output "FAIL: $m"; $script:Fail++ }

function Routes-Snapshot {
    (Get-NetRoute -ErrorAction SilentlyContinue |
        Sort-Object DestinationPrefix, InterfaceIndex, NextHop, RouteMetric |
        Select-Object DestinationPrefix, InterfaceIndex, NextHop, RouteMetric, Protocol |
        Out-String)
}

function Stop-Core {
    if ($script:CoreProc -and -not $script:CoreProc.HasExited) {
        $script:CoreProc.Kill()
        $script:CoreProc.WaitForExit(10000) | Out-Null
    }
    $script:CoreProc = $null
}

function Reset-State {
    Stop-Core
    Remove-Item -Recurse -Force $State, $Configs, $LeshyHome -ErrorAction SilentlyContinue
    Remove-Item $Socket -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $State, $Configs, "$LeshyHome\domains" | Out-Null
    Remove-Item $CoreLog, $CoreErrLog -Force -ErrorAction SilentlyContinue
}

function Start-Core([string]$failpoint) {
    Remove-Item $Socket -Force -ErrorAction SilentlyContinue
    if ($failpoint) { $env:KK_MODE_FAILPOINT = $failpoint }
    else { Remove-Item Env:\KK_MODE_FAILPOINT -ErrorAction SilentlyContinue }
    $script:CoreProc = Start-Process -FilePath $Core `
        -ArgumentList @("serve", "--socket", $Socket, "--state-dir", $State, "--config-dir", $Configs, "--toad-binary", "cmd") `
        -PassThru -NoNewWindow `
        -RedirectStandardOutput $CoreLog -RedirectStandardError $CoreErrLog
    # Cache the process handle NOW: without this, .ExitCode after a later
    # WaitForExit can come back null for a Start-Process -PassThru object
    # (PowerShell keeps the handle only while the object is alive).
    $null = $script:CoreProc.Handle
}

function Wait-Ready {
    & $Core wait-startup --socket $Socket --timeout 30s | Out-Null
    return ($LASTEXITCODE -eq 0)
}

function Wait-Exit86 {
    if (-not $script:CoreProc.WaitForExit(30000)) { return $false }
    return ($script:CoreProc.ExitCode -eq 86)
}

function Enable-Cli {
    & $Core leshy-mode enable --leshy $Leshy --socket $Socket --auth-file $AuthKey --control-socket $Socket | Out-Null
    return ($LASTEXITCODE -eq 0)
}

function Disable-Cli {
    & $Core leshy-mode disable --socket $Socket --auth-file $AuthKey | Out-Null
    return ($LASTEXITCODE -eq 0)
}

function Mode-Field([string]$field) {
    $out = & $Core leshy-mode status --socket $Socket --auth-file $AuthKey --json 2>$null
    if ($LASTEXITCODE -ne 0) { return "" }
    try { return ($out | ConvertFrom-Json).$field } catch { return "" }
}

function Marker-Field([string]$field) {
    try { return (Get-Content $Marker -Raw | ConvertFrom-Json).$field } catch { return "" }
}

function Writer-Lock-Free {
    $probe = Start-Process -FilePath $Core `
        -ArgumentList @("serve", "--socket", "$WorkDir\lockprobe.sock", "--state-dir", "$WorkDir\lockprobe-state", "--config-dir", $Configs, "--toad-binary", "cmd") `
        -Wait -PassThru -NoNewWindow -RedirectStandardOutput "$WorkDir\lockprobe.out" -RedirectStandardError "$WorkDir\lockprobe.err"
    # The probe core is refused on the STATE-dir lock while another core
    # runs; the writer-lock probe below is the leshy-mode enable path, so
    # the presence of a running probe core means the lock was never the
    # question. The real writer-lock probe is the transaction refusal.
    return (-not (Test-Path "$WorkDir\lockprobe.sock"))
}

function Start-AndWait-Failure([string]$needle) {
    # Starts the core and expects it to die with $needle in its stderr.
    Start-Core ""
    $deadline = (Get-Date).AddSeconds(20)
    while ((Get-Date) -lt $deadline) {
        if ($script:CoreProc.HasExited) {
            $text = Get-Content $CoreErrLog -Raw -ErrorAction SilentlyContinue
            $text += Get-Content $CoreLog -Raw -ErrorAction SilentlyContinue
            if ($text -match $needle) { return $true }
            Write-Output "  core output was: $($text | Select-Object -First 1)"
            return $false
        }
        Start-Sleep -Milliseconds 250
    }
    Write-Output "  the core did not exit within 20s"
    return $false
}

Note "harness identities"
Note "core binary: $Core"
Note "core sha256: $((Get-FileHash $Core -Algorithm SHA256).Hash.ToLower())"
Note "leshy binary: $Leshy"
Note "leshy sha256: $((Get-FileHash $Leshy -Algorithm SHA256).Hash.ToLower())"
Note "routeprobe binary: $RouteProbe"
Note "writer lock path: $WriterLock"
$RoutesBefore = Routes-Snapshot
Note "route baseline captured"

if ($leshyService) {
    Note "stopping service $($leshyService.Name) for the writer lock"
    Stop-Service $leshyService.Name -Force -ErrorAction SilentlyContinue
}

try {
    # ---------------------------------------------------------------
    Note "phase 1: the nine enable crash boundaries"
    foreach ($boundary in $Boundaries) {
        $label = "enable/$boundary"
        Reset-State
        Start-Core "enable/$boundary"
        if (-not (Wait-Ready)) { Fail "$label`: the core never became ready"; continue }
        if (Enable-Cli) { Fail "$label`: the enable must have crashed the core at the boundary"; Stop-Core; continue }
        if (-not (Wait-Exit86)) { Fail "$label`: the core did not die with exit 86"; Stop-Core; continue }
        Start-Core ""
        if (-not (Wait-Ready)) { Fail "$label`: the restart never became ready"; Stop-Core; continue }
        if ($boundary -eq "before_journal") {
            if ((Mode-Field "mode") -eq "local" -and (Mode-Field "generation") -eq "0" -and
                -not (Test-Path $Marker) -and -not (Test-Path $Journal)) {
                Pass "$label converged to local/0"
            } else {
                Fail "$label`: expected local/0 with no marker and no journal"
            }
        } else {
            if ((Mode-Field "mode") -eq "managed" -and (Mode-Field "generation") -eq "1" -and
                (Marker-Field "mode_generation") -eq "1" -and -not (Test-Path $Journal)) {
                Pass "$label converged to managed/1"
            } else {
                Fail "$label`: expected managed/1 with the marker and no journal"
            }
        }
        Stop-Core
    }

    # ---------------------------------------------------------------
    Note "phase 2: the nine disable crash boundaries"
    foreach ($boundary in $Boundaries) {
        $label = "disable/$boundary"
        Reset-State
        Start-Core ""
        if (-not (Wait-Ready)) { Fail "$label`: the core never became ready"; continue }
        if (-not (Enable-Cli)) { Fail "$label`: the pre-stage enable failed"; Stop-Core; continue }
        Stop-Core
        $routesBefore = Routes-Snapshot
        Start-Core "disable/$boundary"
        if (-not (Wait-Ready)) { Fail "$label`: the core never became ready"; continue }
        if (Disable-Cli) { Fail "$label`: the disable must have crashed the core at the boundary"; Stop-Core; continue }
        if (-not (Wait-Exit86)) { Fail "$label`: the core did not die with exit 86"; Stop-Core; continue }
        Start-Core ""
        if (-not (Wait-Ready)) { Fail "$label`: the restart never became ready"; Stop-Core; continue }
        if ($boundary -eq "before_journal") {
            if ((Mode-Field "mode") -eq "managed" -and (Mode-Field "generation") -eq "1" -and
                (Marker-Field "mode_generation") -eq "1") {
                Pass "$label converged to managed/1"
            } else {
                Fail "$label`: expected managed/1"
            }
        } else {
            if ((Mode-Field "mode") -eq "local" -and (Mode-Field "generation") -eq "2" -and
                -not (Test-Path $Marker) -and -not (Test-Path $Journal)) {
                Pass "$label converged to local/2"
            } else {
                Fail "$label`: expected local/2 with no marker and no journal"
            }
        }
        if ((Routes-Snapshot) -ne $routesBefore) {
            Fail "$label`: the route table drifted across the crash"
        }
        Stop-Core
    }

    # ---------------------------------------------------------------
    Note "phase 3: N1 - writer lock busy means zero mutation"
    Reset-State
    Start-Core ""
    if (-not (Wait-Ready)) {
        Fail "N1: the core never became ready"
    } else {
        # A local Leshy owns the writer lock for its process lifetime. The
        # TOML literal strings keep the Windows backslashes unescaped.
        Set-Content -Path "$LeshyHome\config.toml" -Value @"
[server]
listen_address = '127.0.0.1:15399'
default_upstream = ['127.0.0.1:53']
writer_lock = '$WriterLock'
"@ -Encoding ascii
        $leshyProc = Start-Process -FilePath $Leshy -ArgumentList @("$LeshyHome\config.toml") `
            -PassThru -NoNewWindow -RedirectStandardOutput "$LeshyHome\leshy.log" -RedirectStandardError "$LeshyHome\leshy.err"
        Start-Sleep -Seconds 3
        if ($leshyProc.HasExited) {
            Fail "N1: local Leshy exited immediately: $((Get-Content "$LeshyHome\leshy.err" -Raw -ErrorAction SilentlyContinue) + (Get-Content "$LeshyHome\leshy.log" -Raw -ErrorAction SilentlyContinue))"
        } else {
            # The busy-lock refusal itself proves the fence is held; the row
            # asserts zero mutation on top of it.
            $genBefore = Mode-Field "generation"
            $enableOut = & $Core leshy-mode enable --leshy $Leshy --socket $Socket --auth-file $AuthKey --control-socket $Socket 2>&1
            if ($LASTEXITCODE -eq 0) {
                Fail "N1: the enable must be refused while the lock is busy"
            } else {
                if (-not (Test-Path $Marker) -and -not (Test-Path $Journal) -and (Mode-Field "generation") -eq $genBefore -and $enableOut -match "busy") {
                    Pass "N1: the enable was refused with the busy writer lock and zero mutation"
                } else {
                    Fail "N1: the refusal was not the busy writer lock with zero mutation: $enableOut"
                }
            }
        }
        if ($leshyProc -and -not $leshyProc.HasExited) {
            $leshyProc.Kill()
            $leshyProc.WaitForExit(10000) | Out-Null
        }
    }
    Stop-Core

    # ---------------------------------------------------------------
    Note "phase 3: N2/N3 - missing and malformed managed auth fail startup"
    Reset-State
    Start-Core ""
    if (-not (Wait-Ready)) { Fail "N2 pre-stage: the core never became ready" }
    if (-not (Enable-Cli)) { Fail "N2 pre-stage: the enable failed" }
    Stop-Core
    Remove-Item $AuthKey -Force
    if (Start-AndWait-Failure "missing its privileged auth key") {
        Pass "N2: a managed install without its auth key failed startup"
    } else {
        Fail "N2: the core did not fail on the missing managed auth key"
    }
    Stop-Core

    Reset-State
    Start-Core ""
    if (-not (Wait-Ready)) { Fail "N3 pre-stage: the core never became ready" }
    if (-not (Enable-Cli)) { Fail "N3 pre-stage: the enable failed" }
    Stop-Core
    Set-Content -Path $AuthKey -Value "not-a-credential`n" -Encoding ascii
    if (Start-AndWait-Failure "is malformed") {
        Pass "N3: a managed install with a malformed auth key failed startup"
    } else {
        Fail "N3: the core did not fail on the malformed managed auth key"
    }
    Stop-Core

    # ---------------------------------------------------------------
    Note "phase 3: N4/N5 - stale fences are zero-mutation denials"
    Reset-State
    Start-Core ""
    if (-not (Wait-Ready)) { Fail "N4 pre-stage: the core never became ready" }
    if (-not (Enable-Cli)) { Fail "N4 pre-stage: the enable failed" }
    $activation = Marker-Field "activation_id"
    $generation = Marker-Field "mode_generation"
    $genBefore = Mode-Field "generation"

    & $RouteProbe --socket $Socket --auth-file $AuthKey ensure `
        --zone "nowhere" --prefix "203.0.113.10/32" `
        --activation "deadbeefdeadbeefdeadbeefdeadbeef" --generation $generation | Out-Null
    if ($LASTEXITCODE -eq 3) {
        if (-not (Test-Path $Journal) -and (Mode-Field "generation") -eq $genBefore) {
            Pass "N4: the stale activation was denied with zero mutation"
        } else {
            Fail "N4: the denial mutated durable state"
        }
    } else {
        Fail "N4: the stale activation must be the stable API denial (rc=$LASTEXITCODE)"
    }

    & $RouteProbe --socket $Socket --auth-file $AuthKey ensure `
        --zone "nowhere" --prefix "203.0.113.10/32" `
        --activation $activation --generation ($generation + 1) | Out-Null
    if ($LASTEXITCODE -eq 3) {
        if (-not (Test-Path $Journal) -and (Mode-Field "generation") -eq $genBefore) {
            Pass "N5: the stale generation was denied with zero mutation"
        } else {
            Fail "N5: the denial mutated durable state"
        }
    } else {
        Fail "N5: the stale generation must be the stable API denial (rc=$LASTEXITCODE)"
    }
    Stop-Core

    # ---------------------------------------------------------------
    Note "phase 3: N6 - a corrupt journal fails startup"
    Reset-State
    Start-Core ""
    if (-not (Wait-Ready)) { Fail "N6 pre-stage: the core never became ready" }
    if (-not (Enable-Cli)) { Fail "N6 pre-stage: the enable failed" }
    Stop-Core
    Set-Content -Path $Journal -Value "{broken journal" -Encoding ascii
    if (Start-AndWait-Failure "transition journal") {
        Pass "N6: a corrupt journal failed startup"
    } else {
        Fail "N6: the core did not fail on the corrupt journal"
    }
    Stop-Core

    # ---------------------------------------------------------------
    Note "phase 3: N7 - a digest mismatch fails startup"
    Reset-State
    Start-Core ""
    if (-not (Wait-Ready)) { Fail "N7 pre-stage: the core never became ready" }
    if (-not (Enable-Cli)) { Fail "N7 pre-stage: the enable failed" }
    $generation = Mode-Field "generation"
    Stop-Core
    $journal = [ordered]@{
        schema             = 1
        id                 = "0123456789abcdef0123456789abcdef"
        from               = "local"
        to                 = "managed"
        phase              = "marker_written"
        socket             = "/run/kikimora/core.sock"
        from_activation_id = ""
        to_activation_id   = "0123456789abcdef0123456789abcdef"
        from_generation    = ($generation - 1)
        to_generation      = $generation
        zones              = @()
        config_sha256      = ("0123456789abcdef" * 4)
    }
    $journal | ConvertTo-Json | Set-Content -Path $Journal -Encoding ascii
    if (Start-AndWait-Failure "does not match the journaled") {
        Pass "N7: a config digest mismatch failed startup"
    } else {
        Fail "N7: the core did not fail on the digest mismatch"
    }
    Stop-Core

    # ---------------------------------------------------------------
    Note "phase 3: N8 - a second core with the same StateDir fails first"
    Reset-State
    Start-Core ""
    if (-not (Wait-Ready)) { Fail "N8 pre-stage: the core never became ready" }
    $second = Start-Process -FilePath $Core `
        -ArgumentList @("serve", "--socket", "$WorkDir\second.sock", "--state-dir", $State, "--config-dir", $Configs, "--toad-binary", "cmd") `
        -Wait -PassThru -NoNewWindow -RedirectStandardOutput "$WorkDir\second.log" -RedirectStandardError "$WorkDir\second.err"
    $secondText = (Get-Content "$WorkDir\second.err" -Raw -ErrorAction SilentlyContinue) + (Get-Content "$WorkDir\second.log" -Raw -ErrorAction SilentlyContinue)
    if ($second.ExitCode -ne 0 -and $secondText -match "another core already owns this state directory") {
        if (Wait-Ready) {
            Pass "N8: the second core was refused before the socket and routes"
        } else {
            Fail "N8: the first core stopped serving after the second-core refusal"
        }
    } else {
        Fail "N8: the second core must fail with the state-dir ownership error (rc=$($second.ExitCode))"
    }
    Stop-Core

    # ---------------------------------------------------------------
    Note "phase 4: foreign route baseline"
    if ((Routes-Snapshot) -eq $RoutesBefore) {
        Pass "foreign route baseline unchanged"
    } else {
        Fail "the route table drifted across the harness run"
    }
} finally {
    Stop-Core
    Remove-Item Env:\KK_MODE_FAILPOINT -ErrorAction SilentlyContinue
    if ($leshyService) {
        Start-Service $leshyService.Name -ErrorAction SilentlyContinue
    }
}

Note "harness complete: PASS=$($script:Pass) FAIL=$($script:Fail)"
if ($script:Fail -eq 0) {
    Write-Output "RESULT: GREEN"
} else {
    Write-Output "RESULT: RED"
    exit 1
}
