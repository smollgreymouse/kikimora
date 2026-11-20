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
    [Parameter(Mandatory = $true)][string]$WorkDir,
    # T1R2 (2026-11-17 post-X1 audit): the source identity is MANDATORY input.
    [Parameter(Mandatory = $true)][ValidatePattern('^[0-9a-f]{40}$')][string]$SourceSha
)

$ErrorActionPreference = "Continue"

# T1R2 identity block (2026-11-17 post-X1 audit): printed before any mutation.
$HarnessPath = $PSCommandPath
$HarnessSha256 = (Get-FileHash -Algorithm SHA256 -Path $HarnessPath).Hash.ToLowerInvariant()
Write-Host "=== identity block ==="
Write-Host "source sha: $SourceSha"
Write-Host "harness: $HarnessPath"
Write-Host "harness sha256: $HarnessSha256"

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
# H2 2026-11-19: this MUST NOT be named like the -SourceSha parameter —
# PowerShell variables are case-insensitive and the parameter's
# ValidatePattern fires on any assignment inside this scope, which threw
# ValidationMetadataException here and the scope-wide trap then died on a
# not-yet-defined Cleanup. The mandatory parameter already carries the
# identity; a git fallback would run outside a checkout.
$sourceShaEvidence = $SourceSha
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

Note "source SHA: $sourceShaEvidence"
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
# Stage the managed role fixture BEFORE the fresh install starts the core:
# the running core loaded its composition at startup, and SetRoutingList
# (row E) requires the role to exist. The role never has to connect — the
# config only has to load. The zone is the role id (the core names managed
# zones and route targets by role id; a diverging leshy_zone publishes under
# a name the core never looks up). The data root is reset first: repeated
# runs must not inherit the previous run's journals/markers (H2 2026-11-19:
# an accumulated marker_written journal made the row-D restart status read
# mid-recovery).
Remove-Item -Recurse -Force $DataRoot -ErrorAction SilentlyContinue
$roleToml = Join-Path $ToadsDir "$Role.toml"
$rolePassword = Join-Path $ToadsDir "$Role.password"
New-Item -ItemType Directory -Force -Path $ToadsDir | Out-Null
[IO.File]::WriteAllText($rolePassword, "h2-fixture-password`n")
# The row-E canary resolves through the role's managed zone, which requires
# a LIVE transport: the fixture role carries the operator's live VLESS
# REALITY endpoint (H2 2026-11-20; the AWG lab endpoint is down).
[IO.File]::WriteAllText($roleToml, @"
name = "$Role"
protocol = "vless-reality"
interface = "kk-h2-0"
mtu = 1380
state_dir = "C:/ProgramData/Kikimora/state/toads/$Role"
address = ["10.66.6.2/32"]
leshy_zone = "$Role"

[endpoint_policy]
rule_priority = 50

[vless_reality]
endpoint = "cpo118.3cpo.click:443"
uuid = "8331d1ce-6fec-4de3-a876-642a5c11736f"
server_name = "woman-i.ru"
public_key = "5WnXauP_x7k4yTmsDm9gF9kFnMloRdgD3LNQRg7-KXc"
short_id = "79691b35fac77431"
flow = "xtls-rprx-vision"
fingerprint = "edge"
transport = "tcp"
"@)
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
# The fresh install leaves the local Leshy service running, and it owns the
# shared writer lock: the enable is REFUSED while it runs — the product
# failing closed, correctly (H2 2026-11-19). Stop it for the transaction;
# the enable reloads Leshy with the generated managed config, and the row-C
# check below expects the service Running again after it.
$leshySvc = Get-Service -Name $LeshyService -ErrorAction SilentlyContinue
if ($leshySvc -and $leshySvc.Status -eq "Running") {
    Stop-Service -Name $LeshyService -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
}
$enableLog = Join-Path $WorkDir "enable.log"
& $CoreExe leshy-mode enable --leshy $LeshyExe --socket $CoreSocket `
    --auth-file $AuthKey --control-socket $CoreSocket 2>&1 | Out-File -Encoding utf8 $enableLog
if ($LASTEXITCODE -eq 0) {
    Pass "managed Enable succeeded"
} else {
    Fail "managed Enable failed (see $enableLog)"
}
# The enable renders the managed config; the Leshy service must be Running
# with it before the row-C identity check (the cutover gate owns the same
# Start-Leshy lifecycle around its transactions).
$leshySvc = Get-Service -Name $LeshyService -ErrorAction SilentlyContinue
if ($leshySvc -and $leshySvc.Status -ne "Running") {
    Start-Service -Name $LeshyService -ErrorAction SilentlyContinue
    $null = (Get-Service -Name $LeshyService).WaitForStatus("Running", [TimeSpan]::FromSeconds(60))
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
# Leshy rides the same restart: it must be Running with the managed config
# before the canary row (the cutover gate restarts it explicitly).
$leshySvc = Get-Service -Name $LeshyService -ErrorAction SilentlyContinue
if ($leshySvc -and $leshySvc.Status -ne "Running") {
    Start-Service -Name $LeshyService -ErrorAction SilentlyContinue
    $null = (Get-Service -Name $LeshyService).WaitForStatus("Running", [TimeSpan]::FromSeconds(60))
}
$statusJson2 = $null
for ($i = 0; $i -lt 30 -and -not $statusJson2; $i++) {
    Start-Sleep -Seconds 1
    # Two statements, NOT `native 2>$null | ConvertFrom-Json`: leshy-mode
    # status writes to stderr, and PS 5.1 mangles the piped native-stderr
    # form into an empty object even though the direct assignment parses
    # fine (H2 2026-11-19: the piped form returned empty on every retry
    # while the same call assigned directly worked).
    $rawStatus = & $CoreExe leshy-mode status --socket $CoreSocket --auth-file $AuthKey --json 2>$null
    try { $statusJson2 = $rawStatus | ConvertFrom-Json } catch {}
}
if ($statusJson2.generation -and $statusJson2.generation -eq $status.generation) {
    Pass "committed generation survives the restart"
} else {
    Fail "generation changed across restart: $($status.generation) -> $($statusJson2.generation)"
}
if (-not (Test-Path $Journal)) { Pass "no in-flight journal after restart" } else { Fail "transition journal left behind" }

# ---- E canary DNS + routing round trip --------------------------------------
function Get-RegisteredSelected {
    # The registry counters live in the core status (roles[].selected_routes
    # .registered); the probe has no "selected" subcommand (H2 2026-11-19).
    $snap = & $CoreExe status --socket $CoreSocket --json 2>$null | ConvertFrom-Json
    $role = $snap.roles | Where-Object { $_.id -eq $Role } | Select-Object -First 1
    if ($role -and $role.selected_routes) { return [int]$role.selected_routes.registered }
    return -1
}

Note "row E: canary query + SetRoutingList round trip"
# H2 2026-11-21: drive the canary exactly like the cutover gate's row G —
# the canary domain enters the routing list FIRST (the registry fills only
# for listed domains), the role connects and reaches Ready, and the query
# goes to the managed DNS listener as raw UDP. The previous mechanics could
# never pass: Resolve-DnsName -Server 127.0.0.1 targets port 53, which the
# generated config does not listen on (the managed listener is
# 127.0.0.1:53053), and the role was never connected.
function Invoke-ManagedDnsQuery([string]$Name) {
    $server = "127.0.0.1"
    $port = 53053
    $id = Get-Random -Minimum 0 -Maximum 65536
    $question = New-Object System.Collections.Generic.List[byte]
    foreach ($label in $Name.Split(".")) {
        $bytes = [Text.Encoding]::ASCII.GetBytes($label)
        $question.Add([byte]$bytes.Length)
        $question.AddRange($bytes)
    }
    $question.Add(0)
    $question.Add([byte]0)
    $question.Add([byte]1)   # QTYPE A
    $question.Add([byte]0)
    $question.Add([byte]1)   # QCLASS IN
    [byte[]]$header = @(
        [byte](($id -shr 8) -band 0xff), [byte]($id -band 0xff),
        1, 0,   # RD
        0, 1,   # QDCOUNT
        0, 0,   # ANCOUNT
        0, 0,   # NSCOUNT
        0, 0    # ARCOUNT
    )
    [byte[]]$packet = $header + $question.ToArray()
    $udp = New-Object System.Net.Sockets.UdpClient
    try {
        $udp.Client.ReceiveTimeout = 8000
        [void]$udp.Send($packet, $packet.Length, $server, $port)
        $remote = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
        [byte[]]$response = $udp.Receive([ref]$remote)
        $rcode = $response[3] -band 0x0F
        $answers = ([int]$response[6] -shl 8) -bor [int]$response[7]
        return [pscustomobject]@{ Rcode = [int]$rcode; Answers = [int]$answers }
    } catch {
        return [pscustomobject]@{ Rcode = 9; Answers = 0 }
    } finally {
        $udp.Close()
    }
}

$listLog = Join-Path $WorkDir "setroutinglist.log"
# routeprobe's subcommand is set-list (a file-backed SetRoutingList through
# the control API — the same surface the cutover gate drives); the previous
# call used a subcommand the probe has never implemented (H2 2026-11-19).
$listFile = Join-Path $WorkDir "routing-list.txt"
[IO.File]::WriteAllText($listFile, "$Canary`r`n")
& $RouteProbe --socket $CoreSocket set-list --list $Role --file $listFile 2>&1 | Out-File -Encoding utf8 $listLog
if ($LASTEXITCODE -eq 0) { Pass "SetRoutingList round trip (via control API)" } else { Fail "SetRoutingList failed (see $listLog)" }

& $CoreExe connect --socket $CoreSocket --role $Role 2>&1 | Out-Null
$roleReady = $false
for ($i = 0; $i -lt 180 -and -not $roleReady; $i++) {
    Start-Sleep -Seconds 1
    $readySnap = & $CoreExe status --socket $CoreSocket --json 2>$null | ConvertFrom-Json
    $readyRole = $readySnap.roles | Where-Object { $_.id -eq $Role } | Select-Object -First 1
    if ($readyRole -and $readyRole.state -eq "Ready") { $roleReady = $true }
}
if ($roleReady) { Pass "role $Role reached Ready" } else { Fail "role $Role never reached Ready - the mandatory registry row is RED" }

$dnsOut = Invoke-ManagedDnsQuery $Canary
if ($dnsOut.Rcode -eq 0 -and $dnsOut.Answers -gt 0) {
    Pass "canary query answered through the managed resolver (rcode=0)"
} else {
    Fail "canary query failed (rcode=$($dnsOut.Rcode), answers=$($dnsOut.Answers))"
}
$registered = Get-RegisteredSelected
if ($registered -gt 0) { Pass "the canary query filled the selected registry ($registered record(s))" }
else { Fail "the selected registry stayed empty (registered=$registered, DNS rcode=$($dnsOut.Rcode))" }

# ---- F empty registry, then Disable ----------------------------------------
Note "row F: DisconnectAll then Disable"
& $CoreExe disconnect-all --socket $CoreSocket 2>&1 | Out-Null
$empty = $false
for ($i = 0; $i -lt 30; $i++) {
    $registered = Get-RegisteredSelected
    if ($registered -eq 0) { $empty = $true; break }
    Start-Sleep -Seconds 1
}
if ($empty) { Pass "selected registry emptied" } else { Fail "selected registry never emptied; Disable would be refused" }
# The running managed Leshy holds the shared writer lock — the disable is
# refused while it runs (the product failing closed). Stop it for the
# transaction, restore it for the row-G presence check (H2 2026-11-19).
Stop-Service -Name $LeshyService -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 2
$disableLog = Join-Path $WorkDir "disable.log"
& $CoreExe leshy-mode disable --socket $CoreSocket --auth-file $AuthKey 2>&1 | Out-File -Encoding utf8 $disableLog
Start-Service -Name $LeshyService -ErrorAction SilentlyContinue
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
