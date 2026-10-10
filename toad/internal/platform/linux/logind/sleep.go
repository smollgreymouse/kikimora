//go:build linux

package logind

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
)

const (
	login1BusName   = "org.freedesktop.login1"
	login1Path      = dbus.ObjectPath("/org/freedesktop/login1")
	login1Interface = "org.freedesktop.login1.Manager"
)

type Event struct{ Preparing bool }
type Source struct{}

func (Source) Watch(ctx context.Context, out chan<- Event) error {
	conn, err := dbus.SystemBusPrivate()
	if err != nil {
		return fmt.Errorf("open system D-Bus: %w", err)
	}
	defer conn.Close()
	if err := conn.Auth(nil); err != nil {
		return fmt.Errorf("authenticate system D-Bus: %w", err)
	}
	if err := conn.Hello(); err != nil {
		return fmt.Errorf("hello system D-Bus: %w", err)
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchSender(login1BusName),
		dbus.WithMatchObjectPath(login1Path),
		dbus.WithMatchInterface(login1Interface),
		dbus.WithMatchMember("PrepareForSleep"),
	); err != nil {
		return fmt.Errorf("subscribe logind PrepareForSleep: %w", err)
	}

	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case signal, ok := <-signals:
			if !ok {
				return fmt.Errorf("system D-Bus signal channel closed")
			}
			event, ok := prepareForSleepEvent(signal)
			if !ok {
				continue
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func prepareForSleepEvent(signal *dbus.Signal) (Event, bool) {
	if signal == nil ||
		signal.Path != login1Path ||
		signal.Name != login1Interface+".PrepareForSleep" ||
		len(signal.Body) != 1 {
		return Event{}, false
	}
	preparing, ok := signal.Body[0].(bool)
	if !ok {
		return Event{}, false
	}
	return Event{Preparing: preparing}, true
}
