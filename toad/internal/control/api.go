// Package control implements the local Kikimora control plane.  It supervises
// Toad processes; protocol implementations remain owned by kikimora-toad.
package control

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
)

// Request represents an incoming API request.
type Request struct {
	Version int    `json:"version"`
	ID      string `json:"id,omitempty"`
	Method  string `json:"method"`
	Role    string `json:"role,omitempty"`
	Profile string `json:"profile,omitempty"`
}

// Response represents an outgoing API response.
type Response struct {
	Version      int       `json:"version"`
	MinVersion   int       `json:"min_version,omitempty"`
	MaxVersion   int       `json:"max_version,omitempty"`
	ID           string    `json:"id,omitempty"`
	OK           bool      `json:"ok"`
	Error        string    `json:"error,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	Snapshot     *Snapshot `json:"snapshot,omitempty"`
	APIError     *APIError `json:"error_detail,omitempty"`
}

type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Role      string `json:"role,omitempty"`
	Retryable bool   `json:"retryable"`
}

// Serve listens on a Unix socket and handles connections.
func Serve(ctx context.Context, socket string, manager *Manager) error {
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socket, err)
	}
	defer listener.Close()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(socket, 0o660); err != nil {
			return fmt.Errorf("protect control socket: %w", err)
		}
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		if err := authorizePeer(conn); err != nil {
			_ = conn.Close()
			continue
		}
		go serveConnection(ctx, conn, manager)
	}
}

func serveConnection(ctx context.Context, conn net.Conn, manager *Manager) {
	defer conn.Close()
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	var writeMu sync.Mutex
	write := func(value any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeFrame(conn, value)
	}
	subscribed := false
	for {
		var request Request
		if err := readFrame(conn, &request); err != nil {
			return
		}
		response := manager.Handle(ctx, request)
		if err := write(response); err != nil {
			return
		}
		if request.Method == "Subscribe" && response.OK && response.Snapshot != nil && !subscribed {
			subscribed = true
			go serveSubscription(connCtx, write, manager, request.ID, request.Version, response.Snapshot.Revision)
		}
	}
}

func serveSubscription(ctx context.Context, write func(any) error, manager *Manager, id string, version int, revision uint64) {
	for {
		snapshot, err := manager.WaitForRevision(ctx, revision)
		if err != nil {
			return
		}
		if err := write(Response{Version: version, ID: id, OK: true, Snapshot: &snapshot}); err != nil {
			return
		}
		revision = snapshot.Revision
	}
}

// readFrame reads a length-prefixed JSON frame from r.
func readFrame(r io.Reader, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > 1024*1024 {
		return fmt.Errorf("invalid frame size %d", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return err
	}
	return json.Unmarshal(payload, value)
}

// writeFrame writes a length-prefixed JSON frame to w.
func writeFrame(w io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(payload) == 0 || len(payload) > 1024*1024 {
		return fmt.Errorf("invalid response size")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, payload)
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

// Call performs a single request/response transaction over a Unix socket.
func Call(socket string, request Request) (Response, error) {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if err := writeFrame(conn, request); err != nil {
		return Response{}, err
	}
	var response Response
	if err := readFrame(conn, &response); err != nil {
		return Response{}, err
	}
	return response, nil
}

// Handle processes an API request and returns a response.
func (m *Manager) Handle(ctx context.Context, request Request) Response {
	response := Response{Version: request.Version, ID: request.ID}
	if request.Version != 1 && request.Version != 2 {
		response.Error = fmt.Sprintf("unsupported API version %d", request.Version)
		response.APIError = &APIError{Code: "unsupported_version", Message: response.Error, Retryable: false}
		return response
	}
	var err error
	switch request.Method {
	case "Handshake":
		response.Capabilities = []string{"Handshake", "GetSnapshot", "Subscribe", "ConnectAll", "DisconnectAll", "ConnectRole", "DisconnectRole", "RetryRole", "SetActiveProfile", "RediscoverEndpoints", "ValidateRole", "GetDiagnosticsSnapshot"}
		response.MinVersion, response.MaxVersion = 1, 2
	case "Snapshot", "GetSnapshot":
		respSnap := m.Snapshot()
		response.Snapshot = &respSnap
	case "ConnectAll":
		err = m.ConnectAll(ctx)
	case "DisconnectAll":
		err = m.DisconnectAll()
	case "ConnectRole":
		err = m.ConnectRole(ctx, request.Role)
	case "DisconnectRole":
		err = m.DisconnectRole(request.Role)
	case "RetryRole":
		err = m.RetryRole(ctx, request.Role)
	case "Subscribe":
		// serveConnection continues this request as a revisioned stream.
		respSnap := m.Snapshot()
		response.Snapshot = &respSnap
	case "SetActiveProfile":
		err = m.SetActiveProfile(request.Profile)
	case "RediscoverEndpoints":
		err = m.RediscoverEndpoints(request.Role)
	case "ValidateRole":
		err = m.ValidateRole(ctx, request.Role)
	case "GetDiagnosticsSnapshot":
		diagSnap := m.DiagnosticSnapshot()
		response.Snapshot = &diagSnap
	default:
		err = fmt.Errorf("unsupported method %q", request.Method)
	}
	if err != nil {
		response.Error = err.Error()
		response.APIError = &APIError{Code: "command_failed", Message: err.Error(), Role: request.Role, Retryable: true}
		return response
	}
	response.OK = true
	if response.Snapshot == nil && request.Method != "Handshake" {
		snapshot := m.Snapshot()
		response.Snapshot = &snapshot
	}
	return response
}
