#!/usr/bin/env bash
set -Eeuo pipefail

# managed-pair-start.sh — Phase E1/E2 contract test (08D1 post-Phase-D audit
# §7): the Go-owned `kk service start` drives the managed-pair state machine,
# and the package postinst itself never performs the cutover.
#
# Proven rows:
#   1 the happy path: start core -> readiness -> Local -> capability
#     preflight -> graceful Leshy stop -> writer-lock proof -> schema2 enable
#     -> managed Leshy start -> managed + transition none;
#   2 an already-managed install verifies the config contract and returns
#     without inventing a new activation (no enable in the log);
#   3 a pre-commit enable failure restarts local Leshy;
#   4 a post-commit enable failure keeps the system fail-closed (local Leshy
#     is NOT restarted);
#   5 a capability-less Leshy refuses the cutover before anything is stopped;
#   6 the packaged postinst contains no leshy-mode/EnableManagedLeshy calls.

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
TEST_DIR="$(mktemp -d /tmp/kikimora-managed-pair.XXXXXX)"
trap 'rm -rf -- "$TEST_DIR"' EXIT

mkdir -p "$TEST_DIR/toads" "$TEST_DIR/state" "$TEST_DIR/leshy" "$TEST_DIR/bin"
printf '%s\n' 'name = "fixture"' >"$TEST_DIR/toads/fixture.toml"
printf '%s\n' '# fixture' 'routing_owner = "go"' 'tunnel_owner = "go"' 'endpoint_owner = "go"' >"$TEST_DIR/ownership.conf"
# The generated-config fixture the already-managed row verifies: generation 7,
# activation 0123456789abcdef0123456789abcdef (its fingerprint is the first
# 12 hex of sha256 of that id).
CONFIG_ACTIVATION=0123456789abcdef0123456789abcdef
CONFIG_FINGERPRINT=$(printf '%s' "$CONFIG_ACTIVATION" | sha256sum | cut -c1-12)
cat >"$TEST_DIR/leshy/config.toml" <<EOF_CONFIG
[server]
writer_lock = "$TEST_DIR/writer.lock"
routing_backend = "kikimora"
kikimora_socket = "$TEST_DIR/core.sock"
kikimora_auth_file = "$TEST_DIR/state/control-route-auth.key"
kikimora_activation_id = "$CONFIG_ACTIVATION"
kikimora_mode_generation = 7
EOF_CONFIG

cat >"$TEST_DIR/toad" <<'EOF_TOAD'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "$1" == validate && "$2" == -config && -r "$3" ]]
EOF_TOAD
chmod 0755 "$TEST_DIR/toad"

# The fake Leshy: the real capability surface only.
cat >"$TEST_DIR/leshy-fake" <<'EOF_LESHY'
#!/usr/bin/env bash
set -Eeuo pipefail
case "$1" in
  capabilities)
    if [[ "${FAKE_PARTIAL_CAPS:-0}" == 1 ]]; then
      printf '["af_unix_control","managed_selected_routes_v1"]\n'
    else
      printf '["af_unix_control","managed_selected_routes_v1","managed_selected_routes_v2","privileged_control_auth_v1"]\n'
    fi
    ;;
  --version)
    printf 'leshy 0.5.2\n'
    ;;
  *)
    exit 1
    ;;
esac
EOF_LESHY
chmod 0755 "$TEST_DIR/leshy-fake"

# The fake core: the real status surfaces plus the schema2 mode transaction.
cat >"$TEST_DIR/core" <<'EOF_CORE'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'core:%s\n' "$*" >>"$FAKE_LOG"
case "$1" in
  status)
    cat <<JSON_STATUS
{"core_state":"Ready","aggregate_state":"Stopped","mode_transition":"none","managed_mode":$([[ "$(cat "$FAKE_MODE" 2>/dev/null || true)" == managed ]] && echo true || echo false),"mode_generation":$([[ "$(cat "$FAKE_MODE" 2>/dev/null || true)" == managed ]] && echo 7 || echo 0),"activation_id_fingerprint":$([[ "$(cat "$FAKE_MODE" 2>/dev/null || true)" == managed ]] && echo "\"$FAKE_FINGERPRINT\"" || echo '""'),"roles":[]}
JSON_STATUS
    ;;
  wait-startup)
    exit 0
    ;;
  leshy-mode)
    method="$2"
    case "$method" in
      status)
        mode="$(cat "$FAKE_MODE" 2>/dev/null || true)"
        if [[ "$mode" == managed ]]; then
          printf '{"mode":"managed","generation":7,"activation_id_fingerprint":"%s","transition":"none"}\n' "$FAKE_FINGERPRINT"
        else
          printf '{"mode":"local","generation":0}\n'
        fi
        ;;
      enable)
        if [[ "${FAKE_ENABLE_FAIL:-0}" == 1 ]]; then
          if [[ "${FAKE_ENABLE_FAIL_AFTER_COMMIT:-0}" == 1 ]]; then
            printf managed >"$FAKE_MODE"
            printf '{"schema":2}' >"$FAKE_MARKER"
          fi
          exit 1
        fi
        printf managed >"$FAKE_MODE"
        printf '{"schema":2}' >"$FAKE_MARKER"
        ;;
      *) exit 1 ;;
    esac
    ;;
  start)
    exit 0
    ;;
  *)
    exit 1
    ;;
esac
EOF_CORE
chmod 0755 "$TEST_DIR/core"

cat >"$TEST_DIR/bin/systemctl" <<'EOF_SYSTEMCTL'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >>"$FAKE_LOG"
verb="$1"
shift || true
case "$verb" in
  is-active)
    if [[ "${1:-}" == "--quiet" ]]; then
      shift
    fi
    unit="${1:-}"
    if [[ "$unit" == kikimora-core.service ]]; then
      [[ -e "$FAKE_ACTIVE" ]] && exit 0
      exit 3
    fi
    if [[ "$unit" == leshy.service ]]; then
      [[ -e "$FAKE_LESHY_ACTIVE" ]] && exit 0
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
      if [[ "$unit" == leshy.service ]]; then
        touch "$FAKE_LESHY_ACTIVE"
      fi
    done
    true
    ;;
  stop)
    for unit in "$@"; do
      if [[ "$unit" == kikimora-core.service ]]; then
        rm -f -- "$FAKE_ACTIVE"
      fi
      if [[ "$unit" == leshy.service ]]; then
        rm -f -- "$FAKE_LESHY_ACTIVE"
      fi
    done
    true
    ;;
  restart)
    true
    ;;
  enable|disable|daemon-reload|is-enabled) ;;
  *) ;;
esac
EOF_SYSTEMCTL
chmod 0755 "$TEST_DIR/bin/systemctl"

run_service() { # run_service <log> (kk start through the real entrypoint)
    local log="$1"
    shift
    env \
        KIKIMORA_ORCHESTRATION_TEST_MODE=1 \
        KIKIMORA_ORCHESTRATION_OWNERSHIP_CONFIG="$TEST_DIR/ownership.conf" \
        KIKIMORA_ORCHESTRATION_CONFIG_DIR="$TEST_DIR/toads" \
        KIKIMORA_ORCHESTRATION_CORE_BIN="$TEST_DIR/core" \
        KIKIMORA_ORCHESTRATION_TOAD_BIN="$TEST_DIR/toad" \
        KIKIMORA_ORCHESTRATION_CORE_SOCKET="$TEST_DIR/core.sock" \
        KIKIMORA_ORCHESTRATION_READY_TIMEOUT=1 \
        KIKIMORA_ORCHESTRATION_SYSTEMCTL="$TEST_DIR/bin/systemctl" \
        KIKIMORA_ORCHESTRATION_NM_CONF="$TEST_DIR/nm.conf" \
        KIKIMORA_ORCHESTRATION_STATE_DIR="$TEST_DIR/state" \
        KIKIMORA_ORCHESTRATION_AUTH_KEY="$TEST_DIR/state/control-route-auth.key" \
        KIKIMORA_ORCHESTRATION_LESHY_BIN="$TEST_DIR/leshy-fake" \
        KIKIMORA_ORCHESTRATION_LESHY_CONFIG="$TEST_DIR/leshy/config.toml" \
        KIKIMORA_ORCHESTRATION_WRITER_LOCK="$TEST_DIR/writer.lock" \
        KIKIMORA_ORCHESTRATION_MARKER="$TEST_DIR/toads/leshy-managed.json" \
        FAKE_LOG="$log" \
        FAKE_ACTIVE="$TEST_DIR/active" \
        FAKE_LESHY_ACTIVE="$TEST_DIR/leshy-active" \
        FAKE_MODE="$TEST_DIR/mode" \
        FAKE_MARKER="$TEST_DIR/toads/leshy-managed.json" \
        FAKE_FINGERPRINT="$CONFIG_FINGERPRINT" \
        FAKE_ENABLE_FAIL="${FAKE_ENABLE_FAIL:-0}" \
        FAKE_ENABLE_FAIL_AFTER_COMMIT="${FAKE_ENABLE_FAIL_AFTER_COMMIT:-0}" \
        FAKE_PARTIAL_CAPS="${FAKE_PARTIAL_CAPS:-0}" \
        bash "$ROOT/linux/kikimora" start
}

reset_pair() {
    rm -f -- "$TEST_DIR/active" "$TEST_DIR/leshy-active" "$TEST_DIR/mode" \
        "$TEST_DIR/toads/leshy-managed.json"
}

# Row 1: the happy path — Local to Managed through the real choreography.
reset_pair
: >"$TEST_DIR/happy.log"
run_service "$TEST_DIR/happy.log" start >/dev/null
grep -Fq 'start kikimora-core.service' "$TEST_DIR/happy.log"
grep -Fq 'stop leshy.service' "$TEST_DIR/happy.log"
grep -Fq 'start leshy.service' "$TEST_DIR/happy.log"
grep -Fq 'leshy-mode enable --leshy' "$TEST_DIR/happy.log"
[[ "$(cat "$TEST_DIR/mode")" == managed ]]
[[ -e "$TEST_DIR/toads/leshy-managed.json" ]]

# Row 2: already-managed verifies the config contract and never re-enables.
reset_pair
printf managed >"$TEST_DIR/mode"
printf '{"schema":2}' >"$TEST_DIR/toads/leshy-managed.json"
touch "$TEST_DIR/active" "$TEST_DIR/leshy-active"
: >"$TEST_DIR/managed.log"
run_service "$TEST_DIR/managed.log" start >"$TEST_DIR/managed.out"
grep -Fq 'already managed at generation 7' "$TEST_DIR/managed.out"
if grep -Fq 'leshy-mode enable' "$TEST_DIR/managed.log"; then
    printf 'an already-managed start invented a new activation\n' >&2
    exit 1
fi

# Row 3: a pre-commit enable failure restarts local Leshy.
reset_pair
if FAKE_ENABLE_FAIL=1 run_service "$TEST_DIR/fail-pre.log" start; then
    printf 'expected a pre-commit enable failure\n' >&2
    exit 1
fi
[[ ! -e "$TEST_DIR/toads/leshy-managed.json" ]]
grep -Fq 'start leshy.service' "$TEST_DIR/fail-pre.log"

# Row 4: a post-commit enable failure keeps the system fail-closed.
reset_pair
if FAKE_ENABLE_FAIL=1 FAKE_ENABLE_FAIL_AFTER_COMMIT=1 run_service "$TEST_DIR/fail-post.log" start; then
    printf 'expected a post-commit enable failure\n' >&2
    exit 1
fi
[[ -e "$TEST_DIR/toads/leshy-managed.json" ]]
# No leshy start may happen after the failed enable: the pre-cutover ensure
# ran before it, the fail-closed path must not restart local Leshy.
if sed -n '/leshy-mode enable/,$p' "$TEST_DIR/fail-post.log" | grep -Fq 'start leshy.service'; then
    printf 'local Leshy was restarted against a managed marker\n' >&2
    exit 1
fi

# Row 5: a capability-less Leshy refuses before anything is stopped.
reset_pair
if FAKE_PARTIAL_CAPS=1 run_service "$TEST_DIR/fail-caps.log" start; then
    printf 'expected a capability refusal\n' >&2
    exit 1
fi
if grep -Fq 'stop leshy.service' "$TEST_DIR/fail-caps.log"; then
    printf 'the capability refusal stopped Leshy first\n' >&2
    exit 1
fi

# Row 6: the packaged postinst never performs the cutover.
postinst="$(mktemp)"
trap 'rm -rf -- "$TEST_DIR" "$postinst"' EXIT
if grep -rnE 'leshy-mode|EnableManagedLeshy|DisableManagedLeshy' \
    "$ROOT"/packaging/linux/stage-release.sh >/dev/null 2>&1; then
    printf 'the packaging stage emits leshy-mode calls into the package scripts\n' >&2
    exit 1
fi
rm -f -- "$postinst"

printf 'Kikimora managed-pair start fixture: OK\n'
