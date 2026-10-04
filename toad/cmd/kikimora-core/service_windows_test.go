//go:build windows

package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func writeGateConfig(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "one.toml")
	content := `name = "one"
protocol = "amneziawg2"
interface = "kkone"
mtu = 1380
state_dir = "C:/Windows/Temp/kikimora-gate-state"
address = ["10.0.0.1/24"]

[awg2]
private_key = "test"
peer_public_key = "test"
preshared_key = ""
endpoint = "1.2.3.4:51820"
allowed_ips = ["0.0.0.0/0", "::/0"]
persistent_keepalive = 0
jc = 1
jmin = 2
jmax = 3
s1 = 0
s2 = 0
s3 = 0
s4 = 0
h1 = "test"
h2 = "test"
h3 = "test"
h4 = "test"
i1 = "test"
i2 = "test"
i3 = "test"
i4 = "test"
i5 = "test"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForServiceState(t *testing.T, status <-chan svc.Status, want svc.State) {
	t.Helper()
	deadline := time.After(8 * time.Second)
	for {
		select {
		case s := <-status:
			if s.State == want {
				return
			}
		case <-deadline:
			t.Fatalf("service never reached state %d", want)
		}
	}
}

func TestServiceExecuteRunsCoreAndStopsBounded(t *testing.T) {
	dir := t.TempDir()
	configPath := writeGateConfig(t, dir)
	socket := filepath.Join(os.TempDir(), "kikimora-service-test.sock")
	_ = os.Remove(socket)

	// Redirect the service file sink into the test tree; the real ProgramData
	// root is only exercised by the installed service.
	realRoot := windowsProgramDataRoot
	windowsProgramDataRoot = dir
	t.Cleanup(func() { windowsProgramDataRoot = realRoot })

	service := &kikimoraService{opts: &serveOptions{
		socket:              socket,
		toadBinary:          "kikimora-toad.exe",
		configs:             []string{configPath},
		stateDir:            filepath.Join(dir, "state"),
		leshyPublicationDir: filepath.Join(dir, "leshy"),
	}}

	req := make(chan svc.ChangeRequest)
	status := make(chan svc.Status, 16)
	done := make(chan exitResult, 1)
	go func() {
		shouldExit, code := service.Execute(nil, req, status)
		done <- exitResult{shouldExit: shouldExit, code: code}
	}()

	waitForServiceState(t, status, svc.Running)

	// The core must serve the control endpoint while the service is Running.
	serveDeadline := time.After(8 * time.Second)
	for {
		conn, err := net.DialTimeout("unix", socket, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		select {
		case <-serveDeadline:
			t.Fatalf("service core never listened on %s: %v", socket, err)
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}

	go func() { req <- svc.ChangeRequest{Cmd: svc.Stop, CurrentStatus: svc.Status{State: svc.Running, Accepts: svc.AcceptStop}} }()

	select {
	case result := <-done:
		if result.code != 0 {
			t.Fatalf("service exited with code %d", result.code)
		}
	case <-time.After(serviceShutdownBudget + 10*time.Second):
		t.Fatal("service did not stop within the shutdown budget")
	}

	// A bounded stop must not leave a listener behind.
	if conn, err := net.DialTimeout("unix", socket, 300*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("core still listening after service stop")
	}
}

type exitResult struct {
	shouldExit bool
	code       uint32
}
