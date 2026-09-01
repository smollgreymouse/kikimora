# Stage 0 share-link import contract

Stage 0 must allow a managed VPN instance to be created from the share links normally copied from VPN clients and control panels. Import is a configuration-boundary operation: it converts an external URI into Kikimora's normalized TOML. The runtime never consumes a share URI directly.

## Supported schemes

### WireGuard / AmneziaWG 2.x

Accepted aliases:

```text
wg://
awg://
amneziawg://
wireguard://
```

The URI payload is a standard or URL-safe base64 encoded WireGuard/AmneziaWG INI configuration. Padding is optional. A trailing `#label` is ignored.

Required input sections for Stage 0:

```text
[Interface]
PrivateKey
Address

[Peer]
PublicKey
Endpoint
```

Optional standard fields:

```text
MTU
PresharedKey
AllowedIPs
PersistentKeepalive
DNS
```

`DNS` is accepted as input but intentionally ignored because the managed VPN client does not own system DNS policy.

AWG2 fields imported from `[Interface]`:

```text
Jc
Jmin
Jmax
S1
S2
S3
S4
H1
H2
H3
H4
I1
I2
I3
I4
I5
```

Stage 0 imports exactly one `[Peer]`. Multiple peers are rejected rather than silently selecting one.

The following keys are rejected instead of executed or copied:

```text
PreUp
PostUp
PreDown
PostDown
Table
```

This is a security boundary. A share link must never gain shell execution or routing-policy ownership.

### VLESS + REALITY

Accepted form:

```text
vless://UUID@HOST:PORT?encryption=none&security=reality&sni=...&pbk=...&sid=...&fp=...&flow=...&type=...&spx=...#LABEL
```

Stage 0 requires `security=reality`. `encryption`, when present, must be `none`.

Imported fields:

```text
UUID
endpoint host/port
sni -> server_name
pbk -> REALITY public key
sid -> short id
fp -> fingerprint
flow
spx -> spider_x
type -> transport
```

The initial runtime backend accepts only the transport subset explicitly implemented and interoperability-tested by Stage 0. Unsupported VLESS or non-REALITY links fail closed during import.

## CLI

The normalized configuration is created with:

```text
kikimora-vpn import [SHARE_LINK] \
    --name NAME \
    --interface IFACE \
    --output PATH \
    [--force]
```

If `SHARE_LINK` is omitted, the command reads one link from stdin.

WG/AWG links contain the private key inside their base64 payload. The preferred invocation therefore avoids putting the URI in shell history or the process argument list:

```text
printf '%s\n' "$VPN_LINK" | \
  kikimora-vpn import \
    --name awg-main \
    --interface kk-awg0 \
    --output /etc/kikimora/vpn/clients/awg-main.toml
```

The output is written atomically through a same-directory temporary file and has mode `0600`. Existing files are not replaced unless `--force` is explicit.

Normal command output contains only the imported instance name, protocol, interface and destination path. It must never echo the URI, private key, UUID, preshared key or full normalized configuration.

## Normalization rules

The importer is deliberately stricter than the source ecosystem:

1. parse the external URI;
2. decode and validate all required fields;
3. reject hooks and foreign routing policy;
4. normalize into `ClientConfig`;
5. run the exact same `ClientConfig::validate()` used by the runtime;
6. serialize TOML;
7. atomically write mode `0600`.

The result is therefore not a second configuration language. Imported and hand-written instances enter the same validated runtime model.

## Tests

Unprivileged unit tests must cover:

- all WG/AWG scheme aliases;
- standard and URL-safe base64 with/without padding;
- one valid AWG2 configuration carrying J/S/H/I fields;
- rejection of `PostUp`/other hooks and `Table`;
- rejection of multiple peers;
- valid VLESS+REALITY URL decoding including percent-encoded `spx`;
- rejection of non-REALITY VLESS;
- redaction: normal CLI output never contains the input URI;
- output file mode `0600`;
- overwrite refusal and explicit `--force` replacement.

Protocol interoperability remains a separate Stage 0 gate. Successful import proves only that external configuration was normalized safely; it does not prove an AWG2 or REALITY session until the corresponding isolated namespace test succeeds.
