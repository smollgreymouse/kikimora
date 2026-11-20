//go:build linux

package config

// leshyRestartSteps is the systemd unit that runs the router.
func leshyRestartSteps() ([]reloadStep, error) {
	return []reloadStep{{Command: []string{"systemctl", "restart", "leshy.service"}}}, nil
}
