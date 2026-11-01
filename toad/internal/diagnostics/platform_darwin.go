//go:build darwin

package diagnostics

import (
	"os/exec"
	"strings"
)

// logGlobs returns the product log locations that exist on macOS.
func logGlobs() []string {
	return []string{
		"/var/log/kikimora/*.log",
		"/var/log/leshy/*.log",
		"/Users/*/Library/Application Support/Kikimora/logs/*.log",
		"/Users/*/Library/Application Support/Kikimora/*/logs/*.log",
	}
}

// networkState captures interface, route and resolver state.
func networkState() string {
	var builder strings.Builder
	commands := [][]string{
		{"ifconfig"},
		{"netstat", "-rn"},
		{"scutil", "--dns"},
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
