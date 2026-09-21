//go:build linux

package platform

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/smollgreymouse/kikimora/toad/internal/interfaceinfo"
	"github.com/vishvananda/netlink"
)

type linuxInterfaceRepairer struct{}

func DefaultInterfaceRepairer() InterfaceRepairer { return linuxInterfaceRepairer{} }

func (linuxInterfaceRepairer) RepairInterface(_ context.Context, name string, expected interfaceinfo.Expectation) error {
	if name == "" {
		return fmt.Errorf("managed interface name is empty")
	}
	link, err := netlink.LinkByName(name)
	if err != nil {
		return fmt.Errorf("lookup managed interface %q: %w", name, err)
	}
	attrs := link.Attrs()
	if attrs == nil || attrs.Index <= 0 {
		return fmt.Errorf("managed interface %q has invalid attributes", name)
	}
	if expected.MTU > 0 && attrs.MTU != expected.MTU {
		if err := netlink.LinkSetMTU(link, expected.MTU); err != nil {
			return fmt.Errorf("restore MTU %d on %q: %w", expected.MTU, name, err)
		}
	}

	existing, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("read addresses on %q: %w", name, err)
	}
	have := make(map[netip.Prefix]bool, len(existing))
	for _, item := range existing {
		if item.IPNet == nil {
			continue
		}
		prefix, err := netip.ParsePrefix(item.IPNet.String())
		if err == nil {
			have[prefix] = true
		}
	}
	for _, prefix := range expected.Addresses {
		if !prefix.IsValid() || have[prefix] {
			continue
		}
		addr, err := netlink.ParseAddr(prefix.String())
		if err != nil {
			return fmt.Errorf("convert expected address %s for %q: %w", prefix, name, err)
		}
		if err := netlink.AddrAdd(link, addr); err != nil {
			return fmt.Errorf("restore address %s on %q: %w", prefix, name, err)
		}
	}
	if attrs.Flags&net.FlagUp == 0 {
		if err := netlink.LinkSetUp(link); err != nil {
			return fmt.Errorf("restore link-up on %q: %w", name, err)
		}
	}

	verify, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("verify addresses on %q: %w", name, err)
	}
	verified := make(map[netip.Prefix]bool, len(verify))
	for _, item := range verify {
		if item.IPNet == nil {
			continue
		}
		if prefix, err := netip.ParsePrefix(item.IPNet.String()); err == nil {
			verified[prefix] = true
		}
	}
	for _, prefix := range expected.Addresses {
		if prefix.IsValid() && !verified[prefix] {
			return fmt.Errorf("address %s was not restored on %q", prefix, name)
		}
	}
	return nil
}
