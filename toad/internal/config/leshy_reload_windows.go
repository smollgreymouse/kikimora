//go:build windows

package config

// leshyRestartCommands uses the Service Control Manager, the same mechanism the installer and
// the core service rely on.
func leshyRestartCommands() ([][]string, error) {
	return [][]string{
		{"sc.exe", "stop", "leshy"},
		{"sc.exe", "start", "leshy"},
	}, nil
}
