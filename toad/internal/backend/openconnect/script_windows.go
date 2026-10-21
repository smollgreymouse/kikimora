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
// OpenConnect on Windows invokes the script through cscript.exe (Windows
// Script Host) with the standard environment variables (TUNDEV, reason,
// INTERNAL_IP4_*, INTERNAL_IP6_*, CISCO_SPLIT_*, etc.). The .js
// extension dispatches through JScript/cscript automatically.
const routeFreeVPNScript = `// Kikimora route-free vpnc-script (Windows JScript)
// Configures TUN interface only; no routes, DNS or firewall changes.

var ws = WScript.CreateObject("WScript.Shell");
var env = ws.Environment("Process");
var fso = WScript.CreateObject("Scripting.FileSystemObject");

var reason = env("reason") || "";
if (reason !== "connect" && reason !== "reconnect") {
    WScript.Quit(0);
}

var tunDev = env("TUNDEV") || "";

// Bring the TUN interface up.
ws.Run("netsh interface ip set interface \"" + tunDev + "\" admin=enable", 0, true);

// Apply the negotiated IPv4 address and prefix.
var ipv4Addr = env("INTERNAL_IP4_ADDRESS") || "";
if (ipv4Addr !== "") {
    var prefix = env("INTERNAL_IP4_NETMASKLEN") || "32";
    ws.Run("netsh interface ip set address \"" + tunDev + "\" static " + ipv4Addr + "/" + prefix, 0, true);
}

// Apply the negotiated IPv6 address.
var ipv6Addr = env("INTERNAL_IP6_ADDRESS") || "";
if (ipv6Addr !== "") {
    ws.Run("netsh interface ipv6 add address \"" + tunDev + "\" " + ipv6Addr, 0, true);
}

// Apply the negotiated MTU when the server pushed one.
var mtu = env("INTERNAL_IP4_MTU") || "";
if (mtu === "") {
    mtu = env("INTERNAL_IP6_MTU") || "";
}
if (mtu !== "") {
    ws.Run("netsh interface ipv4 set subinterface \"" + tunDev + "\" mtu=" + mtu, 0, true);
    ws.Run("netsh interface ipv6 set subinterface \"" + tunDev + "\" mtu=" + mtu, 0, true);
}

// Publish the network state for the orchestrator. The atomic temp+move
// pattern prevents the core from reading a half-written file.
var scriptPath = WScript.ScriptFullName;
var stateDir = scriptPath.substring(0, scriptPath.lastIndexOf("\\") + 1);
var netState = stateDir + "openconnect-network.env";
var tmpFile = netState + ".tmp." + Math.floor(Math.random() * 100000);

var lines = [];
lines.push("reason=" + reason);
lines.push("tun_dev=" + tunDev);
if (mtu !== "") lines.push("mtu=" + mtu);
if (ipv4Addr !== "") {
    lines.push("ipv4_address=" + ipv4Addr);
    var maskLen = env("INTERNAL_IP4_NETMASKLEN") || "";
    if (maskLen !== "") lines.push("ipv4_netmasklen=" + maskLen);
}
var ipv4Dns = env("INTERNAL_IP4_DNS") || "";
if (ipv4Dns !== "") lines.push("ipv4_dns=" + ipv4Dns);
if (ipv6Addr !== "") lines.push("ipv6_address=" + ipv6Addr);
var ipv6Dns = env("INTERNAL_IP6_DNS") || "";
if (ipv6Dns !== "") lines.push("ipv6_dns=" + ipv6Dns);
var splitDns = env("CISCO_SPLIT_DNS") || "";
if (splitDns !== "") lines.push("split_dns=" + splitDns);
var defDomain = env("CISCO_DEF_DOMAIN") || "";
if (defDomain !== "") lines.push("default_domain=" + defDomain);
var banner = env("CISCO_BANNER") || "";
if (banner !== "") lines.push("banner=" + banner);

var f = fso.CreateTextFile(tmpFile, true, false);
for (var i = 0; i < lines.length; i++) {
    f.WriteLine(lines[i]);
}
f.Close();
if (fso.FileExists(netState)) {
    fso.DeleteFile(netState);
}
fso.MoveFile(tmpFile, netState);
`

func writeRouteFreeVPNScript(stateDir string) (string, error) {
	path := filepath.Join(stateDir, "openconnect-vpnc-script.js")
	if err := os.WriteFile(path, []byte(routeFreeVPNScript), 0o644); err != nil {
		return "", fmt.Errorf("write route-free OpenConnect Windows script: %w", err)
	}
	return path, nil
}
