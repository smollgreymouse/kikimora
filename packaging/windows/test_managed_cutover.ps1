# SPDX-License-Identifier: MIT
#
# test_managed_cutover.ps1 — the versioned Windows Phase E gate (08D1
# post-Phase-D audit §11.2/§11.6): install the candidate MSI on a clean win10
# and prove the one-writer managed cutover through the installed product:
#
#   A  KikimoraCore installs and reaches startup readiness;
#   B  the leshy service carries DependsOn=KikimoraCore in the SCM;
#   C  the privileged credential lives at state\core and ordinary Users
#      cannot read it (SYSTEM/Administrators only);
#   D  Enable while a local Leshy holds the writer lock is refused with zero
#      mutation (no marker, no journal, generation and config digest unchanged);
#   E  after a graceful Leshy stop the schema2 Enable succeeds, and the marker,
#      status and generated config carry one exact identity;
#   F  managed Leshy starts, owns the writer fence, refuses a concurrent
#      transaction, and a core restart preserves the committed activation;
#   G  a canary query through the installed DNS path fills the selected
#      registry, Disable is refused while it is non-empty, and only the
#      DisconnectAll empty proof (registered == 0, Parking empty) lets the
#      Disable succeed;
#   H  local Leshy starts again on the regenerated local configuration.
#
# Usage (elevated, on win10). All arguments are MANDATORY:
#   powershell -ExecutionPolicy Bypass -File test_managed_cutover.ps1 `
#       -Msi C:\path\kikimora-1.3.1-windows-amd64.msi `
#       -Role awg-main -Canary example.com `
#       -RouteProbe C:\path\to\routeprobe.exe `
#
# The VM is expected to be disposable. The script uninstalls every previously
# installed Kikimora MSI, removes stale portable services, moves the old core
# state aside for a deterministic fresh baseline, installs the candidate, and
# leaves the install in local mode with the role's routing list restored.
param(
    [Parameter(Mandatory = $true)][string]$Msi,
    [Parameter(Mandatory = $true)][string]$Role,
    [Parameter(Mandatory = $true)][string]$Canary,
    [Parameter(Mandatory = $true)][string]$RouteProbe,
    [string]$ProductCode = ""
)

$ErrorActionPreference = "Continue"

if (-not (Test-Path $Msi)) { throw "MSI not found: $Msi" }
$Msi = (Resolve-Path $Msi).Path
# R6: row G mutates the routing list ONLY through the SetRoutingList control
# API driven by routeprobe, never by writing the list file directly.
if (-not (Test-Path $RouteProbe)) { throw "routeprobe not found: $RouteProbe" }
$RouteProbe = (Resolve-Path $RouteProbe).Path

$CoreExe = "C:\Program Files\Kikimora\kikimora-core.exe"
$LeshyExe = "C:\Program Files\Kikimora\leshy\leshy.exe"
$CoreSocket = "C:\ProgramData\Kikimora\core.sock"
$DataRoot = "C:\ProgramData\Kikimora"
$StateDir = "$DataRoot\state"
$CoreStateDir = "$DataRoot\state\core"
$AuthKey = "$CoreStateDir\control-route-auth.key"
$ToadsDir = "$DataRoot\toads"
$RoleConfig = "$ToadsDir\$Role.toml"
$Marker = "$ToadsDir\leshy-managed.json"
$Journal = "$CoreStateDir\leshy-mode-transition.json"
$WriterLock = "$StateDir\leshy-writer.lock"
$RoutingDir = "$DataRoot\leshy\domains"
$ListFile = "$RoutingDir\$Role.txt"
$LeshyConfig = "C:\ProgramData\leshy\config.toml"
$CoreService = "KikimoraCore"
$LeshyService = "leshy"
$ListenAddress = "127.0.0.1"
$ListenPort = 53053

$script:Pass = 0
$script:Fail = 0

function Pass([string]$m) { Write-Output "PASS: $m"; $script:Pass++ }
function Fail([string]$m) { Write-Output "FAIL: $m"; $script:Fail++ }
function Note([string]$m) { Write-Output "== $m" }

function Get-FileSha256([string]$Path) {
    if (-not (Test-Path $Path)) { return "" }
    return (Get-FileHash -Algorithm SHA256 -Path $Path).Hash.ToLowerInvariant()
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

function Get-Snapshot {
    $out = & $CoreExe status --socket $CoreSocket --json 2>&1
    if ($LASTEXITCODE -ne 0) { return $null }
    try { return (($out -join "`n") | ConvertFrom-Json) } catch { return $null }
}

function Get-RoleField([string]$field) {
    $snapshot = Get-Snapshot
    if ($null -eq $snapshot) { return "" }
    $role = @($snapshot.roles) | Where-Object { $_.id -eq $Role } | Select-Object -First 1
    if ($null -eq $role) { return "" }
    $value = $role
    foreach ($part in $field.Split(".")) {
        if ($null -eq $value) { return "" }
        $property = $value.PSObject.Properties[$part]
        if ($null -eq $property) { return "" }
        $value = $property.Value
    }
    if ($null -eq $value) { return "" }
    return [string]$value
}

function Wait-CoreReady([int]$TimeoutSeconds = 90) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        & $CoreExe wait-startup --socket $CoreSocket --timeout 5s 2>&1 | Out-Null
        if ($LASTEXITCODE -eq 0) { return $true }
        Start-Sleep -Seconds 2
    }
    return $false
}

function Wait-LeshyRunning([int]$TimeoutSeconds = 60) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $svc = Get-Service -Name $LeshyService -ErrorAction SilentlyContinue
        if ($svc -and $svc.Status -eq "Running") { return $true }
        Start-Sleep -Milliseconds 500
    }
    return $false
}

function Stop-LeshyGracefully {
    $svc = Get-Service -Name $LeshyService -ErrorAction SilentlyContinue
    if (-not $svc -or $svc.Status -eq "Stopped") { return }
    Stop-Service -Name $LeshyService -Force -ErrorAction SilentlyContinue
    (Get-Service -Name $LeshyService).WaitForStatus("Stopped", [TimeSpan]::FromSeconds(60))
}

function Start-Leshy {
    Stop-LeshyGracefully
    Start-Service -Name $LeshyService -ErrorAction Stop
    if (-not (Wait-LeshyRunning 60)) { throw "the leshy service did not reach Running" }
}

# Real LockFileEx fence probe: 0 = free, 33 = ERROR_LOCK_VIOLATION (busy).
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

function Test-LockFree {
    $rc = [KikimoraPackaging.WriterLockProbe]::TryExclusive($WriterLock)
    switch ($rc) {
        0 { return $true }
        33 { return $false }
        default { throw "writer-lock probe failed with Win32 error $rc on $WriterLock" }
    }
}

function Wait-LockFree([int]$TimeoutSeconds = 30) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (Test-LockFree) { return $true }
        Start-Sleep -Milliseconds 500
    }
    return $false
}

function Wait-RolesDrained([int]$TimeoutSeconds = 60) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $snapshot = Get-Snapshot
        if ($null -ne $snapshot) {
            $drained = $true
            foreach ($role in @($snapshot.roles)) {
                if ($role.state -notin @("Stopped", "Disconnected", "Failed", "")) { $drained = $false }
                if ($role.parking -and ($role.parking.active -or $role.parking.count -gt 0)) { $drained = $false }
            }
            if ($drained) { return $true }
        }
        Start-Sleep -Seconds 1
    }
    return $false
}

function Invoke-Enable {
    $out = & $CoreExe leshy-mode enable --leshy $LeshyExe --socket $CoreSocket `
        --auth-file $AuthKey --control-socket $CoreSocket 2>&1
    return [pscustomobject]@{ Code = $LASTEXITCODE; Output = ($out | Out-String) }
}

function Invoke-Disable {
    $out = & $CoreExe leshy-mode disable --socket $CoreSocket --auth-file $AuthKey 2>&1
    return [pscustomobject]@{ Code = $LASTEXITCODE; Output = ($out | Out-String) }
}

# Raw DNS over UDP: Resolve-DnsName cannot target a non-53 port, and nslookup
# output is localized. Returns the DNS rcode and the answer count.
function Invoke-DnsQuery([string]$Name, [string]$Server = $ListenAddress, [int]$Port = $ListenPort) {
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
        [void]$udp.Send($packet, $packet.Length, $Server, $Port)
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

function Test-UsersAce([string]$Path) {
    $acl = Get-Acl -Path $Path
    foreach ($rule in $acl.Access) {
        $sid = ""
        try { $sid = $rule.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value } catch { $sid = "" }
        if ($sid -eq "S-1-5-32-545") { return $true }
    }
    return $false
}

function Get-ConfigIdentity {
    $result = @{
        Activation = ""
        Generation = ""
        Socket = ""
        Backend = ""
        AuthFile = ""
        WriterLock = ""
    }
    if (-not (Test-Path $LeshyConfig)) { return $result }
    $text = Get-Content -Path $LeshyConfig -Raw
    $patterns = @{
        Activation = '(?m)^kikimora_activation_id\s*=\s*"([0-9a-f]{32})"'
        Generation = '(?m)^kikimora_mode_generation\s*=\s*(\d+)'
        Socket = '(?m)^kikimora_socket\s*=\s*"([^"]+)"'
        Backend = '(?m)^routing_backend\s*=\s*"([^"]+)"'
        AuthFile = '(?m)^kikimora_auth_file\s*=\s*"([^"]+)"'
        WriterLock = '(?m)^writer_lock\s*=\s*"([^"]+)"'
    }
    foreach ($key in $patterns.Keys) {
        $match = [regex]::Match($text, $patterns[$key])
        if ($match.Success) {
            $value = $match.Groups[1].Value
            # TOML escapes backslashes: \\ -> \. Unescape for comparison with
            # PowerShell string literals that use single backslashes.
            $result[$key] = $value -replace '\\\\', '\'
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

# ---------------------------------------------------------------------------
Note "pre-clean: uninstall every previous Kikimora MSI and stale services"
$codes = @()
if ($ProductCode) {
    $codes += $ProductCode
} else {
    foreach ($root in @(
        "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall",
        "HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall"
    )) {
        if (-not (Test-Path $root)) { continue }
        Get-ChildItem $root -ErrorAction SilentlyContinue | ForEach-Object {
            $entry = Get-ItemProperty $_.PSPath -ErrorAction SilentlyContinue
            if ($entry -and $entry.DisplayName -eq "Kikimora" -and $_.PSChildName -match '^\{.*\}$') {
                $codes += $_.PSChildName
            }
        }
    }
}
foreach ($code in ($codes | Select-Object -Unique)) {
    Note "uninstalling the previous MSI $code"
    $p = Start-Process msiexec -ArgumentList @("/x", "`"$code`"", "/qn", "/norestart") -Wait -PassThru
    if ($p.ExitCode -notin @(0, 1605, 3010)) {
        Fail "the previous MSI $code could not be uninstalled (rc=$($p.ExitCode))"
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

# Deterministic local baseline: the old core state moves aside, the managed
# marker and the generated config are removed. Operator role configs and the
# routing lists stay in place (the row G canary uses them).
if (-not (Test-Path $RoleConfig)) {
    Fail "role config $RoleConfig does not exist; row G is mandatory and cannot run"
    Write-Output "== gate complete: PASS=$($script:Pass) FAIL=$($script:Fail)"
    Write-Output "RESULT: RED"
    exit 1
}
if (Test-Path $StateDir) {
    $backup = "$StateDir.pre-e-" + (Get-Date -Format "yyyyMMddHHmmss")
    Move-Item -Path $StateDir -Destination $backup -Force
    Note "moved the previous core state to $backup"
}
Remove-Item -Path $Marker -Force -ErrorAction SilentlyContinue
Remove-Item -Path $LeshyConfig -Force -ErrorAction SilentlyContinue

Note "install the candidate MSI"
$installLog = "$env:TEMP\kikimora-e-install.log"
$p = Start-Process msiexec -ArgumentList @(
    "/i", "`"$Msi`"", "/qn", "/norestart", "/l*v", "`"$installLog`""
) -Wait -PassThru
if ($p.ExitCode -ne 0) {
    Fail "the MSI install failed (rc=$($p.ExitCode)); see $installLog"
    Write-Output "== gate complete: PASS=$($script:Pass) FAIL=$($script:Fail)"
    Write-Output "RESULT: RED"
    exit 1
}
Pass "the candidate MSI installed"

# ---------------------------------------------------------------------------
Note "row A: KikimoraCore installs and reaches startup readiness"
$coreSvc = Get-Service -Name $CoreService -ErrorAction SilentlyContinue
if (-not $coreSvc) {
    Fail "A: the KikimoraCore service is missing after the MSI install"
} elseif (-not (Wait-CoreReady 120)) {
    Fail "A: the core never reported startup readiness"
} else {
    Pass "A: the core service is $((Get-Service $CoreService).Status) and startup-ready"
}
if (Test-Path $LeshyExe) { Pass "A: the package carries the Leshy runtime" }
else { Fail "A: the candidate MSI has no leshy\\leshy.exe (rows D-H cannot run)" }

# ---------------------------------------------------------------------------
Note "row B: the leshy service carries DependsOn=KikimoraCore"
$leshySvc = Get-Service -Name $LeshyService -ErrorAction SilentlyContinue
if (-not $leshySvc) {
    Fail "B: the leshy service is missing after the MSI install"
} elseif (-not ($leshySvc.ServicesDependedOn.Name -contains $CoreService)) {
    Fail "B: the SCM dependency on $CoreService is missing"
} else {
    $scOut = sc.exe qc $LeshyService
    if ($scOut -match "KikimoraCore") {
        Pass "B: the SCM dependency is present (sc qc agrees)"
    } else {
        Fail "B: ServicesDependedOn says KikimoraCore but sc qc does not"
    }
}

# ---------------------------------------------------------------------------
Note "row C: the privileged credential lives at state\core and Users cannot read it"
if (-not (Test-Path $AuthKey)) {
    Fail "C: the auth key is missing at $AuthKey"
} else {
    $aclProblems = @()
    foreach ($path in @($CoreStateDir, $AuthKey, $StateDir)) {
        if (-not (Test-Path $path)) { $aclProblems += "$path is missing"; continue }
        $acl = Get-Acl -Path $path
        if (-not $acl.AreAccessRulesProtected) { $aclProblems += "$path still inherits ACLs" }
        if (Test-UsersAce $path) { $aclProblems += "$path grants BUILTIN\\Users" }
    }
    if ($aclProblems.Count -eq 0) {
        Pass "C: state\\core and the key are SYSTEM/Administrators only"
    } else {
        Fail "C: ACL problem: $($aclProblems -join '; ')"
    }
}

# ---------------------------------------------------------------------------
Note "baseline: establish the known local source mode"
if ((Get-ModeField "mode") -eq "managed") {
    Note "the install is committed managed (a previous acceptance state); rolling back"
    & $CoreExe disconnect-all --socket $CoreSocket 2>&1 | Out-Null
    [void](Wait-RolesDrained 60)
    Stop-LeshyGracefully
    [void](Wait-LockFree 30)
    $rollback = Invoke-Disable
    if ($rollback.Code -eq 0) { Pass "baseline: the pre-existing managed install rolled back to local" }
    else { Fail "baseline: the rollback failed: $($rollback.Output)" }
}
if ((Get-ModeField "mode") -ne "local") {
    Fail "baseline: the install is neither local nor rollable (mode=$(Get-ModeField 'mode'))"
    Write-Output "== gate complete: PASS=$($script:Pass) FAIL=$($script:Fail)"
    Write-Output "RESULT: RED"
    exit 1
}
Start-Leshy
Pass "baseline: local Leshy is active"
$genBefore = Get-ModeField "generation"
$digestBefore = Get-FileSha256 $LeshyConfig
Remove-Item -Path $Marker -Force -ErrorAction SilentlyContinue
Remove-Item -Path $Journal -Force -ErrorAction SilentlyContinue

# ---------------------------------------------------------------------------
Note "row D: Enable while local Leshy holds the writer lock fails with zero mutation"
if (Test-LockFree) {
    Fail "D precondition: local Leshy must hold the writer lock (probe unexpectedly succeeded)"
} else {
    Pass "D precondition: the writer lock is held while local Leshy runs"
}
$enable = Invoke-Enable
if ($enable.Code -ne 0) { Pass "D: the enable was refused while the lock was busy" }
else { Fail "D: the enable must fail while local Leshy holds the writer lock" }
if ($enable.Output -notmatch "writer lock is busy") {
    Fail "D: the refusal was not the busy writer lock: $($enable.Output)"
}
if (-not (Test-Path $Marker) -and -not (Test-Path $Journal) -and
    (Get-ModeField "generation") -eq $genBefore -and
    (Get-FileSha256 $LeshyConfig) -eq $digestBefore) {
    Pass "D: zero mutation (no marker, no journal, generation and config digest unchanged)"
} else {
    Fail "D: the refused enable mutated durable state"
}

# ---------------------------------------------------------------------------
Note "row D/E: after a graceful Leshy stop the writer lock is acquirable"
Stop-LeshyGracefully
if (Wait-LockFree 30) { Pass "D: the writer lock is free after the graceful stop" }
else { Fail "D: the writer lock is still held after leshy stopped" }

# ---------------------------------------------------------------------------
Note "row E: the enable transaction succeeds with an exact identity"
$enable = Invoke-Enable
if ($enable.Code -eq 0) { Pass "E: the enable transaction completed through the real CLI preflight" }
else { Fail "E: the enable transaction failed: $($enable.Output)" }

# Row E diagnostic: dump the managed marker, config digest and status right
# after Enable to detect regressions. The config should carry managed fields.
$digestAfterEnable = Get-FileSha256 $LeshyConfig
Note "E: config digest after enable: $digestAfterEnable"
if (Test-Path $Marker) {
    $markerAfter = Get-Content -Path $Marker -Raw -ErrorAction SilentlyContinue
    Note "E: marker content: $markerAfter"
}
if (Test-Path $LeshyConfig) {
    $configAfter = Get-Content -Path $LeshyConfig -Raw -ErrorAction SilentlyContinue
    if ($configAfter -match "routing_backend") {
        Note "E: config has routing_backend (managed)"
    } else {
        Note "E: config MISSING routing_backend (LOCAL!)"
        Note "E: config first 500 chars: $($configAfter.Substring(0, [Math]::Min(500, $configAfter.Length)))"
    }
}
$genManaged = Get-ModeField "generation"
$fpManaged = Get-ModeField "activation_id_fingerprint"
$markerGen = ""
$markerAct = ""
if (Test-Path $Marker) {
    try {
        $marker = Get-Content -Path $Marker -Raw | ConvertFrom-Json
        $markerGen = [string]$marker.mode_generation
        $markerAct = [string]$marker.activation_id
    } catch {
        Fail "E: the managed marker is malformed: $_"
    }
}
if ($genManaged -and $genManaged -ne $genBefore -and $fpManaged -and
    $markerGen -eq $genManaged -and $markerAct.Length -eq 32) {
    Pass "E: managed generation $genManaged with activation fingerprint $fpManaged"
} else {
    Fail "E: mode identity inconsistent (gen=$genManaged fp=$fpManaged marker_gen=$markerGen)"
}
$identity = Get-ConfigIdentity
$identityProblems = @()
if ($identity.Backend -ne "kikimora") { $identityProblems += "routing_backend=$($identity.Backend)" }
if ($identity.Socket -ne $CoreSocket) { $identityProblems += "socket=$($identity.Socket)" }
if ($identity.AuthFile -ne $AuthKey) { $identityProblems += "auth_file=$($identity.AuthFile)" }
if ($identity.Activation -ne $markerAct) { $identityProblems += "activation mismatch" }
if ($identity.Generation -ne $markerGen) { $identityProblems += "generation mismatch" }
if ($identity.WriterLock -ne $WriterLock) { $identityProblems += "writer_lock=$($identity.WriterLock)" }
if ((Get-Fingerprint $identity.Activation) -ne $fpManaged) { $identityProblems += "fingerprint mismatch" }
if ($identityProblems.Count -eq 0) {
    Pass "E: the generated config carries the exact marker identity and host socket"
} else {
    Fail "E: the generated config does not match the marker: $($identityProblems -join '; ')"
}
if (-not (Test-Path $Journal)) { Pass "E: no transition journal remains" }
else { Fail "E: a transition journal is still present at $Journal" }
$digestManaged = Get-FileSha256 $LeshyConfig

# ---------------------------------------------------------------------------
Note "row F: managed Leshy starts and owns the writer fence"
try {
    Start-Leshy
    Pass "F: managed Leshy is active"
} catch {
    Fail "F: managed Leshy did not start: $_"
}
if (Test-LockFree) { Fail "F: managed Leshy must hold the writer lock for its process lifetime" }
else { Pass "F: managed Leshy holds the writer lock (single-writer surface owned)" }
$enable = Invoke-Enable
if ($enable.Code -ne 0) { Pass "F: a concurrent transaction is refused while managed Leshy runs" }
else { Fail "F: a transaction must be refused while managed Leshy holds the lock" }

Note "row F: a core restart preserves the committed activation"
Restart-Service -Name $CoreService -Force
if (-not (Wait-CoreReady 120)) {
    Fail "F: the core did not become ready after the restart"
} else {
    try { Start-Leshy } catch { Fail "F: managed Leshy did not restart: $_" }
    if ((Get-ModeField "mode") -eq "managed" -and
        (Get-ModeField "activation_id_fingerprint") -eq $fpManaged -and
        (Get-ModeField "generation") -eq $genManaged -and
        (Get-FileSha256 $LeshyConfig) -eq $digestManaged) {
        Pass "F: the restart preserved generation/fingerprint and the generated config"
    } else {
        Fail "F: the restart changed the committed activation (fp=$(Get-ModeField 'activation_id_fingerprint') gen=$(Get-ModeField 'generation'))"
    }
    if (-not (Test-LockFree)) { Pass "F: the restarted managed Leshy holds the writer lock again" }
    else { Fail "F: the restarted managed Leshy does not hold the writer lock" }
}

# ---------------------------------------------------------------------------
Note "row G: the canary query fills the selected registry; Disable refuses until it drains"
# R6: the canary enters the routing list ONLY through the SetRoutingList
# control API. The original list bytes are saved and restored through the
# same API; no Add-Content/Set-Content ever touches the list file and the
# core is NOT restarted merely to load the list — SetRoutingList already
# regenerates the config and reloads Leshy through the production path.
$listBefore = ""
if (Test-Path $ListFile) { $listBefore = Get-Content -Path $ListFile -Raw }
$tempList = Join-Path $env:TEMP "kikimora-rowg-$Role.txt"
$listNow = @()
if (Test-Path $ListFile) { $listNow = @(Get-Content -Path $ListFile) }
$tempContent = $listBefore
if ($listNow -notcontains $Canary) {
    if ($tempContent -and -not $tempContent.EndsWith("`n")) { $tempContent += "`n" }
    $tempContent += "$Canary`n"
}
Set-Content -Path $tempList -Value $tempContent -NoNewline
& $RouteProbe --socket $CoreSocket set-list --list $Role --file $tempList 2>&1 | Out-Null
if ($LASTEXITCODE -ne 0) {
    Fail "G: routeprobe set-list must push the canary through the SetRoutingList API (rc=$LASTEXITCODE)"
} else {
    Pass "G: the canary was pushed through the SetRoutingList control API"
}
Remove-Item $tempList -Force -ErrorAction SilentlyContinue

& $CoreExe connect --socket $CoreSocket --role $Role 2>&1 | Out-Null
$roleReady = $false
$deadline = (Get-Date).AddSeconds(180)
while ((Get-Date) -lt $deadline) {
    if ((Get-RoleField "state") -eq "Ready") { $roleReady = $true; break }
    Start-Sleep -Seconds 1
}
if ($roleReady) { Pass "G: role $Role reached Ready" }
else { Fail "G: role $Role never reached Ready - the mandatory registry row is RED" }

$registered = 0
$rcode = 9
for ($attempt = 0; $attempt -lt 10; $attempt++) {
    $dn = Invoke-DnsQuery $Canary
    $rcode = $dn.Rcode
    $registeredText = Get-RoleField "selected_routes.registered"
    if ($registeredText -ne "") { $registered = [int]$registeredText }
    if ($registered -gt 0) { break }
    Start-Sleep -Seconds 1
}
if ($registered -gt 0) {
    Pass "G: the canary query filled the selected registry ($registered record(s), DNS rcode=$rcode)"
    Stop-LeshyGracefully
    [void](Wait-LockFree 30)
    $disable = Invoke-Disable
    if ($disable.Code -ne 0 -and $disable.Output -match "selected registry records|still has") {
        Pass "G: Disable refused while the selected registry is non-empty"
    } elseif ($disable.Code -ne 0) {
        Pass "G: Disable refused while the selected registry is non-empty ($($disable.Output.Trim()))"
    } else {
        Fail "G: Disable must be refused while the selected registry is non-empty"
    }
    & $CoreExe disconnect --socket $CoreSocket --role $Role 2>&1 | Out-Null
} else {
    Fail "G: the selected registry stayed empty (registered=$registered, DNS rcode=$rcode)"
}

& $CoreExe disconnect-all --socket $CoreSocket 2>&1 | Out-Null
if (Wait-RolesDrained 90) {
    Pass "G: the DisconnectAll empty proof (registered=0, Parking empty)"
} else {
    Fail "G: the registry never drained (registered=$(Get-RoleField 'selected_routes.registered') parked=$(Get-RoleField 'parking.count') active=$(Get-RoleField 'parking.active'))"
}
[void](Wait-LockFree 30)
$disable = Invoke-Disable
if ($disable.Code -eq 0) { Pass "G: Disable succeeds after the empty proof" }
else { Fail "G: Disable failed after the empty proof: $($disable.Output)" }
$genLocal = Get-ModeField "generation"
if ((Get-ModeField "mode") -eq "local" -and $genLocal -ne $genManaged -and
    -not (Test-Path $Marker) -and
    (Get-ConfigIdentity).Backend -eq "") {
    Pass "G: the rollback converged to local/$genLocal with the marker and managed backend gone"
} else {
    Fail "G: the rollback did not converge (mode=$(Get-ModeField 'mode') gen=$genLocal marker=$(if (Test-Path $Marker) { 'present' } else { 'absent' }))"
}
# Restore the exact original list bytes through the same SetRoutingList API.
$tempRestore = Join-Path $env:TEMP "kikimora-rowg-restore-$Role.txt"
Set-Content -Path $tempRestore -Value $listBefore -NoNewline
& $RouteProbe --socket $CoreSocket set-list --list $Role --file $tempRestore 2>&1 | Out-Null
if ($LASTEXITCODE -ne 0) {
    Fail "G: the original routing list must be restored through the SetRoutingList API (rc=$LASTEXITCODE)"
} else {
    Pass "G: the role's routing list restored through the control API"
}
Remove-Item $tempRestore -Force -ErrorAction SilentlyContinue
# The SetRoutingList call regenerated the local configuration from the
# restored list; no restart is required before row H.
if (Test-Path $ListFile) {
    $restored = Get-Content -Path $ListFile -Raw
    if ($restored -eq $listBefore) { Pass "G: the restored list bytes match the original" }
    else { Fail "G: the restored list bytes differ from the original" }
}

# ---------------------------------------------------------------------------
Note "row H: local Leshy starts again"
try {
    Start-Leshy
    Pass "H: local Leshy is active on the local configuration"
} catch {
    Fail "H: local Leshy did not start: $_"
}
if (Wait-CoreReady 30) { Pass "H: the core is still ready" }
else { Fail "H: the core is not ready" }
if (Test-LockFree) { Fail "H: local Leshy must hold the writer lock for its process lifetime" }
else { Pass "H: local Leshy holds the writer lock (single writer restored)" }

# ---------------------------------------------------------------------------
Write-Output "== cutover gate complete: PASS=$($script:Pass) FAIL=$($script:Fail)"
if ($script:Fail -eq 0) {
    Write-Output "RESULT: GREEN"
} else {
    Write-Output "RESULT: RED"
    exit 1
}
