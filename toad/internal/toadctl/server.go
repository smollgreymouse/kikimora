package toadctl

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
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
				return err
			}
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
