package xray

import (
	"context"
	"errors"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/backend"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

func TestBackendAdvertisesNonDestructiveRebind(t *testing.T) {
	var candidate any = &Backend{}
	rebindable, ok := candidate.(backend.Rebindable)
	if !ok {
		t.Fatal("Xray backend must advertise Rebindable to preserve embedded TUN identity")
	}

	binding := toadctl.UnderlayBinding{
		IPv4: &toadctl.PathBinding{IfIndex: 7, Interface: "eth-test"},
	}
	if err := rebindable.Rebind(context.Background(), binding); err != nil {
		t.Fatalf("route-driven Xray rebind failed: %v", err)
	}
}

func TestBackendRebindHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&Backend{}).Rebind(ctx, toadctl.UnderlayBinding{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Rebind() error = %v, want context.Canceled", err)
	}
}
