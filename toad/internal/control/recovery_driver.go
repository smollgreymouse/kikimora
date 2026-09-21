package control

import (
	"context"
	"fmt"
	"net/netip"
	"sync"

	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/leshy"
	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/platform"
	"github.com/smollgreymouse/kikimora/toad/internal/routing"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
)

// RecoveryServices are the concrete resource owners used by the ordered core
// recovery transaction. Each writer is injected so tests can use a fake kernel
// while production supplies the Linux netlink and Leshy adapters.
type RecoveryServices struct {
	Routes   platform.RouteManager
	Executor routing.Executor
	Resolver endpoint.Resolver
	Leshy    leshy.Bridge
}

type recoveryDriver struct {
	manager   *Manager
	services  RecoveryServices
	mu        sync.Mutex
	endpoints map[string]*endpoint.Manager
	parking   *parking.Manager
}

// NewRecoveryDriver wires the resource owners to the manager. It does not
// enable automatic recovery; callers must explicitly set the global gate.
func NewRecoveryDriver(manager *Manager, services RecoveryServices) core.RecoveryDriver {
	return &recoveryDriver{manager: manager, services: services, endpoints: make(map[string]*endpoint.Manager)}
}

func (d *recoveryDriver) role(name string) (*role, core.RoleRuntime, error) {
	d.manager.mu.Lock()
	defer d.manager.mu.Unlock()
	r, ok := d.manager.roles[name]
	if !ok {
		return nil, core.RoleRuntime{}, fmt.Errorf("unknown Toad %q", name)
	}
	product, _ := d.manager.product.Role(name)
	copy := *r
	return &copy, product, nil
}

func (d *recoveryDriver) ObserveRoutes(ctx context.Context, role string) error {
	if d.services.Executor == nil {
		return nil
	}
	_, err := d.services.Executor.Snapshot(ctx)
	return err
}

func (d *recoveryDriver) Park(ctx context.Context, role string) error {
	if d.services.Executor == nil {
		return nil
	}
	r, _, err := d.role(role)
	if err != nil {
		return err
	}
	if r.observed.Interface.IfIndex <= 0 {
		return nil
	}
	manager := d.parkingManager()
	if err := manager.PrepareWithdrawalFromKernel(ctx, role, r.observed.Interface.IfIndex); err != nil {
		return err
	}
	product, _ := d.manager.product.Role(role)
	_ = d.manager.product.UpdateRoleResources(role, product.Endpoint, product.Publication, manager.Snapshot(role), product.Validation)
	return nil
}

func (d *recoveryDriver) Withdraw(ctx context.Context, role string) error {
	if d.services.Leshy == nil {
		return nil
	}
	if err := d.services.Leshy.Withdraw(ctx, role); err != nil {
		return err
	}
	product, _ := d.manager.product.Role(role)
	_ = d.manager.product.UpdateRoleResources(role, product.Endpoint, leshy.PublicationState{}, product.Parking, product.Validation)
	return nil
}

func (d *recoveryDriver) Quiesce(ctx context.Context, role string) error {
	return d.callToad(ctx, role, toadctl.Request{Version: toadctl.ProtocolVersion, Method: "Quiesce"})
}

func (d *recoveryDriver) Rebind(ctx context.Context, role string) error {
	underlay := d.manager.currentUnderlay()
	binding := bindingFromUnderlay(underlay)
	return d.callToad(ctx, role, toadctl.Request{Version: toadctl.ProtocolVersion, Method: "Rebind", Binding: &binding})
}

func (d *recoveryDriver) ApplyEndpoint(ctx context.Context, role string) error {
	if d.services.Routes == nil {
		return nil
	}
	r, product, err := d.role(role)
	if err != nil {
		return err
	}
	if r.cfg == nil {
		return fmt.Errorf("role %q has no config", role)
	}
	underlay := d.manager.currentUnderlay()
	policy := r.cfg.EffectiveEndpointPolicy()
	if policy.Zone == "" || policy.Priority == 0 {
		return fmt.Errorf("role %q has no endpoint policy zone/priority", role)
	}
	endpointManager := d.endpointManager(role)
	configured, err := r.cfg.ResolveTransportEndpoints(ctx)
	if err != nil {
		return fmt.Errorf("resolve endpoint policy for %q: %w", role, err)
	}
	state := endpointManager.RefreshSpecs(ctx, underlay.Epoch, configured, d.services.Resolver)
	if state.State != "ready" {
		return fmt.Errorf("endpoint policy for %q is %s: %s", role, state.State, state.LastError)
	}
	for _, address := range state.Resolved {
		prefix := netip.PrefixFrom(address.Addr(), address.Addr().BitLen())
		path := underlay.IPv4
		if address.Addr().Is6() {
			path = underlay.IPv6
		}
		if path == nil || path.IfIndex <= 0 {
			return fmt.Errorf("no physical path for endpoint %s", address)
		}
		policy.Routes = append(policy.Routes, endpoint.Route{Prefix: prefix, Gateway: path.Gateway, IfIndex: path.IfIndex, Metric: 1})
	}
	if err := d.services.Routes.ReconcileEndpointPolicy(ctx, policy); err != nil {
		return err
	}
	_ = d.manager.product.UpdateRoleResources(role, state, product.Publication, product.Parking, product.Validation)
	return nil
}

func (d *recoveryDriver) StartTransport(ctx context.Context, role string) error {
	r, _, err := d.role(role)
	if err != nil {
		return err
	}
	underlay := d.manager.currentUnderlay()
	binding := bindingFromUnderlay(underlay)
	response, callErr := d.callToadResponse(ctx, role, toadctl.Request{Version: toadctl.ProtocolVersion, Method: "RestartTransport", Binding: &binding})
	if callErr == nil && response.OK {
		return nil
	}
	if response.Error != nil && response.Error.Code != "capability_unsupported" {
		return response.Error
	}
	// Protocols without stable-TUN restart use the manager's bounded full
	// process replacement, preserving desired=true for the role.
	return d.manager.restartRole(ctx, role, r)
}

func (d *recoveryDriver) Validate(ctx context.Context, role string) error {
	return d.manager.ValidateRole(ctx, role)
}

func (d *recoveryDriver) Publish(ctx context.Context, role string) error {
	if d.services.Leshy == nil {
		return nil
	}
	r, _, err := d.role(role)
	if err != nil {
		return err
	}
	if r.observed.Interface.Name == "" {
		return fmt.Errorf("role %q has no validated interface", role)
	}
	publication := leshy.RolePublication{Role: role, Zone: r.cfg.EffectiveEndpointPolicy().Zone, Interface: r.observed.Interface.Name}
	if err := d.services.Leshy.Publish(ctx, publication); err != nil {
		return err
	}
	product, _ := d.manager.product.Role(role)
	_ = d.manager.product.UpdateRoleResources(role, product.Endpoint, leshy.PublicationState{Published: true, Zone: publication.Zone, Interface: publication.Interface}, product.Parking, product.Validation)
	return nil
}

func (d *recoveryDriver) ResyncLeshy(ctx context.Context, role string) error {
	if d.services.Leshy == nil {
		return nil
	}
	return d.services.Leshy.Resync(ctx, role)
}

func (d *recoveryDriver) ObserveRestoration(ctx context.Context, role string) error {
	if d.services.Executor == nil {
		return nil
	}
	manager := d.parkingManager()
	_, err := manager.ObserveRestorationFromKernel(ctx, role)
	return err
}

func (d *recoveryDriver) parkingManager() *parking.Manager {
	// Keep one parking manager per recovery driver without expanding Manager's
	// public state. The executor is serialized by the caller's service wiring.
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.parking == nil {
		d.parking = parking.NewManager(d.services.Executor)
	}
	return d.parking
}

func (d *recoveryDriver) endpointManager(role string) *endpoint.Manager {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.endpoints[role] == nil {
		d.endpoints[role] = &endpoint.Manager{}
	}
	return d.endpoints[role]
}

func (d *recoveryDriver) callToad(ctx context.Context, role string, request toadctl.Request) error {
	response, err := d.callToadResponse(ctx, role, request)
	if err != nil {
		return err
	}
	if !response.OK {
		if response.Error != nil {
			return response.Error
		}
		return fmt.Errorf("Toad %q rejected %s", role, request.Method)
	}
	return nil
}

func (d *recoveryDriver) callToadResponse(ctx context.Context, role string, request toadctl.Request) (toadctl.Response, error) {
	d.manager.mu.Lock()
	r, ok := d.manager.roles[role]
	socket := ""
	if ok {
		socket = r.controlSocket
	}
	d.manager.mu.Unlock()
	if !ok {
		return toadctl.Response{}, fmt.Errorf("unknown Toad %q", role)
	}
	if socket == "" {
		return toadctl.Response{}, fmt.Errorf("Toad %q has no live control socket", role)
	}
	if request.Generation == 0 {
		switch request.Method {
		case "Quiesce", "Validate", "Rebind", "RestartTransport", "Stop":
			if productRole, exists := d.manager.product.Role(role); exists {
				request.Generation = productRole.ToadGeneration
			}
		}
	}
	return (toadctl.Client{Socket: socket}).Call(ctx, request)
}

func bindingFromUnderlay(snapshot netstate.Snapshot) toadctl.UnderlayBinding {
	var result toadctl.UnderlayBinding
	if snapshot.IPv4 != nil {
		result.IPv4 = &toadctl.PathBinding{IfIndex: snapshot.IPv4.IfIndex, Interface: snapshot.IPv4.Interface, Source: snapshot.IPv4.PreferredSrc}
	}
	if snapshot.IPv6 != nil {
		result.IPv6 = &toadctl.PathBinding{IfIndex: snapshot.IPv6.IfIndex, Interface: snapshot.IPv6.Interface, Source: snapshot.IPv6.PreferredSrc}
	}
	return result
}
