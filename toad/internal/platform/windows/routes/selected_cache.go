//go:build windows

package routes

import (
	"net/netip"
	"sync"

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
// callback never takes e.mu: it bumps the atomic generations first (§48.1)
// and enqueues invalidation material through its subscription's own send
// lock, so an event during a FIB snapshot is immediately visible and can
// never race a closed stream (§48.3).

// selectedObserverSubscription owns one NotifyRouteChange2 registration and
// the stream it feeds (§48.3). close is idempotent: the native registration
// is released exactly once and the channel is closed only after no callback
// can send again.
type selectedObserverSubscription struct {
	out  chan routing.SelectedRouteInvalidation
	stop func()

	mu     sync.Mutex
	closed bool
}

// send enqueues one invalidation unless the subscription already closed. The
// lock is the subscription's own — never the executor mutex.
func (s *selectedObserverSubscription) send(invalidation routing.SelectedRouteInvalidation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.out <- invalidation:
	default:
		select {
		case s.out <- routing.SelectedRouteInvalidation{Kind: routing.SelectedInvalidateFull, Sequence: invalidation.Sequence}:
		default:
		}
	}
}

// close marks the stream closed, unregisters the native observer exactly
// once, then closes the channel. Callers may invoke it more than once.
func (s *selectedObserverSubscription) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	if s.stop != nil {
		s.stop()
	}
	close(s.out)
}

// observeSelectedInvalidations registers the NotifyRouteChange2 observer
// once and returns the invalidation stream. The reconciler calls it at
// startup; the registration precedes the initial stable snapshot so no
// startup event is lost (§38.6).
func (e *Executor) observeSelectedInvalidations() (<-chan routing.SelectedRouteInvalidation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.selectedObserver != nil {
		return e.selectedObserver.out, nil
	}
	sub := &selectedObserverSubscription{out: make(chan routing.SelectedRouteInvalidation, 64)}
	unregister, err := e.api.registerRouteChange(func(notification winipcfg.MibNotificationType, row *winipcfg.MibIPforwardRow2) {
		// §48.1: the generation/sequence Add calls happen BEFORE any lock —
		// an event arriving during a FIB snapshot must become visible
		// immediately, or the before/after samples can falsely compare
		// equal. §48.2: increment-before-use, so Sequence numbers start at 1
		// and processed==current re-opens the fast path right after the
		// event is handled.
		generation := e.notifyGeneration.Add(1)
		if generation == 0 {
			panic("windows notify generation overflowed")
		}
		sequence := e.observationSequence.Add(1)
		if sequence == 0 {
			panic("windows observation sequence overflowed")
		}
		invalidation := classifyNotification(notification, row)
		invalidation.Sequence = sequence
		sub.send(invalidation)
	})
	if err != nil {
		return nil, err
	}
	sub.stop = unregister
	e.selectedObserver = sub
	if _, err := e.stableSelectedRefreshLocked(); err != nil {
		// The registration succeeded but the initial coherent cache could
		// not be published: release the native observer instead of leaking
		// a stream nobody will consume.
		e.selectedObserver = nil
		sub.close()
		return nil, err
	}
	e.selectedCacheReady = true
	return sub.out, nil
}

// CurrentSelectedRouteSequence exposes the §31.9 sequence.
func (e *Executor) CurrentSelectedRouteSequence() uint64 {
	return e.observationSequence.Load()
}

// bumpNotifyGenerationForTest simulates one notification for unit tests
// without touching the FIB.
func (e *Executor) bumpNotifyGenerationForTest() {
	e.notifyGeneration.Add(1)
}

// stableSelectedRefreshLocked performs one coherent full snapshot: retry
// while a notification lands between the generation samples, then publish
// the cache with the observed generation. Caller holds e.mu.
func (e *Executor) stableSelectedRefreshLocked() (map[netip.Prefix][]routing.SelectedKernelRow, error) {
	for {
		// §48.1: the samples read the ATOMIC generations — an event during
		// the FIB read bumps notifyGeneration even while this goroutine
		// holds the writer mutex, so a stale snapshot is always detected.
		before := e.notifyGeneration.Load()

		rows, err := e.buildSelectedSnapshot()
		if err != nil {
			return nil, err
		}

		after := e.notifyGeneration.Load()
		if after == before {
			// Publication re-verifies the generation before committing
			// (§48.1): an event between the sample and the publish retries.
			if e.notifyGeneration.Load() != after {
				continue
			}
			e.selectedCache = rows
			e.cacheGeneration.Store(after)
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
	if e.cacheGeneration.Load() == e.notifyGeneration.Load() {
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
