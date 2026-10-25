# Standalone Windows-side diagnostics for Amnezia XRay/VLESS testing.
# This script does not modify routes, DNS, firewall, VPN state, or Kikimora.
# Run from PowerShell after manually connecting Amnezia XRay.

$ErrorActionPreference = 'Continue'
$ProgressPreference = 'SilentlyContinue'
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$outDir = Join-Path $PSScriptRoot 'vpn-bench-results'
New-Item -ItemType Directory -Force -Path $outDir | Out-Null
$outFile = Join-Path $outDir "$stamp-xray-windows-diagnostics.txt"

function Section([string]$name) {
    "`n== $name ==" | Tee-Object -FilePath $outFile -Append
}
function Run([string]$label, [scriptblock]$cmd) {
    "`n$ $label" | Tee-Object -FilePath $outFile -Append
    try { & $cmd 2>&1 | Out-String -Width 4096 | Tee-Object -FilePath $outFile -Append }
    catch { $_ | Out-String | Tee-Object -FilePath $outFile -Append }
}

"label=xray-windows-diagnostics" | Set-Content $outFile
"timestamp=$(Get-Date -Format o)" | Add-Content $outFile
"computer=$env:COMPUTERNAME" | Add-Content $outFile

Section 'OS and Amnezia processes'
Run 'Get-ComputerInfo (selected)' { Get-ComputerInfo | Select-Object WindowsProductName,WindowsVersion,OsBuildNumber,OsArchitecture }
Run 'Amnezia/XRay/tun processes' { Get-Process | Where-Object { $_.ProcessName -match 'amnezia|xray|tun|wireguard' } | Select-Object Id,ProcessName,Path }

Section 'interfaces and addresses'
Run 'Get-NetAdapter' { Get-NetAdapter | Sort-Object ifIndex | Format-Table -AutoSize ifIndex,Name,InterfaceDescription,Status,MacAddress,LinkSpeed }
Run 'Get-NetIPConfiguration' { Get-NetIPConfiguration -Detailed }
Run 'ipconfig /all' { ipconfig /all }

Section 'routing'
Run 'route print' { route print }
Run 'Get-NetRoute IPv4' { Get-NetRoute -AddressFamily IPv4 | Sort-Object RouteMetric,DestinationPrefix | Format-Table -AutoSize ifIndex,DestinationPrefix,NextHop,RouteMetric,State }
Run 'route to 1.1.1.1' { Find-NetRoute -RemoteIPAddress 1.1.1.1 | Format-List * }
Run 'route to 8.8.8.8' { Find-NetRoute -RemoteIPAddress 8.8.8.8 | Format-List * }

Section 'DNS'
Run 'Get-DnsClientServerAddress' { Get-DnsClientServerAddress | Format-Table -AutoSize InterfaceAlias,InterfaceIndex,AddressFamily,ServerAddresses }
Run 'Resolve-DnsName www.google.com' { Resolve-DnsName www.google.com -Type A -DnsOnly -QuickTimeout }
Run 'Resolve-DnsName cloudflare.com' { Resolve-DnsName cloudflare.com -Type A -DnsOnly -QuickTimeout }
Run 'DNS direct 1.1.1.1' { Resolve-DnsName www.google.com -Type A -Server 1.1.1.1 -DnsOnly -QuickTimeout }

Section 'TCP state before probes'
Run 'Get-NetTCPConnection' { Get-NetTCPConnection | Sort-Object State,RemoteAddress | Format-Table -AutoSize State,LocalAddress,LocalPort,RemoteAddress,RemotePort,OwningProcess }
Run 'netstat -ano' { netstat -ano }

Section 'connectivity probes'
Run 'ping 1.1.1.1' { ping -n 10 1.1.1.1 }
Run 'ping 8.8.8.8' { ping -n 10 8.8.8.8 }
Run 'Test-NetConnection 1.1.1.1:443' { Test-NetConnection 1.1.1.1 -Port 443 -InformationLevel Detailed }
Run 'Test-NetConnection google:443' { Test-NetConnection www.google.com -Port 443 -InformationLevel Detailed }

Section 'HTTPS repeated probes'
1..15 | ForEach-Object {
    $i = $_
    Run "curl google generate_204 #$i" {
        curl.exe -4 --http1.1 --connect-timeout 5 --max-time 12 -sS -o NUL -w "run=$i code=%{http_code} remote=%{remote_ip} connect=%{time_connect} tls=%{time_appconnect} start=%{time_starttransfer} total=%{time_total} speed=%{speed_download}`n" https://www.google.com/generate_204
    }
}

Section 'direct-IP probes bypassing DNS'
Run 'HTTPS 1.1.1.1' { curl.exe -4 -vk --http1.1 --connect-timeout 5 --max-time 12 https://1.1.1.1/ -o NUL }
Run 'HTTP 1.1.1.1' { curl.exe -4 -v --connect-timeout 5 --max-time 12 http://1.1.1.1/ -o NUL }

Section 'path and MTU clues'
Run 'tracert 1.1.1.1' { tracert -d -w 1000 1.1.1.1 }
Run 'ping DF payload 1472' { ping -n 4 -f -l 1472 1.1.1.1 }
Run 'ping DF payload 1400' { ping -n 4 -f -l 1400 1.1.1.1 }
Run 'ping DF payload 1300' { ping -n 4 -f -l 1300 1.1.1.1 }

Section 'TCP state after probes'
Run 'Get-NetTCPConnection after' { Get-NetTCPConnection | Sort-Object State,RemoteAddress | Format-Table -AutoSize State,LocalAddress,LocalPort,RemoteAddress,RemotePort,OwningProcess }
Run 'netstat -ano after' { netstat -ano }

Section 'Windows event hints'
Run 'recent system TCP/DNS/network events' {
    Get-WinEvent -FilterHashtable @{LogName='System'; StartTime=(Get-Date).AddMinutes(-15)} -ErrorAction SilentlyContinue |
        Where-Object { $_.ProviderName -match 'Tcpip|DNS|NlaSvc|NetworkProfile' -or $_.Message -match 'TCP|DNS|network' } |
        Select-Object -First 150 TimeCreated,ProviderName,Id,LevelDisplayName,Message |
        Format-List
}

"`n== finished $(Get-Date -Format o) ==" | Add-Content $outFile
Write-Host "Saved diagnostics to: $outFile"
