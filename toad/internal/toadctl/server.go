package toadctl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Handler interface {
	Handle(context.Context, Request) Response
	WaitForRevision(context.Context, uint64) (Snapshot, error)
}
type Server struct {
	Socket  string
	Handler Handler
}

func (s Server) Serve(ctx context.Context) error {
	if s.Handler == nil {
		return fmt.Errorf("toadctl handler is nil")
	}
	if err := os.MkdirAll(filepath.Dir(s.Socket), 0o700); err != nil {
		return err
	}
	_ = os.Remove(s.Socket)
	ln, err := net.Listen("unix", s.Socket)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close(); _ = os.Remove(s.Socket) }()
	_ = os.Chmod(s.Socket, 0o600)
	go func() { <-ctx.Done(); _ = ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			// A transient accept failure (a client that connects and aborts
			// immediately can produce one on Windows AF_UNIX) must not kill
			// the control socket: toads lose their control plane for the rest
			// of the process lifetime. Back off briefly and keep serving; a
			// real shutdown arrives as a ctx cancellation or a closed listener.
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			time.Sleep(5 * time.Millisecond)
			continue
		}
		go s.connection(ctx, conn)
	}
}
func (s Server) connection(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	var mu sync.Mutex
	write := func(v any) error { mu.Lock(); defer mu.Unlock(); return writeFrame(conn, v) }
	var req Request
	if err := readFrame(conn, &req); err != nil {
		return
	}
	res := s.Handler.Handle(ctx, req)
	if err := write(res); err != nil || !res.OK || req.Method != "Subscribe" {
		return
	}
	revision := uint64(0)
	if res.Snapshot != nil {
		revision = res.Snapshot.Revision
	}
	for {
		snap, err := s.Handler.WaitForRevision(ctx, revision)
		if err != nil {
			return
		}
		if err := write(Response{Version: ProtocolVersion, ID: req.ID, OK: true, Snapshot: &snap}); err != nil {
			return
		}
		revision = snap.Revision
	}
}
