//go:build windows

package config

// leshyRestartSteps uses the Service Control Manager, the same mechanism the installer and
// the core service rely on. The stop step tolerates 1062 ("The service has
// not been started"): the service being stopped is the state the start step
// needs, so an already-stopped Leshy must not fail the reload.
func leshyRestartSteps() ([]reloadStep, error) {
	return []reloadStep{
		{Command: []string{"sc.exe", "stop", "leshy"}, ToleratedExit: map[int]bool{1062: true}},
		{Command: []string{"sc.exe", "start", "leshy"}},
	}, nil
}
