#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
TEST_DIR="$(mktemp -d /tmp/kikimora-orchestration.XXXXXX)"
trap 'rm -rf -- "$TEST_DIR"' EXIT

mkdir -p "$TEST_DIR/toads" "$TEST_DIR/bin"
printf '%s\n' 'name = "fixture"' >"$TEST_DIR/toads/fixture.toml"
printf '%s\n' '# fixture' 'routing_owner = "legacy"' 'tunnel_owner = "external"' 'endpoint_owner = "legacy"' >"$TEST_DIR/ownership.conf"

cat >"$TEST_DIR/toad" <<'EOF_TOAD'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "$1" == validate && "$2" == -config && -r "$3" ]]
EOF_TOAD
chmod 0755 "$TEST_DIR/toad"

cat >"$TEST_DIR/core" <<'EOF_CORE'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'core:%s\n' "$*" >>"$FAKE_LOG"
case "$1" in
  status)
    [[ "${FAKE_API_DOWN:-0}" != 1 && -e "$FAKE_ACTIVE" ]] || exit 1
    if [[ "${FAKE_NEVER_READY:-0}" == 1 ]]; then
      state=Connecting
      route_ready=false
      endpoint_state=pending
      published=false
      validated=0
    elif [[ "${FAKE_ROLE_FAILED:-0}" == 1 ]]; then
      state=Failed
      route_ready=false
      endpoint_state=failed
      published=false
      validated=0
    elif [[ -e "$FAKE_DESIRED" ]]; then
      state=Ready
      route_ready=true
      endpoint_state=ready
      published=true
      validated=7
    else
      cat <<'JSON_STOPPED'
{"core_state":"Ready","aggregate_state":"Stopped","underlay":{"epoch":7,"ipv4":{"interface":"eth0","ifindex":2}},"roles":[{"id":"fixture","state":"Stopped","desired_enabled":false,"route_ready":false,"validated_underlay_epoch":0,"endpoint":{},"parking":{"active":false},"publication":{}}]}
JSON_STOPPED
      exit 0
    fi
    cat <<JSON_READY
{"core_state":"Ready","aggregate_state":"$state","underlay":{"epoch":7,"ipv4":{"interface":"eth0","ifindex":2}},"roles":[{"id":"fixture","state":"$state","desired_enabled":true,"route_ready":$route_ready,"validated_underlay_epoch":$validated,"endpoint":{"state":"$endpoint_state","applied_underlay_epoch":7},"parking":{"active":false},"publication":{"published":$published,"zone":"primary","interface":"kk0"}}]}
JSON_READY
    ;;
  start|connect-all)
    [[ -e "$FAKE_ACTIVE" ]] || exit 1
    touch "$FAKE_DESIRED"
    ;;
  stop|disconnect-all)
    rm -f -- "$FAKE_DESIRED"
    ;;
  *)
    exit 1
    ;;
esac
EOF_CORE
chmod 0755 "$TEST_DIR/core"

write_systemctl_fixture() {
    local path="$1"
    cat >"$path" <<'EOF_SYSTEMCTL'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >>"$FAKE_LOG"
verb="$1"
shift || true
[[ "${1:-}" == "--quiet" ]] && shift || true
case "$verb" in
  is-active)
    unit="${1:-}"
    if [[ "$unit" == kikimora-core.service ]]; then
      [[ -e "$FAKE_ACTIVE" ]] && exit 0
      exit 3
    fi
    if [[ "$unit" == leshy-route-watch.service ]]; then
      [[ -e "$FAKE_LEGACY_ACTIVE" ]] && exit 0
      exit 3
    fi
    exit 3
    ;;
  start)
    for unit in "$@"; do
      if [[ "$unit" == kikimora-core.service ]]; then
        [[ "${FAKE_FAIL_CORE:-0}" == 1 ]] && exit 1
        touch "$FAKE_ACTIVE"
      fi
      [[ "$unit" == leshy-route-watch.service ]] && touch "$FAKE_LEGACY_ACTIVE"
    done
    ;;
  stop)
    for unit in "$@"; do
      [[ "$unit" == kikimora-core.service ]] && rm -f -- "$FAKE_ACTIVE"
      [[ "$unit" == leshy-route-watch.service ]] && rm -f -- "$FAKE_LEGACY_ACTIVE"
    done
    ;;
  is-enabled) exit 3 ;;
  disable|enable|daemon-reload) ;;
  *) ;;
esac
EOF_SYSTEMCTL
    chmod 0755 "$path"
}

run_orchestration() {
    local systemctl="$1" log="$2" action="$3"
    shift 3
    env \
        KIKIMORA_ORCHESTRATION_TEST_MODE=1 \
        KIKIMORA_ORCHESTRATION_OWNERSHIP_CONFIG="$TEST_DIR/ownership.conf" \
        KIKIMORA_ORCHESTRATION_CONFIG_DIR="$TEST_DIR/toads" \
        KIKIMORA_ORCHESTRATION_CORE_BIN="$TEST_DIR/core" \
        KIKIMORA_ORCHESTRATION_TOAD_BIN="$TEST_DIR/toad" \
        KIKIMORA_ORCHESTRATION_CORE_SOCKET="$TEST_DIR/core.sock" \
        KIKIMORA_ORCHESTRATION_READY_TIMEOUT=1 \
        KIKIMORA_ORCHESTRATION_SYSTEMCTL="$systemctl" \
        KIKIMORA_ORCHESTRATION_NM_CONF="$TEST_DIR/nm.conf" \
        KIKIMORA_ORCHESTRATION_LEGACY_LIBEXEC="$TEST_DIR/legacy" \
        KIKIMORA_ORCHESTRATION_LEGACY_UNIT_DIR="$TEST_DIR/units" \
        KIKIMORA_ORCHESTRATION_LEGACY_DROPIN_DIR="$TEST_DIR/dropin" \
        FAKE_LOG="$log" \
        FAKE_ACTIVE="$TEST_DIR/active" \
        FAKE_LEGACY_ACTIVE="$TEST_DIR/legacy-active" \
        FAKE_DESIRED="$TEST_DIR/desired" \
        FAKE_FAIL_CORE="${FAKE_FAIL_CORE:-0}" \
        FAKE_API_DOWN="${FAKE_API_DOWN:-0}" \
        FAKE_ROLE_FAILED="${FAKE_ROLE_FAILED:-0}" \
        FAKE_NEVER_READY="${FAKE_NEVER_READY:-0}" \
        bash "$ROOT/linux/kikimora" orchestration "$action" "$@"
}

reset_legacy() {
    rm -f -- "$TEST_DIR/active" "$TEST_DIR/desired"
    touch "$TEST_DIR/legacy-active"
    printf '%s\n' '# fixture' 'routing_owner = "legacy"' 'tunnel_owner = "external"' 'endpoint_owner = "legacy"' >"$TEST_DIR/ownership.conf"
}

write_systemctl_fixture "$TEST_DIR/bin/systemctl-ok"
reset_legacy

# Read-only preflight must not mutate ownership or services.
run_orchestration "$TEST_DIR/bin/systemctl-ok" "$TEST_DIR/preflight.log" preflight >/dev/null
grep -Fxq 'routing_owner = "legacy"' "$TEST_DIR/ownership.conf"
[[ -e "$TEST_DIR/legacy-active" && ! -e "$TEST_DIR/active" ]]

# Happy path requires API + ConnectAll + authoritative Ready, not service liveness.
run_orchestration "$TEST_DIR/bin/systemctl-ok" "$TEST_DIR/success.log" cutover --go
grep -Fxq 'routing_owner = "go"' "$TEST_DIR/ownership.conf"
grep -Fxq 'tunnel_owner = "go"' "$TEST_DIR/ownership.conf"
grep -Fxq 'endpoint_owner = "go"' "$TEST_DIR/ownership.conf"
grep -Fq 'stop leshy-route-watch.service' "$TEST_DIR/success.log"
grep -Fq 'disable leshy-route-watch.service' "$TEST_DIR/success.log"
grep -Fq 'enable kikimora-core.service' "$TEST_DIR/success.log"
grep -Fq 'core:start --socket' "$TEST_DIR/success.log"
[[ -e "$TEST_DIR/desired" ]]

# Already-Go cutover must not issue ConnectAll and accidentally re-enable roles.
before_count="$(grep -c '^core:start ' "$TEST_DIR/success.log" || true)"
run_orchestration "$TEST_DIR/bin/systemctl-ok" "$TEST_DIR/success.log" cutover --go >/dev/null
after_count="$(grep -c '^core:start ' "$TEST_DIR/success.log" || true)"
[[ "$before_count" == "$after_count" ]]

# Rollback stops Go roles before legacy ownership becomes active.
mkdir -p "$TEST_DIR/legacy" "$TEST_DIR/units" "$TEST_DIR/dropin"
touch "$TEST_DIR/legacy/reconcile" "$TEST_DIR/legacy/route-watch" "$TEST_DIR/legacy/route-lifecycle" \
    "$TEST_DIR/units/leshy-route-watch.service" "$TEST_DIR/dropin/route-cleanup.conf"
run_orchestration "$TEST_DIR/bin/systemctl-ok" "$TEST_DIR/rollback.log" rollback
grep -Fq 'core:stop --socket' "$TEST_DIR/rollback.log"
grep -Fxq 'routing_owner = "legacy"' "$TEST_DIR/ownership.conf"
[[ -e "$TEST_DIR/legacy-active" ]]

# A core process start failure rolls ownership back.
reset_legacy
if FAKE_FAIL_CORE=1 run_orchestration "$TEST_DIR/bin/systemctl-ok" "$TEST_DIR/fail-start.log" cutover --go; then
    printf 'expected core-start failure\n' >&2
    exit 1
fi
grep -Fxq 'routing_owner = "legacy"' "$TEST_DIR/ownership.conf"
[[ -e "$TEST_DIR/legacy-active" ]]

# Service-active but API-unavailable also rolls back.
reset_legacy
if FAKE_API_DOWN=1 run_orchestration "$TEST_DIR/bin/systemctl-ok" "$TEST_DIR/fail-api.log" cutover --go; then
    printf 'expected API readiness failure\n' >&2
    exit 1
fi
grep -Fxq 'routing_owner = "legacy"' "$TEST_DIR/ownership.conf"
[[ -e "$TEST_DIR/legacy-active" ]]

# API role failure / never-ready are not accepted as cutover success.
reset_legacy
if FAKE_ROLE_FAILED=1 run_orchestration "$TEST_DIR/bin/systemctl-ok" "$TEST_DIR/fail-role.log" cutover --go; then
    printf 'expected failed-role cutover failure\n' >&2
    exit 1
fi
grep -Fxq 'routing_owner = "legacy"' "$TEST_DIR/ownership.conf"

reset_legacy
if FAKE_NEVER_READY=1 run_orchestration "$TEST_DIR/bin/systemctl-ok" "$TEST_DIR/fail-timeout.log" cutover --go; then
    printf 'expected readiness-timeout failure\n' >&2
    exit 1
fi
grep -Fxq 'routing_owner = "legacy"' "$TEST_DIR/ownership.conf"

# Legacy retirement remains a separate operator-confirmed action.
reset_legacy
run_orchestration "$TEST_DIR/bin/systemctl-ok" "$TEST_DIR/retire-cutover.log" cutover --go >/dev/null
run_orchestration "$TEST_DIR/bin/systemctl-ok" "$TEST_DIR/retire.log" retire-legacy --confirm
[[ ! -e "$TEST_DIR/legacy/reconcile" && ! -e "$TEST_DIR/legacy/route-watch" && ! -e "$TEST_DIR/legacy/route-lifecycle" ]]
[[ ! -e "$TEST_DIR/units/leshy-route-watch.service" && ! -e "$TEST_DIR/dropin/route-cleanup.conf" ]]
for legacy_script in reconcile route-lifecycle route-watch; do
    legacy_output="$(KIKIMORA_OWNERSHIP_CONFIG="$TEST_DIR/ownership.conf" bash "$ROOT/linux/files/$legacy_script" 2>&1)"
    grep -Fq 'Go owns' <<<"$legacy_output"
done

printf 'Kikimora orchestration cutover fixture: OK\n'
