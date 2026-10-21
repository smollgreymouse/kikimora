package endpoint

import "net/netip"

type State struct {
	State                string           `json:"state"`
	AppliedUnderlayEpoch uint64           `json:"applied_underlay_epoch"`
	Configured           []string         `json:"configured,omitempty"`
	Resolved             []netip.AddrPort `json:"resolved,omitempty"`
	Live                 []netip.AddrPort `json:"live,omitempty"`
	LastError            string           `json:"last_error,omitempty"`
}
