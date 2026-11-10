package control

// Managed selected-route API v3 (08D §2.1). In managed mode Kikimora is the
// single FIB writer for Leshy-selected routes: Leshy sends a zone, a
// canonical prefix, a source kind and the schema2 activation fence, and the
// controller derives everything else (role, interface, gateway, table,
// metric, parking) from its own authoritative state.

// LeshyRouteSource tells the controller which admission policy applies:
// DNS intents are host prefixes (/32 or /128), static intents may be
// wider CIDRs.
type LeshyRouteSource string

const (
	LeshyRouteDNS    LeshyRouteSource = "dns"
	LeshyRouteStatic LeshyRouteSource = "static"
)

// LeshyRouteIntent is the whole request Leshy may make. It must not name a
// role, interface, ifindex, gateway, table, metric or protocol — those are
// derived by the controller.
//
// The v2 fence (D8 §8.1, matching the fields Leshy already sends): the exact
// ActivationID and committed ModeGeneration of the active schema2 activation.
// They are absent on the temporary legacy v1 wire.
type LeshyRouteIntent struct {
	Zone           string           `json:"zone"`
	Prefix         string           `json:"prefix"`
	Source         LeshyRouteSource `json:"source"`
	ActivationID   string           `json:"activation_id,omitempty"`
	ModeGeneration uint64           `json:"mode_generation,omitempty"`
}

// carriesActivationFence reports whether the request speaks the v2 wire.
func (i *LeshyRouteIntent) carriesActivationFence() bool {
	return i != nil && (i.ActivationID != "" || i.ModeGeneration != 0)
}

// LeshyRouteResult is the controller's exact verdict for one intent.
type LeshyRouteResult struct {
	Role       string `json:"role"`
	Zone       string `json:"zone"`
	Prefix     string `json:"prefix"`
	Family     int    `json:"family"` // 4 or 6
	Generation uint64 `json:"generation"`
	State      string `json:"state"` // applied|already-applied|denied|conflict
	// Reason carries the denial marker for Leshy: a family-not-ready denial
	// ("family N is not ready for this role") lets the resolver suppress
	// that family with NODATA instead of failing the whole answer.
	Reason string `json:"reason,omitempty"`
	// ActivationID/ModeGeneration echo the active fence (D8 §8.2): a current
	// v2 request receives the active fence, and Leshy fails closed on any
	// mismatch with its configured ManagedIdentity.
	ActivationID   string `json:"activation_id,omitempty"`
	ModeGeneration uint64 `json:"mode_generation,omitempty"`
}

// Managed selected-route capability strings (D8 §8.7). The handshake is
// generated from runtime support: a persistent install with the auth
// machinery claims v2 + privileged auth; the legacy v1 string is advertised
// only while the explicit compatibility branch is available; a development
// core without a StateDir claims neither v2 nor privileged auth.
const (
	CapabilityAFUnixControl           = "af_unix_control"
	CapabilityManagedSelectedRoutesV1 = "managed_selected_routes_v1"
	CapabilityManagedSelectedRoutesV2 = "managed_selected_routes_v2"
	CapabilityPrivilegedControlAuthV1 = "privileged_control_auth_v1"
)

// ManagedSelectedRoutesCapability is kept as an alias for the mode
// transaction's capability validation messages.
const ManagedSelectedRoutesCapability = CapabilityManagedSelectedRoutesV1

// API methods owned by the managed selected-route surface. They require API
// version 3; versions 1 and 2 must reject them as unsupported.
var leshyRouteMethods = map[string]bool{
	"EnsureLeshyRoute": true,
	"ForgetLeshyRoute": true,
	"FlushLeshyZone":   true,
}

// leshyRouteFamily returns the address family (4 or 6) of a canonical
// prefix string, or 0 when the prefix does not parse.
func leshyRouteFamily(prefix string) int {
	for i := 0; i < len(prefix); i++ {
		if prefix[i] == ':' {
			return 6
		}
		if prefix[i] == '.' {
			return 4
		}
	}
	return 0
}
