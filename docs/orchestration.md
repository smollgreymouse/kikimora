# Service and DNS orchestration

Kikimora keeps Leshy service supervision and DNS runtime ownership separate, but
the operator-facing `kk` lifecycle commands orchestrate both pieces together.

## `kk start`

```text
systemctl start leshy.service leshy-route-watch.service leshy-health-watch.service
        |
        v
leshy-dns resume
        |
        v
leshy-dns check
        |
        +-- healthy --> done
        |
        +-- not healthy --> leshy-dns enable
```

A cold start does not require a separate `kk dns enable` call. If no saved DNS
state exists yet, `kk start` enables DNS integration after the services are up.

## `kk stop`

```text
build-config -> check-config
        |
        v
leshy-dns suspend
        |
        v
systemctl stop leshy.service leshy-route-watch.service leshy-health-watch.service
```

DNS is suspended before services stop, so the system resolver is not left
pointing at an unavailable local Leshy listener.

## `kk restart`

```text
leshy-dns suspend
        |
        v
systemctl stop leshy.service leshy-route-watch.service leshy-health-watch.service
        |
        v
systemctl start leshy.service leshy-route-watch.service leshy-health-watch.service
        |
        v
leshy-dns resume / check / enable
```

Configuration is generated into a temporary file and validated before replacing
the active `config.toml`. A generation or validation error leaves the running
services and their configuration untouched.

If the service restart fails, DNS remains restored to the normal system state.
Kikimora does not re-enable DNS through Leshy unless the services restart
successfully.

## `kk enable --now`

`kk enable --now` starts the managed systemd services and then verifies DNS the
same way as `kk start`. This makes first boot after installation work without a
manual `kk dns enable` step.

## `kk disable --now`

`kk disable --now` suspends DNS first and then disables/stops the managed
services.

## systemd cold boot

The `leshy.service` drop-in still contains non-fatal DNS hooks for OS boot:

```ini
ExecStartPost=-/usr/local/sbin/leshy-dns resume
ExecStartPost=-/bin/sh -c '/usr/local/sbin/leshy-dns check || /usr/local/sbin/leshy-dns enable'
ExecStopPost=-/usr/local/sbin/leshy-dns suspend
```

These hooks protect unattended cold boot. The leading `-` is intentional: DNS
repair must not kill the Leshy service process. Interactive lifecycle commands
still perform their own verification after `systemctl` returns.
