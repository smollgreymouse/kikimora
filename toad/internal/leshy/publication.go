package leshy

import "github.com/smollgreymouse/kikimora/toad/internal/toadctl"

// PublicationState is the selected-route publication the controller exposes
// to Leshy. Generation changes whenever the previous published routes go
// stale: transport generation, ifindex, validated family readiness, or a
// freeze/withdraw cycle.
type PublicationState struct {
	Published  bool                  `json:"published"`
	Zone       string                `json:"zone,omitempty"`
	Interface  string                `json:"interface,omitempty"`
	Generation uint64                `json:"generation,omitempty"`
	Families   toadctl.RouteFamilies `json:"families"`
}
