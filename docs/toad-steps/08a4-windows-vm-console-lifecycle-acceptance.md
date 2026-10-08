# Toad step 08A.4 — Windows VM console/runtime lifecycle parity acceptance

Status: **PLANNED / BLOCKED UNTIL NATIVE WINDOWS NETWORKING IS IMPLEMENTED**.

This is the Windows production-networking parity gate corresponding to
docs/toad-steps/08a2-vm-console-lifecycle-acceptance.md.

It must not be executed against the current Windows FakeCore frontend. Windows
remains FakeCore-only today. This packet becomes executable only after the
native Windows routing/TUN/DNS/control-service substrate exists.

## 0. Activation prerequisites

Do not start until all are true:

1. kikimora-core.exe and kikimora-toad.exe use real Windows networking backends.
2. Kikimora owns native Windows TUN lifecycle (Wintun or another accepted backend).
3. A native route/default-path observer reports real interface/address/gateway changes.
4. A native route manager implements endpoint exceptions, parking/fail-closed and cleanup.
5. Windows DNS ownership/recovery semantics exist.
6. Windows local API authentication is real; current peer_unsupported.go allow-all is forbidden.
7. Local API transport is local-only and ACL-protected.
8. AWG and OpenConnect have real Windows backends.
9. A real installer exists; packaging/windows/stage.ps1 alone is insufficient.
10. Service Control Manager recovery restarts an unexpectedly killed core.
11. Desired state is crash-safe and outside the executable payload.
12. kk status --json exposes generation, PID, interface identity, route readiness,
    validated underlay epoch, endpoint state and recovery state.
13. `08-dual-stack-dns-route-stability.md` has deterministic Windows model coverage,
    including family-specific readiness and zero-mutation steady-state reconcile.

### Canonical Windows acceptance layout

~~~text
Service name:       KikimoraCore
Install directory:  C:\Program Files\Kikimora
CLI:                C:\Program Files\Kikimora\kk.exe
Core:               C:\Program Files\Kikimora\kikimora-core.exe
Toad:               C:\Program Files\Kikimora\kikimora-toad.exe
Persistent state:   C:\ProgramData\Kikimora
Toad configs:       C:\ProgramData\Kikimora\toads
Secrets:            C:\ProgramData\Kikimora\secrets
Lab evidence:       C:\kikimora-lab\08a4
~~~

Secrets must not appear in Program Files, MSI payload, PowerShell history,
Event Log or kk status --json.

# 1. VM definition

Use a disposable Windows VM:

~~~text
Guest OS:        supported Windows 11 x64
vCPU:            4
RAM:             4 GiB minimum
Disk:            64 GiB
NIC count:       one main NIC
NIC attachment:  Bridged Adapter
NIC cable:       connected
Addressing:      DHCP
Snapshot:        clean OS + OpenSSH before Kikimora
~~~

Prefer an in-box VirtualBox NIC driver for first acceptance, for example Intel
PRO/1000 MT Desktop. Add virtio-net later as a separate compatibility run.

Record:

~~~powershell
winver
Get-ComputerInfo | Select-Object WindowsProductName, WindowsVersion, OsBuildNumber
Get-NetAdapter | Format-Table ifIndex, Name, InterfaceDescription, Status, MacAddress, LinkSpeed
Get-NetIPConfiguration
Get-NetRoute -AddressFamily IPv4 | Sort-Object DestinationPrefix, RouteMetric
~~~

Disable unrelated VPN clients and extra VM adapters.

# 2. Remote guest control

Run elevated PowerShell:

~~~powershell
Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0
Set-Service -Name sshd -StartupType Automatic
Start-Service sshd
if (-not (Get-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -ErrorAction SilentlyContinue)) { New-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -DisplayName 'OpenSSH Server (sshd)' -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 22 }
~~~

Install the test public key using standard Windows OpenSSH rules. Record guest
SSH host key and DHCP address.

Hypervisor actions are operator-assisted. If the VirtualBox host has no remote
control, the operator performs Pause, Resume, Save State, Reset, Power Off,
Start and Cable Connected changes in the GUI.

# 3. Acceptance environment

~~~powershell
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$Lab = 'C:\kikimora-lab\08a4'
$Install = 'C:\Program Files\Kikimora'
$ProgramData = 'C:\ProgramData\Kikimora'
$Kk = Join-Path $Install 'kk.exe'
$Core = Join-Path $Install 'kikimora-core.exe'
$Toad = Join-Path $Install 'kikimora-toad.exe'
$CoreService = 'KikimoraCore'
New-Item -ItemType Directory -Force -Path $Lab | Out-Null
$DefaultRoute = Get-NetRoute -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' | Sort-Object RouteMetric, InterfaceMetric | Select-Object -First 1
$PhysicalIfIndex = $DefaultRoute.InterfaceIndex
$Physical = Get-NetAdapter -InterfaceIndex $PhysicalIfIndex
$Physical | Format-List * | Out-File "$Lab\physical-adapter.txt"
Get-NetIPConfiguration -InterfaceIndex $PhysicalIfIndex | Format-List * | Out-File "$Lab\physical-ip-before.txt"
~~~

Rediscover the physical default path after DHCP/link changes. Changed DHCP
address is not a failure.

# 4. Installer gate

Build from exact Git HEAD and record commit, MSI filename, SHA-256, version and architecture.

~~~powershell
Get-FileHash .\kikimora-<VERSION>-windows-amd64.msi -Algorithm SHA256 | Format-List | Out-File "$Lab\artifact-sha256.txt"
$Msi = (Resolve-Path '.\kikimora-<VERSION>-windows-amd64.msi').Path
$Args = @('/i', $Msi, '/qn', '/l*v', "$Lab\install.log")
Start-Process msiexec.exe -Wait -NoNewWindow -ArgumentList $Args
Get-Service $CoreService
sc.exe qc $CoreService
& $Kk version
& $Kk status --json | Tee-Object "$Lab\fresh-install-status.json"
~~~

Fresh install must not silently set VPN desired=true, install test credentials
or use FakeCore.

# 5. Baseline helpers

~~~powershell
function Save-KikimoraSnapshot([string] $Name) {
    & $Kk status --json | Tee-Object "$Lab\$Name.json" | ConvertFrom-Json
}

function Wait-KikimoraReady([int] $TimeoutSeconds = 120) {
    $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        $S = & $Kk status --json | ConvertFrom-Json
        $Bad = @($S.roles | Where-Object { $_.desired_enabled -and ($_.state -ne 'Ready' -or -not $_.route_ready -or $_.validated_underlay_epoch -ne $S.underlay.epoch) })
        if ($S.aggregate_state -eq 'Ready' -and $Bad.Count -eq 0) { return $S }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $Deadline)
    throw "Kikimora did not converge to Ready/current epoch"
}
~~~

~~~powershell
& $Kk connect --role awg
& $Kk connect --role oc
$Baseline = Wait-KikimoraReady
$Baseline | ConvertTo-Json -Depth 20 | Set-Content "$Lab\baseline.json"
& $Kk watch --json | Tee-Object "$Lab\watch.jsonl"
~~~

Capture desired state, state/reason, generation, PID, interface identity,
route_ready, validated epoch, endpoint epoch/state, parking, publication,
recovery and session counters.

## 5.1 Mandatory dual-stack / DNS / route-stability baseline

Execute `08-dual-stack-dns-route-stability.md` before the normal application
probes and again after lifecycle recovery/soak.

At minimum record both address families separately:

~~~powershell
Get-NetIPConfiguration | Out-File "$Lab\dualstack-ip.txt"
Get-NetRoute -AddressFamily IPv4 | Sort-Object DestinationPrefix,RouteMetric | Out-File "$Lab\routes-v4.txt"
Get-NetRoute -AddressFamily IPv6 | Sort-Object DestinationPrefix,RouteMetric | Out-File "$Lab\routes-v6.txt"
Get-DnsClientServerAddress | Format-List * | Out-File "$Lab\dns-servers.txt"
Get-DnsClientNrptPolicy -Effective -ErrorAction SilentlyContinue | Format-List * | Out-File "$Lab\dns-nrpt.txt"
Resolve-DnsName google.com -Type A | Out-File "$Lab\dns-a.txt"
Resolve-DnsName google.com -Type AAAA | Out-File "$Lab\dns-aaaa.txt"
~~~

Requirements:

- no accidental managed `::/1 + 8000::/1` or IPv4 split-default pair exists;
- if public IPv6 is available, an AAAA address has a sane effective route/source
  and a real IPv6 data-plane probe succeeds;
- if public IPv6 is unavailable, Kikimora must not claim IPv6 Ready or capture the
  family through a dead managed route; managed DNS/fail-closed behavior must fail
  promptly rather than create a connect-timeout blackhole;
- transport endpoint exceptions win over any unrelated external VPN default;
- DNS owner/scope is deterministic before and after recovery;
- replaying identical desired/classification state after convergence produces no
  further route/rule/DNS mutations.

Do not accept IPv4-only `curl -4` success as evidence that dual-stack policy is
healthy.

# 6. Real application probes

Read interface indices from the canonical snapshot:

~~~powershell
$S = Wait-KikimoraReady
$Awg = $S.roles | Where-Object id -eq 'awg'
$Oc = $S.roles | Where-Object id -eq 'oc'
$AwgIfIndex = [int]$Awg.interface.ifindex
$OcIfIndex = [int]$Oc.interface.ifindex
~~~

Helper:

~~~powershell
function Invoke-KikimoraHttpsProbe {
    param([int]$InterfaceIndex,[string]$HostName,[string]$Path,[int[]]$ExpectedStatus,[string]$Label)
    $Ip = (Resolve-DnsName $HostName -Type A -ErrorAction Stop | Where-Object Type -eq 'A' | Select-Object -First 1 -ExpandProperty IPAddress)
    $Prefix = "$Ip/32"
    $Existing = @(Get-NetRoute -DestinationPrefix $Prefix -ErrorAction SilentlyContinue)
    if ($Existing.Count -ne 0) { throw "Refusing to replace existing route for $Prefix" }
    try {
        New-NetRoute -DestinationPrefix $Prefix -InterfaceIndex $InterfaceIndex -RouteMetric 3 -PolicyStore ActiveStore | Out-Null
        $ResolveArg = ("{0}:443:{1}" -f $HostName,$Ip)
        $Out = & curl.exe -4 -sS --noproxy '*' --connect-timeout 10 --max-time 30 --resolve $ResolveArg -o NUL -w '%{http_code} %{remote_ip} %{size_download}' "https://$HostName$Path"
        $Parts = $Out -split ' '
        $Status = [int]$Parts[0]
        $Remote = $Parts[1]
        if ($Remote -ne $Ip -or $ExpectedStatus -notcontains $Status) { throw "$Label failed: $Out" }
        "$Label=$Out" | Add-Content "$Lab\application-probes.txt"
    }
    finally {
        Get-NetRoute -DestinationPrefix $Prefix -InterfaceIndex $InterfaceIndex -ErrorAction SilentlyContinue | Where-Object RouteMetric -eq 3 | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue
    }
}
~~~

Required probes:

~~~powershell
Invoke-KikimoraHttpsProbe $AwgIfIndex 'telegram.org' '/' @(200) 'awg_telegram'
Invoke-KikimoraHttpsProbe $AwgIfIndex 'chatgpt.com' '/cdn-cgi/trace' @(200) 'awg_chatgpt'
Invoke-KikimoraHttpsProbe $AwgIfIndex 'api.openai.com' '/v1/models' @(401) 'awg_openai_api'
Invoke-KikimoraHttpsProbe $OcIfIndex 'gitlab.sca.ad-tech.ru' '/' @(200,201,202,204,301,302,303,307,308) 'oc_internal_gitlab'
~~~

# 7. Process/service recovery

After each case return to Ready/current epoch and rerun probes.

## Core service restart

~~~powershell
Restart-Service $CoreService -Force
Wait-KikimoraReady
~~~

## Kill AWG Toad

~~~powershell
$Before = Wait-KikimoraReady
$Role = $Before.roles | Where-Object id -eq 'awg'
Stop-Process -Id ([int]$Role.pid) -Force
Wait-KikimoraReady
~~~

OpenConnect PID/generation/interface must remain stable. Repeat independently
for role oc.

## Hard-kill core

~~~powershell
$CorePid = (Get-CimInstance Win32_Service -Filter "Name='$CoreService'").ProcessId
Stop-Process -Id $CorePid -Force
$Deadline = (Get-Date).AddSeconds(60)
do { Start-Sleep -Milliseconds 500; $Svc = Get-Service $CoreService } until ($Svc.Status -eq 'Running' -or (Get-Date) -gt $Deadline)
if ($Svc.Status -ne 'Running') { throw 'SCM did not restart KikimoraCore' }
Wait-KikimoraReady
~~~

# 8. Underlay lifecycle

Windows has no NetworkManager; parity is route/address/link convergence.

## DHCP renewal

~~~powershell
ipconfig /release
Start-Sleep -Seconds 2
ipconfig /renew
Wait-KikimoraReady
~~~

## Guest adapter off/on

~~~powershell
$Physical = Get-NetAdapter -InterfaceIndex $PhysicalIfIndex
Disable-NetAdapter -Name $Physical.Name -Confirm:$false
Start-Sleep -Seconds 10
Enable-NetAdapter -Name $Physical.Name -Confirm:$false
Wait-KikimoraReady
~~~

## VirtualBox cable off/on

Operator clears Cable Connected for 15–30 seconds, then enables it. Require no
false Ready while physical path is absent, automatic recovery, current validated
epoch and real probes PASS.

# 9. Sleep, hibernate, reboot, cold boot

## Sleep capability

~~~powershell
powercfg /a | Tee-Object "$Lab\powercfg-a.txt"
~~~

Unsupported sleep in the chosen VM is a capability blocker, not PASS.

## OS suspend

~~~powershell
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class NativeSleep {
    [DllImport("powrprof.dll", SetLastError=true)]
    public static extern bool SetSuspendState(bool hibernate, bool forceCritical, bool disableWakeEvent);
}
'@

(& $Kk status --json) | Set-Content "$Lab\suspend-before.json"
(Get-CimInstance Win32_OperatingSystem).LastBootUpTime | Out-File "$Lab\suspend-before-boot.txt"
[NativeSleep]::SetSuspendState($false, $false, $false)
~~~

Operator wakes VM. Immediately capture:

~~~powershell
Get-NetAdapter | Format-Table ifIndex, Name, Status, MacAddress, LinkSpeed | Out-File "$Lab\suspend-post-adapters.txt"
Get-NetIPConfiguration | Format-List * | Out-File "$Lab\suspend-post-ip.txt"
Wait-KikimoraReady | ConvertTo-Json -Depth 20 | Set-Content "$Lab\suspend-after.json"
~~~

Boot timestamp remains unchanged. Mandatory regression: a role that entered
WaitingForUnderlay must automatically re-enter recovery when underlay returns.
Run at least two suspend/resume cycles.

## Hibernate

~~~powershell
powercfg /hibernate on
(& $Kk status --json) | Set-Content "$Lab\hibernate-before.json"
shutdown.exe /h
~~~

Wake/start and require service, desired state, roles and probes to converge.

## Reboot

~~~powershell
(& $Kk status --json) | Set-Content "$Lab\reboot-before.json"
(Get-CimInstance Win32_OperatingSystem).LastBootUpTime | Out-File "$Lab\reboot-before-boot.txt"
Restart-Computer -Force
~~~

Do not log in. Acceptance continues over SSH. New boot timestamp, automatic
service startup, desired state preservation and probes are mandatory.

## Cold boot

~~~powershell
(& $Kk status --json) | Set-Content "$Lab\cold-before.json"
Stop-Computer -Force
~~~

Operator starts VM. Do not log in. Apply reboot assertions.

# 10. Hypervisor lifecycle

## Pause / Resume

Save snapshot and boot timestamp. Operator pauses about 30 seconds, resumes.
Boot timestamp must remain unchanged; Ready/current epoch, no accumulation and
probes must pass.

## Save State / Start

Save pre-state. Operator chooses Save the machine state, waits for stop, starts
VM. Boot timestamp remains unchanged and convergence must pass.

## Hard Reset

Save pre-state and boot timestamp. Operator chooses Reset. Require new boot
timestamp, automatic core startup, persisted desired state and probes PASS.

If VirtualBox itself crashes during Reset, record it and classify next start as
the stricter hypervisor-crash/cold-restart case.

## Hypervisor process crash

Recommended after ordinary Reset: save evidence, terminate only VirtualBox VM
process, restart VirtualBox, start VM, do not log in, assert all state/probes.

# 11. No-accumulation assertions

After repeated events require:

~~~text
1 kikimora-core.exe process
1 kikimora-toad.exe per desired role
1 owned TUN per desired role
0 duplicate endpoint exception routes
0 duplicate parking/fail-closed routes
0 stale old-generation publication
0 unexplained route/rule/DNS mutation after steady-state convergence
0 staging paths in canonical runtime
~~~

~~~powershell
$CoreCount = @(Get-Process kikimora-core -ErrorAction SilentlyContinue).Count
$ToadCount = @(Get-Process kikimora-toad -ErrorAction SilentlyContinue).Count
if ($CoreCount -ne 1) { throw "core process count=$CoreCount, want 1" }
$DesiredCount = @((& $Kk status --json | ConvertFrom-Json).roles | Where-Object desired_enabled).Count
if ($ToadCount -ne $DesiredCount) { throw "Toad process count=$ToadCount, want $DesiredCount" }
~~~

Route/interface checks use real Windows route compartments and canonical role
interface indices, not friendly names alone.

# 12. Bounded lifecycle soak

After individual gates pass, run at least two cycles:

~~~text
core service restart
AWG hard kill/recovery
OpenConnect hard kill/recovery
DHCP renewal or adapter off/on
VirtualBox cable off/on
real probes
no-accumulation assertions
~~~

Then run two suspend/resume cycles separately.

PASS requires automatic return to Ready/current epoch with no manual kk retry,
route cleanup or service restart.

# 13. Package lifecycle

A supported Windows release requires a real installer.

## Upgrade

Create a higher-version test MSI from same source with only version metadata changed.

~~~powershell
$Args = @('/i', $UpgradeMsi, '/qn', '/l*v', "$Lab\upgrade.log")
Start-Process msiexec.exe -Wait -NoNewWindow -ArgumentList $Args
~~~

Running service must return running; desired state/config/secrets survive.

## Ordinary uninstall/reinstall

~~~powershell
$Args = @('/x', $Msi, '/qn', '/l*v', "$Lab\uninstall.log")
Start-Process msiexec.exe -Wait -NoNewWindow -ArgumentList $Args
~~~

Ordinary uninstall removes service/package files while preserving administrator
configs/secrets and desired state unless installer contract explicitly changes.

## Explicit purge

Reserve MSI property:

~~~text
PURGE_STATE=1
~~~

A different property name requires this packet and package tests to change in
the same commit.

~~~powershell
$Args = @('/x', $Msi, 'PURGE_STATE=1', '/qn', '/l*v', "$Lab\purge.log")
Start-Process msiexec.exe -Wait -NoNewWindow -ArgumentList $Args
~~~

Purge removes package-owned runtime/desired state but not administrator VPN
credentials without a separate explicit credential-removal contract. After
reinstall from purge all roles start desired=false.

# 14. Evidence bundle

Collect:

~~~text
Windows version/build
Git HEAD
artifact filename/version/SHA-256
installer logs
service configuration and SCM recovery
baseline snapshot
watch JSONL
per-gate before/after snapshots
physical adapter/IP/route evidence
powercfg /a
sleep/hibernate evidence
application-probes.txt
Kikimora Event Log evidence
no-accumulation evidence
package lifecycle evidence
~~~

~~~powershell
wevtutil epl Application "$Lab\Application.evtx"
Compress-Archive -Path "$Lab\*" -DestinationPath 'C:\kikimora-lab\kikimora-windows-08a4-evidence.zip' -Force
~~~

Review archive before export; no secret/token/cookie/private key may be present.

# 15. Final PASS criteria

Windows real networking is supported only when:

1. no FakeCore participates;
2. local API authentication is real;
3. real AWG and OpenConnect are independently supervised;
4. real probes pass;
5. core/Toad crash recovery passes;
6. link/address loss is fail-closed and auto-recovers;
7. two suspend/resume cycles pass;
8. hibernate passes when supported;
9. reboot and cold boot pass before desktop login;
10. Pause/Resume, Save State/Start and Reset/crash-start pass;
11. no accumulation remains;
12. install, upgrade, uninstall/reinstall and purge/reinstall pass;
13. Snapshot/Subscribe expose all transitions;
14. the `08-dual-stack-dns-route-stability.md` field regression is green, including
    no false IPv6 readiness, deterministic DNS ownership and zero-mutation steady state;
15. evidence contains no secrets.

Only after this packet passes may Windows production default switch from visibly
simulated FakeCore to the real Kikimora backend.
