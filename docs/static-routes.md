# Static IP/CIDR routes

Kikimora normally routes traffic by domain name through Leshy DNS. That path does not cover connections made directly to a bare IP address, because no DNS query happens for a command such as:

```bash
ping 172.25.36.237
```

For bare IPs and private VPN networks, use Kikimora static route lists. These are rendered into Leshy `static_routes` entries.

## Files

Installed route lists live here:

```text
/etc/kikimora/leshy/routes/primary.txt
/etc/kikimora/leshy/routes/secondary.txt
```

Release packages also contain seed files that may be edited before the first install:

```text
linux/files/routes/primary.txt
linux/files/routes/secondary.txt
```

Each non-empty, non-comment line is an IP address or CIDR route:

```text
172.25.36.0/24
172.25.36.237
```

A bare IPv4 address is treated by Leshy as a host route, equivalent to `/32`.

## Before first install

To preseed routes before `kk` exists, edit the package seed file and then run the installer:

```bash
nano linux/files/routes/secondary.txt
sudo ./kikimora install --primary-interface amn0 --secondary-interface vpn0
sudo kk enable --now
```

For example, add this to `linux/files/routes/secondary.txt` before installation:

```text
172.25.36.0/24
```

During install, `build-config` reads package route seeds when no installed route directory exists yet. The generated Leshy config will include those static routes.

If the installed `/etc/kikimora/leshy/routes/*.txt` files do not exist yet, the first `kk routes` command can materialize them back from the generated `config.toml`.

## CLI after install

Show route counts:

```bash
kk routes
```

List configured routes:

```bash
kk routes list
kk routes list secondary
```

Add a private network to the secondary VPN path:

```bash
sudo kk routes add 172.25.36.0/24 --secondary
```

Add one host route:

```bash
sudo kk routes add 172.25.36.237 --secondary
```

Edit a route list directly:

```bash
sudo kk routes edit secondary
```

After a route change, Kikimora rebuilds `config.toml`, validates it with Leshy, rolls back on validation failure, and restarts `leshy.service` when it is active.

## Why this is separate from domain lists

Domain lists are used for DNS-triggered routing:

```text
domain -> Leshy DNS -> resolved IP -> route for resolved IP
```

Static route lists are used for direct IP/CIDR routing:

```text
bare IP or private network -> preconfigured Leshy static route
```

Do not put IP addresses or CIDR routes into:

```text
/etc/kikimora/leshy/domains/primary.txt
/etc/kikimora/leshy/domains/secondary.txt
/etc/kikimora/leshy/domains/bypass.txt
```

Those files are validated as domain lists. Use `/etc/kikimora/leshy/routes/*.txt` or package `linux/files/routes/*.txt` instead.

## Important Leshy behavior

Kikimora writes static routes only on inclusive `primary` and `secondary` zones.

It does not attach `static_routes` to `default-primary` or `default-secondary`, because those default zones are Leshy `exclusive` zones. In Leshy, `static_routes` inside an exclusive zone are treated as exclusion ranges, not routes to add.
