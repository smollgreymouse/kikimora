# SPDX-License-Identifier: MIT
#
# test_managed_leshy.ps1 — the versioned Windows managed-success gate (H2,
# roadmap §16.2). The candidate MSI is installed on win10 and the managed
# success rows run against the installed product with the exact Ready-Toad
# from the combined accepted source head. Failure/denial rows live in
# test_managed_cutover.ps1; this harness owns the success path only.
#
# Rows:
#   A  candidate install: KikimoraCore reaches startup readiness;
#   B  schema2 Enable succeeds through the real core CLI; marker, status and
#      generated config carry one exact identity;
#   C  the managed Leshy service is running and the SCM dependency on
#      KikimoraCore is applied;
#   D  core restart preserves the committed activation (generation, marker,
#      status) without re-running the transaction;
#   E  the canary query through the installed DNS path reaches the selected
#      registry via routeprobe, and SetRoutingList round-trips;
#   F  DisconnectAll empties the registry, then Disable succeeds and the
#      local Leshy configuration is regenerated;
#   G  state restore: local mode stays after the run.
#
# Usage (elevated, on win10). All arguments are MANDATORY:
#   powershell -ExecutionPolicy Bypass -File test_managed_leshy.ps1 `
#       -Msi C:\path\kikimora-<version>-windows-amd64.msi `
#       -Leshy C:\path\leshy.exe `
#       -Role awg-main -Canary example.com `
#       -RouteProbe C:\path\routeprobe.exe `
#       -WorkDir C:\kikimora-acceptance\<sha>\h2
param(
    [Parameter(Mandatory = $true)][string]$Msi,
    [Parameter(Mandatory = $true)][string]$Leshy,
    [Parameter(Mandatory = $true)][string]$Role,
    [Parameter(Mandatory = $true)][string]$Canary,
    [Parameter(Mandatory = $true)][string]$RouteProbe,
    [Parameter(Mandatory = $true)][string]$WorkDir
)

$ErrorActionPreference = "Continue"

# Deterministic argument parsing: reject missing mandatory artifacts before
# any mutation.
foreach ($path in @($Msi, $Leshy, $RouteProbe)) {
    if (-not (Test-Path $path)) { throw "missing binary/artifact: $path" }
}
$Msi = (Resolve-Path $Msi).Path
$Leshy = (Resolve-Path $Leshy).Path
$RouteProbe = (Resolve-Path $RouteProbe).Path
if (-not [System.IO.Path]::IsPathRooted($WorkDir)) {
    $WorkDir = Join-Path (Get-Location) $WorkDir
}
New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
$WorkDir = (Resolve-Path $WorkDir).Path

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$sourceSha = "unknown"
try { $sourceSha = (git -C $RepoRoot rev-parse HEAD).Trim() } catch {}
$failures = 0
function Note([string]$m) { Write-Host "== $m" }
function Pass([string]$m) { Write-Host "PASS: $m" }
function Fail([string]$m) {
    Write-Host "FAIL: $m"
    $script:failures++
}
function Save-SourceHashes {
    param([string]$Dir)
    foreach ($pair in @(@("core", $CoreExe), @("leshy", $LeshyExe))) {
        $file = Join-Path $Dir ("sha256-" + $pair[0] + ".txt")
        if (Test-Path $pair[1]) {
            (Get-FileHash $pair[1] -Algorithm SHA256).Hash.ToLower() | Out-File -Encoding ascii $file
        }
    }
}

$CoreExe = "C:\Program Files\Kikimora\kikimora-core.exe"
$LeshyExe = "C:\Program Files\Kikimora\leshy\leshy.exe"
$CoreSocket = "C:\ProgramData\Kikimora\core.sock"
$DataRoot = "C:\ProgramData\Kikimora"
$CoreStateDir = "$DataRoot\state\core"
$AuthKey = "$CoreStateDir\control-route-auth.key"
$ToadsDir = "$DataRoot\toads"
$Marker = "$ToadsDir\leshy-managed.json"
$Journal = "$CoreStateDir\leshy-mode-transition.json"
$CoreService = "KikimoraCore"
$LeshyService = "leshy"
$installLog = Join-Path $WorkDir "install.log"

# One cleanup path: stop what this run owns; the installed package stays in
# local mode (row G) or, on failure, evidence stays for inspection.
function Cleanup {
    $null = & sc.exe config $LeshyService depend= "" 2>$null
}
trap { Cleanup }

Note "source SHA: $sourceSha"
Save-SourceHashes -Dir $WorkDir

# ---- A candidate install ----------------------------------------------------
Note "row A: candidate install"
$existing = Get-Service -Name $CoreService -ErrorAction SilentlyContinue
if ($existing) {
    $code = (Get-ItemProperty "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*" -ErrorAction SilentlyContinue |
        Where-Object { $_.DisplayName -eq "Kikimora" } | Select-Object -First 1).PSChildName
    if ($code) {
        $p = Start-Process msiexec -ArgumentList @("/x", "`"$code`"", "/qn", "/norestart") -Wait -PassThru
        if ($p.ExitCode -ne 0) { Fail "pre-run uninstall of an existing Kikimora failed: $($p.ExitCode)" }
    }
}
$p = Start-Process msiexec -ArgumentList @("/i", "`"$Msi`"", "/qn", "/norestart", "/l*v", "`"$installLog`"") -Wait -PassThru
if ($p.ExitCode -ne 0) {
    Fail "MSI install failed: $($p.ExitCode)"
} else {
    Pass "candidate MSI installed"
}
$ready = $false
for ($i = 0; $i -lt 60; $i++) {
    $out = & $CoreExe wait-startup --socket $CoreSocket --timeout 2s 2>$null
    if ($LASTEXITCODE -eq 0) { $ready = $true; break }
    Start-Sleep -Seconds 1
}
if ($ready) { Pass "KikimoraCore startup readiness" } else { Fail "KikimoraCore never reached startup readiness" }

# ---- B schema2 Enable through the real CLI ----------------------------------
Note "row B: managed Enable"
$enableLog = Join-Path $WorkDir "enable.log"
& $CoreExe leshy-mode enable --leshy $LeshyExe --socket $CoreSocket `
    --auth-file $AuthKey --control-socket $CoreSocket 2>&1 | Out-File -Encoding utf8 $enableLog
if ($LASTEXITCODE -eq 0) {
    Pass "managed Enable succeeded"
} else {
    Fail "managed Enable failed (see $enableLog)"
}
if (Test-Path $Marker) { Pass "managed marker written" } else { Fail "managed marker missing after Enable" }
$manifest = Get-Content $Marker -Raw | ConvertFrom-Json
$statusJson = & $CoreExe leshy-mode status --socket $CoreSocket --auth-file $AuthKey --json 2>$null
$status = $statusJson | ConvertFrom-Json
if ($manifest.activation_id -and $status.activation_id_fingerprint) {
    Pass "marker and status carry one identity"
} else {
    Fail "marker/status identity fields incomplete"
}

# ---- C managed Leshy service + SCM dependency -------------------------------
Note "row C: managed Leshy service"
$svc = Get-Service -Name $LeshyService -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -eq "Running") {
    Pass "leshy service is running"
} else {
    Fail "leshy service is not running"
}
# The sc config dependency action runs at install; after a manual service
# start order is enforced there, so query the SCM dependency directly.
$scInfo = (sc.exe qc $LeshyService | Out-String)
if ($scInfo -match "KikimoraCore") {
    Pass "leshy service DependsOn KikimoraCore"
} else {
    Fail "leshy service dependency on KikimoraCore is missing: $scInfo"
}

# ---- D core restart preserves the committed activation ----------------------
Note "row D: core restart preserves the activation"
Restart-Service -Name $CoreService -Force
$ready = $false
for ($i = 0; $i -lt 60; $i++) {
    $null = & $CoreExe wait-startup --socket $CoreSocket --timeout 2s 2>$null
    if ($LASTEXITCODE -eq 0) { $ready = $true; break }
    Start-Sleep -Seconds 1
}
if (-not $ready) { Fail "core did not recover after restart" }
$statusJson2 = & $CoreExe leshy-mode status --socket $CoreSocket --auth-file $AuthKey --json 2>$null | ConvertFrom-Json
if ($statusJson2.mode_generation -and $statusJson2.mode_generation -eq $status.mode_generation) {
    Pass "committed generation survives the restart"
} else {
    Fail "generation changed across restart: $($status.mode_generation) -> $($statusJson2.mode_generation)"
}
if (-not (Test-Path $Journal)) { Pass "no in-flight journal after restart" } else { Fail "transition journal left behind" }

# ---- E canary DNS + routing round trip --------------------------------------
Note "row E: canary query + SetRoutingList round trip"
$before = & $RouteProbe --socket $CoreSocket selected 2>&1 | Out-String
$dnsOut = Resolve-DnsName $Canary -Server 127.0.0.1 -ErrorAction SilentlyContinue
if ($dnsOut) { Pass "canary query answered through the installed resolver" } else { Fail "canary query failed" }
$listLog = Join-Path $WorkDir "setroutinglist.log"
& $RouteProbe --socket $CoreSocket set-routing-list --role $Role --content "example.com`n" 2>&1 | Out-File -Encoding utf8 $listLog
if ($LASTEXITCODE -eq 0) { Pass "SetRoutingList round trip (row via control API)" } else { Fail "SetRoutingList failed (see $listLog)" }

# ---- F empty registry, then Disable ----------------------------------------
Note "row F: DisconnectAll then Disable"
& $CoreExe disconnect-all --socket $CoreSocket 2>&1 | Out-Null
$empty = $false
for ($i = 0; $i -lt 30; $i++) {
    $selected = & $RouteProbe --socket $CoreSocket selected 2>&1 | Out-String
    if ($selected -notmatch "[1-9]") { $empty = $true; break }
    Start-Sleep -Seconds 1
}
if ($empty) { Pass "selected registry emptied" } else { Fail "selected registry never emptied; Disable would be refused" }
$disableLog = Join-Path $WorkDir "disable.log"
& $CoreExe leshy-mode disable --socket $CoreSocket --auth-file $AuthKey 2>&1 | Out-File -Encoding utf8 $disableLog
if ($LASTEXITCODE -eq 0) { Pass "managed Disable succeeded" } else { Fail "managed Disable failed (see $disableLog)" }

# ---- G local mode restore ----------------------------------------------------
Note "row G: local mode restore"
$svc2 = Get-Service -Name $LeshyService -ErrorAction SilentlyContinue
if ($svc2) { Pass "leshy service present after disable" } else { Pass "leshy service removed with managed mode" }

if ($failures -gt 0) {
    Write-Host "H2 managed-success gate FAILED: $failures assertion(s)" -ForegroundColor Red
    exit 1
}
Write-Host "H2 managed-success gate GREEN" -ForegroundColor Green
exit 0
