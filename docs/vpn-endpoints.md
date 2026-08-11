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

On Linux Kikimora installs high-priority destination rules into a dedicated routing table. The underlay table excludes the configured primary and secondary VPN interfaces and uses the best remaining default route. An unreachable fallback prevents endpoint traffic from falling back into the normal VPN routing policy while the physical route is temporarily unavailable.

This makes VPN startup order irrelevant: primary then secondary and secondary then primary both keep each VPN server endpoint on the physical underlay.

## Shared-IP limitation

Routing happens by IP after DNS resolution. If a VPN endpoint hostname shares an IP address with another service, all traffic to that IP is forced through the physical underlay. This is an inherent limitation of IP routing and should be considered when choosing endpoint hostnames.
