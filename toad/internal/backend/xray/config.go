package xray

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	"github.com/smollgreymouse/kikimora/toad/internal/config"
)

type xrayConfig struct {
	Log       xrayLog        `json:"log"`
	Inbounds  []xrayInbound  `json:"inbounds"`
	Outbounds []xrayOutbound `json:"outbounds"`
}

type xrayLog struct {
	LogLevel string `json:"loglevel"`
}

type xrayInbound struct {
	Tag      string          `json:"tag"`
	Protocol string          `json:"protocol"`
	Settings xrayTunSettings `json:"settings"`
}

type xrayTunSettings struct {
	Name    string   `json:"name"`
	MTU     int      `json:"mtu"`
	Gateway []string `json:"gateway"`
}

type xrayOutbound struct {
	Tag            string             `json:"tag"`
	Protocol       string             `json:"protocol"`
	Settings       xrayVLESSSettings  `json:"settings"`
	StreamSettings xrayStreamSettings `json:"streamSettings"`
}

type xrayVLESSSettings struct {
	VNext []xrayVNext `json:"vnext"`
}

type xrayVNext struct {
	Address string     `json:"address"`
	Port    uint16     `json:"port"`
	Users   []xrayUser `json:"users"`
}

type xrayUser struct {
	ID         string `json:"id"`
	Encryption string `json:"encryption"`
	Flow       string `json:"flow,omitempty"`
}

type xrayStreamSettings struct {
	Network         string              `json:"network"`
	Security        string              `json:"security"`
	RealitySettings xrayRealitySettings `json:"realitySettings"`
}

type xrayRealitySettings struct {
	ServerName  string `json:"serverName"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId"`
	SpiderX     string `json:"spiderX"`
}

func buildCoreJSON(cfg *config.Config) ([]byte, error) {
	if cfg == nil || cfg.VLESS == nil {
		return nil, fmt.Errorf("Xray backend requires normalized VLESS Reality config")
	}
	host, portText, err := net.SplitHostPort(cfg.VLESS.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse VLESS endpoint: %w", err)
	}
	port64, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port64 == 0 {
		return nil, fmt.Errorf("parse VLESS endpoint port")
	}

	network := "raw"
	switch cfg.VLESS.Transport {
	case "", "raw", "tcp":
	default:
		return nil, fmt.Errorf("unsupported VLESS transport %q", cfg.VLESS.Transport)
	}
	spiderX := cfg.VLESS.SpiderX
	if spiderX == "" {
		spiderX = "/"
	}
	fingerprint := cfg.VLESS.Fingerprint
	if fingerprint == "" {
		fingerprint = "chrome"
	}

	built := xrayConfig{
		Log: xrayLog{LogLevel: "warning"},
		Inbounds: []xrayInbound{{
			Tag:      "toad-tun",
			Protocol: "tun",
			Settings: xrayTunSettings{
				Name:    cfg.Interface,
				MTU:     cfg.MTU,
				Gateway: append([]string(nil), cfg.Address...),
			},
		}},
		Outbounds: []xrayOutbound{{
			Tag:      "toad-vless",
			Protocol: "vless",
			Settings: xrayVLESSSettings{VNext: []xrayVNext{{
				Address: host,
				Port:    uint16(port64),
				Users: []xrayUser{{
					ID:         cfg.VLESS.UUID,
					Encryption: "none",
					Flow:       cfg.VLESS.Flow,
				}},
			}}},
			StreamSettings: xrayStreamSettings{
				Network:  network,
				Security: "reality",
				RealitySettings: xrayRealitySettings{
					ServerName:  cfg.VLESS.ServerName,
					Fingerprint: fingerprint,
					PublicKey:   cfg.VLESS.PublicKey,
					ShortID:     cfg.VLESS.ShortID,
					SpiderX:     spiderX,
				},
			},
		}},
	}

	data, err := json.Marshal(built)
	if err != nil {
		return nil, fmt.Errorf("marshal Xray config: %w", err)
	}
	return data, nil
}
