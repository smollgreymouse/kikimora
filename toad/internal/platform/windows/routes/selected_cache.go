//go:build windows

package routes

import (
	"net/netip"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"github.com/smollgreymouse/kikimora/toad/internal/routing"
)

// The coherent selected-route cache (08D1 §38.1): the request path reads the
// cache while cacheGeneration == notifyGeneration, performs exactly one
// stable refresh when they diverge, and never scans the FIB for a converged
// duplicate.
//
// Locking: the request path (ReconcileSelectedRoute and friends) holds the
// executor mutex for the whole transaction, so every cache helper below is a
// *Locked variant that assumes e.mu is already held. The notification
// callback takes e.mu only to bump the generation and enqueue invalidation
// material — it never enumerates the FIB and never mutates the cache
// directly (§38.5).

// observeSelectedInvalidations registers the NotifyRouteChange2 observer
// once and returns the invalidation stream. The reconciler calls it at
// startup; the registration precedes the initial stable snapshot so no
// startup event is lost (§38.6).
func (e *Executor) observeSelectedInvalidations() (<-chan routing.SelectedRouteInvalidation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.selectedObserverStop != nil {
		return e.selectedInvalidations, nil
	}
	out := make(chan routing.SelectedRouteInvalidation, 64)
	unregister, err := e.api.registerRouteChange(func(notification winipcfg.MibNotificationType, row *winipcfg.MibIPforwardRow2) {
		// Generation bump first (§38.5); the invalidation carries only the
		// row's identity material, and the dropped-event fallback escalates
		// to Full.
		e.mu.Lock()
		e.notifyGeneration++
		if e.notifyGeneration == 0 {
			e.mu.Unlock()
			panic("windows notify generation overflowed")
		}
		sequence := e.observationSequence
		e.observationSequence++
		if e.observationSequence == 0 {
			e.mu.Unlock()
			panic("windows observation sequence overflowed")
		}
		e.mu.Unlock()
		invalidation := classifyNotification(notification, row)
		invalidation.Sequence = sequence
		select {
		case out <- invalidation:
		default:
			select {
			case out <- routing.SelectedRouteInvalidation{Kind: routing.SelectedInvalidateFull, Sequence: sequence}:
			default:
			}
		}
	})
	if err != nil {
		return nil, err
	}
	e.selectedObserverStop = unregister
	e.selectedInvalidations = out
	if _, err := e.stableSelectedRefreshLocked(); err != nil {
		return nil, err
	}
	e.selectedCacheReady = true
	return e.selectedInvalidations, nil
}

// CurrentSelectedRouteSequence exposes the §31.9 sequence.
func (e *Executor) CurrentSelectedRouteSequence() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.observationSequence
}

// bumpNotifyGenerationForTest simulates one notification for unit tests
// without touching the FIB.
func (e *Executor) bumpNotifyGenerationForTest() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.notifyGeneration++
}

// stableSelectedRefreshLocked performs one coherent full snapshot: retry
// while a notification lands between the generation samples, then publish
// the cache with the observed generation. Caller holds e.mu.
func (e *Executor) stableSelectedRefreshLocked() (map[netip.Prefix][]routing.SelectedKernelRow, error) {
	for {
		before := e.notifyGeneration

		rows, err := e.buildSelectedSnapshot()
		if err != nil {
			return nil, err
		}

		after := e.notifyGeneration
		if after == before {
			e.selectedCache = rows
			e.cacheGeneration = after
			return rows, nil
		}
	}
}

// buildSelectedSnapshot projects one full v4+v6 forwarding-table snapshot
// onto the exact row model, grouped by canonical prefix.
func (e *Executor) buildSelectedSnapshot() (map[netip.Prefix][]routing.SelectedKernelRow, error) {
	out := make(map[netip.Prefix][]routing.SelectedKernelRow)
	for _, family := range []winipcfg.AddressFamily{winipcfg.AddressFamily(windows.AF_INET), winipcfg.AddressFamily(windows.AF_INET6)} {
		mibRows, err := e.api.routeTable(family)
		if err != nil {
			return nil, err
		}
		for i := range mibRows {
			row := &mibRows[i]
			prefix := row.DestinationPrefix.Prefix()
			if !prefix.IsValid() {
				continue
			}
			prefix = prefix.Masked()
			state := routing.KernelState{Routes: []routing.Operation{mibToOperation(row)}}
			out[prefix] = append(out[prefix], selectedRowsFor(&state, prefix)...)
		}
	}
	return out, nil
}

// mibToOperation projects one MIB row onto the neutral operation view the
// shared classifier consumes.
func mibToOperation(row *winipcfg.MibIPforwardRow2) routing.Operation {
	prefix := row.DestinationPrefix.Prefix()
	hop := row.NextHop.Addr()
	gateway := ""
	if hop.IsValid() && !hop.IsUnspecified() {
		gateway = hop.String()
	}
	return routing.Operation{
		Kind:          "route",
		Prefix:        prefix.String(),
		Table:         kernelTable,
		IfIndex:       int(row.InterfaceIndex),
		InterfaceLuid: uint64(row.InterfaceLUID),
		Protocol:      int(row.Protocol),
		Metric:        row.Metric,
		Gateway:       gateway,
	}
}

// cachedSelectedRowsLocked returns the same-prefix rows from the coherent
// cache, refreshing exactly once when a notification moved the generation.
// Caller holds e.mu.
func (e *Executor) cachedSelectedRowsLocked(prefix netip.Prefix) ([]routing.SelectedKernelRow, error) {
	if !e.selectedCacheReady {
		if _, err := e.stableSelectedRefreshLocked(); err != nil {
			return nil, err
		}
		e.selectedCacheReady = true
		return e.selectedCache[prefix], nil
	}
	if e.cacheGeneration == e.notifyGeneration {
		return e.selectedCache[prefix], nil
	}
	// One stable refresh; the converged duplicate never reaches here.
	rows, err := e.stableSelectedRefreshLocked()
	if err != nil {
		return nil, err
	}
	return rows[prefix], nil
}

// refreshAfterLocalMutationLocked re-proves one prefix after this process
// mutated it: exactly one coherent refresh of the affected entry (§38.1).
// Caller holds e.mu.
func (e *Executor) refreshAfterLocalMutationLocked(prefix netip.Prefix) ([]routing.SelectedKernelRow, error) {
	if _, err := e.stableSelectedRefreshLocked(); err != nil {
		return nil, err
	}
	return e.selectedCache[prefix], nil
}
