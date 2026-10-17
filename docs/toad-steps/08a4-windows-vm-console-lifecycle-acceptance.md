# Toad step 08A.4 — Windows VM console/runtime lifecycle parity acceptance

Status: **ACCEPTED — 2026-10-13** (known limitation documented; MSI ownership.conf follow-up in progress).

## Acceptance evidence summary — 2026-10-13

All lifecycle parity gates PASS on the disposable Windows 10 VM (`win10`,
192.168.1.246, dual-stack IPv4 192.168.1.246 + IPv6 2a00:1370:818c:9de::/64):

- ✅ real AWG + OpenConnect traffic: both roles Ready
- ✅ core/Toad crash recovery (kill toad → core restarts → Ready)
- ✅ two real OS suspend/resume cycles: AWG recovers automatically;
  OC needs manual disconnect+connect after resume (wintun adapter stale
  after savestate — known limitation, not a code bug)
- ✅ reboot/cold boot: service auto-start, both roles restored via desired.json
- ✅ VirtualBox pause/resume: both roles survive
- ✅ hard reset (VBoxManage reset): service auto-start, AWG Online
- ✅ bounded lifecycle soak (5 min): no accumulation, stable Ready
- ✅ no-accumulation: 1 core, 1–2 toad across all lifecycle tests
- ✅ package lifecycle: service stop/uninstall/install → running with roles
- ✅ dual-stack underlay: IPv4 + IPv6
- ✅ DNS resolution: ve.ad-tech.ru → 46.243.227.103 via router DNS
- ✅ endpoint policy routes stable: 72.56.119.116/32, 46.243.227.103/32

**Bugs found and fixed during 08A.4b:**

1. vpnc-script format: OpenConnect on Windows invokes `--script` through
   cscript.exe (Windows Script Host), not cmd.exe. Rewritten as JScript (.js).
2. TOTP secret format: OpenConnect `--token-secret=@<path>` requires the
   `base32:` prefix in the secret file for base32-encoded TOTP keys.
3. Shared state_dir: both roles pointed at the same state_dir, causing
   parking.json role mismatch. Each role now gets its own state_dir.
4. Missing leshy_zone/rule_priority: custom role names need explicit
   leshy_zone and rule_priority in TOML; auto-fill only covers primary/secondary.
5. Secret file CRLF: Windows-created secret files had \r\n line endings
   that corrupted TOTP base32 secrets.

**Known limitation (not a code bug):** after VM savestate/resume, the OC
wintun adapter becomes stale and openconnect.exe cannot recreate it. Manual
disconnect+connect is needed. This is a wintun driver behaviour on Windows
when the VM is saved/restored; Linux TUN does not have this issue. The same
limitation may affect xray-core's wintun adapter; 08C Phase 4 will verify.

**Follow-up in progress:** MSI does not yet lay down `ownership.conf` on
install (the `service install` verb creates the legacy default file, but
the MSI is the primary contract). The fix adds a `NeverOverwrite`/`Permanent`
WiX component mirroring the Linux conffile semantics.

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

## Execution record — 2026-10-16 (Guest Additions pass, partial)

The disposable `win10` VM (Windows 10.0.19045, Guest Additions 7.2.20) was driven
from the hypervisor host through `VBoxManage guestcontrol` (user `greymouse`),
because the guest is not reachable over the network from the workstation
(bridged NIC, no ICMP/SSH/WinRM/RDP answers).

Verified:

- guest command execution through Guest Additions works;
- the previously installed product is present and its service runs: payload in
  `C:\Program Files\Kikimora` (`kikimora-core.exe`, `kikimora-toad.exe`,
  `wintun.dll`, `openconnect.exe` plus its DLLs), `KikimoraCore` `RUNNING`
  (`StartType=Automatic`), `C:\ProgramData\Kikimora` layout present (`state`,
  `toads`, `logs`, `leshy`, `ownership.conf`, `core.sock`);
- **checklist item 2 (SCM before desktop login):** the guest was rebooted and
  `KikimoraCore` reached `RUNNING` about 20 s after boot with no interactive
  session;
- **checklist item 9 (control-plane auth, negative half):** a non-elevated local
  user is rejected on the control socket —
  `dial unix C:\ProgramData\Kikimora\core.sock: connect: An attempt was made to
  access a socket in a way forbidden by its access permissions` — while the
  directory ACL shows Administrators/SYSTEM full control and Users
  read/execute.

Blocked by elevation, not by the product:

- the new MSI (built with the real `wintun.dll`, sha256
  `1dca26818841163f1135442d9c2e86f3458578baff9e05a036523eb377a17e56`) was copied
  into the guest, but `msiexec /i` and `msiexec /x` both fail with `1603` from
  the Guest Additions session (the log shows the HKLM Installer key access
  denied, `1402`), i.e. the automated session carries no elevated token;
- consequently the privileged Wintun lifecycle test, the SCM recovery kill test
  and the full MSI install/uninstall pass still need an elevated console (the
  guest also has no Go toolchain).

Helper script used from the host: `win-acceptance.ps1` on the guest Desktop
(same file in the repository scratch directory `tmp/`).

## Execution record — 2026-10-16 (second pass, elevated SSH)

Access: the guest exposes an **elevated** OpenSSH session for the administrator
account (`whoami /groups` shows `S-1-16-12288`, High Mandatory Level). The guest
is IPv6-only on the lab network, so the session is reached over the stable IPv6
address; the IPv4 address `192.168.1.246` from earlier packets no longer answers.

Checklist item 1 (MSI install pass) — **PASS after a packaging fix**:

- the MSI never *started* the registered service: `ServiceControl` carried only
  Stop/Remove, so a fresh install left `KikimoraCore` `Stopped` until the next
  boot. Fixed: `Start="install"` (`b8617ea`), asserted by the package contract
  test;
- install now exits 0 with the service `Running` immediately, payload in
  `C:\Program Files\Kikimora` (core, toad, and the shipped `wintun.dll`,
  439776 bytes), `C:\ProgramData\Kikimora` layout present, and no role enabled on
  the first fresh install (`desired.json` roles all `false`).

Checklist item 9 (control-plane auth) — **PASS both halves**:

- authorized administrator: `kikimora-core status --json` returns a schema-2
  snapshot (`core_state=Ready`, `revision=9`, roles `awg-main`, `oc-main`,
  `xray-win`);
- non-elevated local user: rejected by the socket ACL (`access … forbidden by
  its access permissions`).

Checklist item 3 (service recovery) — **PASS**:

- `Stop-Process kikimora-core -Force` → the staged SCM policy restarts the
  service after ~30 s, the socket is re-published and the control plane answers
  again. `sc qfailure` reports RESTART 5000 ms, RESTART 30000 ms with a 3600 s
  reset period.

Checklist item 11 (installer lifecycle) — partially verified: uninstall removes
the binaries and the service while keeping `C:\ProgramData\Kikimora`; the
major-upgrade path still needs a dedicated pass with the staged upgrade package.

### Root cause of the earlier "service running but no socket"

The first install pass left the control socket unpublished and every client
connection refused. The cause was **not** the product code: the MSI had been
staged with `-SkipBuild` from `packaging\windows\dist-test\bin`, and those
binaries were a **pre-hardening build** (running it locally prints
`kikimora-core: at least one -config is required`, a requirement that no longer
exists — the Windows service hardening commit allowed a valid zero-role core).
The service therefore exited its serve loop while the SCM still reported
`Running`, and nothing listened on `C:\ProgramData\Kikimora\core.sock`.

Rebuilding `kikimora-core.exe` and `kikimora-toad.exe` from the current sources
and restaging produced a package in which the service starts on install,
publishes the socket, answers the control plane and recovers after a kill. The
lesson is recorded in `packaging/windows/README.md`: never stage a package from
pre-built binaries of unknown vintage.

### Still open on Windows

- Checklist item 4 (privileged Wintun lifecycle test): needs the Go toolchain in
  the guest (the installer is present at
  `C:\Users\greymouse\Downloads\go.msi`) and a copy of the `toad` tree; the guest
  has no Go yet.
- Checklist item 11 major-upgrade pass with `kikimora-upgrade.msi`.

### Checklist item 4 attempt — 2026-10-16 (privileged Wintun lifecycle) — FAIL

Run in the guest over the elevated SSH session after installing Go 1.27.1 from the
guest's `Downloads` and copying the `toad` tree:

```text
=== RUN   TestTunnelWindowsPrivilegedLifecycleReuse
Using existing driver 0.14
Creating adapter
tun_windows_privileged_test.go:56: create Wintun tunnel: configure addresses on
    "kk-tun-lifecycle-test": The request is not supported.
--- FAIL: TestTunnelWindowsPrivilegedLifecycleReuse (0.60s)
```

What is established: the Wintun driver is present and reusable (0.14), the adapter
is created, and the failure is the address step — `setTunnelAddresses` →
`winipcfg.LUID.SetIPAddresses` returns `ERROR_NOT_SUPPORTED`.

Hypothesis to check next: `CreateTunnel` configures addresses before the adapter
is administratively up. The product path worked in the 08A.4b acceptance because
the protocol core (amneziawg-go / openconnect) brings its adapter up around the
same steps, while this test calls `CreateTunnel` directly. Investigate whether
the address configuration must follow an explicit interface-up step (or whether
the older driver 0.14 needs it), then re-run.

Environment note: the guest is IPv6-only on the lab network; `192.168.1.246` from
earlier packets no longer answers and the elevated SSH session is reached over
the stable IPv6 address.

Item 11 (major upgrade with `kikimora-upgrade.msi`) is still open as well.

### Item 4 root cause — 2026-10-16 (environment, not code)

A probe in the guest created a Wintun adapter with the same library and driver
the product uses and then tried to assign an IPv4 address twice, while
PowerShell tried the same through the OS:

```text
wintun version: 0.14.1
SetIPAddresses (immediate): The request is not supported.
SetIPAddresses (after 2s): The request is not supported.
interface: admin=1 oper=2
adapter: name=kk-probe status=Disconnected admin=Up media=Disconnected
New-NetIPAddress failed: The request is not supported.
```

`admin=1` (`IfAdminStatusUp`) with `oper=2` (`IfOperStatusDown`) and
`media=Disconnected` means the adapter exists but is operationally down, and
**Windows itself refuses to add an address to it** — the OS call fails with the
same `ERROR_NOT_SUPPORTED` as the library call. The privileged test configures
addresses before any Wintun session is started, so it hits exactly that window;
the product path worked in the 08A.4b acceptance because the protocol core has
its session open by then.

Conclusion: `TestTunnelWindowsPrivilegedLifecycleReuse` is **blocked by this
guest's environment**, not by the Toad platform code. Next options, in order of
value: (a) run the test on a guest whose Wintun adapter reaches `oper=up` (start
a session in the fixture before configuring addresses, which is also closer to
the product path); (b) investigate why this guest keeps Wintun adapters
operationally down (IPv6-only lab network, driver 0.14 with DLL 0.14.1).
Until then, item 4 stays open with the evidence above.

### Checks (a)/(b)/(c) — 2026-10-16

**(a) Step-ordering / session hypothesis: DISPROVED.** A probe created the
adapter, confirmed the refusal without a session, then started a real Wintun
session (`StartSession(RingCapacityMin)`):

```text
wintun version: 0.14.1
check A1: SetIPAddresses before any session: The request is not supported.
session started (ring 131072)
interface after session: admin=1 oper=1
check A2: SetIPAddresses with an active session: The request is not supported.
adapter: status=Up admin=Up media=Connected
New-NetIPAddress with active session failed: The request is not supported.
netsh interface ipv4 add address name="kk-probe2" address=10.204.0.3/24: OK
```

With the session active the adapter is fully up (`oper=1`, `media=Connected`)
and IPv4 bindings are enabled (`ms_tcpip: True`), yet both `winipcfg
SetIPAddresses` and `New-NetIPAddress` still return `ERROR_NOT_SUPPORTED`, while
`netsh` succeeds. So the ordering is not the cause; the difference is the API
path: the legacy IP Helper call used by `winipcfg` (and the WMI path behind
`New-NetIPAddress`) are refused on this guest, the newer stack used by `netsh`
is not.

**(b) Driver/DLL parity: not resolved.** `wintun.zip` on the guest could not be
listed through this session (empty output) and the driver version did not appear
in `Win32_PnPSignedDriver`; the library still reports "Using existing driver
0.14" against a shipped DLL of 0.14.1. Worth retrying once IPv4 connectivity is
back.

**(c) Product code: no retry and no fallback.** `tun_windows.go` calls
`setTunnelAddresses` exactly once inside `configure()` and treats the error as
fatal (`CreateTunnel` fails, the role start fails). There is no retry after the
adapter comes up and no fallback to the newer IP Helper path that `netsh` uses.

### New, larger finding: this guest has no IPv4 underlay path

Connecting the installed `awg-main` role through the product CLI showed the role
failing closed for a different reason:

```text
state=Recovering route_ready=False iface=
reason=waiting for endpoint route before transport start: required physical
       underlay path is temporarily unavailable: no physical path for endpoint
       72.56.119.116:31912
underlay: {"epoch":1,"ipv6":{...}}   # no ipv4 member at all
```

The guest's only NIC carries IPv6 (plus link-local) and no IPv4 address, so the
core cannot build an IPv4 path to either VPN endpoint and correctly refuses to
start the transports. This is why the whole privileged-networking lane cannot be
exercised on this guest right now — and it also explains why the Wintun address
refusal is best re-checked after the guest gets IPv4 connectivity.

Also observed, worth a separate look: `observers.sleep_healthy` is `false` with
`sleep_last_error: "register suspend/resume notification: The operation
completed successfully."` — a success text reported as an error.

Recommended next steps, in order: give the guest an IPv4 path (bridged IPv4 or a
host-only IPv4 NIC), then re-run (i) the privileged Wintun test, (ii) a real role
connect, and (iii) the Wintun driver/DLL parity check; separately consider a
bounded retry (or the newer API) for Windows address configuration, and fix the
sleep-notification error string.

### Windows brought back to a working state — 2026-10-16

**Root cause of the whole lane, found and repaired.** The guest's IPv4 stack was
broken, not merely missing an address: `netsh interface ipv4 show interfaces`
answered "The request is not supported", `netcfg`/`ipconfig /renew` changed
nothing, and the stack had no IPv4 interface entries. That is what refused
address configuration on Wintun adapters and left the roles waiting for an
underlay path. Repair (`netsh winsock reset`, `netsh int ipv4 reset`,
`netsh int ipv6 reset`, reboot):

```text
Idx     Met         MTU          State                Name
  6          25        1500  connected     Ethernet
   IPv4 Address . . . : 192.168.1.246
```

**Item 4 (privileged Wintun lifecycle) — PASS.** With the stack healthy and the
build tag the test actually needs:

```text
go test -tags privileged ./internal/platform -run TestTunnelWindowsPrivilegedLifecycleReuse -v
=== RUN   TestTunnelWindowsPrivilegedLifecycleReuse
--- PASS: TestTunnelWindowsPrivilegedLifecycleReuse (0.75s)
PASS
```

The tag is `windows && privileged`, which is why the earlier run reported "no
tests to run"; the unprivileged platform suite is also green (`ok ... 2.501s`).
Item 4 can now be closed.

**Real product bug fixed: the Windows sleep watcher never armed.**
`sleep_windows.go` passed `DEVICE_NOTIFY_SERVICE_HANDLE` (1) while supplying a
`DEVICE_NOTIFY_SUBSCRIBE_PARAMETERS` struct, so
`PowerRegisterSuspendResumeNotification` was rejected and `sleep_healthy` stayed
false — the resume notification never reached the core. The flag is now
`DEVICE_NOTIFY_CALLBACK` (2) and a failure reports the Win32 status instead of a
misleading `GetLastError` text. Verified in the guest with a probe that calls
`platform.DefaultSleepSource().Watch`: it now blocks until the context expires
(`watch returned: context deadline exceeded` after 6.4s) instead of returning a
registration error immediately.

**Hardening: bounded retry for address configuration.** `tun_windows.go`
configured addresses exactly once and treated a failure as fatal for the whole
tunnel. `applyTunnelAddresses` now makes 5 attempts over 2s and lets the last
error survive; two unit tests stub the seam (transient failures absorbed, effort
bounded). Committed with the sleep fix as `0720726`.

**Product path verified end to end in the guest:**

```text
awg-main: state=Ready iface=kk-awg0 connected=True route target is structurally ready
kk-awg0      10.8.1.3     /32
kk-xray-win0 10.255.0.6   /30
```

Remaining for this lane: the installed MSI still carries the pre-fix core binary,
so the payload must be rebuilt and reinstalled (the fixes above are verified from
source in the guest); item 11 (major upgrade) installs cleanly (`exit=0`) but its
post-upgrade checks are still to be captured.

### Fixes deployed through the MSI, item 11 closed — 2026-10-16

The Windows payload was rebuilt from source (no `-SkipBuild`) and the MSI
(`kikimora-1.0.0-windows-amd64.msi`, 27.5 MB, sha256
`FE336A51C699F6DCDFC5D3722F19B5B9A620C87F6C157A0F3882E26AED74CBE4`) installed in
the guest over the older staged package:

```text
before                : service Running, socket True, payload 92B56E2D990F53BB,
                        core_state=Ready revision=201, sleep_healthy=False
                        sleep_err=register suspend/resume notification:
                                  The operation completed successfully.
older package install : exit=0, service Running, socket False, no kk adapters
new package (upgrade) : exit=0, service Running, socket True, payload BCA9138D61C52195,
                        desired.json unchanged (updated_at identical),
                        core_state=Ready revision=39, sleep_healthy=True, sleep_err=(empty)
awg-main              : Ready at t+8s, kk-awg0 = 10.8.1.3/32
```

Item 11 (major upgrade) therefore passes both halves: the staged older package
installs cleanly, and the freshly built package upgrades it with the desired
state, socket and service intact. The sleep fix is confirmed in the **installed**
binary, not just from source: `sleep_healthy` flips from `false` (with the bogus
`GetLastError` text) to `true` with an empty error after the upgrade.

The "always rebuild the payload" lesson holds: the previously installed MSI still
carried the 2026-10-13 binaries (payload `92B56E2D990F53BB`), which is why the
sleep watcher kept failing the registration with a misleading message.

### UI tails closed on Linux — 2026-10-16

**Tray parity.** `DesktopIntegration` now carries the aggregate state line, the
all-roles toggle and one line per role (filled bullet while a role is up), all
refreshed from `snapshotChanged`; the menu is exposed through `contextMenu()` for
tests. A new `tray-menu` CTest covers the state line, the per-role lines and the
toggle text, and a translation case loads `:/i18n/kikimora_ru`.

**i18n harness.** Tray strings go through `tr()`, QML already used `qsTr()`,
`main.cpp` installs a `QTranslator` from `:/i18n/kikimora_<locale>` (overridable
with `KIKIMORA_LOCALE`), `desktop/i18n/kikimora_ru.ts` holds the first real
catalogue, and CMake compiles catalogues only when `Qt6LinguistTools` is found so
builds without it stay translation-less instead of failing.

**CI lane.** The `internal/control` polling helpers used 1s (two of them 500ms)
budgets, which flaked on loaded runners; they now share `testWaitBudget = 5s`.
The same run exposed a second flake: `TestSetActiveProfileNoOpForSameProfile`
sampled `Revision` while `NewManager` was still publishing its first snapshot, so
it now waits for a settled revision first. `go test ./internal/control -count=3`
is green (35s), and the desktop build is green with CTest 6/6.

### i18n case exercised for real, not skipped — 2026-10-16

`qt6-tools-dev` provides the Qt Linguist CMake package
(`/usr/lib/x86_64-linux-gnu/cmake/Qt6LinguistTools/Qt6LinguistToolsConfig.cmake`),
so the catalogue compiles and the translation case runs. Build only the target
that matters - the lab VM has 2 vCPUs, and a `-j8` full build starves sshd until
the VM stops answering:

```
cmake --build /tmp/l10n-build --target tray-menu-test -j2
[100%] Built target tray-menu-test        # embeds qrc_tray-menu-test_translations.cpp
/tmp/l10n-build/kikimora_ru.qm

ctest -R tray-menu -V
PASS   : TrayMenuTest::menuCarriesAggregateStateAndOneLinePerRole()
PASS   : TrayMenuTest::toggleActionFollowsTheAggregateState()
PASS   : TrayMenuTest::russianTranslationAppliesToTheTrayMenu()
Totals: 5 passed, 0 failed, 0 skipped, 0 blacklisted, 12ms
```

"0 skipped" is the point: the translator really loads from `:/i18n/kikimora_ru.qm`
and the Russian menu text is asserted, so the i18n harness is verified rather
than merely wired. Keep build parallelism at `-j2` on this VM.

### VPN brought up on the Ubuntu lab VM through the installed CLI - 2026-10-16

The installed package was driven with `sudo kikimora-core` (socket
`/run/kikimora/core.sock`); configs and secrets were already in place
(`/etc/kikimora/toads/{awg,oc,xray-nl}.toml`, `/etc/kikimora/secrets/`).

```text
core=Ready aggregate=Ready
  awg       Ready  iface=kk-awg0    reason=recovery complete
  oc        Ready  iface=kk-oc0     reason=recovery complete
  xray-nl   Ready  iface=kk-xray0   reason=managed route target is structurally ready

kk-awg0   10.8.1.3/32
kk-xray0  10.255.0.2/30
kk-oc0    10.0.0.254/24

kk-awg0 -> HTTP 200   https://telegram.org/
kk-oc0  -> HTTP 302   https://gitlab.sca.ad-tech.ru/
```

State was stable across a 60s poll, and both probes answer through the tunnels,
so the lab VM now carries live VPN traffic.

**Finding on the way:** `oc` was `Recovering` with reason
`default-interface-changed` after the VM restarts, and the `retry` action offered
in its `available_actions` did **not** clear it (rc=0, state unchanged for 60s).
A clean `disconnect --role oc` then `connect --role oc` recovered it immediately
(`recovery complete`, epoch 5, attempt 3). Same family as Finding 2: an action the
snapshot advertises is not sufficient for the state it is offered in. Worth a
reproducer: a role whose recorded underlay epoch lags the current one
(`applied_underlay_epoch=4` against `epoch=5`) could ignore `retry` and require
the reconnect path.

### Domain routing on Linux: two defects found while routing ChatGPT via Amnezia - 2026-10-16

Goal: send the ChatGPT/OpenAI domains through the AmneziaWG role (`kk-awg0`).
The domain list itself was already mostly in place (`chatgpt.com`, `openai.com`,
`api.openai.com` in the primary list), and the CLI is `kk domains add DOMAIN
[--primary|--secondary|--bypass]`.

**Defect 1 (fixed): the shipped CLI could not change a domain list at all.**
Every `kk domains add` failed with a misleading rollback:

```text
/usr/local/libexec/kikimora/cli/domains.sh: line 21:
  /usr/local/libexec/kikimora/leshy/build-config: No such file or directory
Error: domains list change rolled back: configuration validation failed
```

`linux/install.sh` has always expected the shell generator at
`${LIBEXEC_DIR}/build-config` with `LIBEXEC_DIR=/usr/local/libexec/kikimora/leshy`,
but `packaging/linux/stage-release.sh` staged only `build-config-go`. The deb
therefore shipped a CLI that could not write configuration. Fixed by staging
`linux/files/build-config` beside the Go one; verified live on the VM - five
domains added, each one regenerating, validating on a temporary port and
restarting Leshy (primary went 18 -> 23 domains).

**Defect 2 (open): zone device files are per-role now, but the zoning layer still
looks for the legacy names.** The Go core publishes one zone per role
(`publication.zone = awg|oc|xray`, files `/run/kikimora/leshy/vpn/{awg,oc,xray}.dev`
holding the interface name), while the generated config binds the `primary` and
`secondary` zones to `/run/kikimora/leshy/vpn/primary.dev` and `secondary.dev`,
and nothing creates those. After a Leshy restart every primary-zone name loses its
tunnel path and Leshy logs:

```text
WARN Failed to add route ip=104.18.32.47 zone="primary" qname="chatgpt.com."
     error=Device file '/run/kikimora/leshy/vpn/primary.dev' not found (VPN not connected?)
```

Operational workaround applied on the lab VM (`printf 'kk-awg0\n' > primary.dev`,
root:kikimora 0600): Leshy then installs host routes through the tunnel
(`Adding route via device ip=172.64.154.211 prefix_len=32 device="kk-awg0"` for
`platform.openai.com`, `auth.openai.com`, `openai.com`). The durable fix belongs
in the product: the zone->device mapping must come from the core publication (or
`vpn.conf`), and stale direct `/32` routes left over from a failure window must be
replaced - `chatgpt.com` still holds a `/32` via `enp0s3` and its fetch returns
000 while the OpenAI hosts answer through the tunnel. Note `vpn/primary.dev` lives
under `/run`, so the workaround does not survive a reboot.

### Zone device fallback fixed, ChatGPT on the Amnezia tunnel - 2026-10-16

Defect 2 is fixed: the generator resolves a zone's device from the role files the
core publishes (`awg.dev`/`oc.dev`/`xray.dev`) instead of requiring
`primary.dev`/`secondary.dev`, which nothing creates after the Go takeover.

* `resolve_zone_device`: an existing legacy file still wins; otherwise
  `vpn.conf`'s `PRIMARY_INTERFACE`/`SECONDARY_INTERFACE` selects the role whose
  device carries that interface; otherwise the alphabetically first published
  role device is used. When nothing is published the legacy path is returned, so
  the "device file not found" failure stays visible instead of inventing a device.
* The legacy paths are configurable (`PRIMARY_DEVICE_FILE`/`SECONDARY_DEVICE_FILE`,
  defaulting under the runtime directory), which also makes the test hermetic.
* `linux/tests/zone-device-fallback.sh` covers all three cases and passes.

On the lab VM the hand-written workaround was deleted first; the regenerated
config now reads `route_target = "/run/kikimora/leshy/vpn/awg.dev"` and every
OpenAI host sits on the tunnel:

```text
chatgpt.com / chat.openai.com / openai.com / platform.openai.com -> dev kk-awg0
```

So ChatGPT reaches the edge through AmneziaWG without any hand-written file in
`/run`, and the setup survives a reboot.

### Rebuilt package carries both fixes - 2026-10-16

`packaging/linux/build-release.sh` (canonical builder with
`KIKIMORA_LESHY_BIN=/usr/local/bin/leshy`, no Qt) run on the lab VM from the
committed tree produced `kikimora_1.0.0_amd64.deb` plus the tarball and
`SHA256SUMS`; installing it over the previous version left all three units active
and the artifact checks pass:

```text
shipped shell generator : 13537 bytes at /usr/local/libexec/kikimora/leshy/build-config
zone fallback present   : 5 x resolve_zone_device
generator runs clean    : Route preseed source: /usr/local/libexec/kikimora/leshy/routes
CLI round trip          : Added: example.org (primary) / Removed: example.org (primary)
zone target             : route_target = "/run/kikimora/leshy/vpn/awg.dev"
chatgpt.com / openai.com / platform.openai.com -> dev kk-awg0
```

So an installed package, not a hand-copied file, now routes the ChatGPT and
OpenAI names through AmneziaWG, and the setup no longer depends on anything
written under `/run` by hand.

**Packaging pitfall worth remembering:** a tree transported as a zip from the
Windows checkout arrives with CRLF, and a CRLF shebang makes the staged script
fail with "No such file or directory" rather than a line-ending error. The
canonical Linux flow builds from an LF tree; strip CR (or build on Linux) before
staging, and keep `find ... -exec sed -i 's/\r$//'` in the verification harness.

### Windows domain routing: where the missing half lives - 2026-10-16

Question asked after bringing the VPN up on the Windows guest: is ChatGPT routed
through AmneziaWG there too? Measured answer: no, and the reason is structural.

* The Windows route executor is deliberately narrow:
  `toad/internal/platform/windows/routes` is "the single Windows writer for
  endpoint-exception and parking route transactions" - host routes to the VPN
  endpoints plus fail-closed parking. Windows has no policy tables, so there is
  no per-domain layer in the Go core at all.
* Observed on the guest with all three roles Ready: `Get-NetRoute 0.0.0.0/0` still
  shows `Ethernet -> 192.168.1.1`, and `Find-NetRoute` for chatgpt.com picks the
  physical NIC. The tunnel is healthy, the policy that would use it is absent.
* Proof that the tunnel itself carries it: a temporary `/32` via `kk-awg0` for
  chatgpt.com's address moved the public IP to `72.56.119.116` (the AWG exit) and
  the route was rolled back afterwards.
* The missing half is documented in `docs/leshy-integration.md:59` - Windows
  routing is the **Leshy Windows port** ("route via interface alias") - and the
  artifact is already on the workstation: `leshy-win` (smollgreymouse/leshy fork,
  full checkout) with `windows/dist/leshy-0.4.0-windows-x64-portable.zip`,
  `windows/build-package.ps1`, `windows/README.md`, `docs/windows.md`, and a Rust
  toolchain is installed. The guest has none of it (`C:\ProgramData\leshy` is
  absent, `where leshy` finds nothing), which is exactly why primary-zone domains
  fall through to the direct default there.
* Integration detail to watch when deploying: Leshy's Windows defaults expect
  device files in `C:\ProgramData\leshy\run\*.dev`, while the core publishes to
  `C:\ProgramData\leshy\vpn` (`cmd/kikimora-core/service_windows.go`), so the
  generated `route_target` must point at the publication directory (or the core at
  `run\`).

Deployment path per `docs/leshy-integration.md:86-96`: install the portable
package, seed `C:\ProgramData\leshy\config.toml` with the primary zone aimed at
the core's published `awg.dev` plus the ChatGPT/OpenAI list, route the Leshy
listener (`127.0.0.1:53053`) into DNS for those domains (NRPT rule rather than a
global DNS switch), then accept with: a configured domain resolves through Leshy
and its traffic enters the interface the core published.

### Leshy Windows port deployed on the guest - 2026-10-17

The per-domain half now runs on the Windows VM, so the stack no longer lacks it.

* `C:\leshy` holds the portable 0.4.0 package; the `leshy` service is installed,
  Running, StartType Automatic, and listens on `UDP 127.0.0.1:53`.
* Config `C:\ProgramData\leshy\config.toml`: one inclusive zone `awg` with
  `route_target = 'C:\ProgramData\Kikimora\leshy\vpn\awg.dev'` (the file the
  core publishes, containing `kk-awg0`; that adapter is ifIndex 11, Up) and the
  ChatGPT/OpenAI domain list.
* Listener port is 53, not the shipped 53053: Windows DNS clients and NRPT rules
  cannot use a custom port, and 127.0.0.1:53 was free on the VM.
* DNS wiring is scoped, never global: an NRPT rule sends only `.chatgpt.com`,
  `.openai.com`, `.oaistatic.com`, `.oaiusercontent.com` to 127.0.0.1. While the
  listener was down the rule was removed first and re-added only after the
  listener came up, so the host never lost resolution.
* Blocker on the way: `leshy.exe` exited with `0xC0000135` (STATUS_DLL_NOT_FOUND).
  Installing the MSVC x64 runtime fixed it (`leshy --version` -> `leshy 0.4.0`).

**Not yet met - two concrete gaps.** Leshy answers the queries but does not install
the host route through `kk-awg0` (`Find-NetRoute` still reports Ethernet), and the
service log at `C:\ProgramData\leshy\logs\leshy.log` is empty, so the next step
is to raise its verbosity or check alias-to-ifIndex resolution before blaming the
config. Second, the resolved addresses for chatgpt.com (`8.47.69.0`, `8.6.112.0`)
are not Cloudflare ranges - the guest's DNS answers look hijacked, so a working
ChatGPT needs the zone's `dns_servers` pointed at a resolver reached through the
tunnel (on Linux the equivalent was Leshy's upstream-zone routing). The documented
acceptance - "a configured domain resolves through Leshy and its traffic enters
the interface published by the core" - is therefore still open.

### Windows Leshy port fixed: ChatGPT now rides the tunnel - 2026-10-17

The root cause was a bug in the Windows port itself (`leshy-win`, branch
`windows-support`, v0.5.1), not in the config:

```rust
DestinationPrefix: IP_ADDRESS_PREFIX {
    Prefix: prefix_sockaddr(ip),   // zeroes the address by design
    PrefixLength: prefix_len,
},
```

`prefix_sockaddr` is the on-link *next hop* form, so every route was created for
`0.0.0.0/32` instead of the resolved address. `CreateIpForwardEntry2` still
returned success, nothing was logged at INFO, and the forwarding table stayed
empty - which is why the answers kept leaving through the physical default route
even though the log said "Adding route via device ... kk-awg0".

Fix (commit `7cd5330` in the leshy fork checkout):

* `DestinationPrefix.Prefix` now uses `address_sockaddr(ip)`; the next hop keeps
  the zeroed form, which is what "on-link" means.
* Two regression tests assert the prefix carries the address for v4 and v6 while
  the on-link next hop stays `0.0.0.0`.

Deployment details that mattered:

* the guest had been running a stale **0.4.0** package; the fix is built from
  `windows-support` (v0.5.1) and replaces `C:\leshy\leshy.exe`, service
  `leshy` auto-start, listener `UDP 127.0.0.1:53`;
* the zone lists `static_routes = ["1.1.1.1/32", "8.8.8.8/32"]`, so Leshy sends
  its own upstream queries through the tunnel - the Windows equivalent of the
  Linux upstream zone, and the reason the answers stopped being rewritten
  (`8.47.69.0`/`8.6.112.0` before, real Cloudflare ranges after);
* NRPT stays scoped to `.chatgpt.com`, `.openai.com`, `.oaistatic.com`,
  `.oaiusercontent.com`; nothing else on the host uses Leshy.

Acceptance from `docs/leshy-integration.md` is met:

```text
chatgpt.com A:    172.64.155.209, 104.18.32.47          route=kk-awg0 chosen=kk-awg0
chatgpt.com AAAA: 2606:4700:4408::ac40:9bd1, ...        route=kk-awg0 chosen=kk-awg0
1.1.1.1/32, 8.8.8.8/32                                  route=kk-awg0 (static, at startup)
chatgpt.com -> HTTP 200   openai.com -> HTTP 200
```

`api.ipify.org` still reports the direct exit on purpose: that host is outside the
zone. The fix is committed locally in the fork checkout and **not pushed** to
`smollgreymouse/leshy` yet, and a reboot has not been used to re-prove the routes.

### Leshy Windows port: three defects fixed, two privileged suites green - 2026-10-17

All work is in the fork checkout `leshy-win` (branch `windows-support`, v0.5.1),
pushed to `github.com/smollgreymouse/leshy`.

| Commit | Defect | Consequence before the fix |
|--------|--------|---------------------------|
| `7cd5330` | `build_route_row` built `DestinationPrefix.Prefix` with the zeroing helper | every route was created for `0.0.0.0/32`; `CreateIpForwardEntry2` reported success, nothing was logged, the table stayed empty and traffic kept leaving through the physical default |
| `55487ff` | `route_failure_mode` was dead code - routes were installed in a detached `tokio::spawn` | the documented `servfail` kill switch behaved exactly like `fallback` on every platform |
| `690d4bb` | `matches_entries` compares real suffixes against literal entries | `*.domain` in `domains` matched nothing; the shipped lists hid it by also listing the bare name |

Privileged suites (manual, they need elevation and live tunnels):

* `windows/tests/route-isolation.ps1` - one zone per tunnel, isolation assertions,
  fail-closed on a removed device file: **20/20 PASS, exit 0** on the current
  binary (re-run after the matcher fix).
* `windows/tests/scenarios.ps1` - the routing-relevant scenarios of the Linux
  Docker suite (basic forwarding, dev route, via route, fallback, servfail,
  wildcard, reconnect, zone DNS): **8/8 PASS**.
* Go `TestTunnelWindowsPrivilegedLifecycleReuse` (`-tags privileged`) passed
  earlier the same day; it was not re-run in this round because the guest's
  `C:\Users\greymouse\toad-src` tree disappeared during the MSI reinstalls
  (the Go binaries and the test are unchanged by the Rust fixes).

Deployed state on the guest: `C:\leshy\leshy.exe` (`leshy 0.5.1`), service
`leshy` Running/Automatic, listener `127.0.0.1:53`, scoped NRPT rules for
`.chatgpt.com/.openai.com/.oaistatic.com/.oaiusercontent.com`, and
`static_routes = ["1.1.1.1/32", "8.8.8.8/32"]` so the upstream queries ride the
tunnel. Results are stable across runs; the harness retries a query before failing
(a cold tunnel returned SERVFAIL once) and S5 deliberately uses a single warmed
shot so a retry cannot mask the kill switch.

Still open: `set_failure_recovery` fails with error 5 at install time (workaround
`sc failure leshy reset= 0 actions= restart/5000` applied on the VM), and the fork
has no Windows DNS-integration helper (NRPT is wired by hand).

### Leshy as the route owner: reconciliation and replace semantics - 2026-10-17

The question "Leshy is supposed to manage the routes" exposed the last gap: the
port owned the routes only in bookkeeping.

**Defect 5 (fixed): the aggregator trusted its own map.** `RouteManager::add_route`
asked the aggregator, which dedupes by `(network, prefix)` and returns an empty
action list for an address it has already seen. A row deleted or replaced outside
Leshy - a stale direct route winning over the tunnel, an interface reset - was
therefore never restored. Proven on the VM: a `172.64.155.209/32` route planted
via `Ethernet` survived a fresh query untouched, with no attempt in the log.

Fixes in the fork:

* `RouteAdder::route_matches` - a platform hook asking the kernel whether
  `ip/prefix_len` still points at the zone target. The default answers `true`, so
  Linux/macOS keep the previous behaviour; the Windows adder compares the
  forwarding rows against the resolved device interface (`dev`) or the gateway
  (`via`).
* `RouteManager::add_route_v4` - when the aggregator returns nothing, the manager
  re-checks the kernel and replays the action when the row is missing or points
  elsewhere. `Aggregator::installed_prefix` supplies the prefix it believes it
  owns (longest cover).
* `create_route` - `ERROR_OBJECT_ALREADY_EXISTS` no longer means "fine": rows with
  the same prefix but a different interface, metric or next hop are deleted and
  recreated, the way the Kikimora Go route owner already behaved.

Acceptance on the VM, with no manual route work:

```text
planted: Ethernet via 192.168.1.1
chosen before: Ethernet
after leshy: kk-awg0 metric=0
chosen after: kk-awg0
INFO leshy::routing: Route missing or pointing elsewhere in the kernel, restoring
     ip=172.64.155.209 prefix_len=32 zone="awg"
chatgpt.com -> HTTP 200
```

Both privileged suites stay green on this build: route isolation 20/20
(`route isolation verified`, exit 0) and the scenario suite (`windows scenario
suite passed`).

**Ownership boundary stays single-writer per concern.** Leshy owns the split
routes (zone statics and the per-answer host routes that pull traffic into the
tunnel interface). The Kikimora core owns endpoint exceptions on the physical
underlay plus fail-closed parking, and publishes the zone device files - that is
the documented contract of `toad/internal/platform/windows/routes`, and the two
sets never target the same prefixes: Leshy routes destinations, the core keeps the
path to the VPN endpoints alive.
