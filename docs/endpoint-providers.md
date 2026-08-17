# Endpoint provider API

Kikimora resolves VPN transport endpoints through small executable providers.
Provider selection belongs to each role of each VPN profile; it is not a global
machine setting. This lets a normal `amn0 + vpn0` profile stay fully static while
a different profile can use a dynamic provider such as Happ only for `tun0`.

Built-in providers are installed in:

```text
/usr/local/libexec/kikimora/leshy/endpoint-providers/
    static
    happ
    command
```

The source package keeps the same layout under:

```text
linux/files/endpoint-providers/
```

## Profile configuration

A profile stores six endpoint-relevant fields:

```text
primary interface
primary endpoint provider
primary provider args
secondary interface
secondary endpoint provider
secondary provider args
```

For example:

```bash
sudo kk profiles add normal amn0 vpn0
sudo kk profiles add happ-test amn1 tun0 --secondary-provider happ
```

The first profile is `static/static`. The second is `static/happ`.

A command-backed provider can be selected with:

```bash
sudo kk profiles add custom amn1 tun0 \
  --secondary-provider command \
  --secondary-provider-args /usr/local/libexec/my-endpoint-provider
```

`profiles use` atomically switches interfaces and provider selections together.
A newly specified interface defaults to `static`. A role kept with `-` (or an
omitted secondary interface) keeps its current provider and provider args unless
they are explicitly overridden.

The persistent profile store contains parallel associative arrays:

```bash
declare -Ag VPN_PROFILE_PRIMARY=(...)
declare -Ag VPN_PROFILE_SECONDARY=(...)
declare -Ag VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER=(...)
declare -Ag VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER=(...)
declare -Ag VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS=(...)
declare -Ag VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS=(...)
```

The active values are materialized into `/etc/kikimora/leshy/vpn.conf` as:

```bash
PRIMARY_ENDPOINT_PROVIDER="static"
PRIMARY_ENDPOINT_PROVIDER_ARGS=""
SECONDARY_ENDPOINT_PROVIDER="happ"
SECONDARY_ENDPOINT_PROVIDER_ARGS=""
```

Older pair-only profile stores remain readable and are migrated on the next
modifying profile command. Historical profiles without provider metadata default
to `static`, while the currently selected pair preserves its current provider
state.

## Provider invocation contract

A provider is an executable file named by the profile. Kikimora accepts provider
names containing alphanumerics plus `.`, `_`, and `-`; slashes are intentionally
not allowed. The provider executable is invoked as:

```text
PROVIDER ROLE PROVIDER_ARGS
```

`ROLE` is exactly `primary` or `secondary`. `PROVIDER_ARGS` is one opaque
single-line string stored in the profile. Providers may define their own syntax
for that string.

The provider receives these environment variables:

```text
KIKIMORA_ENDPOINT_ROLE
KIKIMORA_ENDPOINT_INTERFACE
KIKIMORA_ENDPOINTS_DIR
KIKIMORA_VPN_CONFIG
KIKIMORA_UNDERLAY4_DEVICE
KIKIMORA_UNDERLAY6_DEVICE
```

The last two are the physical devices selected from the main-table defaults
after excluding both currently managed VPN interfaces.

A provider must not modify routing policy itself. It only reports endpoint
specifications on stdout. Kikimora owns `ip rule`, routing table `51890`, pending
state, live-update safety, and cleanup.

## Provider output

Output is line oriented. Blank lines and comments are ignored. Every non-comment
line is one exact endpoint hostname or numeric IP address. Wildcards, CIDRs and
parent-domain matching are rejected by core.

A provider may declare one capability header:

```text
# kikimora-endpoint-provider-mode: static
```

or:

```text
# kikimora-endpoint-provider-mode: dynamic-additive
```

If the header is omitted, `static` is assumed.

A non-zero exit status means discovery failed. Kikimora keeps the previous
endpoint policy. If the role interface is currently active, a provider failure
also creates endpoint pending state so a replacement/unready interface cannot be
published without endpoint protection. An already-published instance of the same
interface retains the PR #16 same-interface pending behavior.

### `static` mode

`static` means the complete desired endpoint set is stable enough to use the
normal PR #16 lifecycle. If it differs while the VPN is active, Kikimora will not
rewrite the live transport path. The role becomes `underlay-pending` until a safe
disconnect unless the already-installed rules and physical paths exactly match
what the provider now requests.

Use this mode for configuration files, fixed WireGuard/OpenVPN endpoints, or
custom providers whose result must be treated as an exact set.

### `dynamic-additive` mode

`dynamic-additive` is for clients that select or rotate transport servers while
the tunnel remains up.

This capability carries a stronger provider contract: every newly emitted
endpoint must already be a transport endpoint observed on the currently selected
physical underlay. Core therefore permits only a monotonic live change:

```text
installed endpoints := installed endpoints UNION newly observed endpoints
```

While the role interface remains active, Kikimora never removes an old endpoint
rule and never rewrites an existing endpoint onto a different physical default.
A physical-underlay change still becomes pending. Exact cleanup is deferred until
the managed VPN interface is down.

This prevents a dynamic plugin from silently turning a live VPN transport into a
new route merely because it emitted an arbitrary address. Authors of
`dynamic-additive` providers are responsible for making the observation proof
specific enough for their VPN client.

## Built-in `static` provider

`static` reads the existing role file:

```text
/etc/kikimora/leshy/endpoints/primary.txt
/etc/kikimora/leshy/endpoints/secondary.txt
```

The files remain shared configuration. A normal profile needs no provider flags:

```bash
sudo kk profiles add normal amn0 vpn0
```

Both sides default to `static`.

`static` accepts no provider args.

## Built-in `happ` provider

Happ can rotate among several servers automatically, so storing one endpoint or
a subscription-wide server dump is the wrong abstraction. The provider discovers
what Happ is actually using from the live tunnel/socket topology.

With the current Linux Happ layout:

```text
application traffic
      -> tun0 (sing-box TUN)
      -> local Xray proxy
      -> Xray logical outbound socket from tun0 address
      -> sing-box physical socket from Wi-Fi/Ethernet address
      -> remote Happ transport server
```

The provider collects two sets from `ss -ntup`:

1. remote numeric IPs used by `xray` sockets whose local address belongs to the
   managed Happ TUN interface;
2. remote numeric IPs used by `sing-box` sockets whose local address belongs to
   the selected physical underlay.

Only the intersection is emitted.

This matters because sing-box also has unrelated direct sockets, including DNS
traffic such as `8.8.8.8:53`, while Xray may have logical sockets that are not a
currently established physical Happ transport. Neither side alone is precise
enough.

The default process pair is:

```text
xray,sing-box
```

It can be overridden through provider args if a Happ build uses other process
names:

```bash
sudo kk profiles add happ-alt amn1 tun0 \
  --secondary-provider happ \
  --secondary-provider-args 'my-xray,my-sing-box'
```

The `happ` provider declares `dynamic-additive`. When Happ switches servers,
Kikimora may add the newly observed physical transport endpoint while `tun0`
remains active. Old endpoint rules stay in place until the safe down boundary.

The provider intentionally does not parse `~/.config/Happ/subs.db`. The database
contains an application-specific BLOB and represents available subscription
state, not necessarily the transport Happ is using right now. Runtime socket
intersection is the source of truth for this provider.

## Built-in `command` provider

`command` is a generic adapter for locally supplied provider scripts. Its provider
args must be one absolute executable path:

```bash
--secondary-provider command \
--secondary-provider-args /usr/local/libexec/my-endpoint-provider
```

The adapter executes:

```text
/usr/local/libexec/my-endpoint-provider ROLE
```

with the same provider environment described above, and passes stdout/status
through unchanged. If arguments beyond the role are needed, create a small
wrapper executable and point `command` at that wrapper. Kikimora deliberately
does not evaluate a shell command string.

## Writing a new provider

A minimal exact/static provider can be:

```bash
#!/usr/bin/env bash
set -Eeuo pipefail

role="${1:?role required}"
printf '# kikimora-endpoint-provider-mode: static\n'
printf 'vpn.example.net\n'
```

A dynamic provider must additionally prove its endpoints are already live on the
physical underlay before printing them. Do not mark a provider
`dynamic-additive` merely because its configuration changes often.

Provider implementation rules:

- write endpoint specs only to stdout; diagnostics go to stderr;
- exit non-zero when discovery is unreliable rather than guessing;
- do not call `ip rule`, modify table `51890`, or manipulate Kikimora runtime
  state;
- do not emit CIDRs or wildcards;
- avoid secrets on stdout: only endpoint hostnames/IPs belong in the protocol;
- treat provider args as configuration, not as shell text;
- for `dynamic-additive`, emit only transports proven to be currently physical;
- make empty output meaningful: it is a valid empty exact set while a role is
  down, and while a dynamic role is active it causes no live removals.

For a package-shipped provider, add the executable under
`linux/files/endpoint-providers/`, install it with mode `0755`, include it in
installer pre-flight validation, add Bash/ShellCheck coverage, and document its
provider-args syntax here.

For a local one-off provider, prefer the built-in `command` adapter so package
upgrades do not overwrite the custom executable.
