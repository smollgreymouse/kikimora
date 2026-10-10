package netstate

import (
	"net/netip"
	"time"
)

type Path struct {
	Family       int        `json:"family"`
	IfIndex      int        `json:"ifindex"`
	Interface    string     `json:"interface"`
	Gateway      netip.Addr `json:"gateway,omitempty"`
	PreferredSrc netip.Addr `json:"preferred_source,omitempty"`
	MTU          int        `json:"mtu"`
	Table        int        `json:"table"`
	Metric       uint32     `json:"metric"`
}

type Metadata struct {
	ConnectionID string `json:"connection_id,omitempty"`
	BSSID        string `json:"bssid,omitempty"`
}

type Snapshot struct {
	Epoch      uint64    `json:"epoch"`
	IPv4       *Path     `json:"ipv4,omitempty"`
	IPv6       *Path     `json:"ipv6,omitempty"`
	Metadata   Metadata  `json:"metadata,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
}

type ChangeReason string

const (
	ChangeInitial          ChangeReason = "initial"
	ChangeInterface        ChangeReason = "default-interface-changed"
	ChangeGateway          ChangeReason = "gateway-changed"
	ChangePreferredSource  ChangeReason = "source-address-changed"
	ChangeAvailability     ChangeReason = "underlay-availability-changed"
	ChangeWiFiIdentity     ChangeReason = "wifi-identity-changed"
	ChangeResumeValidation ChangeReason = "resume-validation"
)
