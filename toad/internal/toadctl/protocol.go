package toadctl

import (
	"net/netip"
	"time"
)

const ProtocolVersion = 1

type StartRequest struct {
	Generation uint64          `json:"generation"`
	Underlay   UnderlayBinding `json:"underlay"`
}
type UnderlayBinding struct {
	IPv4 *PathBinding `json:"ipv4,omitempty"`
	IPv6 *PathBinding `json:"ipv6,omitempty"`
}
type PathBinding struct {
	IfIndex   int        `json:"ifindex"`
	Interface string     `json:"interface"`
	Source    netip.Addr `json:"source"`
}
type Capabilities struct {
	Validate                   bool `json:"validate"`
	Rebind                     bool `json:"rebind"`
	RestartTransportKeepingTUN bool `json:"restart_transport_keeping_tun"`
	ReportsLiveEndpoints       bool `json:"reports_live_endpoints"`
}
type TransportEndpoint struct {
	Network    string         `json:"network"`
	Address    netip.AddrPort `json:"address"`
	Hostname   string         `json:"hostname,omitempty"`
	ObservedAt time.Time      `json:"observed_at"`
	Active     bool           `json:"active"`
}
type ValidationResult struct {
	Healthy bool   `json:"healthy"`
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
}
type Snapshot struct {
	ProtocolVersion    int                 `json:"protocol_version"`
	Revision           uint64              `json:"revision"`
	Generation         uint64              `json:"generation"`
	State              string              `json:"state"`
	Reason             string              `json:"reason,omitempty"`
	InterfaceName      string              `json:"interface_name,omitempty"`
	IfIndex            int                 `json:"ifindex,omitempty"`
	MTU                int                 `json:"mtu,omitempty"`
	Addresses          []string            `json:"addresses,omitempty"`
	RouteReady         bool                `json:"route_ready"`
	SessionConnected   bool                `json:"session_connected"`
	LastHandshakeAgeMS *int64              `json:"last_handshake_age_ms,omitempty"`
	RXBytes            uint64              `json:"rx_bytes,omitempty"`
	TXBytes            uint64              `json:"tx_bytes,omitempty"`
	Endpoint           string              `json:"endpoint,omitempty"`
	Capabilities       Capabilities        `json:"capabilities"`
	Endpoints          []TransportEndpoint `json:"endpoints,omitempty"`
	UpdatedAt          time.Time           `json:"updated_at"`
}
type Request struct {
	Version    int              `json:"version"`
	ID         string           `json:"id,omitempty"`
	Method     string           `json:"method"`
	Generation uint64           `json:"generation,omitempty"`
	Start      *StartRequest    `json:"start,omitempty"`
	Binding    *UnderlayBinding `json:"binding,omitempty"`
}

func (r Request) BindingValue() UnderlayBinding {
	if r.Binding == nil {
		return UnderlayBinding{}
	}
	return *r.Binding
}

type Response struct {
	Version      int               `json:"version"`
	ID           string            `json:"id,omitempty"`
	OK           bool              `json:"ok"`
	Error        *APIError         `json:"error,omitempty"`
	Capabilities *Capabilities     `json:"capabilities,omitempty"`
	Snapshot     *Snapshot         `json:"snapshot,omitempty"`
	Validation   *ValidationResult `json:"validation,omitempty"`
}
type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func errResponse(id, code, message string, retryable bool) Response {
	return Response{Version: ProtocolVersion, ID: id, Error: &APIError{Code: code, Message: message, Retryable: retryable}}
}
