# Toad profiles

Profiles are a core-level concept for grouping managed VPN roles and switching
the active group without a UI. A profile is an **explicit set of roles**; the
active profile determines which roles are desired-enabled at any time.

## TOML configuration

Each role can declare the profiles it belongs to:

```toml
name = "work-vpn"
protocol = "amneziawg2"
profiles = ["work"]
# ... rest of config
```

If `profiles` is omitted, the role belongs only to the implicit `default`
profile. The `default` profile always includes **all** configured roles for
backwards compatibility.

A role may belong to multiple profiles:

```toml
profiles = ["work", "travel"]
```

## API / CLI

- `kikimora-core profiles` lists known profiles (derived from role configs).
- `kikimora-core profiles use <name>` activates a profile.
- `SetActiveProfile` API call does the same.

Switching a profile:

- enables all roles that belong to the target profile;
- disables all roles that do not belong to it;
- persists the new desired state to `desired.json`;
- rolls back in-memory changes if persistence fails.

## How routing works with profiles

Leshy owns routing/DNS policy. When a profile is active, only the roles in that
profile remain desired-enabled; Leshy then routes traffic between the active
managed Toads according to the existing endpoint/DNS classification and
`rule_priority`/`leshy_zone` rules. A profile change is therefore a **selection
of which Toads participate in routing**, not a separate routing policy editor.

## Limitations

- There is no UI yet; profiles are driven through the core CLI/API.
- Profiles cannot be configured independently of roles; a profile exists only
  if at least one role names it.
