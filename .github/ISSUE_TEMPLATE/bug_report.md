---
name: Bug report
about: Report a reproducible Kikimora problem
title: "[Bug] "
labels: bug
assignees: ""
---

## Summary

Describe the problem clearly.

## Environment

- Kikimora version:
- Linux distribution:
- Kernel:
- systemd version:
- Leshy version:
- Primary VPN client:
- Secondary VPN client:
- Primary interface:
- Secondary interface:

## Expected behavior

What should have happened?

## Actual behavior

What happened instead?

## Reproduction steps

1.
2.
3.

## Status output

```text
Paste `kk status` here.
```

## Resolver state

```text
Paste relevant `resolvectl status` output here.
```

## Service logs

```text
Paste relevant journal output here.
```

## Routing and firewall

```text
Paste relevant `ip route` and redacted nftables output here.
```

## Additional context

Mention whether:

- AmneziaVPN Kill Switch is disabled;
- browser DNS-over-HTTPS is disabled;
- IPv6 is enabled;
- the problem survives a service restart.

## Redaction checklist

- [ ] VPN credentials removed
- [ ] Public IP addresses redacted if necessary
- [ ] Private domains redacted if necessary
- [ ] Authentication tokens removed
