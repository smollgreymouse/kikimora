#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# stage-release.sh — canonical Linux staging builder.
#
# Stages all product files into a directory tree suitable for .deb and .tar.gz.
# Called by build-release.sh; not meant to be executed directly.
#
# Usage: stage-release.sh <stage-dir> [arch]

set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=packaging/common/version.sh
source "$ROOT/packaging/common/version.sh"

STAGE="${1:?usage: stage-release.sh <stage-dir> [arch]}"
ARCH="${2:-$(dpkg --print-architecture 2>/dev/null || echo amd64)}"

rm -rf "$STAGE"
install -d "$STAGE/DEBIAN" \
    "$STAGE/usr/local/bin" \
    "$STAGE/usr/local/sbin" \
    "$STAGE/usr/local/libexec/kikimora/endpoint-providers" \
    "$STAGE/usr/local/libexec/kikimora/cli" \
    "$STAGE/usr/local/libexec/kikimora/leshy/routes" \
    "$STAGE/usr/lib/systemd/system" \
    "$STAGE/usr/lib/systemd/system/leshy.service.d" \
    "$STAGE/usr/lib/tmpfiles.d" \
    "$STAGE/usr/lib/sysusers.d" \
    "$STAGE/usr/share/kikimora/leshy/domains" \
    "$STAGE/usr/share/kikimora/leshy/endpoints" \
    "$STAGE/usr/share/bash-completion/completions" \
    "$STAGE/usr/local/share/zsh/site-functions" \
    "$STAGE/usr/share/fish/vendor_completions.d" \
    "$STAGE/etc/NetworkManager/conf.d"

# ---- Go binaries ----
echo "  Building kikimora-core..."
(cd "$ROOT/toad" && go build -trimpath -ldflags "-s -w -X main.coreVersion=$KIKIMORA_VERSION" \
    -o "$STAGE/usr/local/bin/kikimora-core" ./cmd/kikimora-core)
chmod 755 "$STAGE/usr/local/bin/kikimora-core"

echo "  Building kikimora-toad..."
(cd "$ROOT/toad" && go build -trimpath -ldflags "-s -w -X main.toadVersion=$KIKIMORA_VERSION" \
    -o "$STAGE/usr/local/bin/kikimora-toad" ./cmd/kikimora-toad)
chmod 755 "$STAGE/usr/local/bin/kikimora-toad"

# ---- CLI ----
install -m 0755 "$ROOT/linux/kikimora" "$STAGE/usr/local/sbin/kikimora"
ln -sfn /usr/local/sbin/kikimora "$STAGE/usr/local/bin/kk"

# ---- CLI library files ----
for cli_file in common.sh help.sh dns.sh service.sh status.sh domains.sh config.sh maintenance.sh orchestration.sh; do
    install -m 0644 "$ROOT/linux/files/kikimora-cli/$cli_file" \
        "$STAGE/usr/local/libexec/kikimora/cli/$cli_file"
done

# ---- Endpoint providers ----
for provider in static command happ; do
    if [[ -f "$ROOT/linux/files/endpoint-providers/$provider" ]]; then
        install -m 0755 "$ROOT/linux/files/endpoint-providers/$provider" \
            "$STAGE/usr/local/libexec/kikimora/endpoint-providers/$provider"
    fi
done

# ---- Leshy runtime (Go orchestration steering) ----
# The Go core owns tunnel lifecycle, endpoint policy and route parking, and
# publishes ready interfaces as "<zone>.dev" files. Steering selected traffic
# into those interfaces is Leshy's job: the binary resolves the configured
# domains and installs the kernel routes. The core unit declares
# Wants=leshy.service, so the package must ship the Leshy runtime or the
# dependency stays dangling and nothing is ever routed through the tunnels.
# The legacy bash watcher services (route-watch/health-watch) are NOT part of
# the Go runtime and are deliberately not shipped.
LESHY_BIN_SRC="${KIKIMORA_LESHY_BIN:-}"
LESHY_SRC_DIR="${KIKIMORA_LESHY_SRC:-$ROOT/../leshy}"
if [[ -z "$LESHY_BIN_SRC" ]]; then
    echo "  Building leshy from $LESHY_SRC_DIR..."
    if [[ ! -f "$LESHY_SRC_DIR/Cargo.toml" ]]; then
        echo "FAIL: Leshy sources not found at $LESHY_SRC_DIR" >&2
        echo "      Leshy is required by the kikimora package: provide a checkout" >&2
        echo "      of ftelnov/leshy (v0.4.0) via KIKIMORA_LESHY_SRC or a prebuilt" >&2
        echo "      binary via KIKIMORA_LESHY_BIN." >&2
        exit 1
    fi
    (cd "$LESHY_SRC_DIR" && cargo build --release)
    LESHY_BIN_SRC="$LESHY_SRC_DIR/target/release/leshy"
fi
[[ -x "$LESHY_BIN_SRC" ]] || { echo "FAIL: leshy binary not executable: $LESHY_BIN_SRC" >&2; exit 1; }
install -m 0755 "$LESHY_BIN_SRC" "$STAGE/usr/local/bin/leshy"

# Config tooling and the DNS integration helper (imperative helpers, not services).
install -m 0755 "$ROOT/linux/files/build-config-go" \
    "$STAGE/usr/local/libexec/kikimora/leshy/build-config-go"
install -m 0755 "$ROOT/linux/files/check-config" \
    "$STAGE/usr/local/libexec/kikimora/leshy/check-config"
install -m 0755 "$ROOT/linux/files/leshy-dns" "$STAGE/usr/local/sbin/leshy-dns"
for route_seed in primary.txt secondary.txt; do
    if [[ -f "$ROOT/linux/files/routes/$route_seed" ]]; then
        install -m 0644 "$ROOT/linux/files/routes/$route_seed" \
            "$STAGE/usr/local/libexec/kikimora/leshy/routes/$route_seed"
    fi
done

# Leshy service unit. The canonical core unit pulls it in via Wants=; the
# DNS hooks are non-fatal and mirror the documented cold-boot contract.
install -m 0644 "$ROOT/linux/files/leshy.service" \
    "$STAGE/usr/lib/systemd/system/leshy.service"
cat > "$STAGE/usr/lib/systemd/system/leshy.service.d/kikimora-dns-hooks.conf" <<'EOF'
[Unit]
After=kikimora-core.service

[Service]
ExecStartPost=-/usr/local/sbin/leshy-dns resume
ExecStartPost=-/bin/sh -c '/usr/local/sbin/leshy-dns check || /usr/local/sbin/leshy-dns enable'
ExecStopPost=-/usr/local/sbin/leshy-dns suspend
EOF

# Default Leshy configuration seeds. postinst copies them into
# /etc/kikimora/leshy only when the admin has not created their own.
for domain_seed in primary.txt secondary.txt bypass.txt; do
    install -m 0644 "$ROOT/linux/files/domains/$domain_seed" \
        "$STAGE/usr/share/kikimora/leshy/domains/$domain_seed"
done
for endpoint_seed in primary.txt secondary.txt; do
    install -m 0644 "$ROOT/linux/files/endpoints/$endpoint_seed" \
        "$STAGE/usr/share/kikimora/leshy/endpoints/$endpoint_seed"
done
printf 'DEFAULT_ZONE=direct\n' > "$STAGE/usr/share/kikimora/leshy/routing.conf"

# ---- System integration ----
install -m 0644 "$ROOT/linux/files/kikimora-core.service" \
    "$STAGE/usr/lib/systemd/system/kikimora-core.service"
install -m 0644 "$ROOT/linux/files/kikimora-core.tmpfiles.conf" \
    "$STAGE/usr/lib/tmpfiles.d/kikimora-core.conf"
install -m 0644 "$ROOT/linux/files/kikimora-core.sysusers.conf" \
    "$STAGE/usr/lib/sysusers.d/kikimora-core.conf"
install -m 0644 "$ROOT/linux/files/orchestration-ownership.conf" \
    "$STAGE/usr/share/kikimora/orchestration-ownership.conf"
install -m 0644 "$ROOT/VERSION" \
    "$STAGE/usr/share/kikimora/VERSION"
install -m 0644 "$ROOT/linux/files/90-kikimora-unmanaged.conf" \
    "$STAGE/etc/NetworkManager/conf.d/90-kikimora-unmanaged.conf"

# ---- Shell completion ----
install -m 0644 "$ROOT/linux/completions/kikimora.bash" \
    "$STAGE/usr/share/bash-completion/completions/kikimora"
install -m 0644 "$ROOT/linux/completions/_kikimora" \
    "$STAGE/usr/local/share/zsh/site-functions/_kikimora"
install -m 0644 "$ROOT/linux/completions/kikimora.fish" \
    "$STAGE/usr/share/fish/vendor_completions.d/kikimora.fish"

# ---- DEBIAN control file ----
cat > "$STAGE/DEBIAN/control" <<EOF
Package: kikimora
Version: $KIKIMORA_VERSION
Section: net
Priority: optional
Architecture: $ARCH
Maintainer: Kikimora maintainers
Depends: bash, iproute2, systemd, openconnect, network-manager
Description: Kikimora console VPN control plane and managed Toad runtime
 Kikimora provides a versioned local control API, the kk console client,
 and independently supervised AmneziaWG, Xray (VLESS/REALITY), and
 OpenConnect Toad processes. The optional Qt UI is packaged separately.
EOF

# ---- DEBIAN prerm ----
cat > "$STAGE/DEBIAN/prerm" <<'PRERM'
#!/bin/sh
set -e

case "${1:-}" in
    upgrade)
        # Preserve whether the installed service was actually running. Fresh
        # installs remain inert, while an active runtime is restarted only as
        # part of an explicit package upgrade.
        if command -v systemctl >/dev/null 2>&1 &&
           systemctl is-active --quiet kikimora-core.service; then
            install -d -o root -g root -m 0755 /run/kikimora-package
            : > /run/kikimora-package/restart-after-upgrade
            systemctl stop kikimora-core.service
        fi
        ;;
    remove|deconfigure)
        if command -v systemctl >/dev/null 2>&1; then
            systemctl stop kikimora-core.service 2>/dev/null || true
            systemctl disable kikimora-core.service 2>/dev/null || true
            systemctl stop leshy.service 2>/dev/null || true
            systemctl disable leshy.service 2>/dev/null || true
        fi
        ;;
esac

exit 0
PRERM
chmod 0755 "$STAGE/DEBIAN/prerm"

# ---- DEBIAN postinst ----
cat > "$STAGE/DEBIAN/postinst" <<'POSTINST'
#!/bin/sh
set -e

# Create the service group before assigning group-owned paths.
if command -v systemd-sysusers >/dev/null 2>&1; then
    systemd-sysusers /usr/lib/sysusers.d/kikimora-core.conf 2>/dev/null || true
fi

# Create required directories without making secrets world-readable.
install -d -o root -g root -m 0755 /etc/kikimora
install -d -o root -g root -m 0755 /etc/kikimora/leshy
install -d -o root -g kikimora -m 0750 /etc/kikimora/toads
install -d -o root -g root -m 0700 /etc/kikimora/secrets

# Install default ownership config only when missing (preserve admin edits).
if [ ! -f /etc/kikimora/leshy/orchestration-ownership.conf ]; then
    cp /usr/share/kikimora/orchestration-ownership.conf \
       /etc/kikimora/leshy/orchestration-ownership.conf
    chmod 0644 /etc/kikimora/leshy/orchestration-ownership.conf
fi

# Leshy runtime configuration: seed defaults and generate config.toml only
# when missing, so admin domain lists and generated zones survive upgrades.
# Generation failure is non-fatal: the core keeps running, traffic steering
# stays disabled until the config is repaired.
install -d -o root -g root -m 0755 /etc/kikimora/leshy/domains
install -d -o root -g root -m 0755 /etc/kikimora/leshy/endpoints
for seed in primary.txt secondary.txt bypass.txt; do
    if [ ! -f "/etc/kikimora/leshy/domains/$seed" ]; then
        cp "/usr/share/kikimora/leshy/domains/$seed" "/etc/kikimora/leshy/domains/$seed"
        chmod 0644 "/etc/kikimora/leshy/domains/$seed"
    fi
done
for seed in primary.txt secondary.txt; do
    if [ ! -f "/etc/kikimora/leshy/endpoints/$seed" ]; then
        cp "/usr/share/kikimora/leshy/endpoints/$seed" "/etc/kikimora/leshy/endpoints/$seed"
        chmod 0644 "/etc/kikimora/leshy/endpoints/$seed"
    fi
done
if [ ! -f /etc/kikimora/leshy/routing.conf ]; then
    cp /usr/share/kikimora/leshy/routing.conf /etc/kikimora/leshy/routing.conf
    chmod 0644 /etc/kikimora/leshy/routing.conf
fi
if [ ! -f /etc/kikimora/leshy/config.toml ]; then
    if ! /usr/local/libexec/kikimora/leshy/build-config-go \
        /etc/kikimora/leshy/domains \
        /etc/kikimora/leshy/config.toml \
        /etc/kikimora/leshy/routing.conf >/dev/null 2>&1; then
        echo "kikimora: warning: Leshy config generation failed; traffic steering stays disabled" >&2
        rm -f /etc/kikimora/leshy/config.toml
    fi
fi

if command -v systemd-tmpfiles >/dev/null 2>&1; then
    systemd-tmpfiles --create 2>/dev/null || true
fi
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload 2>/dev/null || true
    if [ -f /run/kikimora-package/restart-after-upgrade ]; then
        rm -f /run/kikimora-package/restart-after-upgrade
        rmdir /run/kikimora-package 2>/dev/null || true
        systemctl restart kikimora-core.service
    fi
fi

exit 0
POSTINST
chmod 0755 "$STAGE/DEBIAN/postinst"

# ---- DEBIAN postrm ----
cat > "$STAGE/DEBIAN/postrm" <<'POSTRM'
#!/bin/sh
set -e

case "${1:-}" in
    upgrade)
        # Debian runs the old package's postrm upgrade before the new package's
        # postinst. Keep the marker so the new postinst can restart only a
        # service that was active before the upgrade.
        ;;
    failed-upgrade|abort-upgrade)
        if [ -f /run/kikimora-package/restart-after-upgrade ] &&
           command -v systemctl >/dev/null 2>&1; then
            systemctl daemon-reload 2>/dev/null || true
            systemctl restart kikimora-core.service 2>/dev/null || true
        fi
        rm -f /run/kikimora-package/restart-after-upgrade 2>/dev/null || true
        rmdir /run/kikimora-package 2>/dev/null || true
        ;;
    purge)
        # Purge package-owned runtime state only. Admin-created Toad configs,
        # secrets and the shared ownership guard are deliberately preserved so
        # package removal cannot destroy migration/rollback material.
        rm -rf /var/lib/kikimora/core
        rm -rf /var/lib/kikimora/leshy
        rmdir /var/lib/kikimora 2>/dev/null || true
        rmdir /etc/kikimora/toads 2>/dev/null || true
        rmdir /etc/kikimora/secrets 2>/dev/null || true
        rm -f /run/kikimora-package/restart-after-upgrade 2>/dev/null || true
        rmdir /run/kikimora-package 2>/dev/null || true
        ;;
    *)
        rm -f /run/kikimora-package/restart-after-upgrade 2>/dev/null || true
        rmdir /run/kikimora-package 2>/dev/null || true
        ;;
esac

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload 2>/dev/null || true
fi

exit 0
POSTRM
chmod 0755 "$STAGE/DEBIAN/postrm"

echo "  Staged: $STAGE"
