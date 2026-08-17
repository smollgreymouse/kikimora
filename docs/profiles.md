# VPN profiles

Kikimora profiles select the Linux VPN interface **and endpoint provider** for
each existing routing role (`primary` and `secondary`). Domain lists, static
route lists and generated Leshy zones remain shared.

A profile therefore describes two role states, not only an interface pair:

```text
primary   = interface + endpoint provider + provider args
secondary = interface + endpoint provider + provider args
```

This is useful when one profile uses ordinary fixed-endpoint VPN interfaces and
another uses a client such as Happ whose transport endpoint changes while its
`tun0` interface stays up.

## Commands

```text
kk profiles
sudo kk profiles add NAME PRIMARY [SECONDARY] [provider options]
sudo kk profiles use NAME
sudo kk profiles remove NAME
```

Provider options are:

```text
--primary-provider NAME
--secondary-provider NAME
--primary-provider-args ARG
--secondary-provider-args ARG
```

The first `profiles add` creates `/etc/kikimora/leshy/profiles.conf` and keeps the
complete current primary/secondary state as profile `default`.

Examples:

```bash
# Change only primary. The omitted secondary keeps its current interface,
# endpoint provider and provider args.
sudo kk profiles add office amn1

# Change only secondary; '-' keeps the complete current primary role.
sudo kk profiles add backup - vpn1

# Change both interfaces. Newly specified roles default to provider `static`.
sudo kk profiles add travel amn2 vpn2

# New primary is static; secondary tun0 dynamically discovers Happ transports.
sudo kk profiles add happ-test amn2 tun0 --secondary-provider happ

# A custom provider can be delegated to a local executable.
sudo kk profiles add custom amn2 tun0 \
    --secondary-provider command \
    --secondary-provider-args /usr/local/libexec/my-endpoint-provider

sudo kk profiles use happ-test
```

A newly specified interface defaults to endpoint provider `static`. A role kept
with `-` (and an omitted secondary role) keeps its current provider and args
unless explicitly overridden.

Profiles may share either interface or even the same interface pair. This is
intentional: `normal=(amn0[static],vpn0[static])` and
`happ=(amn0[static],tun0[happ])` differ only in secondary; two profiles can also
use the same interface pair with different provider configuration. Only an exact
duplicate of the complete profile state is rejected because active-profile
detection would otherwise be ambiguous.

Within one profile primary and secondary interfaces must be different.

## What a switch changes

`profiles use` atomically rewrites all role selections in `vpn.conf`:

```text
PRIMARY_INTERFACE
PRIMARY_ENDPOINT_PROVIDER
PRIMARY_ENDPOINT_PROVIDER_ARGS
SECONDARY_INTERFACE
SECONDARY_ENDPOINT_PROVIDER
SECONDARY_ENDPOINT_PROVIDER_ARGS
```

Unrelated settings are preserved. The provider configuration therefore follows
the interface profile: switching back to an ordinary `amn0 + vpn0` profile also
switches back to `static/static`; Happ discovery is not global.

The role device files remain:

```text
/run/kikimora/leshy/vpn/primary.dev
/run/kikimora/leshy/vpn/secondary.dev
```

Their contents follow the selected interfaces only after endpoint safety and
readiness validation.

## Readiness and PR #16 endpoint semantics

Readiness belongs to a `(role, candidate interface)` tuple, not just to
`primary` or `secondary`. When a profile changes a role, readiness accumulated by
the old interface cannot be inherited by the replacement.

An already-published device is preserved across `underlay-pending` only when the
published interface is still the configured interface for that role. If a
profile changes the role to another interface, the old publication is withdrawn
and the replacement starts from readiness zero.

Endpoint providers are evaluated by the same endpoint-underlay controller used
by PR #16. A provider failure or an unsafe exact endpoint change keeps the role
pending instead of publishing a replacement without endpoint protection.

The built-in Happ provider uses the `dynamic-additive` provider capability. It
may add a newly observed Happ transport endpoint while `tun0` remains up, but it
cannot remove old endpoint rules or move existing endpoint routes to another
physical underlay until a safe down transition. See
[`endpoint-providers.md`](endpoint-providers.md) for the provider contract.

## Fail-closed route parking

When a changed role is withdrawn, route-watch sees the old device disappear from
the published runtime set. The PR #16 route lifecycle parks the last observed
Leshy-owned IPv4 `/32` destinations as high-metric `unreachable` routes. They
cannot fall through to Wi-Fi, Ethernet, the other VPN, or another default route
while the replacement interface stabilizes.

When the replacement VPN becomes ready, Leshy recreates real lower-metric host
routes. Parking is removed only after those real routes have been observed.

If only one role changes, the unchanged role remains published and keeps its
readiness state.

## Static endpoint files remain shared

Provider selection is profile-local, but the files used by the built-in
`static` provider remain role-wide shared configuration:

```text
/etc/kikimora/leshy/endpoints/primary.txt
/etc/kikimora/leshy/endpoints/secondary.txt
```

Consequently, every profile role using `static` reads the corresponding shared
file. Put every fixed VPN-server hostname/IP needed by those static roles in the
file. Profiles using `happ` or another provider do not require their dynamically
discovered endpoints to be copied into these files.

Endpoint underlay remains separate from Leshy route parking: VPN transport
endpoints stay reachable over the physical network so tunnels can establish or
recover while user destinations remain fail closed.

## Persistent format and compatibility

The current profile store uses parallel associative arrays:

```bash
declare -Ag VPN_PROFILE_PRIMARY=(...)
declare -Ag VPN_PROFILE_SECONDARY=(...)
declare -Ag VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER=(...)
declare -Ag VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER=(...)
declare -Ag VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER_ARGS=(...)
declare -Ag VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER_ARGS=(...)
```

Two older draft formats remain readable:

- `PRIMARY_PROFILES`, which described only primary interfaces;
- the previous primary/secondary pair format without endpoint-provider metadata.

They are migrated on the next modifying profile command. Pair-only historical
profiles default to the old `static` behavior; the currently selected state
preserves provider values already materialized in `vpn.conf`.
