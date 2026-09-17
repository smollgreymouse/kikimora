#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
TEST_DIR="$(mktemp -d /tmp/kikimora-orchestration.XXXXXX)"
trap 'rm -rf -- "$TEST_DIR"' EXIT

mkdir -p "$TEST_DIR/toads" "$TEST_DIR/bin"
printf '%s\n' 'name = "fixture"' >"$TEST_DIR/toads/fixture.toml"
printf '%s\n' '# fixture' 'routing_owner = "legacy"' 'tunnel_owner = "external"' 'endpoint_owner = "legacy"' >"$TEST_DIR/ownership.conf"
touch "$TEST_DIR/core"
chmod 0755 "$TEST_DIR/core"

write_systemctl_fixture() {
    local path="$1"
    cat >"$path" <<'EOF_SYSTEMCTL'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >>"$FAKE_LOG"
last=""
for value in "$@"; do last="$value"; done
case "$1" in
  is-active)
    [[ "$last" == kikimora-core.service && -e "$FAKE_ACTIVE" ]] && exit 0
    exit 3
    ;;
  start)
    if [[ "$last" == kikimora-core.service ]]; then
      [[ "${FAKE_FAIL_CORE:-0}" == 1 ]] && exit 1
      touch "$FAKE_ACTIVE"
    fi
    ;;
  stop)
    if [[ "$last" == kikimora-core.service ]]; then rm -f -- "$FAKE_ACTIVE"; fi
    ;;
  is-enabled) exit 3 ;;
  disable|enable|daemon-reload) ;;
  *) ;;
esac
EOF_SYSTEMCTL
    chmod 0755 "$path"
}

run_cli() {
    env \
        KIKIMORA_ORCHESTRATION_TEST_MODE=1 \
        KIKIMORA_ORCHESTRATION_OWNERSHIP_CONFIG="$TEST_DIR/ownership.conf" \
        KIKIMORA_ORCHESTRATION_CONFIG_DIR="$TEST_DIR/toads" \
        KIKIMORA_ORCHESTRATION_CORE_BIN="$TEST_DIR/core" \
        KIKIMORA_ORCHESTRATION_SYSTEMCTL="$1" \
        KIKIMORA_ORCHESTRATION_LEGACY_LIBEXEC="$TEST_DIR/legacy" \
        KIKIMORA_ORCHESTRATION_LEGACY_UNIT_DIR="$TEST_DIR/units" \
        KIKIMORA_ORCHESTRATION_LEGACY_DROPIN_DIR="$TEST_DIR/dropin" \
        FAKE_LOG="$2" FAKE_ACTIVE="$TEST_DIR/active" FAKE_FAIL_CORE="${3:-0}" \
        bash "$ROOT/linux/kikimora" orchestration cutover --go
}

run_retire() {
    env \
        KIKIMORA_ORCHESTRATION_TEST_MODE=1 \
        KIKIMORA_ORCHESTRATION_OWNERSHIP_CONFIG="$TEST_DIR/ownership.conf" \
        KIKIMORA_ORCHESTRATION_CONFIG_DIR="$TEST_DIR/toads" \
        KIKIMORA_ORCHESTRATION_CORE_BIN="$TEST_DIR/core" \
        KIKIMORA_ORCHESTRATION_SYSTEMCTL="$1" \
        KIKIMORA_ORCHESTRATION_LEGACY_LIBEXEC="$TEST_DIR/legacy" \
        KIKIMORA_ORCHESTRATION_LEGACY_UNIT_DIR="$TEST_DIR/units" \
        KIKIMORA_ORCHESTRATION_LEGACY_DROPIN_DIR="$TEST_DIR/dropin" \
        FAKE_LOG="$2" FAKE_ACTIVE="$TEST_DIR/active" \
        bash "$ROOT/linux/kikimora" orchestration retire-legacy --confirm
}

write_systemctl_fixture "$TEST_DIR/bin/systemctl-ok"
run_cli "$TEST_DIR/bin/systemctl-ok" "$TEST_DIR/success.log"
grep -Fxq 'routing_owner = "go"' "$TEST_DIR/ownership.conf"
grep -Fxq 'tunnel_owner = "go"' "$TEST_DIR/ownership.conf"
grep -Fxq 'endpoint_owner = "go"' "$TEST_DIR/ownership.conf"
grep -Fq 'stop leshy-route-watch.service' "$TEST_DIR/success.log"
grep -Fq 'disable leshy-route-watch.service' "$TEST_DIR/success.log"
if grep -Eq '^(stop|disable) (leshy\.service|leshy-health-watch\.service)' "$TEST_DIR/success.log"; then
    printf 'cutover disabled a retained Leshy/DNS-health unit\n' >&2
    exit 1
fi
grep -Fq 'enable kikimora-core.service' "$TEST_DIR/success.log"
mkdir -p "$TEST_DIR/legacy" "$TEST_DIR/units" "$TEST_DIR/dropin"
touch "$TEST_DIR/legacy/reconcile" "$TEST_DIR/legacy/route-watch" "$TEST_DIR/legacy/route-lifecycle" \
    "$TEST_DIR/units/leshy-route-watch.service" "$TEST_DIR/dropin/route-cleanup.conf"
run_retire "$TEST_DIR/bin/systemctl-ok" "$TEST_DIR/retire.log"
[[ ! -e "$TEST_DIR/legacy/reconcile" && ! -e "$TEST_DIR/legacy/route-watch" && ! -e "$TEST_DIR/legacy/route-lifecycle" ]]
[[ ! -e "$TEST_DIR/units/leshy-route-watch.service" && ! -e "$TEST_DIR/dropin/route-cleanup.conf" ]]
for legacy_script in reconcile route-lifecycle route-watch; do
    legacy_output="$(KIKIMORA_OWNERSHIP_CONFIG="$TEST_DIR/ownership.conf" bash "$ROOT/linux/files/$legacy_script" 2>&1)"
    grep -Fq 'Go owns' <<<"$legacy_output"
done

printf '%s\n' '# fixture' 'routing_owner = "legacy"' 'tunnel_owner = "external"' 'endpoint_owner = "legacy"' >"$TEST_DIR/ownership.conf"
write_systemctl_fixture "$TEST_DIR/bin/systemctl-fail"
if run_cli "$TEST_DIR/bin/systemctl-fail" "$TEST_DIR/failure.log" 1; then
    printf 'expected core-start failure\n' >&2
    exit 1
fi
grep -Fxq 'routing_owner = "legacy"' "$TEST_DIR/ownership.conf"
grep -Fxq 'tunnel_owner = "external"' "$TEST_DIR/ownership.conf"
grep -Fxq 'endpoint_owner = "legacy"' "$TEST_DIR/ownership.conf"
grep -Fq 'start leshy.service leshy-route-watch.service leshy-health-watch.service' "$TEST_DIR/failure.log"

printf 'Kikimora orchestration cutover fixture: OK\n'
