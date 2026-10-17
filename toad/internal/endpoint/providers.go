package endpoint

import (
	"bufio"
	"context"
	"errors"
	"net/netip"
	"os"
	"strings"
	"time"
)

type Provider interface {
	Resolve(context.Context) ([]netip.AddrPort, error)
}
type StaticProvider struct {
	Path     string
	MaxBytes int
}

func (p StaticProvider) Resolve(_ context.Context) ([]netip.AddrPort, error) {
	if p.Path == "" {
		return nil, errors.New("static endpoint file is empty")
	}
	out, err := os.ReadFile(p.Path)
	if err != nil {
		return nil, err
	}
	if p.MaxBytes <= 0 {
		p.MaxBytes = 64 * 1024
	}
	if len(out) > p.MaxBytes {
		return nil, errors.New("endpoint provider output too large")
	}
	var result []netip.AddrPort
	s := bufio.NewScanner(strings.NewReader(string(out)))
	for s.Scan() {
		v := strings.TrimSpace(s.Text())
		if v == "" {
			continue
		}
		a, err := netip.ParseAddrPort(v)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, s.Err()
}

type CommandProvider struct {
	Command  string
	Timeout  time.Duration
	MaxBytes int
	Env      map[string]string
}

func (p CommandProvider) Resolve(ctx context.Context) ([]netip.AddrPort, error) {
	out, err := p.output(ctx)
	if err != nil {
		return nil, err
	}
	var result []netip.AddrPort
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		for _, raw := range strings.Fields(scanner.Text()) {
			if strings.HasPrefix(raw, "#") {
				break
			}
			a, err := netip.ParseAddrPort(raw)
			if err != nil {
				return nil, err
			}
			result = append(result, a)
		}
	}
	return result, scanner.Err()
}
