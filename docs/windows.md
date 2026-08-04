# Windows orchestration (experimental)

Leshy may run on Windows, but Kikimora's Linux `systemd`/`systemd-resolved`
integration and the macOS `launchd` bundle do not. The `windows/` bundle
provides a separate elevated PowerShell orchestration layer built on:

- Windows Scheduled Tasks;
- `Get-NetAdapter` / `Get-NetIPAddress` for VPN interface readiness;
- `Get-DnsClientServerAddress` / `Set-DnsClientServerAddress` for DNS ownership.

It manages three scheduled tasks:

- `KikimoraLeshy` — keeps the Leshy process running;
- `KikimoraRouteWatch` — publishes ready VPN interfaces to Leshy device files;
- `KikimoraHealthWatch` — checks the local resolver and repairs DNS ownership.

## Important DNS limitation

The Windows DNS client accepts DNS server IP addresses, but not custom DNS
ports. For Windows system DNS ownership, configure Leshy to listen on
`127.0.0.1:53`, not only on `127.0.0.1:53053`.

Kikimora still writes only `127.0.0.1` into the selected Windows DNS interfaces.

## Install

Install a working Leshy binary first. The default expected path is:

```text
C:\Program Files\Kikimora\leshy.exe
```

Identify VPN and DNS interface aliases:

```powershell
Get-NetAdapter
Get-DnsClientServerAddress -AddressFamily IPv4
```

Run the Windows installer from an elevated PowerShell session:

```powershell
.\install.ps1 `
  -PrimaryInterface "AmneziaVPN" `
  -SecondaryInterface "My VPN" `
  -DnsInterface "Wi-Fi" `
  -LeshyBinary "C:\Program Files\Kikimora\leshy.exe" `
  -LeshyConfig "C:\path\to\config.toml"
```

Multiple DNS interfaces are supported:

```powershell
.\install.ps1 `
  -PrimaryInterface "AmneziaVPN" `
  -SecondaryInterface "My VPN" `
  -DnsInterface "Wi-Fi","Ethernet" `
  -LeshyConfig "C:\path\to\config.toml"
```

The installer creates scheduled tasks disabled by default and does not change
DNS unless `-Start` is passed. Start and enable after installation with:

```powershell
& "C:\Program Files\Kikimora\kk.ps1" enable --now
```

## Commands

Use the installed PowerShell CLI from an elevated session:

```powershell
& "C:\Program Files\Kikimora\kk.ps1" status
& "C:\Program Files\Kikimora\kk.ps1" start
& "C:\Program Files\Kikimora\kk.ps1" stop
& "C:\Program Files\Kikimora\kk.ps1" restart
& "C:\Program Files\Kikimora\kk.ps1" dns status
& "C:\Program Files\Kikimora\kk.ps1" dns check
```

## DNS ownership and recovery

Before assigning `127.0.0.1`, the Windows DNS helper snapshots the existing
IPv4 DNS servers for each configured interface under:

```text
C:\ProgramData\Kikimora\Leshy\state
```

`stop`, `dns suspend`, and Health Watch fallback restore those snapshots.
`dns disable` restores DNS and removes the saved state.

This keeps the recovery path bounded: do not include an interface in
`DnsInterfaces` unless Kikimora should own and restore its DNS servers.

## Routing scope

Route Watch does not create Windows routes itself. It detects ready Windows
network interfaces and writes the device files that Leshy consumes:

```text
C:\ProgramData\Kikimora\Leshy\run\vpn\primary.dev
C:\ProgramData\Kikimora\Leshy\run\vpn\secondary.dev
```

Confirm that the installed Leshy build supports your Windows route behavior
before relying on it for production traffic.

## Validation

After installation and start, verify:

```powershell
& "C:\Program Files\Kikimora\kk.ps1" status
& "C:\Program Files\Kikimora\kk.ps1" dns check
Resolve-DnsName example.com -Server 127.0.0.1
Get-Content "C:\ProgramData\Kikimora\logs\kikimora.log" -Tail 80
```

Test a VPN reconnect and confirm that `dns check` returns zero after Health
Watch repairs resolver ownership.
