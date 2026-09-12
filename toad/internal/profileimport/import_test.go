package profileimport

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"reflect"
	"testing"
)

const wgConfig = `[Interface]
PrivateKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=
Address = 10.77.0.2/24
MTU = 1380
Jc = 4
Jmin = 40
Jmax = 80
S1 = 15
S2 = 16
S3 = 17
S4 = 18
H1 = 1001
H2 = 1002
H3 = 1003
H4 = 1004
I1 = <r 8><t>
I2 = <rd 6>
I3 = <rc 6>
I4 = <b 0x01020304>
I5 = <r 4><rc 4>

[Peer]
PublicKey = AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI=
PresharedKey = AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM=
Endpoint = vpn.example:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
`

const amneziaVPNWGConfig = `[Interface]
Address = 10.8.1.2/32
DNS = 1.1.1.1, 1.0.0.1
PrivateKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=
Jc = 4
Jmin = 10
Jmax = 50
S1 = 55
S2 = 43
S3 = 60
S4 = 16
H1 = 1000000000-1000000999
H2 = 1100000000-1100000999
H3 = 1200000000-1200000999
H4 = 1300000000-1300000999
I1 = <r 2><b 0x0102030405060708>
I2 =
I3 =
I4 =
I5 =

[Peer]
PublicKey = AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI=
PresharedKey = AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM=
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = 192.0.2.10:31912
PersistentKeepalive = 25
`

func testOptions(t *testing.T, name string) Options {
	t.Helper()
	return Options{Name: name, StateDir: t.TempDir()}
}

func TestImportRawWireGuard(t *testing.T) {
	cfg, err := Parse(wgConfig, testOptions(t, "real-awg"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AWG2 == nil || cfg.AWG2.Endpoint != "vpn.example:51820" {
		t.Fatalf("unexpected AWG2 config: %#v", cfg.AWG2)
	}
	if cfg.AWG2.JC != 4 || cfg.AWG2.S4 != 18 || cfg.AWG2.I5 == "" {
		t.Fatalf("AWG2 obfuscation fields lost: %#v", cfg.AWG2)
	}
	if len(cfg.AWG2.AllowedIPs) != 2 || len(cfg.Address) != 1 || cfg.Address[0] != "10.77.0.2/24" {
		t.Fatalf("addresses lost: %#v", cfg)
	}
}

func TestImportWGBase64URI(t *testing.T) {
	link := "wg://" + base64.RawURLEncoding.EncodeToString([]byte(wgConfig)) + "#provider-awg"
	cfg, err := Parse(link, testOptions(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "provider-awg" || cfg.Interface != "kk-awg0" {
		t.Fatalf("unexpected common fields: %#v", cfg)
	}
}

func TestImportAmneziaVPNQCompressMatchesNativeAWG(t *testing.T) {
	lastConfig, err := json.Marshal(map[string]any{"config": amneziaVPNWGConfig})
	if err != nil {
		t.Fatal(err)
	}
	document, err := json.Marshal(map[string]any{
		"defaultContainer": "amnezia-awg2",
		"description":      "provider-awg2",
		"containers": []any{map[string]any{
			"container": "amnezia-awg2",
			"awg": map[string]any{
				"last_config": string(lastConfig),
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var compressed bytes.Buffer
	if err := binary.Write(&compressed, binary.BigEndian, uint32(len(document))); err != nil {
		t.Fatal(err)
	}
	writer, err := zlib.NewWriterLevel(&compressed, zlib.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(document); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	opts := testOptions(t, "real-awg2")
	fromLink, err := Parse("vpn://"+base64.RawURLEncoding.EncodeToString(compressed.Bytes()), opts)
	if err != nil {
		t.Fatal(err)
	}
	fromNative, err := Parse(amneziaVPNWGConfig, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromLink, fromNative) {
		t.Fatalf("vpn:// and native AWG imports differ:\nlink=%#v\nnative=%#v", fromLink, fromNative)
	}
	if fromLink.AWG2.H1 != "1000000000-1000000999" || fromLink.AWG2.I1 == "" {
		t.Fatalf("AWG2 range/special junk fields lost: %#v", fromLink.AWG2)
	}
	if fromLink.AWG2.I2 != "" || fromLink.AWG2.I3 != "" || fromLink.AWG2.I4 != "" || fromLink.AWG2.I5 != "" {
		t.Fatalf("empty optional junk fields changed: %#v", fromLink.AWG2)
	}
}

func TestImportWGQueryURI(t *testing.T) {
	link := "wireguard://vpn.example:51820?private_key=AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE%3D&public_key=AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI%3D&address=10.77.0.2%2F24&allowed_ips=0.0.0.0%2F0%2C%3A%3A%2F0&persistent_keepalive=25#query-wg"
	cfg, err := Parse(link, testOptions(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AWG2.Endpoint != "vpn.example:51820" || cfg.Name != "query-wg" || len(cfg.AWG2.AllowedIPs) != 2 {
		t.Fatalf("unexpected query import: %#v", cfg)
	}
}

func TestImportVLESSReality(t *testing.T) {
	link := "vless://11111111-1111-4111-8111-111111111111@203.0.113.10:443?encryption=none&security=reality&type=tcp&flow=xtls-rprx-vision&sni=www.example.org&fp=chrome&pbk=PUBLICKEY&sid=abcd&spx=%2F#nl-vps"
	cfg, err := Parse(link, testOptions(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VLESS == nil {
		t.Fatal("missing VLESS config")
	}
	if cfg.Name != "nl-vps" || cfg.VLESS.Endpoint != "203.0.113.10:443" || cfg.VLESS.ServerName != "www.example.org" || cfg.VLESS.PublicKey != "PUBLICKEY" || cfg.VLESS.ShortID != "abcd" || cfg.VLESS.SpiderX != "/" {
		t.Fatalf("unexpected VLESS import: %#v", cfg.VLESS)
	}
	if cfg.VLESS.Flow != "xtls-rprx-vision" || cfg.VLESS.Fingerprint != "chrome" || cfg.VLESS.Transport != "tcp" {
		t.Fatalf("VLESS transport fields lost: %#v", cfg.VLESS)
	}
}

func TestRejectNonRealityVLESS(t *testing.T) {
	_, err := Parse("vless://11111111-1111-4111-8111-111111111111@example.com:443?security=tls&sni=example.com&pbk=x", testOptions(t, ""))
	if err == nil {
		t.Fatal("expected non-Reality VLESS to be rejected")
	}
}
