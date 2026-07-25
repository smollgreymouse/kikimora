## Summary

Describe the change and the problem it solves.

## Type of change

- [ ] Bug fix
- [ ] New feature
- [ ] Refactor
- [ ] Documentation
- [ ] Tests
- [ ] CI or tooling
- [ ] Breaking change

## Design

Explain important implementation decisions.

## Testing

Describe the test environment and commands used.

- [ ] ShellCheck passes
- [ ] Healthy startup tested
- [ ] `leshy-dns check` tested
- [ ] DNS repair tested
- [ ] VPN reconnect tested
- [ ] Primary routing tested
- [ ] Secondary routing tested
- [ ] Fallback behavior tested
- [ ] Documentation updated

## Compatibility

List any impact on:

- command names;
- exit codes;
- configuration files;
- systemd units;
- interface names;
- supported VPN clients.

## Logs or screenshots

Include redacted evidence where useful.

## Checklist

- [ ] The change is focused.
- [ ] Recovery paths are idempotent.
- [ ] Runtime state is used as the source of truth.
- [ ] No secrets or private configuration are included.
- [ ] User-visible changes are added to `CHANGELOG.md`.
- [ ] I have read `CONTRIBUTING.md`.
