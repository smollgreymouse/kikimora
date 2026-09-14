package openconnect

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/backend"
	"github.com/smollgreymouse/kikimora/toad/internal/config"
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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

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

	go b.wait(cmd, done, passwordFile)
	return nil
}

func (b *Backend) wait(cmd *exec.Cmd, done chan error, passwordFile *os.File) {
	err := cmd.Wait()
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
