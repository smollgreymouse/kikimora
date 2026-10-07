//go:build windows

package openconnect

import (
	"fmt"
	"os"
	"path/filepath"
)

// routeFreeVPNScript is the Windows route-free vpnc-script invoked by the
// official openconnect.exe child process via --script. Like the Linux
// shell equivalent, this script is intentionally limited to configuring
// the OpenConnect-owned TUN as a stable route target and publishing the
// non-secret network parameters pushed by the server for the orchestrator.
//
// Kikimora owns routing and DNS policy; this script never installs routes,
// never mutates DNS and never touches the firewall. The published
// openconnect-network.env file is the only state the core reads to
// determine negotiated addresses, MTU and publication readiness.
//
// OpenConnect on Windows invokes the script through CreateProcess with
// the standard environment variables (TUNDEV, reason, INTERNAL_IP4_*,
// INTERNAL_IP6_*, CISCO_SPLIT_*, etc.). The .bat extension causes Windows
// to dispatch through cmd.exe automatically.
const routeFreeVPNScript = `@echo off
setlocal enabledelayedexpansion

set "STATE_DIR=%~dp0"
set "NETWORK_STATE=!STATE_DIR!openconnect-network.env"
set "NETWORK_STATE_TMP=!NETWORK_STATE!.tmp.!RANDOM!"

if "!reason!"=="connect" goto :configure
if "!reason!"=="reconnect" goto :configure
goto :eof

:configure

:: Bring the TUN interface up (it is typically already enabled).
netsh interface ip set interface "!TUNDEV!" admin=enable >nul 2>&1

:: Apply the negotiated IPv4 address and prefix.
if defined INTERNAL_IP4_ADDRESS (
    set "PREFIX=32"
    if defined INTERNAL_IP4_NETMASKLEN set "PREFIX=!INTERNAL_IP4_NETMASKLEN!"
    netsh interface ip set address "!TUNDEV!" static !INTERNAL_IP4_ADDRESS!/!PREFIX! >nul 2>&1
)

:: Apply the negotiated IPv6 address.
if defined INTERNAL_IP6_ADDRESS (
    netsh interface ipv6 add address "!TUNDEV!" !INTERNAL_IP6_ADDRESS! >nul 2>&1
)

:: Apply the negotiated MTU when the server pushed one.
set "MTU="
if defined INTERNAL_IP4_MTU set "MTU=!INTERNAL_IP4_MTU!"
if defined INTERNAL_IP6_MTU if not defined MTU set "MTU=!INTERNAL_IP6_MTU!"
if defined MTU (
    netsh interface ipv4 set subinterface "!TUNDEV!" mtu=!MTU! >nul 2>&1
    netsh interface ipv6 set subinterface "!TUNDEV!" mtu=!MTU! >nul 2>&1
)

:: Publish the network state for the orchestrator. The atomic temp+move
:: pattern prevents the core from reading a half-written file.
(
    echo reason=!reason!
    echo tun_dev=!TUNDEV!
    if defined MTU echo mtu=!MTU!
    if defined INTERNAL_IP4_ADDRESS echo ipv4_address=!INTERNAL_IP4_ADDRESS!
    if defined INTERNAL_IP4_NETMASKLEN echo ipv4_netmasklen=!INTERNAL_IP4_NETMASKLEN!
    if defined INTERNAL_IP4_DNS echo ipv4_dns=!INTERNAL_IP4_DNS!
    if defined INTERNAL_IP6_ADDRESS echo ipv6_address=!INTERNAL_IP6_ADDRESS!
    if defined INTERNAL_IP6_DNS echo ipv6_dns=!INTERNAL_IP6_DNS!
    if defined CISCO_SPLIT_DNS echo split_dns=!CISCO_SPLIT_DNS!
    if defined CISCO_DEF_DOMAIN echo default_domain=!CISCO_DEF_DOMAIN!
    if defined CISCO_BANNER echo banner=!CISCO_BANNER!
) > "!NETWORK_STATE_TMP!"
move /y "!NETWORK_STATE_TMP!" "!NETWORK_STATE!" >nul 2>&1
`

func writeRouteFreeVPNScript(stateDir string) (string, error) {
	path := filepath.Join(stateDir, ".openconnect-vpnc-script.bat")
	if err := os.WriteFile(path, []byte(routeFreeVPNScript), 0o700); err != nil {
		return "", fmt.Errorf("write route-free OpenConnect Windows script: %w", err)
	}
	return path, nil
}
