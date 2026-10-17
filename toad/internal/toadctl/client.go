package toadctl

import (
	"context"
	"net"
)

type Client struct{ Socket string }

func (c Client) Call(ctx context.Context, req Request) (Response, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if err := writeFrame(conn, req); err != nil {
		return Response{}, err
	}
	var res Response
	if err := readFrame(conn, &res); err != nil {
		return Response{}, err
	}
	return res, nil
}
func (c Client) Subscribe(ctx context.Context, id string, fn func(Snapshot) error) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := writeFrame(conn, Request{Version: ProtocolVersion, ID: id, Method: "Subscribe"}); err != nil {
		return err
	}
	for {
		var res Response
		if err := readFrame(conn, &res); err != nil {
			return err
		}
		if !res.OK {
			if res.Error == nil {
				return &APIError{Code: "subscribe_failed", Message: "subscription failed"}
			}
			return res.Error
		}
		if res.Snapshot != nil {
			if err := fn(*res.Snapshot); err != nil {
				return err
			}
		}
	}
}
