//go:build darwin

package underlay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strings"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
)

// Darwin uses the kernel route database through the route(8) monitor and
// route-get interface. This keeps connectivity-only SystemConfiguration
// changes out of the canonical identity while still reacting to route events.
func DefaultSnapshot(ctx context.Context, excluded map[string]bool) (netstate.Snapshot, error) {
	result := netstate.Snapshot{ObservedAt: time.Now()}
	for _, family := range []int{4, 6} {
		path, err := defaultPath(ctx, family, excluded)
		if err != nil {
			return netstate.Snapshot{}, err
		}
		if family == 4 {
			result.IPv4 = path
		} else {
			result.IPv6 = path
		}
	}
	return result, nil
}

func defaultPath(ctx context.Context, family int, excluded map[string]bool) (*netstate.Path, error) {
	args := []string{"-n", "get"}
	if family == 4 {
		args = append(args, "-inet")
	} else {
		args = append(args, "-inet6")
	}
	args = append(args, "default")
	out, err := exec.CommandContext(ctx, "route", args...).CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, err
		}
		// No default route is a valid underlay-unavailable state.
		return nil, nil
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	iface := values["interface"]
	if iface == "" || excluded[iface] || strings.HasPrefix(iface, "utun") {
		return nil, nil
	}
	link, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, fmt.Errorf("lookup macOS underlay interface %q: %w", iface, err)
	}
	path := &netstate.Path{Family: family, IfIndex: link.Index, Interface: iface, MTU: link.MTU, Table: 254}
	if gateway, err := netip.ParseAddr(values["gateway"]); err == nil {
		path.Gateway = gateway
	}
	if source, err := netip.ParseAddr(strings.TrimSuffix(values["if address"], "%"+iface)); err == nil {
		path.PreferredSrc = source
	}
	return path, nil
}

func DefaultWatch(ctx context.Context, out chan<- netstate.Invalidation) error {
	cmd := exec.CommandContext(ctx, "route", "monitor")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start macOS route monitor: %w", err)
	}
	defer cmd.Process.Kill()
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		select {
		case out <- netstate.Invalidation{Source: "darwin-route-monitor"}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		return err
	}
	return ctx.Err()
}
