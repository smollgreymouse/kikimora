package control

import (
	"context"
	"sync"

	"github.com/smollgreymouse/kikimora/toad/internal/supervisor"
)

// ExecLauncher adapts the process-lifetime supervisor to the control manager's
// compatibility launcher contract. Process-group setup and bounded shutdown
// live exclusively in internal/supervisor.
type ExecLauncher struct {
	Binary          string
	LegacyVPNConfig string
	ProviderDir     string
}

func (l ExecLauncher) Start(ctx context.Context, configPath string) (Process, error) {
	var extra []string
	if l.LegacyVPNConfig != "" {
		extra = append(extra, "-legacy-vpn-config", l.LegacyVPNConfig)
		if l.ProviderDir != "" {
			extra = append(extra, "-endpoint-provider-dir", l.ProviderDir)
		}
	}
	launcher := supervisor.ExecLauncher{Binary: l.Binary, ExtraArgs: extra}
	process, err := launcher.Start(ctx, configPath)
	if err != nil {
		return nil, err
	}
	return &supervisedProcess{process: process, launcher: launcher}, nil
}

type supervisedProcess struct {
	process  supervisor.Process
	launcher supervisor.ExecLauncher
	once     sync.Once
	stopErr  error
}

func (p *supervisedProcess) Wait() error { return p.process.Wait() }
func (p *supervisedProcess) Stop() error {
	p.once.Do(func() { p.stopErr = p.launcher.Stop(context.Background(), p.process) })
	return p.stopErr
}
