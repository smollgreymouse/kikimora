package control

// Managed selected-route API v3 (08D §2.1). In managed mode Kikimora is the
// single FIB writer for Leshy-selected routes: Leshy sends a zone, a
// canonical prefix and a source kind, and the controller derives everything
// else (role, interface, gateway, table, metric, parking) from its own
// authoritative state.

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
type LeshyRouteIntent struct {
	Zone   string           `json:"zone"`
	Prefix string           `json:"prefix"`
	Source LeshyRouteSource `json:"source"`
}

// LeshyRouteResult is the controller's exact verdict for one intent.
type LeshyRouteResult struct {
	Role       string `json:"role"`
	Zone       string `json:"zone"`
	Prefix     string `json:"prefix"`
	Family     int    `json:"family"` // 4 or 6
	Generation uint64 `json:"generation"`
	State      string `json:"state"` // applied|already-applied|denied|conflict
}

// Managed selected-route capability string reported by the v3 handshake.
const ManagedSelectedRoutesCapability = "managed_selected_routes_v1"

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
