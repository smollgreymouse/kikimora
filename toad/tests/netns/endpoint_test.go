package netns

import (
	"context"
	"errors"
	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"net/netip"
	"testing"
)

type provider struct{ err error }

func (p provider) Resolve(context.Context) ([]netip.AddrPort, error) { return nil, p.err }
func TestEndpointFailureRetainsLastState(t *testing.T) {
	m := endpoint.Manager{Provider: provider{}}
	s := m.Refresh(context.Background(), 4, []string{"vpn.example:443"})
	if s.State != "ready" {
		t.Fatal("initial provider state")
	}
	m.Provider = provider{err: errors.New("resolver offline")}
	s = m.Refresh(context.Background(), 5, nil)
	if s.State != "degraded" || s.AppliedUnderlayEpoch != 4 {
		t.Fatalf("last known good lost: %#v", s)
	}
}
