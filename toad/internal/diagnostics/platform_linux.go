//go:build linux

package diagnostics

import (
	"os/exec"
	"strings"
)

// logGlobs returns the product log locations that exist on Linux. The desktop
// UI writes under the per-user data directory, so every real user is covered.
func logGlobs() []string {
	return []string{
		"/var/log/kikimora/*.log",
		"/var/log/kikimora/toads/*.log",
		"/var/log/leshy/*.log",
		"/home/*/.local/share/Kikimora/logs/*.log",
		"/home/*/.local/share/Kikimora/*/logs/*.log",
		"/root/.local/share/Kikimora/logs/*.log",
	}
}

// networkState captures the addresses, routes and resolver state that explain
// most tunnel failures without reading any configuration.
func networkState() string {
	var builder strings.Builder
	commands := [][]string{
		{"ip", "-brief", "address"},
		{"ip", "route", "show", "table", "all"},
		{"ip", "-6", "route", "show", "table", "all"},
		{"resolvectl", "status"},
	}
	for _, command := range commands {
		output, err := exec.Command(command[0], command[1:]...).Output()
		builder.WriteString("$ " + strings.Join(command, " ") + "\n")
		if err != nil && len(output) == 0 {
			builder.WriteString("error: " + err.Error() + "\n\n")
			continue
		}
		builder.Write(output)
		if len(output) == 0 || output[len(output)-1] != '\n' {
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}
	return builder.String()
}
