#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly ROOT
readonly CLI="${ROOT}/linux/kikimora"

die() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

command -v sha256sum >/dev/null 2>&1 || die 'sha256sum is required'

tmp="$(mktemp -d)"
trap 'sudo rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/domains"

cat >"$tmp/vpn.conf" <<'EOF_VPN'
PRIMARY_INTERFACE="amn0"
PRIMARY_DEVICE_FILE="/run/kikimora/leshy/vpn/primary.dev"
PRIMARY_ENDPOINT_PROVIDER="static"
PRIMARY_ENDPOINT_PROVIDER_ARGS=""
SECONDARY_INTERFACE="vpn0"
SECONDARY_DEVICE_FILE="/run/kikimora/leshy/vpn/secondary.dev"
SECONDARY_ENDPOINT_PROVIDER="static"
SECONDARY_ENDPOINT_PROVIDER_ARGS=""
VPN_LINK_READY_SUCCESSES=3
UNRELATED_SETTING="keep-me"
EOF_VPN

printf 'github.com\nopenai.com\n' >"$tmp/domains/primary.txt"
printf 'example.org\n' >"$tmp/domains/secondary.txt"
printf 'localhost\n' >"$tmp/domains/bypass.txt"
domains_before="$(sha256sum "$tmp/domains"/*.txt)"

run_profiles() {
    sudo env \
        KIKIMORA_VPN_CONFIG="$tmp/vpn.conf" \
        KIKIMORA_PROFILE_CONFIG="$tmp/profiles.conf" \
        bash "$CLI" profiles "$@"
}

list_before="$(env KIKIMORA_VPN_CONFIG="$tmp/vpn.conf" KIKIMORA_PROFILE_CONFIG="$tmp/profiles.conf" bash "$CLI" profiles list)"
grep -Fq 'Active profile: default (amn0[static] / vpn0[static])' <<<"$list_before" || die 'current state was not exposed as default profile'
grep -Fq 'Profile storage is not initialized yet' <<<"$list_before" || die 'uninitialized profile store was not reported'
[[ ! -e "$tmp/profiles.conf" ]] || die 'read-only profile listing created persistent state'

# Backward-compatible two-argument add changes only primary. The new interface
# defaults to static; the omitted secondary keeps its complete current state.
run_profiles add office amn1 >"$tmp/add-office.out"
grep -Fq 'Profile added: office -> primary=amn1[static] secondary=vpn0[static]' "$tmp/add-office.out" || die 'primary-only add did not preserve secondary state'
[[ -r "$tmp/profiles.conf" ]] || die 'profile store was not created'
grep -Fq '[default]=amn0' "$tmp/profiles.conf" || die 'current primary was not preserved in default profile'
grep -Fq '[default]=vpn0' "$tmp/profiles.conf" || die 'current secondary was not preserved in default profile'
grep -Fq 'VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER' "$tmp/profiles.conf" || die 'provider metadata was not persisted'
grep -Fq '[office]=amn1' "$tmp/profiles.conf" || die 'office primary was not persisted'

# '-' keeps the current primary including its provider; a new secondary defaults static.
run_profiles add backup - vpn1 >"$tmp/add-backup.out"
grep -Fq 'Profile added: backup -> primary=amn0[static] secondary=vpn1[static]' "$tmp/add-backup.out" || die 'secondary-only add did not preserve primary state'

# Both roles may change.
run_profiles add travel amn2 vpn2 >"$tmp/add-travel.out"
grep -Fq 'Profile added: travel -> primary=amn2[static] secondary=vpn2[static]' "$tmp/add-travel.out" || die 'pair add did not report both interfaces'

# Endpoint providers belong to each role of the profile. Happ is enabled only
# where explicitly selected.
run_profiles add happ-test amn3 tun0 --secondary-provider happ >"$tmp/add-happ.out"
grep -Fq 'Profile added: happ-test -> primary=amn3[static] secondary=tun0[happ]' "$tmp/add-happ.out" || die 'Happ provider was not stored on secondary'
grep -Fq '[happ-test]=happ' "$tmp/profiles.conf" || die 'Happ provider was not persisted'

# command is generic and provider args are persisted as one opaque string.
run_profiles add command-test amn4 tun1 \
    --secondary-provider command \
    --secondary-provider-args /usr/local/libexec/example-provider >/dev/null
grep -Fq '/usr/local/libexec/example-provider' "$tmp/profiles.conf" || die 'command provider args were not persisted'

# Same interfaces may have another provider state; only an exact full state is ambiguous.
run_profiles add office-happ amn1 vpn0 --secondary-provider happ >/dev/null
if run_profiles add duplicate-state amn1 vpn0 >/dev/null 2>&1; then
    die 'duplicate complete profile state was accepted'
fi
if run_profiles add invalid-same tun9 tun9 >/dev/null 2>&1; then
    die 'same interface was accepted for primary and secondary'
fi
if run_profiles add office amn9 vpn9 >/dev/null 2>&1; then
    die 'duplicate profile name was accepted'
fi

# Change only secondary. Interfaces and provider assignments are rewritten
# atomically from one tmp file while unrelated settings stay untouched.
run_profiles use backup >"$tmp/use-backup.out"
grep -Fq 'Active VPN profile changed to backup (primary=amn0[static] secondary=vpn1[static]).' "$tmp/use-backup.out" || die 'secondary-only switch did not report success'
grep -Fq 'PRIMARY_INTERFACE=amn0' "$tmp/vpn.conf" || grep -Fq 'PRIMARY_INTERFACE="amn0"' "$tmp/vpn.conf" || die 'secondary-only switch changed primary'
grep -Fq 'SECONDARY_INTERFACE=vpn1' "$tmp/vpn.conf" || grep -Fq 'SECONDARY_INTERFACE="vpn1"' "$tmp/vpn.conf" || die 'secondary-only switch did not update secondary'
grep -Fq 'PRIMARY_ENDPOINT_PROVIDER=static' "$tmp/vpn.conf" || die 'primary provider changed unexpectedly'
grep -Fq 'SECONDARY_ENDPOINT_PROVIDER=static' "$tmp/vpn.conf" || die 'secondary provider did not stay static'
grep -Fq 'PRIMARY_DEVICE_FILE="/run/kikimora/leshy/vpn/primary.dev"' "$tmp/vpn.conf" || die 'profile switch changed primary device file'
grep -Fq 'VPN_LINK_READY_SUCCESSES=3' "$tmp/vpn.conf" || die 'profile switch changed readiness setting'
grep -Fq 'UNRELATED_SETTING="keep-me"' "$tmp/vpn.conf" || die 'profile switch changed unrelated config'

if run_profiles remove backup >/dev/null 2>&1; then
    die 'active profile was removable'
fi

list_backup="$(env KIKIMORA_VPN_CONFIG="$tmp/vpn.conf" KIKIMORA_PROFILE_CONFIG="$tmp/profiles.conf" bash "$CLI" profiles list)"
grep -Fq 'Active profile: backup (amn0[static] / vpn1[static])' <<<"$list_backup" || die 'active complete state was not resolved'

# Switching to Happ changes both the selected interface and its provider.
run_profiles use happ-test >/dev/null
grep -Fq 'SECONDARY_INTERFACE=tun0' "$tmp/vpn.conf" || die 'Happ profile did not select tun0'
grep -Fq 'SECONDARY_ENDPOINT_PROVIDER=happ' "$tmp/vpn.conf" || die 'Happ profile did not activate Happ provider'
grep -Fq "SECONDARY_ENDPOINT_PROVIDER_ARGS=''" "$tmp/vpn.conf" || die 'Happ provider args were not written'

# Change only primary, then both roles, then return to the static default.
run_profiles use office >/dev/null
grep -Fq 'PRIMARY_INTERFACE=amn1' "$tmp/vpn.conf" || die 'primary-only switch did not update primary'
grep -Fq 'SECONDARY_INTERFACE=vpn0' "$tmp/vpn.conf" || die 'primary-only switch did not restore shared secondary'
grep -Fq 'SECONDARY_ENDPOINT_PROVIDER=static' "$tmp/vpn.conf" || die 'returning from Happ did not restore static provider'

run_profiles use travel >/dev/null
grep -Fq 'PRIMARY_INTERFACE=amn2' "$tmp/vpn.conf" || die 'pair switch did not update primary'
grep -Fq 'SECONDARY_INTERFACE=vpn2' "$tmp/vpn.conf" || die 'pair switch did not update secondary'

run_profiles use default >/dev/null
run_profiles remove backup >/dev/null
run_profiles remove travel >/dev/null
! grep -Fq '[backup]=' "$tmp/profiles.conf" || die 'inactive profile was not removed'
grep -Fq 'PRIMARY_INTERFACE=amn0' "$tmp/vpn.conf" || die 'switching back to default did not restore primary'
grep -Fq 'SECONDARY_INTERFACE=vpn0' "$tmp/vpn.conf" || die 'switching back to default did not restore secondary'
grep -Fq 'PRIMARY_ENDPOINT_PROVIDER=static' "$tmp/vpn.conf" || die 'default primary provider was not restored'
grep -Fq 'SECONDARY_ENDPOINT_PROVIDER=static' "$tmp/vpn.conf" || die 'default secondary provider was not restored'

domains_after="$(sha256sum "$tmp/domains"/*.txt)"
[[ "$domains_before" == "$domains_after" ]] || die 'profile operations changed shared domain lists'

# Migrate the old primary-only draft format on the next modifying command.
sudo tee "$tmp/profiles.conf" >/dev/null <<'EOF_LEGACY'
declare -Ag PRIMARY_PROFILES=(
    [default]=amn0
    [legacy]=amn7
)
EOF_LEGACY
legacy_list="$(env KIKIMORA_VPN_CONFIG="$tmp/vpn.conf" KIKIMORA_PROFILE_CONFIG="$tmp/profiles.conf" bash "$CLI" profiles list)"
grep -Fq 'Legacy primary-only profile storage detected' <<<"$legacy_list" || die 'legacy profile store was not detected'
run_profiles add migrated amn8 vpn8 >/dev/null
grep -Fq 'VPN_PROFILE_PRIMARY' "$tmp/profiles.conf" || die 'legacy store was not migrated to pair format'
grep -Fq 'VPN_PROFILE_PRIMARY_ENDPOINT_PROVIDER' "$tmp/profiles.conf" || die 'legacy store was not migrated to provider format'
grep -Fq '[legacy]=amn7' "$tmp/profiles.conf" || die 'legacy primary entry was lost during migration'
grep -Fq '[legacy]=vpn0' "$tmp/profiles.conf" || die 'legacy entry did not inherit current secondary'

# Pair-only format from the previous revision defaults inactive profiles to
# static and preserves the currently selected pair's provider state.
sudo tee "$tmp/profiles.conf" >/dev/null <<'EOF_PAIR_ONLY'
declare -Ag VPN_PROFILE_PRIMARY=(
    [default]=amn0
    [oldpair]=amn9
)
declare -Ag VPN_PROFILE_SECONDARY=(
    [default]=vpn0
    [oldpair]=vpn9
)
EOF_PAIR_ONLY
pair_legacy_list="$(env KIKIMORA_VPN_CONFIG="$tmp/vpn.conf" KIKIMORA_PROFILE_CONFIG="$tmp/profiles.conf" bash "$CLI" profiles list)"
grep -Fq 'Legacy pair-only profile storage detected' <<<"$pair_legacy_list" || die 'pair-only provider migration was not detected'
run_profiles add provider-migration amn10 vpn10 >/dev/null
grep -Fq 'VPN_PROFILE_SECONDARY_ENDPOINT_PROVIDER' "$tmp/profiles.conf" || die 'pair-only store was not migrated to provider metadata'

bash -n \
    "$ROOT/linux/kikimora" \
    "$ROOT/linux/files/kikimora-cli/config.sh" \
    "$ROOT/linux/files/kikimora-cli/help.sh" \
    "$ROOT/linux/completions/kikimora.bash"

printf 'VPN profile/provider tests: OK\n'
