#!/bin/bash
set -Eeu -o pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="$(mktemp -d /tmp/kikimora-macos-build-test.XXXXXX)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/domains" "$tmp/routes"

cat > "$tmp/domains/primary.txt" <<'EOF'
example.com
*.example.net
EOF
cat > "$tmp/domains/secondary.txt" <<'EOF'
corp.example.org
EOF
: > "$tmp/domains/bypass.txt"
: > "$tmp/routes/primary.txt"
printf '10.20.0.0/16\n' > "$tmp/routes/secondary.txt"
cat > "$tmp/routing.conf" <<'EOF'
DEFAULT_ZONE=secondary
DEFAULT_UPSTREAMS="1.1.1.1:53 8.8.8.8:53"
UPSTREAM_ZONE=direct
EOF

"$ROOT/macos/build-config" "$tmp/domains" "$tmp/config.toml" "$tmp/routing.conf" "$tmp/routes"
grep -Fqx 'listen_address = "127.0.0.1:53"' "$tmp/config.toml"
grep -Fqx 'name = "primary"' "$tmp/config.toml"
grep -Fqx 'name = "secondary"' "$tmp/config.toml"
grep -Fqx 'name = "default-secondary"' "$tmp/config.toml"
grep -Fqx '    "10.20.0.0/16",' "$tmp/config.toml"
if [[ -x /usr/local/bin/leshy ]] && [[ $(/usr/local/bin/leshy --version 2>/dev/null || true) == 'leshy 0.4.0' ]]; then
  "$ROOT/macos/check-config" /usr/local/bin/leshy "$tmp/config.toml"
fi

printf 'example.com\n' >> "$tmp/domains/secondary.txt"
if "$ROOT/macos/build-config" "$tmp/domains" "$tmp/invalid.toml" "$tmp/routing.conf" "$tmp/routes" >/dev/null 2>&1; then
  printf 'duplicate domain validation unexpectedly succeeded\n' >&2
  exit 1
fi
printf 'macOS build-config tests: OK\n'
