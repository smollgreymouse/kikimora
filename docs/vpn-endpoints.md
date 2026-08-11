# VPN endpoint underlay routing

Kikimora can force the control/data endpoints used to establish the primary and secondary VPN tunnels through the physical network instead of through either VPN.

The installation package contains separate endpoint templates:

```text
linux/files/endpoints/primary.txt
linux/files/endpoints/secondary.txt
```

The installer creates the corresponding user configuration files and preserves existing ones on reinstall/upgrade:

```text
/etc/kikimora/leshy/endpoints/primary.txt
/etc/kikimora/leshy/endpoints/secondary.txt
```

The route watcher also recreates an empty template if an endpoint file is unexpectedly missing.

Each non-comment line is one exact hostname or IP address. Matching is intentionally exact: wildcards and parent-domain matching are not supported.

Example:

```text
# /etc/kikimora/leshy/endpoints/secondary.txt
ve.ad-tech.ru
```

This remains exact even when normal domain routing contains a parent domain such as:

```text
# /etc/kikimora/leshy/domains/secondary.txt
ad-tech.ru
```

In that case `ve.ad-tech.ru` is resolved and its IP addresses are forced through the physical Wi-Fi/Ethernet underlay, while other hosts below `ad-tech.ru` continue to use the secondary VPN.

On Linux Kikimora installs high-priority destination rules into routing table `51890`. Primary and secondary endpoint rules use separate priorities, so each role can be transitioned independently. The underlay route excludes both configured VPN interfaces and uses the best remaining physical default route. An unreachable fallback prevents a protected endpoint from falling through into normal VPN routing while a physical path is temporarily unavailable.

## Lifecycle

Endpoint routing is active only while Kikimora is running.

`sudo kk start` applies or reconciles endpoint DIRECT policy before starting Leshy and route-watch. `sudo kk enable --now` follows the same order. This prevents a VPN started afterwards from accidentally building its transport through the other VPN.

`sudo kk restart` keeps endpoint policy in place while restarting Kikimora services. It does not remove and recreate endpoint routes underneath already-connected VPN clients.

`sudo kk stop` removes endpoint policy after stopping Kikimora, but refuses before changing anything if protected policy is present and either managed VPN interface is still UP. Disconnect the VPN interfaces first. `sudo kk stop --force` explicitly overrides this protection and removes the policy anyway. `sudo kk disable --now` has the same safety check; `--force` is accepted only together with `--now`.

A plain `sudo kk enable` only enables autostart. It does not start Kikimora and therefore does not activate endpoint policy until `kk start`, `kk enable --now`, or the next system boot starts the services.

## Starting while a VPN is already connected

Kikimora never rewrites a conflicting endpoint route underneath a live VPN merely to make the policy correct.

If a role's VPN interface is already UP and the desired endpoint path differs from the currently applied endpoint policy, that role becomes `underlay-pending`. Its runtime device file is withdrawn, so Leshy cannot treat the role as ready while the endpoint transition is unresolved.

Disconnect that VPN once. Route-watch sees the safe down transition, installs the pending endpoint DIRECT policy, clears the pending state, and the next VPN connection goes through the physical underlay. Normal link readiness then starts again from zero.

The logic is symmetric. A secondary endpoint accidentally routed through primary and a primary endpoint accidentally routed through secondary are handled the same way; Kikimora does not depend on OpenConnect, WireGuard, Amnezia, or any other VPN-client-specific reconnect mechanism.

During the draft PR migration, an older combined endpoint policy is left untouched while either VPN is UP. Disconnect both managed VPN interfaces once to let Kikimora migrate that temporary draft state without cutting a live transport.

## Shared-IP limitation

Routing happens by IP after DNS resolution. If a VPN endpoint hostname shares an IP address with another service, all traffic to that IP is forced through the physical underlay. This is an inherent limitation of IP routing and should be considered when choosing endpoint hostnames.
