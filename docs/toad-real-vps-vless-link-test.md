# Toad real-VPS smoke — VLESS/REALITY/Vision link

## Purpose

Run one production `kikimora-toad` Xray instance against a real VPS using a **`vless://` share link as the only profile input**.

This is a manual workstation smoke test after the hermetic Step 05 REALITY/VLESS/Vision gate. It deliberately does not replace the workstation default route on the first run.

Only one public destination is routed through `kk-xray0`. That keeps normal Internet/SSH management traffic on the existing underlay while proving that the real VPS carries application traffic.

## Supported real link

The importer currently accepts direct VLESS links with:

```text
vless://<uuid>@<host>:<port>?security=reality&...
```

Required/expected Stage 0 fields are:

- `security=reality`;
- UUID in URI user-info;
- VPS host and port;
- `sni`;
- `pbk` REALITY public key;
- `sid` short id;
- `fp` browser fingerprint;
- optional `spx`;
- optional `flow=xtls-rprx-vision`;
- `type=tcp` or `type=raw` (missing type defaults to TCP).

The current production gate is specifically VLESS + REALITY over TCP/raw, with Vision exercised when the link configures it. WebSocket/gRPC/etc. are not part of this Stage 0 smoke.

Treat the VLESS URI as a bearer credential. Never paste the real link into GitHub, CI, shell scripts, issues, logs, or committed fixtures.

## Prerequisites

Linux workstation with:

```bash
go version
ip -V
curl --version
sudo -v
```

Run from repository root on the branch containing completed Toad Step 05.

## 1. Build Toad

```bash
mkdir -p .tmp/toad-real-vless
cd toad
go build -o ../.tmp/toad-real-vless/kikimora-toad ./cmd/kikimora-toad
cd ..
```

Create private temporary paths:

```bash
TOAD_BIN="$PWD/.tmp/toad-real-vless/kikimora-toad"
TEST_DIR="$(mktemp -d)"
CFG="$TEST_DIR/vless.toml"
STATE_DIR="$TEST_DIR/state"
PID_FILE="$TEST_DIR/toad.pid"
mkdir -p "$STATE_DIR"
chmod 700 "$TEST_DIR" "$STATE_DIR"
```

## 2. Read the real VLESS link without shell-history exposure

```bash
read -rsp 'VLESS share link: ' VLESS_LINK
echo
```

Import through stdin, not `-link`, so the credential does not appear in the process command line:

```bash
printf '%s\n' "$VLESS_LINK" | "$TOAD_BIN" import \
  -name real-vless \
  -interface kk-xray0 \
  -state-dir "$STATE_DIR" \
  > "$CFG"
chmod 600 "$CFG"
unset VLESS_LINK
```

Validate the normalized result:

```bash
"$TOAD_BIN" validate -config "$CFG"
```

Expected shape:

```text
configuration OK: name=real-vless protocol=vless-reality interface=kk-xray0
```

Do not print the generated TOML into shared logs: it contains UUID/REALITY credentials.

## 3. Pick one public application destination before changing routes

Use an HTTPS IP-check host as the single test destination:

```bash
TEST_HOST=api.ipify.org
TEST_IP="$(getent ahostsv4 "$TEST_HOST" | awk 'NR==1 {print $1}')"
printf 'test host: %s -> %s\n' "$TEST_HOST" "$TEST_IP"
```

Abort if `TEST_IP` is empty.

Optional direct-underlay baseline:

```bash
BASELINE_IP="$(curl -4fsS --connect-timeout 5 --max-time 15 "https://$TEST_HOST")" || true
printf 'baseline public IP: %s\n' "$BASELINE_IP"
```

This baseline is diagnostic only. The real pass criterion is successful HTTPS through the route pointing at `kk-xray0`.

## 4. Start one VLESS Toad

Xray creates its own managed TUN and therefore needs privileges.

Use a root shell that records the actual Toad PID before `exec`:

```bash
sudo bash -c '
  echo $$ > "$1"
  exec "$2" run -config "$3"
' bash "$PID_FILE" "$TOAD_BIN" "$CFG" >"$TEST_DIR/toad.log" 2>&1 &
TOAD_SUDO_PID=$!
```

Wait for the pid file and interface:

```bash
for i in $(seq 1 100); do
  [[ -s "$PID_FILE" ]] && ip link show kk-xray0 >/dev/null 2>&1 && break
  sleep 0.1
done
TOAD_PID="$(cat "$PID_FILE")"
```

Verify runtime state:

```bash
sudo kill -0 "$TOAD_PID"
ip -details link show kk-xray0
ip addr show dev kk-xray0
```

Record TUN identity:

```bash
XRAY_IFINDEX="$(cat /sys/class/net/kk-xray0/ifindex)"
printf 'kk-xray0 ifindex=%s\n' "$XRAY_IFINDEX"
```

At this point the process may still report conservative lifecycle health (`connecting`) because the current Xray backend deliberately does not invent a cryptographic handshake timestamp/session state. Step 05's real protocol proof is application traffic, not that health label.

## 5. Route only the chosen test IP into Xray

Do **not** replace the default route.

```bash
sudo ip route replace "$TEST_IP/32" dev kk-xray0
ip route get "$TEST_IP"
```

The route lookup must show:

```text
dev kk-xray0
```

The VLESS server endpoint itself remains reachable through the existing workstation default route, so Xray has an underlay and does not recursively route its own connection back into the TUN.

## 6. Prove real application traffic through VLESS/REALITY

Use the IP resolved before adding the test route, so DNS is not part of the assertion:

```bash
VPN_IP="$(curl -4fsS \
  --connect-timeout 10 \
  --max-time 30 \
  --resolve "$TEST_HOST:443:$TEST_IP" \
  "https://$TEST_HOST")"
printf 'VPN public IP: %s\n' "$VPN_IP"
```

A successful HTTPS response here is the manual real-VPS data-plane proof:

```text
application TCP
  -> host /32 route
  -> kk-xray0
  -> embedded official Xray
  -> VLESS
  -> REALITY
  -> Vision when present in imported link
  -> real VPS
  -> Internet destination
  -> response back through kk-xray0
```

Inspect interface traffic before/after repeated requests:

```bash
ip -s link show kk-xray0
for i in 1 2 3; do
  curl -4fsS --connect-timeout 10 --max-time 30 \
    --resolve "$TEST_HOST:443:$TEST_IP" \
    "https://$TEST_HOST" && echo
  sleep 1
done
ip -s link show kk-xray0
```

Inspect Toad state without expecting a fake Xray handshake field:

```bash
sudo cat "$STATE_DIR/state.json"
```

The Toad process must stay alive and the managed interface identity must remain unchanged:

```bash
sudo kill -0 "$TOAD_PID"
test "$(cat /sys/class/net/kk-xray0/ifindex)" = "$XRAY_IFINDEX"
```

## 7. Optional real-server recovery check

A real VPS restart/reload test is optional on the first workstation run because Step 05 already proves reference-server restart and underlay recovery hermetically.

If you control the VPS and can safely restart only its Xray service:

1. record `XRAY_IFINDEX`;
2. restart only server-side Xray;
3. do not restart local Toad;
4. retry the HTTPS request until it succeeds;
5. assert the local `kk-xray0` ifindex is unchanged.

Do not intentionally drop the workstation's primary physical interface on a remote machine unless you have an out-of-band recovery path.

## 8. Cleanup

Remove the single test route first:

```bash
sudo ip route del "$TEST_IP/32" dev kk-xray0 2>/dev/null || true
```

Stop production Toad cleanly:

```bash
sudo kill -INT "$TOAD_PID"
wait "$TOAD_SUDO_PID" 2>/dev/null || true
```

Xray owns this TUN, so it must disappear with the Toad process:

```bash
if ip link show kk-xray0 >/dev/null 2>&1; then
  echo 'ERROR: kk-xray0 survived Toad shutdown' >&2
else
  echo 'kk-xray0 removed cleanly'
fi
```

Delete generated credentials/config/state:

```bash
rm -rf "$TEST_DIR"
```

## Pass criteria

The first real VLESS VPS smoke passes when all of these are true:

1. the provider's real `vless://` link imports directly, with no manual config conversion;
2. normalized config validates;
3. production Toad stays alive;
4. official Xray creates `kk-xray0`;
5. only the chosen public `/32` is manually routed through `kk-xray0`;
6. HTTPS succeeds through that route and real VPS;
7. `kk-xray0` counters increase;
8. `kk-xray0` retains the same ifindex during the test;
9. the workstation default route remains unchanged;
10. clean shutdown removes `kk-xray0`.

If import rejects the provider link, keep the full URI private and report only non-secret structure: scheme plus which parameter names/transport type it uses.
