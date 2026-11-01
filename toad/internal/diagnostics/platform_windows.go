//go:build windows

package diagnostics

import (
	"os/exec"
	"strings"
)

// logGlobs returns the product log locations that exist on Windows: the core
// and Toad logs under ProgramData, the portable Leshy logs and the desktop UI
// log of every interactive user.
func logGlobs() []string {
	return []string{
		`C:\ProgramData\Kikimora\logs\*.log`,
		`C:\ProgramData\Kikimora\logs\toads\*.log`,
		`C:\ProgramData\leshy\logs\*.log`,
		`C:\Users\*\AppData\Local\Kikimora\logs\*.log`,
		`C:\Users\*\AppData\Local\Kikimora\*\logs\*.log`,
	}
}

// networkState captures the adapter, route and resolver state. The commands
// are the ones an operator would run by hand on a Windows guest.
func networkState() string {
	var builder strings.Builder
	commands := [][]string{
		{"ipconfig", "/all"},
		{"route", "print", "-4"},
		{"netsh", "interface", "ipv6", "show", "route"},
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
