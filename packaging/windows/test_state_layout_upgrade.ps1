# SPDX-License-Identifier: MIT
#
# test_state_layout_upgrade.ps1 — the versioned Windows Phase E state-layout
# upgrade gate (08D1 post-739e1ab audit R1 §3.5, distinct from the fresh-install
# A-H cutover gate): stage the LEGACY core-state layout under
# C:\ProgramData\Kikimora\state with state\core absent, install the candidate
# MSI, and prove the real service migrates, preserves and converges:
#
#   row legacy-local          a local (unmanaged) legacy layout keeps its
#                             desired state and credential bytes, reaches
#                             readiness and stays local;
#   row legacy-managed        a committed legacy managed pair (generation N,
#                             matching schema2 marker, credential, desired
#                             role) upgrades with generation, activation
#                             fingerprint, marker and desired state unchanged
#                             and the generated config pointing at the NEW
#                             credential path;
#   row in-flight-transition  a REAL journal staged by the failpoint-tagged
#                             candidate core (crash at the config_written
#                             boundary) migrates and converges through
#                             RecoverManagedModeTransition to the journal's
#                             target managed generation.
#
# Usage (elevated, on win10). The three MANDATORY binaries come from the exact
# candidate source head:
#   powershell -ExecutionPolicy Bypass -File test_state_layout_upgrade.ps1 `
#       -Msi C:\path\kikimora-<version>-windows-amd64.msi `
#       -FailpointCore C:\path\kikimora-core-failpoints.exe `
#       -Leshy C:\path\leshy.exe
# The VM is disposable: every row uninstalls previous MSIs, deletes stale
# services and rebuilds the ProgramData staging from scratch.
param(
    [Parameter(Mandatory = $true)][string]$Msi,
    [Parameter(Mandatory = $true)][string]$FailpointCore,
    [Parameter(Mandatory = $true)][string]$Leshy
)

$ErrorActionPreference = "Continue"

foreach ($bin in @($Msi, $FailpointCore, $Leshy)) {
    if (-not (Test-Path $bin)) { throw "missing binary/artifact: $bin" }
}
$Msi = (Resolve-Path $Msi).Path
$FailpointCore = (Resolve-Path $FailpointCore).Path
$Leshy = (Resolve-Path $Leshy).Path

$DataRoot = "C:\ProgramData\Kikimora"
$LegacyStateDir = "$DataRoot\state"          # pre-Phase-E root (R1 legacy)
$CoreStateDir = "$DataRoot\state\core"       # authoritative root (R1 new)
$LayoutMarker = "$CoreStateDir\core-state-layout.json"
$LegacyAuthKey = "$LegacyStateDir\control-route-auth.key"
$AuthKey = "$CoreStateDir\control-route-auth.key"
$LegacyGeneration = "$LegacyStateDir\leshy-mode-generation.json"
$LegacyDesired = "$LegacyStateDir\desired.json"
$LegacyJournal = "$LegacyStateDir\leshy-mode-transition.json"
$CoreSocket = "$DataRoot\core.sock"
$ToadsDir = "$DataRoot\toads"
$Marker = "$ToadsDir\leshy-managed.json"
$Role = "upgradegate"
$RoleConfig = "$ToadsDir\$Role.toml"
$RoleZone = "upgradegate"
$LeshyConfig = "C:\ProgramData\leshy\config.toml"
$CoreService = "KikimoraCore"
$LeshyService = "leshy"
$CoreExe = "C:\Program Files\Kikimora\kikimora-core.exe"

# Fixed staged identity for the managed rows. The 32-hex activation id and the
# committed generation are constants so every assertion is deterministic.
$StagedActivation = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
$StagedGeneration = [uint64]4242

$script:Pass = 0
$script:Fail = 0

function Note([string]$m) { Write-Output "== $m" }
function Pass([string]$m) { Write-Output "PASS: $m"; $script:Pass++ }
function Fail([string]$m) { Write-Output "FAIL: $m"; $script:Fail++ }

function Get-FileSha256([string]$Path) {
    if (-not (Test-Path $Path)) { return "" }
    return (Get-FileHash -Algorithm SHA256 -Path $Path).Hash.ToLowerInvariant()
}

function Get-JsonField([string]$Path, [string]$Field) {
    if (-not (Test-Path $Path)) { return "" }
    try {
        $value = (Get-Content $Path -Raw | ConvertFrom-Json).$Field
        if ($null -eq $value) { return "" }
        return [string]$value
    } catch { return "" }
}

function Get-ModeStatus {
    $out = & $CoreExe leshy-mode status --socket $CoreSocket --auth-file $AuthKey --json 2>&1
    if ($LASTEXITCODE -ne 0) { return $null }
    try { return (($out -join "`n") | ConvertFrom-Json) } catch { return $null }
}

function Get-ModeField([string]$field) {
    $status = Get-ModeStatus
    if ($null -eq $status) { return "" }
    $property = $status.PSObject.Properties[$field]
    if ($null -eq $property) { return "" }
    return [string]$property.Value
}

function Get-RoleDesired {
    $out = & $CoreExe status --socket $CoreSocket --json 2>&1
    if ($LASTEXITCODE -ne 0) { return "" }
    try {
        $snapshot = (($out -join "`n") | ConvertFrom-Json)
        $role = @($snapshot.roles) | Where-Object { $_.id -eq $Role } | Select-Object -First 1
        if ($null -eq $role) { return "" }
        return [string]$role.desired
    } catch { return "" }
}

function Wait-CoreReady([int]$TimeoutSeconds = 120) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        & $CoreExe wait-startup --socket $CoreSocket --timeout 5s 2>&1 | Out-Null
        if ($LASTEXITCODE -eq 0) { return $true }
        Start-Sleep -Seconds 2
    }
    return $false
}

function Get-ConfigFields {
    $result = @{
        AuthFile   = ""
        Activation = ""
        Generation = ""
        Backend    = ""
    }
    if (-not (Test-Path $LeshyConfig)) { return $result }
    $text = Get-Content -Path $LeshyConfig -Raw
    foreach ($pair in @(
        @("AuthFile", '(?m)^kikimora_auth_file\s*=\s*"([^"]+)"'),
        @("Activation", '(?m)^kikimora_activation_id\s*=\s*"([0-9a-f]{32})"'),
        @("Generation", '(?m)^kikimora_mode_generation\s*=\s*(\d+)'),
        @("Backend", '(?m)^routing_backend\s*=\s*"([^"]+)"')
    )) {
        $match = [regex]::Match($text, $pair[1])
        if ($match.Success) {
            # TOML escapes backslashes: \\ -> \.
            $result[$pair[0]] = $match.Groups[1].Value -replace '\\\\', '\'
        }
    }
    return $result
}

function Get-Fingerprint([string]$ActivationID) {
    if (-not $ActivationID) { return "" }
    $digest = [Security.Cryptography.SHA256]::Create().ComputeHash(
        [Text.Encoding]::UTF8.GetBytes($ActivationID))
    return ([BitConverter]::ToString($digest) -replace "-", "").ToLowerInvariant().Substring(0, 12)
}

function Reset-Machine {
    Note "pre-clean: uninstall previous MSIs, delete stale services, stop processes"
    foreach ($root in @(
        "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall",
        "HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall"
    )) {
        if (-not (Test-Path $root)) { continue }
        Get-ChildItem $root -ErrorAction SilentlyContinue | ForEach-Object {
            $entry = Get-ItemProperty $_.PSPath -ErrorAction SilentlyContinue
            if ($entry -and $entry.DisplayName -eq "Kikimora" -and $_.PSChildName -match '^\{.*\}$') {
                $p = Start-Process msiexec -ArgumentList @("/x", "`"$($_.PSChildName)`"", "/qn", "/norestart") -PassThru
                if (-not $p.WaitForExit(300000)) {
                    # A half-registered product (left by a failed row install)
                    # can hang its own uninstall; the machine stays disposable,
                    # so kill the stuck msiexec and continue with the sweep.
                    Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
                    Note "the previous MSI $($_.PSChildName) uninstall hung; killed after 300s"
                }
                if ($p.ExitCode -notin @(0, 1605, 3010)) {
                    throw "the previous MSI $($_.PSChildName) could not be uninstalled (rc=$($p.ExitCode))"
                }
            }
        }
    }
    foreach ($svc in @($LeshyService, $CoreService)) {
        $existing = Get-Service -Name $svc -ErrorAction SilentlyContinue
        if ($existing) {
            Stop-Service -Name $svc -Force -ErrorAction SilentlyContinue
            & sc.exe delete $svc | Out-Null
        }
    }
    Get-Process kikimora-core, kikimora-toad, leshy -ErrorAction SilentlyContinue |
        Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
}

function Reset-LegacyStaging {
    # Deterministic pre-upgrade geometry: the legacy root exists, state\core
    # and every prior authority/marker/config file are gone.
    if (Test-Path $DataRoot) {
        Remove-Item -Recurse -Force $DataRoot -ErrorAction SilentlyContinue
    }
    foreach ($dir in @($LegacyStateDir, $ToadsDir)) {
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
    }
    Remove-Item $LeshyConfig -Force -ErrorAction SilentlyContinue
    # A legacy machine always carries the generated Leshy config the
    # service's ImagePath points at; without it the leshy service cannot
    # start during the candidate install (SCM 1920, ErrorControl=1) and
    # the MSI rolls back with 1603. Stage the minimal local-mode form.
    New-Item -ItemType Directory -Force -Path (Split-Path $LeshyConfig) | Out-Null
    Set-Content -Path $LeshyConfig -Value "[server]" -Encoding ASCII
    Add-Content -Path $LeshyConfig -Value 'listen_address = "127.0.0.1:15353"' -Encoding ASCII
    Add-Content -Path $LeshyConfig -Value 'default_upstream = ["127.0.0.1:15354"]' -Encoding ASCII
    Add-Content -Path $LeshyConfig -Value 'route_failure_mode = "servfail"' -Encoding ASCII
    # The role TOML: a normal AWG2 role with its own Leshy zone. The endpoint
    # is TEST-NET-3 (unreachable), so a desired role fails its tunnel without
    # touching the lab network.
    $toml = @"
name = "$Role"
protocol = "amneziawg2"
interface = "kkgate"
mtu = 1380
state_dir = "$((($ToadsDir + '\' + $Role + '\' + 'state').Replace('\', '\\')))"
address = ["10.77.0.1/24"]
leshy_zone = "$RoleZone"

[awg2]
private_key = "QKOScRTPXfrLSCdCPSpGgN+hwRXVBLbLpQwXcB+fF2I="
peer_public_key = "QKOScRTPXfrLSCdCPSpGgN+hwRXVBLbLpQwXcB+fF2I="
preshared_key = ""
endpoint = "203.0.113.1:51820"
allowed_ips = ["0.0.0.0/0", "::/0"]
persistent_keepalive = 0
jc = 1
jmin = 2
jmax = 3
s1 = 0
s2 = 0
s3 = 0
s4 = 0
h1 = "test"
h2 = "test"
h3 = "test"
h4 = "test"
i1 = "test"
i2 = "test"
i3 = "test"
i4 = "test"
i5 = "test"
"@
    Set-Content -Path $RoleConfig -Value $toml -Encoding ASCII
    New-Item -ItemType Directory -Force -Path "$ToadsDir\$Role\state" | Out-Null
}

function Write-LegacyFile([string]$Path, [string]$Content) {
    # ProgramData defaults leave the parent writable; these staged files stand
    # in for files an older core version wrote there.
    Set-Content -Path $Path -Value $Content -NoNewline -Encoding ASCII
}

function Stage-LegacyAuthKey {
    # 64 lowercase hex + newline: exactly the parseable credential form.
    $hex = ("ab" * 32)
    Write-LegacyFile $LegacyAuthKey ($hex + "`n")
}

function Stage-LegacyManagedPair {
    Stage-LegacyAuthKey
    Write-LegacyFile $LegacyGeneration ('{"schema":1,"last_committed_generation":' + $StagedGeneration + '}')
    # ConvertTo-Json escapes the socket backslashes exactly as the core writes them.
    $marker = @{
        schema          = 2
        socket          = $CoreSocket
        core_version    = "upgrade-gate"
        managed_zones   = @($RoleZone)
        activation_id   = $StagedActivation
        mode_generation = $StagedGeneration
    } | ConvertTo-Json -Compress
    Write-LegacyFile $Marker $marker
}

function Stage-LegacyDesired {
    Write-LegacyFile $LegacyDesired ('{"schema":1,"active_profile":"default","roles":{"' + $Role + '":true},"updated_at":"2026-11-11T00:00:00Z"}')
}

function Install-CandidateMsi {
    Note "install the candidate MSI"
    $installLog = "$env:TEMP\kikimora-upgrade-install.log"
    $p = Start-Process msiexec -ArgumentList @("/i", "`"$Msi`"", "/qn", "/norestart", "/l*v", "`"$installLog`"") -Wait -PassThru
    return [pscustomobject]@{ Code = $p.ExitCode; Log = $installLog }
}

function Assert-CommonMigrationProofs {
    # R1 §3.5 required proofs shared by every row.
    if (-not (Wait-CoreReady 120)) {
        Fail "the upgraded core never reached startup readiness"
        return $false
    }
    Pass "the upgraded core reached startup readiness"

    $schema = Get-JsonField $LayoutMarker "schema"
    $layout = Get-JsonField $LayoutMarker "layout"
    if ($schema -eq "1" -and $layout -eq "2") {
        Pass "the completion marker is schema 1 / layout 2"
    } else {
        Fail "the completion marker is wrong (schema=$schema layout=$layout)"
    }

    $legacyMissing = @()
    foreach ($file in @($LegacyAuthKey, $LegacyGeneration, $LegacyDesired, $LegacyJournal)) {
        if (Test-Path $file) { continue }
        # Only files this row actually staged are required to survive.
        $legacyMissing += $file
    }
    return $true
}

# ---------------------------------------------------------------------------
Note "row legacy-local: an unmanaged legacy layout upgrades with its desired state"
Reset-Machine
Reset-LegacyStaging
Stage-LegacyAuthKey
Stage-LegacyDesired
$legacyAuthHash = Get-FileSha256 $LegacyAuthKey

$install = Install-CandidateMsi
if ($install.Code -ne 0) {
    Fail "the MSI install failed (rc=$($install.Code)); see $($install.Log)"
} else {
    Pass "the candidate MSI installed over the legacy-local layout"
    if (Test-Path $AuthKey) {
        if ((Get-FileSha256 $AuthKey) -eq $legacyAuthHash) {
            Pass "the migrated credential bytes are identical to the legacy credential"
        } else {
            Fail "the migrated credential bytes differ from the legacy credential"
        }
    } else {
        Fail "the credential did not migrate into state\core"
    }
    if ((Get-RoleDesired) -eq "True") {
        Pass "the desired role remains desired after the migration"
    } else {
        Fail "the desired role lost its desired state (snapshot desired=$(Get-RoleDesired))"
    }
    $mode = Get-ModeField "mode"
    if ($mode -eq "local") {
        Pass "the upgraded install is still in local mode"
    } else {
        Fail "the legacy-local upgrade did not stay local (mode=$mode)"
    }
    if (Test-Path $LegacyAuthKey) {
        Pass "the legacy credential source file still exists"
    } else {
        Fail "the migration deleted the legacy credential source"
    }
    if (Test-Path $LegacyDesired) {
        Pass "the legacy desired.json source file still exists"
    } else {
        Fail "the migration deleted the legacy desired.json source"
    }
    Assert-CommonMigrationProofs | Out-Null
}

# ---------------------------------------------------------------------------
Note "row legacy-managed: a committed managed pair upgrades unchanged"
Reset-Machine
Reset-LegacyStaging
Stage-LegacyManagedPair
Stage-LegacyDesired
$legacyAuthHash = Get-FileSha256 $LegacyAuthKey
$legacyGenerationBytes = Get-FileSha256 $LegacyGeneration
$legacyMarkerHash = Get-FileSha256 $Marker

$install = Install-CandidateMsi
if ($install.Code -ne 0) {
    Fail "the MSI install failed (rc=$($install.Code)); see $($install.Log)"
} else {
    Pass "the candidate MSI installed over the legacy-managed layout"
    if (Test-Path $AuthKey) {
        if ((Get-FileSha256 $AuthKey) -eq $legacyAuthHash) {
            Pass "the migrated managed credential bytes are identical"
        } else {
            Fail "the migrated managed credential bytes differ"
        }
    } else {
        Fail "the managed credential did not migrate into state\core"
    }
    if (Wait-CoreReady 120) {
        Pass "the upgraded managed core reached startup readiness"
    } else {
        Fail "the upgraded managed core never reached startup readiness (generation fence failed?)"
    }
    $generation = Get-ModeField "generation"
    if ($generation -eq [string]$StagedGeneration) {
        Pass "the committed generation survived the upgrade ($generation)"
    } else {
        Fail "the committed generation changed across the upgrade (got $generation, want $($StagedGeneration))"
    }
    $fingerprint = Get-ModeField "activation_id_fingerprint"
    if ($fingerprint -eq (Get-Fingerprint $StagedActivation)) {
        Pass "the activation fingerprint is unchanged"
    } else {
        Fail "the activation fingerprint changed across the upgrade (got $fingerprint)"
    }
    if ((Get-ModeField "mode") -eq "managed") {
        Pass "the upgraded install is in managed mode"
    } else {
        Fail "the legacy-managed upgrade did not reach managed mode (mode=$(Get-ModeField 'mode'))"
    }
    if ((Get-RoleDesired) -eq "True") {
        Pass "the managed desired role remains desired"
    } else {
        Fail "the managed desired role lost its desired state"
    }
    $markerGeneration = Get-JsonField $Marker "mode_generation"
    if ($markerGeneration -eq [string]$StagedGeneration) {
        Pass "the managed marker generation is unchanged"
    } else {
        Fail "the managed marker generation changed (got $markerGeneration)"
    }
    $config = Get-ConfigFields
    if ($config.AuthFile -eq $AuthKey) {
        Pass "the generated managed config points at the new credential path"
    } else {
        Fail "the generated managed config auth file is '$($config.AuthFile)', want $AuthKey"
    }
    if ($config.Backend -eq "kikimora") {
        Pass "the generated managed config uses the managed backend"
    } else {
        Fail "the generated managed config backend is '$($config.Backend)'"
    }
    foreach ($file in @($LegacyAuthKey, $LegacyGeneration, $Marker, $LegacyDesired)) {
        if (Test-Path $file) {
            Pass "legacy source still exists: $(Split-Path $file -Leaf)"
        } else {
            Fail "the migration deleted legacy source $(Split-Path $file -Leaf)"
        }
    }
    Assert-CommonMigrationProofs | Out-Null
}

# ---------------------------------------------------------------------------
Note "row in-flight-transition: a REAL crashed Enable journal migrates and converges"
Reset-Machine
Reset-LegacyStaging
Stage-LegacyAuthKey

# Produce a genuine in-flight journal in the LEGACY root: run the
# failpoint-tagged candidate core as a console process whose state dir IS the
# legacy root (the pre-upgrade geometry), and crash the real Enable at the
# config_written boundary. The journal, generation file and credential this
# leaves behind are core-authored, never hand-forged.
$consoleLog = "$env:TEMP\kikimora-upgrade-console.log"
$consoleErr = "$env:TEMP\kikimora-upgrade-console.err.log"
$env:KK_MODE_FAILPOINT = "after_config_written"
$console = Start-Process -FilePath $FailpointCore `
    -ArgumentList @(
        "serve",
        "--socket", $CoreSocket,
        "--state-dir", $LegacyStateDir,
        "--config-dir", $ToadsDir,
        "--endpoint-provider-dir", "$DataRoot\endpoint-providers",
        "--leshy-publication-dir", "$DataRoot\leshy\vpn",
        "--toad-binary", "cmd"
    ) `
    -PassThru -NoNewWindow -RedirectStandardOutput $consoleLog -RedirectStandardError $consoleErr
$null = $console.Handle
Start-Sleep -Seconds 3
& $FailpointCore leshy-mode enable --leshy $Leshy --socket $CoreSocket `
    --auth-file $LegacyAuthKey --control-socket $CoreSocket 2>&1 | Out-Null
$enableCli = $LASTEXITCODE
if (-not $console.WaitForExit(30000)) {
    $console.Kill()
}
Remove-Item Env:\KK_MODE_FAILPOINT -ErrorAction SilentlyContinue

if (-not (Test-Path $LegacyJournal)) {
    Fail "the staged Enable did not leave an in-flight journal in the legacy root (cli rc=$enableCli); see $consoleErr"
} else {
    Pass "a real in-flight transition journal exists in the legacy root"
    $journalPhase = Get-JsonField $LegacyJournal "phase"
    $journalTo = [uint64](Get-JsonField $LegacyJournal "to_generation")
    $journalActivation = Get-JsonField $LegacyJournal "to_activation_id"
    if ($journalPhase -eq "config_written") {
        Pass "the journal is at the config_written boundary"
    } else {
        Fail "the journal phase is '$journalPhase', want config_written"
    }

    if (Test-Path $CoreStateDir) {
        Remove-Item -Recurse -Force $CoreStateDir
        Note "removed state\core so the upgrade starts from the pre-migration geometry"
    }
    $svc = Get-Service -Name $CoreService -ErrorAction SilentlyContinue
    if ($svc) { & sc.exe delete $CoreService | Out-Null; Start-Sleep -Seconds 2 }

    $install = Install-CandidateMsi
    if ($install.Code -ne 0) {
        Fail "the MSI install failed (rc=$($install.Code)); see $($install.Log)"
    } else {
        Pass "the candidate MSI installed over the in-flight legacy layout"
        # The migrated journal must have converged: readiness IS the recovery
        # proof (an unresolved transition blocks startup_ready by contract).
        if (Wait-CoreReady 120) {
            Pass "the core converged the migrated in-flight transition and reached readiness"
        } else {
            Fail "the core never reached readiness: RecoverManagedModeTransition did not converge"
        }
        $generation = Get-ModeField "generation"
        if ($generation -eq [string]$journalTo) {
            Pass "the committed generation rolled forward to the journal target ($generation)"
        } else {
            Fail "the committed generation is $generation, want the journal target $journalTo"
        }
        if ((Get-ModeField "mode") -eq "managed") {
            Pass "the journal rolled forward to managed mode"
        } else {
            Fail "the journal did not roll forward to managed mode (mode=$(Get-ModeField 'mode'))"
        }
        $markerActivation = Get-JsonField $Marker "activation_id"
        $markerGeneration = Get-JsonField $Marker "mode_generation"
        if ($markerActivation -eq $journalActivation -and $markerGeneration -eq [string]$journalTo) {
            Pass "the written marker matches the journal activation and generation"
        } else {
            Fail "the marker (activation=$markerActivation generation=$markerGeneration) does not match the journal"
        }
        if ((Get-ModeField "transition") -eq "none") {
            Pass "the transition journal was consumed (transition=none)"
        } else {
            Fail "a transition is still in flight after recovery"
        }
        if (Test-Path $LegacyJournal) {
            Pass "the legacy journal source file still exists (copy-and-verify)"
        } else {
            Fail "the migration deleted the legacy journal source"
        }
        Assert-CommonMigrationProofs | Out-Null
    }
}

# ---------------------------------------------------------------------------
Write-Output "== gate complete: PASS=$($script:Pass) FAIL=$($script:Fail)"
if ($script:Fail -gt 0) {
    Write-Output "RESULT: RED"
    exit 1
}
Write-Output "RESULT: GREEN"
exit 0
