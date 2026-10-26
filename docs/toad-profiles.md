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

A profile is an explicit set of roles. That means a profile can also exist with
**no** roles at all - otherwise a freshly created profile could not appear before
its first role was assigned. Such names live in `profiles.list` next to the toad
configurations (one name per line; deliberately not a `*.toml`, because every
`*.toml` in that directory is a role). The registry is loaded at startup and
seeded into the profile map, so an empty profile is listed, selectable and simply
enables nothing.

## API / CLI

- `kikimora-core profiles` lists known profiles (role configs plus the registry).
- `kikimora-core profiles use <name>` activates a profile.
- `SetActiveProfile` API call does the same.
- `CreateProfile` registers a profile and optionally fills it with a role set;
  `SetProfileRoles` replaces that set later. Both are runtime operations: a
  profile is a label, not a process, so the running core applies it at once.

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

## Testing

Profile support is covered by both isolated unit tests and privileged,
OS-specific integration tests.

### Isolated tests

These tests run without elevated privileges and use fake launchers:

```bash
cd toad
go test ./internal/config -run TestValidateProfiles -v
go test ./internal/control -run Profile -v
```

Covered behaviours:

- `default` profile is normalized when `profiles` is omitted.
- Custom profiles are preserved and validated.
- Duplicate or empty profile names are rejected.
- `SetActiveProfile` enables/disables the correct roles.
- `SetActiveProfile` is a no-op when the target profile is already active.
- Persistence failures roll back in-memory state.
- `Manager.Profiles()` lists all known profiles.

### Privileged / whole-system tests

These tests require administrator/root access and run with the `privileged`
build tag. They create a real `Manager` (on Linux inside a fresh network
namespace) and exercise profile switching against the actual platform
underlay/leshy code path.

```bash
cd toad
# Linux (root)
go test ./internal/control -tags privileged -run TestPrivilegedProfileSwitchingLinux -v

# Windows (elevated PowerShell/cmd)
go test ./internal/control -tags privileged -run TestPrivilegedProfileSwitchingWindows -v

# Darwin (root)
go test ./internal/control -tags privileged -run TestPrivilegedProfileSwitchingDarwin -v
```

The Linux variant uses a throw-away `netns` namespace so it does not modify
host routes or interfaces.

## Limitations

- There is no UI yet; profiles are driven through the core CLI/API.
- Profiles cannot be configured independently of roles; a profile exists only
  if at least one role names it.
