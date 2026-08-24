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
endpoint must be positively observed as a transport endpoint of the selected VPN
client rather than inferred from subscription/configuration state. Core then
permits only a monotonic live change:

```text
installed endpoints := installed endpoints UNION newly observed endpoints
```

While the role interface remains active, Kikimora never removes an old endpoint
rule and never rewrites an existing endpoint onto a different physical default.
A physical-underlay change still becomes pending. Exact cleanup is deferred until
the managed VPN interface is down.

A dynamic provider may discover a transport after another managed VPN has already
captured its existing socket. In that case the newly installed physical pin only
affects future connections. The provider must make that degraded condition
explicit; Kikimora does not kill or reconnect foreign VPN processes automatically.

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
what Happ is actually using by correlating an active probe with live process
sockets.

Happ has shipped multiple Linux TUN layouts. In particular, installations may
have both `sing-box` and `xray`, with either process owning different sides of the
TUN/proxy chain, while other builds can use Xray as a single-process TUN backend.
Kikimora therefore does **not** hard-code either process as the physical transport
owner.

The default candidate process pair is:

```text
xray,sing-box
```

For every reconciliation the provider records which candidates are actually
running and their PIDs. An active HTTPS request is then sent through the managed
Happ interface. Before and after each probe round the provider samples TCP socket
byte counters for all running candidates and ranks `(process, remote IP)` by the
resulting byte delta. A round is conclusive only when the dominant flow clears the
minimum-byte and dominance-ratio thresholds. The union of dominant remote IPs
across conclusive rounds is emitted, and the process or processes that won those
rounds are recorded as the transport owner set.

This matches both common layouts:

```text
sing-box TUN -> local proxy chain -> Xray -> physical Happ transport
```

and:

```text
Xray built-in TUN -> physical Happ transport
```

while still allowing the inverse process ownership when a Happ build behaves
that way. Background/direct activity from the non-winning candidate is not
emitted merely because that process has an external socket.

The cache records:

```text
timestamp=...
other_interface=vpn0
other_active=0|1
degraded=0|1
process=xray:<pid>
process=sing-box:<pid>
owner=xray
endpoint=<observed transport IP>
```

Only running candidates are stored, so an Xray-only build contains no synthetic
sing-box identity. A cache is reusable only while the complete running-process
signature, other-managed-VPN topology, TTL, and endpoint liveness still match.
Changing either candidate PID invalidates the transport proof immediately.
Older cache formats that do not contain process/owner records invalidate once and
are rediscovered safely.

Bringing the other managed VPN up or down also invalidates the cache immediately.
If fresh correlation is temporarily inconclusive but the same Happ process set
still has a previously proven endpoint active, the provider keeps that endpoint,
marks the cache `degraded=1`, re-keys it to the current topology, and uses the
normal cache TTL as a retry cooldown. It prints a warning that a manual Happ
reconnect may be required if the existing transport is nested or broken. No VPN
process is killed, restarted, or toggled automatically.

Endpoint liveness and nested-transport warnings inspect both TCP and connected
UDP sockets for the recorded transport owner. UDP is intentionally **not** used
as fresh discovery proof: Linux `ss` does not expose reliable cumulative UDP byte
counters equivalent to the TCP counters used by the active correlation. A
UDP-only transport that has never been proven therefore remains fail-closed
instead of causing Kikimora to pin an arbitrary direct UDP destination.

The process pair can be overridden through provider args if a Happ build uses
other executable names:

```bash
sudo kk profiles add happ-alt amn1 tun0 \
  --secondary-provider happ \
  --secondary-provider-args 'my-xray,my-tun-process'
```

Both names are candidates; their position does not force one to be the transport
owner. Process names are restricted to alphanumerics plus `.`, `_`, and `-`.

The `happ` provider declares `dynamic-additive`. When Happ switches servers,
Kikimora may add newly observed transport endpoints while the managed TUN remains
active. Old endpoint rules stay in place until the safe down boundary.

The provider intentionally does not parse `~/.config/Happ/subs.db`. The database
represents available subscription state, not necessarily the transport Happ is
using right now. Active runtime correlation is the source of truth for this
provider.

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

A dynamic provider must additionally prove its endpoints are live transport
endpoints of the selected VPN before printing them. Do not mark a provider
`dynamic-additive` merely because its configuration changes often.

Provider implementation rules:

- write endpoint specs only to stdout; diagnostics go to stderr;
- exit non-zero when discovery is unreliable rather than guessing;
- do not call `ip rule`, modify table `51890`, or manipulate Kikimora runtime
  state;
- do not emit CIDRs or wildcards;
- avoid secrets on stdout: only endpoint hostnames/IPs belong in the protocol;
- treat provider args as configuration, not as shell text;
- for `dynamic-additive`, emit only positively observed VPN transports;
- make empty output meaningful: it is a valid empty exact set while a role is
  down, and while a dynamic role is active it causes no live removals.

For a package-shipped provider, add the executable under
`linux/files/endpoint-providers/`, install it with mode `0755`, include it in
installer pre-flight validation, add Bash/ShellCheck coverage, and document its
provider-args syntax here.

For a local one-off provider, prefer the built-in `command` adapter so package
upgrades do not overwrite the custom executable.
