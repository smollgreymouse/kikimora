# SPDX-License-Identifier: MIT
#
# test_dual_profile.ps1 — PROD4 Windows native companion gate
# (2026-11-16 production-readiness audit §6).
#
# It does not duplicate the deterministic netns negatives. It proves on the
# installed native service, with two real lab roles already staged:
#
#   D1  both roles reach Ready simultaneously
#   D2  profile both changes exactly the desired role set
#   D3  rapid both -> primary-only -> both keeps the latest intent
#   D4  the default-route owner stays compatible with the active profile
#   D5  disconnecting one role leaves the sibling Ready and desired
#   D6  a service restart restores the active profile and both desired roles
#
# Prerequisites (staged by the lab before this gate):
#   * the candidate MSI is installed and KikimoraCore is running;
#   * <DataRoot>\toads contains the two role TOMLs with profiles
#     ["default","both","primary-only"] / ["default","both","secondary-only"];
#   * both roles reach Ready on this host (real transport acceptance is H2).
#
# Usage (elevated, on win10):
#   powershell -ExecutionPolicy Bypass -File test_dual_profile.ps1 `
#       -PrimaryRole primary -SecondaryRole secondary `
#       -RouteProbe C:\path\routeprobe.exe
param(
    [Parameter(Mandatory = $true)][string]$PrimaryRole,
    [Parameter(Mandatory = $true)][string]$SecondaryRole,
    [Parameter(Mandatory = $true)][string]$RouteProbe
)

$ErrorActionPreference = "Stop"

$CoreExe = "C:\Program Files\Kikimora\kikimora-core.exe"
$DataRoot = "C:\ProgramData\Kikimora"
$CoreSocket = "$DataRoot\core.sock"
$CoreStateDir = "$DataRoot\state\core"
$AuthKey = "$CoreStateDir\control-route-auth.key"
$ToadsDir = "$DataRoot\toads"
$Service = "KikimoraCore"

if (-not (Test-Path $CoreExe)) { throw "missing $CoreExe" }
if (-not (Test-Path $RouteProbe)) { throw "missing routeprobe: $RouteProbe" }
$RouteProbe = (Resolve-Path $RouteProbe).Path

function Pass([string]$Message) { Write-Host "PASS: $Message" }
function Fail([string]$Message) { Write-Host "FAIL: $Message" -ForegroundColor Red; exit 1 }
function Note([string]$Message) { Write-Host "== $Message" }

function CoreStatus {
    $raw = & $CoreExe status --socket $CoreSocket --json 2>$null
    if ($LASTEXITCODE -ne 0 -or -not $raw) { return $null }
    return ($raw | ConvertFrom-Json)
}

function RoleField([string]$Role, [string]$Field) {
    $snapshot = CoreStatus
    if (-not $snapshot) { return "" }
    $entry = $snapshot.roles | Where-Object { $_.id -eq $Role } | Select-Object -First 1
    if (-not $entry) { return "" }
    $value = $entry
    foreach ($part in $Field.Split(".")) {
        if ($null -eq $value) { return "" }
        $value = $value.$part
    }
    if ($null -eq $value) { return "" }
    return [string]$value
}

function WaitRole([string]$Role, [string]$State, [int]$Seconds = 90) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        if ((RoleField $Role "state") -eq $State) { return }
        Start-Sleep -Milliseconds 500
    }
    Fail "role $Role never reached $State (state=$(RoleField $Role 'state'), error=$(RoleField $Role 'last_error'))"
}

function WaitNotReady([string]$Role, [int]$Seconds = 90) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        if ((RoleField $Role "state") -ne "Ready") { return }
        Start-Sleep -Milliseconds 500
    }
    Fail "role $Role stayed Ready"
}

function UseProfile([string]$Name) {
    & $CoreExe profiles use --socket $CoreSocket $Name > $null 2>&1
    return $LASTEXITCODE
}

function Bypass([string]$Mode) {
    $params = @{ mode = $Mode } | ConvertTo-Json -Compress
    & $RouteProbe --socket $CoreSocket --auth-file $AuthKey call --method SetBypassMode --params $params > $null 2>&1
    return $LASTEXITCODE
}

function EnsurePrefix([string]$Zone, [string]$Prefix, [string]$Activation, [string]$Generation) {
    & $RouteProbe --socket $CoreSocket --auth-file $AuthKey ensure --zone $Zone --prefix $Prefix `
        --activation $Activation --generation $Generation > $null 2>&1
    return $LASTEXITCODE
}

Note "artifact identities"
foreach ($path in @($CoreExe, (Join-Path $ToadsDir "$PrimaryRole.toml"), (Join-Path $ToadsDir "$SecondaryRole.toml"))) {
    if (Test-Path $path) {
        Note "$path sha256: $((Get-FileHash -Algorithm SHA256 -Path $path).Hash.ToLowerInvariant())"
    }
}
$snapshot = CoreStatus
if (-not $snapshot) { Fail "the core status endpoint is unavailable" }
$markerPath = Join-Path $ToadsDir "leshy-managed.json"
if (-not (Test-Path $markerPath)) { Fail "managed mode is not active (no schema2 marker)" }
$marker = Get-Content $markerPath -Raw | ConvertFrom-Json
$activation = [string]$marker.activation_id
$generation = [string]$marker.mode_generation

Note "D1: two roles Ready simultaneously"
WaitRole $PrimaryRole "Ready"
WaitRole $SecondaryRole "Ready"
Pass "D1: $PrimaryRole and $SecondaryRole are Ready"

Note "D2: profile both selects exactly the two roles"
if ((UseProfile "both") -ne 0) { Fail "profiles use both failed" }
WaitRole $PrimaryRole "Ready"
WaitRole $SecondaryRole "Ready"
if ((RoleField $PrimaryRole "desired_enabled") -ne "True") { Fail "D2: $PrimaryRole is not desired" }
if ((RoleField $SecondaryRole "desired_enabled") -ne "True") { Fail "D2: $SecondaryRole is not desired" }
Pass "D2: profile both enables both roles"

Note "D3: rapid both -> primary-only -> both keeps the latest intent"
$beforeOperation = RoleField $SecondaryRole "operation"
if ((UseProfile "primary-only") -ne 0) { Fail "profiles use primary-only failed" }
if ((UseProfile "both") -ne 0) { Fail "profiles use both failed" }
$deadline = (Get-Date).AddSeconds(90)
while ((Get-Date) -lt $deadline) {
    if ((RoleField $SecondaryRole "state") -eq "Ready" -and
        [uint64](RoleField $SecondaryRole "operation") -gt [uint64]$beforeOperation) { break }
    Start-Sleep -Milliseconds 500
}
if ((RoleField $SecondaryRole "state") -ne "Ready") { Fail "D3: secondary did not converge to Ready" }
if ((RoleField $SecondaryRole "desired_enabled") -ne "True") { Fail "D3: the newest intent did not win" }
if ((CoreStatus).active_profile -ne "both") { Fail "D3: the active profile is not the newest intent" }
$primaryV4 = EnsurePrefix "primary" "203.0.113.10/32" $activation $generation
$secondaryV4 = EnsurePrefix "secondary" "203.0.113.20/32" $activation $generation
if ($primaryV4 -ne 0 -or $secondaryV4 -ne 0) { Fail "D3: the restarted composition did not accept both ownership rows" }
Pass "D3: the rapid switch converged with both ownership rows"

Note "D4: the default route stays compatible with the active profile"
if ((Bypass $SecondaryRole) -ne 0) { Fail "D4: setting the default route to $SecondaryRole failed" }
if ((UseProfile "primary-only") -eq 0) { Fail "D4: a profile that disables the default-route role was accepted" }
if ((CoreStatus).active_profile -ne "both") { Fail "D4: the rejected switch moved the active profile" }
if ((Bypass "direct") -ne 0) { Fail "D4: resetting the default route to direct failed" }
Pass "D4: the core rejected the incompatible profile switch"

Note "D5: one role disconnect does not break the sibling"
$siblingBefore = RoleField $SecondaryRole "generation"
& $CoreExe disconnect --role $PrimaryRole --socket $CoreSocket > $null 2>&1
WaitNotReady $PrimaryRole
if ((RoleField $SecondaryRole "state") -ne "Ready") { Fail "D5: the sibling left Ready" }
if ((RoleField $SecondaryRole "desired_enabled") -ne "True") { Fail "D5: the sibling lost its desired state" }
& $CoreExe connect --role $PrimaryRole --socket $CoreSocket > $null 2>&1
WaitRole $PrimaryRole "Ready"
Pass "D5: the sibling stayed Ready and desired"

Note "D6: a service restart restores the active profile and both desired roles"
Restart-Service $Service -Force
& $CoreExe wait-startup --socket $CoreSocket --timeout 90s > $null 2>&1
if ($LASTEXITCODE -ne 0) { Fail "D6: the restarted service never reached startup readiness" }
WaitRole $PrimaryRole "Ready" 120
WaitRole $SecondaryRole "Ready" 120
$after = CoreStatus
if ($after.active_profile -ne "both") { Fail "D6: the active profile was not restored" }
if ((RoleField $PrimaryRole "desired_enabled") -ne "True" -or (RoleField $SecondaryRole "desired_enabled") -ne "True") {
    Fail "D6: a desired role was not restored"
}
Pass "D6: restart restored profile both and both desired roles"

Write-Host ""
Write-Host "DUAL-PROFILE WINDOWS GATE (PROD4): PASS"
