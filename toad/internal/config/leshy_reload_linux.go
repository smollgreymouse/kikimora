//go:build linux

package config

// leshyRestartCommands is the systemd unit that runs the router.
func leshyRestartCommands() ([][]string, error) {
	return [][]string{{"systemctl", "restart", "leshy.service"}}, nil
}
