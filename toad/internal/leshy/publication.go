package leshy

type PublicationState struct {
	Published bool   `json:"published"`
	Zone      string `json:"zone,omitempty"`
	Interface string `json:"interface,omitempty"`
}
