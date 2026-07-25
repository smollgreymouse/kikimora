# Security Policy

## Supported versions

| Version | Supported |
|---|---|
| 1.0.x | Yes |
| Earlier versions | No |

Only the latest released patch version is expected to receive security fixes.

## Reporting a vulnerability

Do not open a public GitHub issue for a suspected security vulnerability.

Until a dedicated security email address is published, contact the repository
owner privately through an account-associated contact method.

Include:

- affected version;
- operating system and distribution;
- relevant configuration;
- reproduction steps;
- expected and actual behavior;
- security impact;
- whether the issue is already being exploited;
- suggested mitigation, if known.

Do not include:

- VPN credentials;
- private keys;
- authentication tokens;
- unredacted private domain lists;
- personally identifying network logs.

## Response process

The maintainer will attempt to:

1. acknowledge the report;
2. reproduce and assess the issue;
3. identify affected versions;
4. prepare a fix or mitigation;
5. coordinate disclosure;
6. publish a security release when appropriate.

Response times are not guaranteed.

## Security scope

Security-sensitive areas include:

- privileged script execution;
- command injection;
- unsafe configuration parsing;
- writable system script paths;
- temporary-file handling;
- route or DNS manipulation;
- resolver bypass;
- unintended VPN bypass;
- firewall interactions;
- service unit privilege configuration.

## Out of scope

The following are generally outside the project security boundary:

- vulnerabilities in the VPN provider;
- vulnerabilities in Leshy itself;
- malicious behavior by privileged local administrators;
- VPN client kill-switch behavior already documented as incompatible;
- application-level DNS-over-HTTPS bypass;
- unsupported local modifications;
- leaks caused by unconfigured IPv6 routing;
- denial of service requiring root access.

## Disclosure

Please allow reasonable time for investigation and release before publishing
details that could help others exploit the issue.
