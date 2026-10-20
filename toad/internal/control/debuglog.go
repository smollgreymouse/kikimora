package control

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// debugLog is the shared debug sink. It stays nil unless KIKIMORA_DEBUG_LOG names a file,
// so a production core pays nothing for the instrumentation.
var (
	debugMu   sync.Mutex
	debugFile *os.File
	debugOn   bool
	debugInit bool
)

// DebugLogPath is the environment variable that turns core logging on.
const DebugLogPath = "KIKIMORA_DEBUG_LOG"

func debugEnabled() bool {
	debugMu.Lock()
	defer debugMu.Unlock()
	if !debugInit {
		debugInit = true
		path := os.Getenv(DebugLogPath)
		if path != "" {
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err == nil {
				debugFile = file
				debugOn = true
			}
		}
	}
	return debugOn
}

// Debugf writes one timestamped line when debug logging is enabled.
func Debugf(format string, args ...any) {
	if !debugEnabled() {
		return
	}
	line := fmt.Sprintf("%s %s\n", time.Now().Format("2006-01-02T15:04:05.000"), fmt.Sprintf(format, args...))
	debugMu.Lock()
	if debugFile != nil {
		_, _ = debugFile.WriteString(line)
	}
	debugMu.Unlock()
}

// debugRequest logs one API exchange: who asked for what, how long it took and what came back.
func debugRequest(method, role, profile string, started time.Time, err error) {
	if !debugEnabled() {
		return
	}
	status := "ok"
	if err != nil {
		status = "error: " + err.Error()
	}
	Debugf("api method=%s role=%q profile=%q took=%s status=%s",
		method, role, profile, time.Since(started).Round(time.Millisecond), status)
}
