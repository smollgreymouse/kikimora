package endpoint

import "net/netip"

type Source string

const (
	SourceNative        Source = "native"
	SourceStatic        Source = "static"
	SourceCommandCompat Source = "command-compat"
	SourceHapp          Source = "happ"
)

type Policy struct {
	Role       string
	Zone       string
	Priority   int
	Source     Source
	StaticFile string
	Command    string
	Routes     []Route
}
type Route struct {
	Prefix  netip.Prefix
	Gateway netip.Addr
	IfIndex int
	Metric  uint32
}
type EndpointSpec struct {
	Network  string
	Hostname string
	Address  netip.AddrPort
	Port     uint16
}

func (p Policy) Valid() bool { return p.Role != "" && p.Zone != "" && p.Priority > 0 }
