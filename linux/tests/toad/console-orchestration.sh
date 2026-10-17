#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP="$(mktemp -d /tmp/kikimora-console.XXXXXX)"
trap 'rm -rf -- "$TMP"' EXIT

mkdir -p "$TMP/toads" "$TMP/bin"
cat >"$TMP/ownership.conf" <<'EOF'
routing_owner = "go"
tunnel_owner = "go"
endpoint_owner = "go"
EOF

cat >"$TMP/core" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >>"$FAKE_LOG"
case "$1" in
  status|start|stop|connect|disconnect|retry|restart|interfaces|profiles)
    printf '{"schema":2,"revision":7,"core_state":"Ready","aggregate_state":"Ready","roles":[]}\n'
    ;;
  *) exit 64 ;;
esac
EOF
chmod 0755 "$TMP/core"

run_kk() {
  env     KIKIMORA_ORCHESTRATION_OWNERSHIP_CONFIG="$TMP/ownership.conf"     KIKIMORA_ORCHESTRATION_CONFIG_DIR="$TMP/toads"     KIKIMORA_ORCHESTRATION_CORE_BIN="$TMP/core"     KIKIMORA_ORCHESTRATION_TOAD_BIN="$TMP/toad"     KIKIMORA_ORCHESTRATION_CORE_SOCKET="$TMP/core.sock"     KIKIMORA_ORCHESTRATION_SYSTEMCTL="$TMP/bin/systemctl"     KIKIMORA_ORCHESTRATION_NM_CONF="$TMP/nm.conf"     FAKE_LOG="$TMP/core.log"     bash "$ROOT/linux/kikimora" "$@"
}

: >"$TMP/core.log"
run_kk status >/dev/null
run_kk status --json >/dev/null
run_kk connect >/dev/null
run_kk connect --role awg >/dev/null
run_kk disconnect --role awg >/dev/null
run_kk retry --role awg >/dev/null
run_kk restart --all >/dev/null
run_kk restart --role awg >/dev/null
run_kk interfaces >/dev/null
run_kk interfaces --json >/dev/null
run_kk profiles >/dev/null
run_kk profiles --json >/dev/null

grep -Fxq "status --socket $TMP/core.sock" "$TMP/core.log"
grep -Fxq "status --socket $TMP/core.sock --json" "$TMP/core.log"
grep -Fxq "start --socket $TMP/core.sock" "$TMP/core.log"
grep -Fxq "connect --socket $TMP/core.sock --role awg" "$TMP/core.log"
grep -Fxq "disconnect --socket $TMP/core.sock --role awg" "$TMP/core.log"
grep -Fxq "retry --socket $TMP/core.sock --role awg" "$TMP/core.log"
grep -Fxq "restart --socket $TMP/core.sock" "$TMP/core.log"
grep -Fxq "interfaces --socket $TMP/core.sock" "$TMP/core.log"
grep -Fxq "interfaces --socket $TMP/core.sock --json" "$TMP/core.log"
grep -Fxq "profiles --socket $TMP/core.sock" "$TMP/core.log"
grep -Fxq "profiles --socket $TMP/core.sock --json" "$TMP/core.log"

# Per-role restart is deliberately expressed through the same API as explicit
# operator disconnect/connect, so desired state remains the single source.
disconnect_line="$(grep -nFx "disconnect --socket $TMP/core.sock --role awg" "$TMP/core.log" | tail -n1 | cut -d: -f1)"
connect_line="$(grep -nFx "connect --socket $TMP/core.sock --role awg" "$TMP/core.log" | tail -n1 | cut -d: -f1)"
[[ -n "$disconnect_line" && -n "$connect_line" && "$disconnect_line" -lt "$connect_line" ]]

# Legacy ownership must not silently route these new commands into the Go core.
cat >"$TMP/ownership.conf" <<'EOF'
routing_owner = "legacy"
tunnel_owner = "external"
endpoint_owner = "legacy"
EOF
if run_kk connect --role awg >/dev/null 2>&1; then
  echo "FAIL: Go role command was accepted before Go ownership" >&2
  exit 1
fi

echo "Kikimora console orchestration fixture: PASS"
