# Contributing to Kikimora

Thank you for considering a contribution to Kikimora.

Kikimora changes privileged Linux networking and DNS state. Contributions must
therefore favor correctness, observability, idempotence, and safe recovery over
cleverness.

## Ways to contribute

You can help by:

- reporting reproducible bugs;
- improving documentation;
- adding tests;
- improving diagnostics;
- proposing safer recovery behavior;
- testing other VPN clients or Linux distributions;
- reviewing shell scripts and systemd units.

## Before opening an issue

Please check:

1. The existing issues.
2. The troubleshooting section in `DOCUMENTATION.md`.
3. Whether AmneziaVPN Kill Switch is disabled.
4. Whether application-level DNS-over-HTTPS is disabled.
5. Whether the problem is reproducible after a clean service restart.

Collect the following information:

```bash
kk status
systemctl status leshy.service
systemctl status leshy-route-watch.service
systemctl status leshy-health-watch.service
resolvectl status
ip route
sudo nft list ruleset
```

Redact secrets, public IP addresses, account identifiers, and private domains.

## Development setup

A development environment should include:

- a Linux VM or dedicated test machine;
- systemd;
- systemd-resolved;
- Bash;
- ShellCheck;
- `shfmt`;
- `iproute2`;
- `dig`;
- a test Leshy installation.

Do not test disruptive route or DNS changes on a production workstation without
a recovery plan.

## Branching

Create a focused branch:

```bash
git switch -c fix/dns-repair
```

Recommended prefixes:

```text
fix/
feature/
docs/
refactor/
test/
ci/
```

## Coding style

### Bash

- Use `#!/usr/bin/env bash` or the project-standard shebang.
- Use `set -Eeuo pipefail` when compatible with the script.
- Quote variable expansions.
- Prefer `[[ ... ]]` over `[ ... ]`.
- Use local variables inside functions.
- Use descriptive function names.
- Return meaningful exit codes.
- Avoid parsing human-formatted command output when a stable machine-readable
  form exists.
- Keep privileged operations explicit.
- Log state transitions and recovery results.
- Make recovery functions idempotent.

Example:

```bash
check_runtime() {
    require_command ip
    require_command resolvectl
    runtime_is_enabled
}
```

### ShellCheck

All shell changes must pass:

```bash
shellcheck path/to/script
```

Repository-wide example:

```bash
find . -type f -name '*.sh' -print0 | xargs -0 shellcheck
```

### Formatting

Recommended:

```bash
shfmt -w -i 4 -ci .
```

Do not reformat unrelated files in a focused pull request.

## Architectural rules

Contributions must preserve these boundaries:

- `leshy-dns` owns `systemd-resolved` integration.
- Health Watch calls public `leshy-dns` commands.
- Route Watch owns route reconstruction triggered by interface changes.
- Runtime state is the source of truth.
- Marker files are hints, not health checks.
- A recoverable resolver failure should not require a Leshy restart.
- Failure handling should leave the machine with usable DNS whenever possible.
- CLI commands are implemented as sourced modules in `files/kikimora-cli/`.
  Do not add command logic to the root `kikimora` dispatcher — add a new
  module in `files/kikimora-cli/` and `source` it from the entrypoint.

## Tests

Every behavior change should include a test plan.

Minimum manual test matrix for DNS changes:

1. Healthy startup.
2. `leshy-dns check` returns `0`.
3. Remove `~.` from `leshy-dns0`.
4. `leshy-dns check` returns `1`.
5. Run `leshy-dns resume`.
6. Verify `~.` is restored.
7. Restart Health Watch.
8. Repeat the failure and verify automatic recovery.
9. Disconnect the primary VPN.
10. Reconnect it and verify routes and DNS.
11. Confirm normal DNS fallback when Leshy is stopped.

For routing changes, test both primary and secondary destinations.

## Commit messages

Use concise imperative commit messages.

Good examples:

```text
Fix DNS repair after VPN reconnect
Add runtime resolver validation
Document Kill Switch incompatibility
```

Avoid:

```text
changes
fix stuff
update
```

## Pull requests

A pull request should:

- solve one focused problem;
- explain the failure mode;
- explain the chosen design;
- include testing evidence;
- mention operational or compatibility risks;
- update documentation when behavior changes;
- update `CHANGELOG.md` when user-visible behavior changes.

Complete the pull request template.

## Documentation changes

Documentation should:

- use clear technical English;
- distinguish observed behavior from assumptions;
- include exact commands when useful;
- avoid promising support that has not been tested;
- keep interface names identified as reference defaults;
- document new failure modes and recovery steps.

## Backward compatibility

Version 1.x changes should avoid breaking:

- command names;
- exit-code semantics;
- configuration paths;
- systemd unit names;
- domain list formats.

A breaking change should be reserved for a new major version.

## Security-sensitive changes

Changes involving any of the following require extra review:

- root privileges;
- command execution;
- file permissions;
- route tables;
- nftables;
- resolver ownership;
- configuration parsing;
- untrusted domain lists;
- temporary files.

Do not submit known vulnerabilities in a public issue. Follow `SECURITY.md`.

## Code of conduct

All contributors must follow `CODE_OF_CONDUCT.md`.

## License

By contributing, you agree that your contributions will be licensed under the
MIT License.
