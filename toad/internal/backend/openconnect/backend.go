package openconnect

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/backend"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
	"github.com/smollgreymouse/kikimora/toad/internal/interfaceinfo"
)

var _ backend.Backend = (*Backend)(nil)

const closeTimeout = 5 * time.Second

type Backend struct {
	mu sync.Mutex

	cfg        *config.Config
	cmd        *exec.Cmd
	done       chan error
	closing    bool
	exitErr    error
	scriptPath string
	password   *os.File
	health     backend.Health
}

func (b *Backend) LocalInterfaceExpectation(context.Context) (interfaceinfo.Expectation, error) {
	b.mu.Lock()
	cfg := b.cfg
	b.mu.Unlock()
	if cfg == nil {
		return interfaceinfo.Expectation{}, fmt.Errorf("OpenConnect config is unavailable")
	}
	file, err := os.Open(filepath.Join(cfg.StateDir, "openconnect-network.env"))
	if err != nil {
		return interfaceinfo.Expectation{}, err
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(io.LimitReader(file, 64*1024))
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "mtu", "ipv4_address", "ipv4_netmasklen", "ipv6_address":
			values[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return interfaceinfo.Expectation{}, err
	}
	expectation := interfaceinfo.Expectation{}
	if raw := values["mtu"]; raw != "" {
		mtu, err := strconv.Atoi(raw)
		if err != nil || mtu <= 0 {
			return interfaceinfo.Expectation{}, fmt.Errorf("invalid OpenConnect negotiated MTU %q", raw)
		}
		expectation.MTU = mtu
	}
	if raw := values["ipv4_address"]; raw != "" {
		bits := 32
		if mask := values["ipv4_netmasklen"]; mask != "" {
			value, err := strconv.Atoi(mask)
			if err != nil || value < 0 || value > 32 {
				return interfaceinfo.Expectation{}, fmt.Errorf("invalid OpenConnect IPv4 prefix %q", mask)
			}
			bits = value
		}
		addr, err := netip.ParseAddr(raw)
		if err != nil || !addr.Is4() {
			return interfaceinfo.Expectation{}, fmt.Errorf("invalid OpenConnect IPv4 address %q", raw)
		}
		expectation.Addresses = append(expectation.Addresses, netip.PrefixFrom(addr, bits))
	}
	if raw := values["ipv6_address"]; raw != "" {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			addr, addrErr := netip.ParseAddr(raw)
			if addrErr != nil || !addr.Is6() {
				return interfaceinfo.Expectation{}, fmt.Errorf("invalid OpenConnect IPv6 address %q", raw)
			}
			prefix = netip.PrefixFrom(addr, 128)
		}
		expectation.Addresses = append(expectation.Addresses, prefix)
	}
	if len(expectation.Addresses) == 0 {
		return interfaceinfo.Expectation{}, fmt.Errorf("OpenConnect negotiated addresses are not available")
	}
	return expectation, nil
}

func (b *Backend) Validate(context.Context) backend.Validation {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cmd == nil || b.exitErr != nil {
		reason := "official OpenConnect client is not running"
		if b.exitErr != nil {
			reason = fmt.Sprintf("official OpenConnect client exited: %v", b.exitErr)
		}
		return backend.Validation{Healthy: false, State: "degraded", Reason: reason}
	}
	return backend.Validation{Healthy: true, State: "ready", Reason: "official OpenConnect client owns the managed TUN"}
}
func (b *Backend) TransportEndpoints(context.Context) ([]backend.TransportEndpoint, error) {
	if b.cfg == nil {
		return nil, fmt.Errorf("OpenConnect config is unavailable")
	}
	specs, err := b.cfg.TransportEndpointSpecs()
	if err != nil {
		return nil, err
	}
	out := make([]backend.TransportEndpoint, 0, len(specs))
	for _, spec := range specs {
		value := backend.TransportEndpoint{Network: spec.Network, Address: spec.Address, Hostname: spec.Hostname, Port: spec.Port, Active: true}
		if spec.Address.IsValid() {
			value.Port = uint16(spec.Address.Port())
		}
		out = append(out, value)
	}
	return out, nil
}

type redactingLineWriter struct {
	mu      sync.Mutex
	dst     io.Writer
	pending []byte
}

func newRedactingLineWriter(dst io.Writer) *redactingLineWriter {
	return &redactingLineWriter{dst: dst}
}

func (w *redactingLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.pending = append(w.pending, p...)
	for {
		newline := bytes.IndexByte(w.pending, '\n')
		if newline < 0 {
			break
		}
		line := append([]byte(nil), w.pending[:newline+1]...)
		w.pending = w.pending[newline+1:]
		if _, err := w.dst.Write(redactOpenConnectLogLine(line)); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (w *redactingLineWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return nil
	}
	_, err := w.dst.Write(redactOpenConnectLogLine(w.pending))
	w.pending = nil
	return err
}

func redactOpenConnectLogLine(line []byte) []byte {
	text := string(line)
	lower := strings.ToLower(text)
	for _, header := range []string{"proxy-authorization:", "authorization:", "set-cookie:", "cookie:"} {
		if i := strings.Index(lower, header); i >= 0 {
			suffix := ""
			if strings.HasSuffix(text, "\n") {
				suffix = "\n"
			}
			return []byte(text[:i+len(header)] + " <redacted>" + suffix)
		}
	}
	return line
}

func New(cfg *config.Config) *Backend {
	return &Backend{
		cfg:    cfg,
		health: backend.Health{State: "stopped"},
	}
}

func (b *Backend) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	b.mu.Lock()
	if b.cmd != nil {
		b.mu.Unlock()
		return nil
	}
	cfg := b.cfg
	b.mu.Unlock()

	if cfg == nil || cfg.OpenConnect == nil {
		return fmt.Errorf("OpenConnect backend requires normalized openconnect config")
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return fmt.Errorf("create OpenConnect state directory: %w", err)
	}

	scriptPath, err := writeRouteFreeVPNScript(cfg.StateDir)
	if err != nil {
		return err
	}
	cleanupScript := true
	defer func() {
		if cleanupScript {
			_ = os.Remove(scriptPath)
		}
	}()

	binary := cfg.OpenConnect.OpenConnectBinary
	if binary == "" {
		binary = "openconnect"
	}
	args := buildArgs(cfg, scriptPath)
	cmd := exec.Command(binary, args...)
	stdout := newRedactingLineWriter(os.Stdout)
	stderr := newRedactingLineWriter(os.Stderr)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	var passwordFile *os.File
	if cfg.OpenConnect.PasswordFile != "" {
		passwordFile, err = os.Open(cfg.OpenConnect.PasswordFile)
		if err != nil {
			return fmt.Errorf("open OpenConnect password file: %w", err)
		}
		cmd.Stdin = passwordFile
	}

	if err := cmd.Start(); err != nil {
		if passwordFile != nil {
			_ = passwordFile.Close()
		}
		return fmt.Errorf("start official OpenConnect client: %w", err)
	}

	done := make(chan error, 1)
	b.mu.Lock()
	b.cmd = cmd
	b.done = done
	b.closing = false
	b.exitErr = nil
	b.scriptPath = scriptPath
	b.password = passwordFile
	b.health = backend.Health{
		State:    "connecting",
		Reason:   "official OpenConnect client running; waiting for managed TUN",
		Endpoint: cfg.OpenConnect.Gateway,
	}
	b.mu.Unlock()
	cleanupScript = false

	go b.wait(cmd, done, passwordFile, stdout, stderr)
	return nil
}

func (b *Backend) wait(cmd *exec.Cmd, done chan error, passwordFile *os.File, stdout, stderr *redactingLineWriter) {
	err := cmd.Wait()
	_ = stdout.Flush()
	_ = stderr.Flush()
	if passwordFile != nil {
		_ = passwordFile.Close()
	}

	b.mu.Lock()
	if b.cmd == cmd {
		b.exitErr = err
		if !b.closing {
			reason := "official OpenConnect client exited"
			if err != nil {
				reason = fmt.Sprintf("official OpenConnect client exited: %v", err)
			}
			b.health = backend.Health{State: "degraded", Reason: reason, Endpoint: endpointOf(b.cfg)}
		}
	}
	b.mu.Unlock()

	done <- err
	close(done)
}

func (b *Backend) Health(context.Context) backend.Health {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cmd == nil {
		return b.health
	}
	if b.exitErr != nil {
		return b.health
	}

	iface, err := net.InterfaceByName(b.cfg.Interface)
	if err != nil {
		b.health = backend.Health{
			State:    "connecting",
			Reason:   "official OpenConnect client running; managed TUN not ready",
			Endpoint: endpointOf(b.cfg),
		}
		return b.health
	}

	rx, tx := readInterfaceCounters(b.cfg.Interface)
	connected := iface.Flags&net.FlagUp != 0
	state := "connecting"
	reason := "OpenConnect TUN exists but link is not up"
	if connected {
		state = "online"
		reason = "official OpenConnect data phase owns stable managed TUN"
	}
	b.health = backend.Health{
		State:     state,
		Reason:    reason,
		Connected: connected,
		RXBytes:   rx,
		TXBytes:   tx,
		Endpoint:  endpointOf(b.cfg),
	}
	return b.health
}

func (b *Backend) Close() error {
	b.mu.Lock()
	cmd := b.cmd
	done := b.done
	scriptPath := b.scriptPath
	b.closing = true
	b.mu.Unlock()

	if cmd == nil {
		return nil
	}

	if cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	var waitErr error
	select {
	case waitErr = <-done:
	case <-time.After(closeTimeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		waitErr = <-done
	}

	if scriptPath != "" {
		_ = os.Remove(scriptPath)
	}

	b.mu.Lock()
	b.cmd = nil
	b.done = nil
	b.scriptPath = ""
	b.password = nil
	b.exitErr = nil
	b.closing = false
	b.health = backend.Health{State: "stopped"}
	b.mu.Unlock()

	// OpenConnect exits non-zero on some signal-driven shutdown paths. The Toad
	// deliberately requested shutdown, so process termination itself is not a
	// protocol failure here.
	_ = waitErr
	return nil
}

func buildArgs(cfg *config.Config, scriptPath string) []string {
	oc := cfg.OpenConnect
	reconnectTimeout := oc.ReconnectTimeout
	if reconnectTimeout == 0 {
		reconnectTimeout = 300
	}
	args := []string{
		"--protocol=" + oc.VPNProtocol,
		"--interface=" + cfg.Interface,
		"--script=" + scriptPath,
		"--user=" + oc.Username,
		"--mtu=" + strconv.Itoa(cfg.MTU),
		"--reconnect-timeout=" + strconv.Itoa(reconnectTimeout),
		"--non-inter",
		"--timestamp",
		"--verbose",
	}
	if oc.AuthGroup != "" {
		args = append(args, "--authgroup="+oc.AuthGroup)
	}
	if oc.PasswordFile != "" {
		args = append(args, "--passwd-on-stdin")
	}
	if oc.TokenMode != "" && oc.TokenMode != "none" {
		args = append(args, "--token-mode="+oc.TokenMode, "--token-secret=@"+oc.TokenSecretFile)
	}
	if oc.UserAgent != "" {
		args = append(args, "--useragent="+oc.UserAgent)
	}
	if oc.ServerCert != "" {
		args = append(args, "--servercert="+oc.ServerCert)
	}
	if oc.DisableUDP {
		args = append(args, "--no-dtls")
	}
	if oc.DisableIPv6 {
		args = append(args, "--disable-ipv6")
	}
	args = append(args, oc.Gateway)
	return args
}

func endpointOf(cfg *config.Config) string {
	if cfg == nil || cfg.OpenConnect == nil {
		return ""
	}
	return cfg.OpenConnect.Gateway
}
