# Toad real-VPS smoke — AWG link

## Purpose

Run one production `kikimora-toad` AWG2 instance against a real VPS using a **share link as the only input**.

This is a manual workstation smoke test. It is intentionally separate from the hermetic CI/netns gates.

Do **not** switch the machine default route during the first test. Route only one public test destination through `kk-awg0`, so a bad profile cannot cut off SSH or the workstation's normal Internet connection.

## Supported link forms

The importer currently accepts AWG/WireGuard links whose scheme is one of:

```text
wg://
wireguard://
amneziawg://
```

The link may contain either:

- a URL-escaped or base64-encoded `[Interface]` / `[Peer]` profile; or
- query fields such as `private_key`, `public_key`, `preshared_key`, `endpoint`, `address`, `allowed_ips`, `persistent_keepalive`, `jc`, `jmin`, `jmax`, `s1..s4`, `h1..h4`, `i1..i5`.

There is no universal WireGuard share-URI standard. If a provider exports a different proprietary scheme/payload, do not manually convert it for this smoke: record only the non-secret scheme/shape and extend the importer separately.

Treat the real link as a bearer credential. Never paste it into GitHub, CI, shell scripts, issues, logs, or documentation.

## Prerequisites

Linux workstation with:

```bash
go version
ip -V
curl --version
sudo -v
```

Run from repository root on the branch containing Toad Stage 5.

## 1. Build Toad

```bash
mkdir -p .tmp/toad-real-awg
cd toad
go build -o ../.tmp/toad-real-awg/kikimora-toad ./cmd/kikimora-toad
cd ..
```

Set convenience paths:

```bash
TOAD_BIN="$PWD/.tmp/toad-real-awg/kikimora-toad"
TEST_DIR="$(mktemp -d)"
CFG="$TEST_DIR/awg.toml"
STATE_DIR="$TEST_DIR/state"
mkdir -p "$STATE_DIR"
chmod 700 "$TEST_DIR" "$STATE_DIR"
```

## 2. Read the provider link without putting it in shell history

```bash
read -rsp 'AWG share link: ' AWG_LINK
echo
```

Import through stdin so the credential is not present in the process command line:

```bash
printf '%s\n' "$AWG_LINK" | "$TOAD_BIN" import \
  -name real-awg \
  -interface kk-awg0 \
  -state-dir "$STATE_DIR" \
  > "$CFG"
chmod 600 "$CFG"
unset AWG_LINK
```

Validate the normalized config:

```bash
"$TOAD_BIN" validate -config "$CFG"
```

Expected shape:

```text
configuration OK: name=real-awg protocol=amneziawg2 interface=kk-awg0
```

Do not `cat` the generated config into a shared terminal/log: it contains VPN credentials.

## 3. Record the normal underlay and choose one public test destination

Keep the real VPS endpoint on the workstation's existing underlay. Since this smoke does not modify the default route, no special endpoint route should normally be required.

Use an IP-check HTTPS host as a single routed destination:

```bash
TEST_HOST=api.ipify.org
TEST_IP="$(getent ahostsv4 "$TEST_HOST" | awk 'NR==1 {print $1}')"
printf 'test host: %s -> %s\n' "$TEST_HOST" "$TEST_IP"
```

Abort if `TEST_IP` is empty.

Optional baseline before starting Toad:

```bash
BASELINE_IP="$(curl -4fsS --connect-timeout 5 --max-time 15 "https://$TEST_HOST")" || true
printf 'baseline public IP: %s\n' "$BASELINE_IP"
```

The baseline value is diagnostic only; the VPN exit IP is not required to differ if the workstation already uses another tunnel/proxy.

## 4. Start one AWG Toad

The TUN needs privileges:

```bash
sudo "$TOAD_BIN" run -config "$CFG" >"$TEST_DIR/toad.log" 2>&1 &
TOAD_PID=$!
```

Wait for the managed interface:

```bash
for i in $(seq 1 100); do
  ip link show kk-awg0 >/dev/null 2>&1 && break
  kill -0 "$TOAD_PID" 2>/dev/null || break
  sleep 0.1
done
```

Check that the process and interface are alive:

```bash
sudo kill -0 "$TOAD_PID"
ip -details link show kk-awg0
ip addr show dev kk-awg0
```

Record its identity:

```bash
AWG_IFINDEX="$(cat /sys/class/net/kk-awg0/ifindex)"
printf 'kk-awg0 ifindex=%s\n' "$AWG_IFINDEX"
```

## 5. Route only the selected test IP through AWG

Add a single host route, not a default route:

```bash
sudo ip route replace "$TEST_IP/32" dev kk-awg0
ip route get "$TEST_IP"
```

The route lookup must show `dev kk-awg0`.

## 6. Prove real traffic through the VPS

Force the already-resolved IP so DNS is not part of the VPN test:

```bash
VPN_IP="$(curl -4fsS \
  --connect-timeout 10 \
  --max-time 30 \
  --resolve "$TEST_HOST:443:$TEST_IP" \
  "https://$TEST_HOST")"
printf 'VPN public IP: %s\n' "$VPN_IP"
```

Success means the HTTPS request completed after the destination was routed into `kk-awg0`.

Inspect protocol/TUN evidence:

```bash
ip -s link show kk-awg0
sudo cat "$STATE_DIR/state.json"
```

For AWG2, state should eventually show an observed handshake/traffic rather than a permanently connecting session, and RX/TX counters should advance after the request.

Repeat the HTTPS request a few times if the first one races the initial handshake:

```bash
for i in 1 2 3; do
  curl -4fsS --connect-timeout 10 --max-time 30 \
    --resolve "$TEST_HOST:443:$TEST_IP" \
    "https://$TEST_HOST" && echo
  sleep 1
done
```

## 7. Non-destructive recovery smoke

Record the ifindex again before any experiment:

```bash
cat /sys/class/net/kk-awg0/ifindex
```

A safe first recovery check is to temporarily block only the VPS endpoint with a host firewall rule or otherwise interrupt its underlay **only if you are sure this cannot affect SSH/management traffic**. This is optional for the first real-VPS run because restart/underlay recovery is already covered by the hermetic CI gate.

Do not bounce the workstation's main interface remotely unless you have out-of-band access.

## 8. Cleanup

Remove the test route first:

```bash
sudo ip route del "$TEST_IP/32" dev kk-awg0 2>/dev/null || true
```

Stop Toad cleanly:

```bash
sudo kill -INT "$TOAD_PID"
wait "$TOAD_PID" 2>/dev/null || true
```

The Toad-owned AWG TUN should disappear:

```bash
if ip link show kk-awg0 >/dev/null 2>&1; then
  echo 'ERROR: kk-awg0 survived Toad shutdown' >&2
else
  echo 'kk-awg0 removed cleanly'
fi
```

Remove secret temporary material:

```bash
rm -rf "$TEST_DIR"
```

## Pass criteria

The first real AWG VPS smoke passes when all of these are true:

1. the real AWG share link imports and validates without manual conversion;
2. `kikimora-toad run` stays alive;
3. `kk-awg0` appears;
4. only the selected `/32` test route points at `kk-awg0`;
5. the HTTPS request succeeds through that route;
6. AWG handshake/RX/TX evidence appears in Toad state/interface counters;
7. the workstation default route is unchanged;
8. clean shutdown removes `kk-awg0`.

If the link itself fails import, preserve the credential locally and report only its scheme and structural form, never the full URI.
