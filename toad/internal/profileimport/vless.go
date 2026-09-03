package profileimport

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

func parseVLESS(raw string, opts Options) (*config.Config, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("parse VLESS URI: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "vless") {
		return nil, fmt.Errorf("unsupported VLESS URI scheme %q", u.Scheme)
	}
	if u.User == nil || u.User.Username() == "" {
		return nil, fmt.Errorf("VLESS URI is missing UUID")
	}
	host := u.Hostname()
	port := u.Port()
	if host == "" || port == "" {
		return nil, fmt.Errorf("VLESS URI requires host and port")
	}
	q := u.Query()
	if security := strings.ToLower(q.Get("security")); security != "reality" {
		return nil, fmt.Errorf("only VLESS Reality links are supported, got security=%q", security)
	}
	if encryption := strings.ToLower(q.Get("encryption")); encryption != "" && encryption != "none" {
		return nil, fmt.Errorf("unsupported VLESS encryption=%q", encryption)
	}
	transport := strings.ToLower(q.Get("type"))
	if transport == "" {
		transport = "tcp"
	}

	cfg := &config.Config{
		Protocol: config.ProtocolVLESSReality,
		VLESS: &config.VLESSRealityConfig{
			Endpoint:    net.JoinHostPort(host, port),
			UUID:        u.User.Username(),
			ServerName:  q.Get("sni"),
			PublicKey:   q.Get("pbk"),
			ShortID:     q.Get("sid"),
			Flow:        q.Get("flow"),
			Fingerprint: q.Get("fp"),
			Transport:   transport,
			SpiderX:     q.Get("spx"),
		},
	}
	applyCommon(cfg, opts, u.Fragment, "kk-xray0", []string{"10.255.0.2/30"})
	return cfg, nil
}
